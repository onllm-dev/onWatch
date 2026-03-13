package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// maxCodexAuthFailures is the number of consecutive auth failures before pausing polling.
const maxCodexAuthFailures = 3

// codexTokenRefreshThreshold is how soon before expiry we proactively refresh the token.
// Codex tokens expire weekly, so refreshing 6 hours early provides a comfortable buffer.
const codexTokenRefreshThreshold = 6 * time.Hour

// CodexTokenRefreshFunc is called before each poll to get a fresh Codex token.
type CodexTokenRefreshFunc func() string

// CodexCredentialsRefreshFunc returns the full credentials for proactive OAuth refresh.
type CodexCredentialsRefreshFunc func() *api.CodexCredentials

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
	lastToken    string

	// Multi-account support
	accountID   int64  // Database account ID from provider_accounts
	profileName string // Profile name for logging

	// Auth failure rate limiting
	authFailCount   int
	authPaused      bool
	lastFailedToken string
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
						a.logger.Error("Proactive Codex OAuth refresh failed", "error", err)
						// Continue with existing token - it might still work
					}
				} else {
					// CRITICAL: Save new tokens to disk IMMEDIATELY (refresh tokens are one-time use!)
					if err := api.WriteCodexCredentials(newTokens.AccessToken, newTokens.RefreshToken, newTokens.IDToken, newTokens.ExpiresIn); err != nil {
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
