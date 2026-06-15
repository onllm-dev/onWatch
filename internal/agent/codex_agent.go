package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// maxCodexAuthFailures is the number of consecutive auth failures before pausing polling.
const maxCodexAuthFailures = 3

// Auto quota-starter (Beta) rate limiting. The starter is re-evaluated every
// poll (so a failed start is retried at the user's polling cadence), but is hard
// capped to codexStarterMaxFires pings per quota window within a rolling
// codexStarterRateWindow. This guarantees we never loop or waste more than a
// handful of requests even if a start never "takes".
const (
	codexStarterMaxFires   = 5
	codexStarterRateWindow = 4 * time.Hour
)

// codexTokenRefreshThreshold is how soon before expiry we proactively refresh the token.
// Codex tokens expire weekly, so refreshing 6 hours early provides a comfortable buffer.
const codexTokenRefreshThreshold = 6 * time.Hour

// CodexTokenRefreshFunc is called before each poll to get a fresh Codex token.
type CodexTokenRefreshFunc func() string

// CodexCredentialsRefreshFunc returns the full credentials for proactive OAuth refresh.
type CodexCredentialsRefreshFunc func() *api.CodexCredentials

// CodexTokenSaveFunc saves refreshed OAuth tokens to the appropriate storage.
// For named profiles this writes to the profile JSON file; for the default
// profile it writes to the global CODEX_HOME/auth.json.
type CodexTokenSaveFunc func(accessToken, refreshToken, idToken string, expiresIn int) error

// isCodexAuthError returns true if the error is an authentication/authorization error.
func isCodexAuthError(err error) bool {
	return errors.Is(err, api.ErrCodexUnauthorized) || errors.Is(err, api.ErrCodexForbidden)
}

// CodexAgent manages the background polling loop for Codex quota tracking.
type CodexAgent struct {
	client       *api.CodexClient
	store        *store.Store
	tracker      *tracker.CodexTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	tokenRefresh CodexTokenRefreshFunc
	credsRefresh CodexCredentialsRefreshFunc
	tokenSave    CodexTokenSaveFunc
	lastToken    string

	// Multi-account support
	accountID   int64  // Database account ID from provider_accounts
	profileName string // Profile name for logging

	// Auth failure rate limiting
	authFailCount            int
	authPaused               bool
	lastFailedToken          string
	proactiveRefreshFailures int // consecutive proactive refresh failures (non-reused-token)

	// Auto quota-starter (Beta).
	// codexAccountID is the Codex account_id (string) used for the
	// ChatGPT-Account-ID header on starter pings. autoStartCheck reports, fresh
	// per poll, whether auto-start is enabled for a given window ("five_hour" /
	// "seven_day"). starterFires (guarded by starterMu) holds recent ping times
	// per window for the rolling rate cap; it is read/written from a goroutine
	// concurrent with poll().
	codexAccountID string
	autoStartCheck func(quotaName string) bool
	starterMu      sync.Mutex
	starterFires   map[string][]time.Time
}

// codexAutoStartWindowSeconds returns the nominal length of an auto-startable
// Codex quota window, or 0 for windows the starter does not manage.
func codexAutoStartWindowSeconds(quotaName string) int64 {
	switch quotaName {
	case "five_hour":
		return 5 * 60 * 60
	case "seven_day":
		return 7 * 24 * 60 * 60
	default:
		return 0
	}
}

// codexUnstartedTolerance is how close the time-until-reset must be to the full
// window length to treat the window as "unstarted". An unstarted Codex window
// reports reset_at = now + window (it rolls forward every poll, so the countdown
// stays pinned at ~the full length). Once a turn is sent the window's reset_at
// becomes fixed and the countdown starts decreasing, dropping below this band.
const codexUnstartedTolerance = 2 * time.Minute

// isUnstartedCodexWindow reports whether a quota window looks unstarted: its
// time-until-reset is within codexUnstartedTolerance of the full window length.
func isUnstartedCodexWindow(quotaName string, resetsAt *time.Time, now time.Time) bool {
	windowSec := codexAutoStartWindowSeconds(quotaName)
	if windowSec == 0 || resetsAt == nil {
		return false
	}
	remaining := resetsAt.Sub(now)
	if remaining <= 0 {
		return false
	}
	full := time.Duration(windowSec) * time.Second
	return remaining >= full-codexUnstartedTolerance
}

