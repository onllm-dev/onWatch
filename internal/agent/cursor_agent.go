package agent

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type CursorTokenRefreshFunc func() string
type CursorCredentialsRefreshFunc func() *api.CursorCredentials
type CursorTokenSaveFunc func(accessToken, refreshToken, source, previousRefreshToken string) error

// cursorRefreshOutcome separates "a usable access token is now set in memory"
// from "the rotated tokens were persisted". A refresh consumes a one-time
// refresh token, so an unsaved but usable token must still be tried, while a
// saved token means normal auto-refresh may continue.
type cursorRefreshOutcome struct {
	usable    bool
	persisted bool
}

const cursorMaxAuthFailures = 3
const cursorRefreshBuffer = 5 * time.Minute

// cursorTokensEqual compares two token values in constant time, treating two
// empty values as equal. Tokens are credentials, so no early-exit comparison.
func cursorTokensEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// cursorCredentialsWritable is a seam over api.CursorCredentialsWritable so
// tests can force the pre-check outcome without a real token store.
var cursorCredentialsWritable = api.CursorCredentialsWritable

// cursorRefreshFn is a seam over api.RefreshCursorToken so package tests can
// point the refresh call at a test server without an exported setter in the
// api package (which owns an unsynchronised package global).
var cursorRefreshFn = api.RefreshCursorToken

type CursorAgent struct {
	client             *api.CursorClient
	store              *store.Store
	tracker            *tracker.CursorTracker
	interval           time.Duration
	logger             *slog.Logger
	sm                 *SessionManager
	tokenRefresh       CursorTokenRefreshFunc
	credentialsRefresh CursorCredentialsRefreshFunc
	tokenSave          CursorTokenSaveFunc
	lastToken          string
	notifier           *notify.NotificationEngine
	pollingCheck       func() bool

	// autoRefreshDisabled is set for the lifetime of the process when a refresh
	// succeeded but its rotated tokens could not be saved. The token is kept in
	// memory so polling continues, but another rotation would be lost too.
	autoRefreshDisabled bool
	// refreshedTokenPinned marks that the in-memory token came from a refresh
	// whose result could not be saved. Re-reading the store would replace it
	// with the stale on-disk token, so the pin suppresses those re-reads.
	refreshedTokenPinned bool
	// pinnedDiskToken and pinnedDiskRefreshToken are the on-disk tokens that
	// were current when the unsaveable refresh was pinned, and pinnedDiskSource
	// is the store they came from. Each poll re-reads that same store; a
	// different value means the user logged in again (or Cursor rewrote the
	// store), so the pin is released and the on-disk credential adopted. A
	// re-read that fails or falls back to another store is not evidence of a
	// change, so the pin stays.
	pinnedDiskToken        string
	pinnedDiskRefreshToken string
	pinnedDiskSource       string

	authFailCount   int
	authPaused      bool
	lastFailedToken string
}