// SetCodexAccountID sets the Codex account_id used for the ChatGPT-Account-ID
// header on auto quota-starter pings.
func (a *CodexAgent) SetCodexAccountID(id string) {
	a.codexAccountID = id
}

// SetAutoStartCheck wires a callback that reports, fresh per poll, whether the
// auto quota-starter (Beta) is enabled for a given window. Read fresh so a
// dashboard toggle takes effect without a daemon restart.
func (a *CodexAgent) SetAutoStartCheck(fn func(quotaName string) bool) {
	a.autoStartCheck = fn
}

// maybeAutoStartWindows inspects the freshly polled quotas and, for any window
// that is enabled and observed unstarted, fires a starter ping (cooldown-guarded
// inside SendStarterPing). Pings run in a goroutine so they never block the poll.
func (a *CodexAgent) maybeAutoStartWindows(ctx context.Context, quotas []api.CodexQuota, now time.Time) {
	if a.autoStartCheck == nil {
		return
	}
	for _, q := range quotas {
		if codexAutoStartWindowSeconds(q.Name) == 0 {
			continue
		}
		if !isUnstartedCodexWindow(q.Name, q.ResetsAt, now) {
			continue
		}
		if !a.autoStartCheck(q.Name) {
			continue
		}
		quotaName := q.Name
		go a.SendStarterPing(ctx, a.codexAccountID, quotaName)
	}
}

// allowStarterPing enforces the rolling rate cap using a bounded ring of the
// last codexStarterMaxFires fire timestamps for the window:
//   - fewer than codexStarterMaxFires recorded -> always fire (append now);
//   - otherwise fire only if the oldest of the five is more than
//     codexStarterRateWindow ago (then drop it and append now).
//
// This guarantees at most codexStarterMaxFires pings per rolling
// codexStarterRateWindow per window. The slot is reserved even if the subsequent
// send fails, so repeated unstarted observations retry at the polling cadence but
// can never exceed the cap.
func (a *CodexAgent) allowStarterPing(quotaName string) bool {
	a.starterMu.Lock()
	defer a.starterMu.Unlock()
	if a.starterFires == nil {
		a.starterFires = make(map[string][]time.Time)
	}
	now := time.Now()
	fires := a.starterFires[quotaName]

	if len(fires) < codexStarterMaxFires {
		a.starterFires[quotaName] = append(fires, now)
		return true
	}
	// Five recorded: only fire if the oldest is older than the rate window.
	if now.Sub(fires[0]) <= codexStarterRateWindow {
		return false
	}
	next := make([]time.Time, 0, codexStarterMaxFires)
	next = append(next, fires[1:]...)
	next = append(next, now)
	a.starterFires[quotaName] = next
	return true
}

// NewCodexAgent creates a new CodexAgent with the given dependencies.
// Uses DefaultCodexAccountID (1) for backward compatibility.
func NewCodexAgent(client *api.CodexClient, st *store.Store, tracker *tracker.CodexTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *CodexAgent {
	return NewCodexAgentWithAccount(client, st, tracker, interval, logger, sm, store.DefaultCodexAccountID)
}

// NewCodexAgentWithAccount creates a new CodexAgent for a specific account.
func NewCodexAgentWithAccount(client *api.CodexClient, st *store.Store, tracker *tracker.CodexTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager, accountID int64) *CodexAgent {
	if logger == nil {
		logger = slog.Default()
	}
	if accountID == 0 {
		accountID = store.DefaultCodexAccountID
	}
	return &CodexAgent{
		client:    client,
		store:     st,
		tracker:   tracker,
		interval:  interval,
		logger:    logger,
		sm:        sm,
		accountID: accountID,
	}
}

// SetPollingCheck sets a function called before each poll.
func (a *CodexAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets notification engine for sending alerts.
func (a *CodexAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// SetTokenRefresh sets a function called before each poll to refresh Codex token from credentials.
func (a *CodexAgent) SetTokenRefresh(fn CodexTokenRefreshFunc) {
	a.tokenRefresh = fn
}

// SetCredentialsRefresh sets a function that returns full credentials for
// proactive OAuth token refresh before expiry.
func (a *CodexAgent) SetCredentialsRefresh(fn CodexCredentialsRefreshFunc) {
	a.credsRefresh = fn
}

// SetTokenSave sets a function that saves refreshed OAuth tokens to disk.
// This allows named profiles to write to their profile file instead of
// the global CODEX_HOME/auth.json, preventing auth contamination between profiles.
func (a *CodexAgent) SetTokenSave(fn CodexTokenSaveFunc) {
	a.tokenSave = fn
}

// SendStarterPing sends a minimal Codex generation request to "start" a limit
// window after a reset (auto quota-starter, Beta). Failures are logged, never
// fatal, and never retried (to avoid API spam) - a stale/paused token simply
// yields a logged auth error. codexAccountID is the Codex account_id used for
// the ChatGPT-Account-ID header; quotaName is for logging only.
//
// This may run in a goroutine concurrent with poll(), so it must not read
// poll-owned agent state (e.g. authPaused) without synchronization. It relies
// only on the client, which is internally synchronized.
func (a *CodexAgent) SendStarterPing(ctx context.Context, codexAccountID, quotaName string) {
	if !a.allowStarterPing(quotaName) {
		a.logger.Info("Codex auto quota-starter skipped (rate cap reached)",
			"quota", quotaName, "account_id", a.accountID,
			"max_fires", codexStarterMaxFires, "window", codexStarterRateWindow)
		return
	}

	a.logger.Info("Codex auto quota-starter firing",
		"quota", quotaName, "account_id", a.accountID, "model", api.CodexStarterModel())

	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := a.client.SendStarterPing(pingCtx, codexAccountID); err != nil {
		a.logger.Warn("Codex auto quota-starter ping failed",
			"quota", quotaName, "account_id", a.accountID, "model", api.CodexStarterModel(), "error", err)
		return
	}
	a.logger.Info("Codex auto quota-starter ping sent",
		"quota", quotaName, "account_id", a.accountID, "model", api.CodexStarterModel())
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *CodexAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "codex",
		Title:       title,
		Message:     message,
		AccountID:   fmt.Sprintf("%d", a.accountID),
		IsRecovable: isRecoverable,
	})
}

// Run starts the agent polling loop.
func (a *CodexAgent) Run(ctx context.Context) error {
	a.logger.Info("Codex agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Codex agent stopped")
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

func (a *CodexAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	// Proactive OAuth refresh: check if token expires soon and refresh via OAuth API
	if a.credsRefresh != nil {
		if creds := a.credsRefresh(); creds != nil {
			// Check if token is expiring soon or already expired
			if creds.IsExpiringSoon(codexTokenRefreshThreshold) && creds.RefreshToken != "" {
				a.logger.Info("Codex token expiring soon, attempting proactive OAuth refresh",
					"expires_in", creds.ExpiresIn.Round(time.Second))

				newTokens, err := api.RefreshCodexToken(ctx, creds.RefreshToken)
				if err != nil {
					if errors.Is(err, api.ErrCodexRefreshTokenReused) {
						// Unrecoverable - token is dead, user must re-authenticate
						a.logger.Error("Codex refresh token already used - re-authenticate via 'codex auth'",
							"error", err)
						a.authPaused = true
						a.lastFailedToken = creds.AccessToken
						// Send auth error notification
						a.sendAuthErrorNotification(
							"Token Refresh Failed",
							"Codex refresh token has been reused. Please re-authenticate via 'codex auth' to resume quota tracking.",
							false, // not recoverable
						)
					} else {
						a.proactiveRefreshFailures++
						a.logger.Error("Proactive Codex OAuth refresh failed",
							"error", err,
							"consecutive_failures", a.proactiveRefreshFailures)
						if a.proactiveRefreshFailures >= maxCodexAuthFailures {
							a.authPaused = true
							a.lastFailedToken = creds.AccessToken
							a.logger.Error("Codex proactive refresh PAUSED - too many consecutive failures",
								"failure_count", a.proactiveRefreshFailures,
								"action", "Re-authenticate via 'codex auth' to resume polling")
							a.sendAuthErrorNotification(
								"Token Refresh Failed",
								fmt.Sprintf("Codex proactive OAuth refresh failed %d times. Please re-authenticate via 'codex auth' to resume.", a.proactiveRefreshFailures),
								false,
							)
						}
					}
				} else {
					// Proactive refresh succeeded - reset failure counter
					a.proactiveRefreshFailures = 0

					// CRITICAL: Save new tokens to disk IMMEDIATELY (refresh tokens are one-time use!)
					saveFn := a.tokenSave
					if saveFn == nil {
						saveFn = api.WriteCodexCredentials // fallback for backward compat
					}
					if err := saveFn(newTokens.AccessToken, newTokens.RefreshToken, newTokens.IDToken, newTokens.ExpiresIn); err != nil {
						a.logger.Error("Failed to save refreshed Codex credentials", "error", err)
					} else {
						a.client.SetToken(newTokens.AccessToken)
						a.lastToken = newTokens.AccessToken
						a.logger.Info("Proactively refreshed Codex OAuth token",
							"expires_in_hours", newTokens.ExpiresIn/3600)

						// Reset auth failures since we have fresh credentials
						if a.authPaused {
							a.authPaused = false
							a.authFailCount = 0
							a.lastFailedToken = ""
							a.logger.Info("Codex auth failure pause lifted - token refreshed via OAuth")
						}
					}
				}
			}
		}
	}

	// Refresh token before each poll (picks up rotated credentials from disk)
	if a.tokenRefresh != nil {
		newToken := a.tokenRefresh()
		if newToken != "" && newToken != a.lastToken {
			a.client.SetToken(newToken)
			a.lastToken = newToken
			a.logger.Info("Codex token refreshed from credentials")

			// If we were paused due to auth failures and credentials changed, resume.
			if a.authPaused && newToken != a.lastFailedToken {
				a.authPaused = false
				a.authFailCount = 0
				a.lastFailedToken = ""
				a.logger.Info("Codex auth failure pause lifted - new credentials detected")
			}
		}
	}

	// If auth is paused, skip polling until credentials change.
	if a.authPaused {
		return
	}

	resp, err := a.client.FetchUsage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		// On auth error, force token re-read and retry once.
		if isCodexAuthError(err) && a.tokenRefresh != nil {
			a.logger.Warn("Codex auth error, forcing credential re-read", "error", err)
			a.lastToken = "" // force re-read even if unchanged on disk
			if retryToken := a.tokenRefresh(); retryToken != "" {
				a.client.SetToken(retryToken)
				a.lastToken = retryToken
				a.logger.Info("Retrying Codex poll with refreshed token")
				resp, err = a.client.FetchUsage(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					if isCodexAuthError(err) {
						a.authFailCount++
						a.logger.Error("Codex auth retry failed",
							"error", err,
							"failure_count", a.authFailCount,
							"max_failures", maxCodexAuthFailures)

						if a.authFailCount >= maxCodexAuthFailures {
							a.authPaused = true
							a.lastFailedToken = retryToken
							a.logger.Error("Codex polling PAUSED due to repeated auth failures",
								"failure_count", a.authFailCount,
								"action", "Re-authenticate Codex to resume polling")
							// Send auth error notification
							a.sendAuthErrorNotification(
								"Authentication Failed",
								"Codex polling has been paused due to repeated authentication failures. Please re-authenticate via 'codex auth' to resume.",
								false, // not recoverable without re-auth
							)
						}
					} else {
						a.logger.Error("Codex retry failed with non-auth error", "error", err)
					}
					return
				}
				// Retry succeeded, reset auth failure count.
				a.authFailCount = 0
			} else {
				a.logger.Error("No Codex token available after re-read")
				return
			}
		} else {
			a.logger.Error("Failed to fetch Codex usage", "error", err)
			return
		}
	} else {
		// Success, reset auth failure count.
		a.authFailCount = 0
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)
	snapshot.AccountID = a.accountID // Set the database account ID

	if _, err := a.store.InsertCodexSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Codex snapshot", "error", err, "account_id", a.accountID)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Codex tracker processing failed", "error", err, "account_id", a.accountID)
		}
	}

	// Auto quota-starter (Beta): start any enabled window that is observed
	// unstarted (reset countdown pinned at ~full window length).
	a.maybeAutoStartWindows(ctx, snapshot.Quotas, now)

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "codex",
				QuotaKey:    q.Name,
				AccountID:   fmt.Sprintf("%d", a.accountID),
				Utilization: q.Utilization,
				Limit:       100,
			})
		}
	}

	if a.sm != nil {
		values := make([]float64, 0, len(snapshot.Quotas))
		for _, q := range snapshot.Quotas {
			values = append(values, q.Utilization)
		}
		a.sm.ReportPoll(values)
	}

	for _, q := range snapshot.Quotas {
		a.logger.Info("Codex poll complete", "quota", q.Name, "utilization", q.Utilization, "plan", resp.PlanType, "account_id", a.accountID)
	}
}