func (a *CursorAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

func (a *CursorAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

func (a *CursorAgent) SetTokenRefresh(fn CursorTokenRefreshFunc) {
	a.tokenRefresh = fn
}

func (a *CursorAgent) SetCredentialsRefresh(fn CursorCredentialsRefreshFunc) {
	a.credentialsRefresh = fn
}

func (a *CursorAgent) SetTokenSave(fn CursorTokenSaveFunc) {
	a.tokenSave = fn
}

func NewCursorAgent(client *api.CursorClient, store *store.Store, tracker *tracker.CursorTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *CursorAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &CursorAgent{
		client:   client,
		store:    store,
		tracker:  tracker,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

func (a *CursorAgent) Run(ctx context.Context) error {
	a.logger.Info("Cursor agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Cursor agent stopped")
	}()

	a.poll(ctx)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *CursorAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	// While pinned, keep re-reading the same store: a changed on-disk token
	// means the user logged in again (or Cursor rewrote it), so the unsaveable
	// refresh no longer needs to keep the in-memory token. A re-read that fails
	// or falls back to another store proves nothing, so the pin stays.
	if a.refreshedTokenPinned {
		onDisk, onDiskRefresh, ok := a.readPinnedDiskCredential()
		accessChanged := onDisk != "" && !cursorTokensEqual(onDisk, a.pinnedDiskToken)
		refreshChanged := onDiskRefresh != "" && !cursorTokensEqual(onDiskRefresh, a.pinnedDiskRefreshToken)
		if ok && (accessChanged || refreshChanged) {
			a.logger.Info("Cursor on-disk credentials changed, resuming normal polling")
			a.refreshedTokenPinned = false
			a.pinnedDiskToken = ""
			a.pinnedDiskRefreshToken = ""
			a.pinnedDiskSource = ""
			a.autoRefreshDisabled = false
			a.authPaused = false
			a.authFailCount = 0
			a.lastFailedToken = ""
			if onDisk != "" {
				a.client.SetToken(onDisk)
				a.lastToken = onDisk
			}
		}
	}

	if a.authPaused {
		if a.tokenRefresh != nil && !a.refreshedTokenPinned {
			newToken := a.tokenRefresh()
			if newToken != "" && newToken != a.lastFailedToken {
				a.authPaused = false
				a.authFailCount = 0
				a.client.SetToken(newToken)
				a.logger.Info("Cursor auth recovered, resuming polling")
			}
		}
		if a.authPaused {
			return
		}
	}

	refreshedThisPoll := false
	if a.credentialsRefresh != nil {
		creds := a.credentialsRefresh()
		if creds != nil && api.NeedsCursorRefresh(creds) && creds.RefreshToken != "" {
			switch {
			case a.store != nil && !a.store.AutoRefreshTokensEnabled():
				a.logger.Debug("Skipping Cursor OAuth refresh - auto_refresh_tokens disabled")
			case creds.ReadOnly:
				a.logger.Debug("Skipping Cursor OAuth refresh - credential is read-only")
			case a.autoRefreshDisabled:
				a.logger.Debug("Skipping Cursor OAuth refresh - tokens could not be saved earlier")
			default:
				a.logger.Info("Cursor token expiring soon, refreshing", "expires_in", creds.ExpiresIn.Round(time.Minute))
				refreshedThisPoll = a.refreshToken(ctx, creds).usable
			}
		}
	}

	if !refreshedThisPoll && !a.refreshedTokenPinned && a.tokenRefresh != nil {
		newToken := a.tokenRefresh()
		if newToken != "" && newToken != a.lastFailedToken && newToken != a.lastToken {
			a.client.SetToken(newToken)
			a.lastToken = newToken
		}
	}

	snapshot, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		if api.IsCursorAuthError(err) {
			a.authFailCount++
			a.lastFailedToken = a.client.GetToken()
			a.logger.Warn("Cursor auth failure",
				"error", err,
				"fail_count", a.authFailCount,
			)

			if a.authFailCount >= cursorMaxAuthFailures {
				if a.credentialsRefresh != nil && (a.store == nil || a.store.AutoRefreshTokensEnabled()) {
					creds := a.credentialsRefresh()
					// A read-only credential must not be refreshed here either:
					// the refresh token may be Desktop's, held encrypted.
					refreshable := creds != nil && creds.RefreshToken != "" && !creds.ReadOnly && !a.autoRefreshDisabled
					if refreshable {
						a.logger.Info("Cursor: attempting token refresh due to auth failure")
						// Retry whenever a usable access token was set, even if
						// the rotated tokens could not be saved: that token is
						// the only working credential left once the old refresh
						// token was consumed.
						if a.refreshToken(ctx, creds).usable {
							snapshot, err = a.client.FetchQuotas(ctx)
							if err == nil {
								a.authFailCount = 0
								a.authPaused = false
								goto processSnapshot
							}
							// A retry that failed for a non-auth reason (5xx,
							// network, timeout) says nothing about the fresh
							// token. Pausing would strand the agent while it
							// still holds the only working credential, so
							// report a normal failed poll and leave the pinned
							// token for the next attempt.
							if !api.IsCursorAuthError(err) {
								if ctx.Err() == nil {
									a.logger.Error("Failed to fetch Cursor quotas after token refresh", "error", err)
									pollFailed("cursor", "", err)
								}
								return
							}
						}
					}
				}
				a.authPaused = true
				pollPaused("cursor", "")
				a.logger.Warn("Cursor polling paused due to auth failures",
					"fail_count", a.authFailCount,
				)
			}
			return
		}

		if api.IsCursorSessionExpired(err) {
			a.logger.Error("Cursor session expired - user must re-authenticate", "error", err)
			a.authPaused = true
			pollFailed("cursor", "", err)
			return
		}

		a.logger.Error("Failed to fetch Cursor quotas", "error", err)
		pollFailed("cursor", "", err)
		return
	}

	a.authFailCount = 0
	a.authPaused = false

processSnapshot:
	pollSucceeded("cursor", "")
	if _, err := a.store.InsertCursorSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Cursor snapshot", "error", err)
	}

	if err := a.tracker.Process(snapshot); err != nil {
		a.logger.Error("Cursor tracker processing failed", "error", err)
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "cursor",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
				Limit:       q.Limit,
				ResetAt:     derefTime(q.ResetsAt),
			})
		}
	}

	if a.sm != nil {
		var values []float64
		for _, q := range snapshot.Quotas {
			values = append(values, q.Utilization)
		}
		a.sm.ReportPoll(values)
	}

	a.logger.Info("Cursor poll complete",
		"account_type", snapshot.AccountType,
		"plan_name", snapshot.PlanName,
		"quota_count", len(snapshot.Quotas),
	)
}

// readPinnedDiskCredential re-reads the store the pin was recorded against.
// ok is false when the read failed, returned nothing, or came from a different
// store: none of those is evidence that the user's store changed, so the
// caller keeps the pin.
func (a *CursorAgent) readPinnedDiskCredential() (accessToken, refreshToken string, ok bool) {
	if a.pinnedDiskSource == "" || a.credentialsRefresh == nil {
		// The source cannot be re-checked, so nothing here can prove the
		// pinned store changed.
		return "", "", false
	}
	creds := a.credentialsRefresh()
	if creds == nil || creds.Source != a.pinnedDiskSource {
		// A failed read can make detection fall back to the other store. That
		// is not a change in the store the pin was recorded against.
		return "", "", false
	}
	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return "", "", false
	}
	return creds.AccessToken, creds.RefreshToken, true
}

func (a *CursorAgent) refreshToken(ctx context.Context, creds *api.CursorCredentials) cursorRefreshOutcome {
	if creds == nil || creds.RefreshToken == "" {
		return cursorRefreshOutcome{}
	}
	if creds.ReadOnly {
		// The Keychain token may be a copy sharing Cursor Desktop's refresh
		// token, which Desktop holds encrypted. Refreshing it would consume
		// that one-time token and log Desktop out, so treat it as read-only
		// and let the normal auth-failure handling surface the state.
		a.logger.Debug("Skipping Cursor OAuth refresh - credential is read-only")
		return cursorRefreshOutcome{}
	}
	if a.autoRefreshDisabled {
		a.logger.Debug("Skipping Cursor OAuth refresh - tokens could not be saved earlier")
		return cursorRefreshOutcome{}
	}
	if err := cursorCredentialsWritable(creds.Source); err != nil {
		// A refresh consumes a one-time refresh token, so never rotate one
		// whose result could not be persisted.
		a.logger.Warn("Skipping Cursor OAuth refresh - refreshed tokens could not be saved", "error", err)
		return cursorRefreshOutcome{}
	}

	a.logger.Info("Cursor: refreshing OAuth token")

	oauthResp, err := cursorRefreshFn(ctx, creds.RefreshToken)
	if err != nil {
		if errors.Is(err, api.ErrCursorSessionExpired) {
			a.logger.Error("Cursor session expired during refresh - user must re-authenticate")
			a.authPaused = true
		} else {
			a.logger.Warn("Cursor token refresh failed", "error", err)
		}
		return cursorRefreshOutcome{}
	}

	return a.applyRefreshedCredentials(oauthResp, creds.Source, creds.RefreshToken, creds.AccessToken)
}

// applyRefreshedCredentials saves the rotated tokens back to the store the
// credential was read from, plus a duplicated session in the other store. A
// Keychain-sourced credential must never replace Cursor Desktop's encrypted
// SQLite values.
//
// If the save fails after a successful refresh, the new access token is kept in
// memory so polling continues, and auto-refresh is disabled for the lifetime of
// the process: the rotated refresh token was lost, so retrying would rotate a
// token nobody can save. The returned outcome still reports the token as
// usable, so the caller tries it before pausing. diskAccessToken is the value
// the store held before the refresh; recording it lets a later poll notice a
// changed on-disk credential and drop the pin.
func (a *CursorAgent) applyRefreshedCredentials(oauthResp *api.CursorOAuthResponse, source, previousRefreshToken, diskAccessToken string) cursorRefreshOutcome {
	if oauthResp == nil || oauthResp.AccessToken == "" {
		return cursorRefreshOutcome{}
	}

	saveFn := a.tokenSave
	if saveFn == nil {
		saveFn = api.WriteCursorCredentials
	}
	if err := saveFn(oauthResp.AccessToken, oauthResp.RefreshToken, source, previousRefreshToken); err != nil {
		a.logger.Error("Failed to save refreshed Cursor credentials; keeping the new token in memory and disabling further auto-refresh", "error", err)
		a.autoRefreshDisabled = true
		a.refreshedTokenPinned = true
		a.pinnedDiskToken = diskAccessToken
		a.pinnedDiskRefreshToken = previousRefreshToken
		a.pinnedDiskSource = source
		a.client.SetToken(oauthResp.AccessToken)
		a.lastToken = oauthResp.AccessToken
		a.lastFailedToken = ""
		return cursorRefreshOutcome{usable: true, persisted: false}
	}

	a.autoRefreshDisabled = false
	a.refreshedTokenPinned = false
	a.pinnedDiskToken = ""
	a.pinnedDiskRefreshToken = ""
	a.pinnedDiskSource = ""
	a.client.SetToken(oauthResp.AccessToken)
	a.lastToken = oauthResp.AccessToken
	a.lastFailedToken = ""
	a.logger.Info("Cursor token refreshed successfully")
	return cursorRefreshOutcome{usable: true, persisted: true}
}
