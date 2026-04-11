// Package agent provides the background polling agent for onWatch.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// TokenRefreshFunc is called before each poll to get a fresh token.
// Returns the new token, or empty string if refresh is not needed/available.
type TokenRefreshFunc func() string

// CredentialsRefreshFunc returns the full credentials for proactive OAuth refresh.
type CredentialsRefreshFunc func() *api.AnthropicCredentials

// maxAuthFailures is the number of consecutive auth failures before pausing polling.
const maxAuthFailures = 3

// maxRateLimitFailures is the number of consecutive OAuth 429s before entering extended backoff.
const maxRateLimitFailures = 5

// IsClaudeCodeRunning checks if Claude Code is currently executing.
// When Claude Code is running, onWatch skips proactive OAuth refresh to avoid
// competing for the same refresh token - a refresh by onWatch invalidates
// Claude Code's pending refresh, causing it to get invalid_grant and re-auth.
// Exported as a package-level variable so tests can override it.
//
// Uses pattern matching (-f) instead of exact name matching (-x) because
// Claude Code may run as a Node.js process where the process name is "node"
// rather than "claude". This trades false positive risk for reliable detection.
var IsClaudeCodeRunning = func() bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		cmd := exec.Command("pgrep", "-f", "claude")
		return cmd.Run() == nil
	}
	// Windows: tasklist always returns exit 0, so pipe through findstr
	// to verify the process actually exists in the output.
	cmd := exec.Command("cmd", "/C", `tasklist /FI "IMAGENAME eq claude.exe" /NH 2>nul | findstr /I "claude.exe"`)
	return cmd.Run() == nil
}

// rateLimitBaseBackoff is the initial backoff duration after an OAuth 429.
const rateLimitBaseBackoff = 5 * time.Minute

// rateLimitMaxBackoff is the maximum backoff duration for OAuth 429 errors.
const rateLimitMaxBackoff = 6 * time.Hour

// tokenRefreshThreshold is how soon before expiry we proactively refresh the token.
const tokenRefreshThreshold = 10 * time.Minute

// AnthropicAgent manages the background polling loop for Anthropic quota tracking.
type AnthropicAgent struct {
	client       *api.AnthropicClient
	store        *store.Store
	tracker      *tracker.AnthropicTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	tokenRefresh TokenRefreshFunc
	credsRefresh CredentialsRefreshFunc
	lastToken    string
	notifier     *notify.NotificationEngine
	pollingCheck func() bool

	// Auth failure rate limiting
	authFailCount   int    // consecutive auth failures (401 or 403)
	authPaused      bool   // true when polling is paused due to auth failures
	lastFailedToken string // token that caused the failures (to detect credential refresh)

	// OAuth rate limit backoff (429 from the OAuth refresh endpoint)
	rateLimitFailCount int       // consecutive OAuth 429 failures
	rateLimitPaused    bool      // true when OAuth refresh is in backoff
	rateLimitResumeAt  time.Time // when to next attempt OAuth refresh

	// isClaudeCodeRunning checks if Claude Code is executing. If nil, uses the
	// package-level IsClaudeCodeRunning. Override in tests to control behavior.
	isClaudeCodeRunning func() bool

	// Statusline bridge: reads Anthropic rate limits from Claude Code's statusline
	// output file, avoiding the rate-limited usage API entirely.
	statuslinePath      string        // path to statusline JSON file
	statuslineStaleness time.Duration // max age before falling back to API

	// Hybrid polling: in auto mode, do a full API poll every N cycles to get
	// supplementary quotas (seven_day_sonnet, extra_usage, etc.) that the
	// statusline doesn't provide. 0 = disabled.
	apiPollCycleInterval int // API poll every N cycles (default: 10)
	pollCycleCount       int // current cycle counter
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled).
func (a *AnthropicAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *AnthropicAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *AnthropicAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "anthropic",
		Title:       title,
		Message:     message,
		IsRecovable: isRecoverable,
	})
}

// NewAnthropicAgent creates a new AnthropicAgent with the given dependencies.
func NewAnthropicAgent(client *api.AnthropicClient, store *store.Store, tr *tracker.AnthropicTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *AnthropicAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnthropicAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// SetTokenRefresh sets a function that will be called before each poll to
// refresh the Anthropic OAuth token. This enables automatic token rotation
// when Claude Code refreshes credentials on disk.
func (a *AnthropicAgent) SetTokenRefresh(fn TokenRefreshFunc) {
	a.tokenRefresh = fn
}

// SetCredentialsRefresh sets a function that returns full credentials for
// proactive OAuth token refresh before expiry.
func (a *AnthropicAgent) SetCredentialsRefresh(fn CredentialsRefreshFunc) {
	a.credsRefresh = fn
}

// EnableStatuslineBridge activates the statusline file bridge for zero-429
// Anthropic monitoring. When enabled, the agent checks a shared file written
// by Claude Code's statusline before falling back to the OAuth usage API.
// Must be called explicitly - not enabled by default to avoid test interference.
func (a *AnthropicAgent) EnableStatuslineBridge() {
	a.statuslinePath = StatuslineDataPath()
	a.statuslineStaleness = statuslineStalenessDefault
	a.apiPollCycleInterval = 10 // default: full API poll every 10 cycles
}

// SetAPIPollCycleInterval sets how often (in cycles) a full API poll is done
// alongside statusline data to get supplementary quotas like seven_day_sonnet.
// 0 disables periodic API polling. Default is 10 (every 10th cycle).
func (a *AnthropicAgent) SetAPIPollCycleInterval(n int) {
	a.apiPollCycleInterval = n
}

// SetStatuslineStaleness sets the maximum age of the statusline file before
// falling back to API polling. Overrides the default (5 minutes).
func (a *AnthropicAgent) SetStatuslineStaleness(d time.Duration) {
	a.statuslineStaleness = d
}

// SetCCDetectionEnabled controls whether the agent checks if Claude Code is
// running before attempting OAuth token refresh. When disabled, OAuth refresh
// is always attempted regardless of CC state.
func (a *AnthropicAgent) SetCCDetectionEnabled(enabled bool) {
	if enabled {
		a.isClaudeCodeRunning = IsClaudeCodeRunning
	} else {
		a.isClaudeCodeRunning = func() bool { return false }
	}
}

// Run starts the Anthropic agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *AnthropicAgent) Run(ctx context.Context) error {
	a.logger.Info("Anthropic agent started", "interval", a.interval)

	// Ensure any active session is closed on exit
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Anthropic agent stopped")
	}()

	// Poll immediately on start
	a.poll(ctx)

	// Create ticker for periodic polling
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Main polling loop
	for {
		select {
		case <-ticker.C:
			// Periodic statusline bridge health check (only when bridge is enabled)
			if a.statuslinePath != "" {
				EnsureStatuslineBridge(a.logger)
			}
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// isAuthError returns true if the error is an authentication/authorization error.
func isAuthError(err error) bool {
	return errors.Is(err, api.ErrAnthropicUnauthorized) || errors.Is(err, api.ErrAnthropicForbidden)
}

// rateLimitBackoff calculates the exponential backoff duration for OAuth 429 errors.
// Formula: min(base * 2^(n-1), max) where n is the failure count.
func rateLimitBackoff(failCount int) time.Duration {
	if failCount <= 0 {
		return rateLimitBaseBackoff
	}
	shift := failCount - 1
	if shift > 10 {
		shift = 10 // prevent overflow
	}
	backoff := rateLimitBaseBackoff * (1 << shift)
	if backoff > rateLimitMaxBackoff {
		return rateLimitMaxBackoff
	}
	return backoff
}

// proactiveRefresh attempts to refresh the OAuth token before it expires.
// Respects rate limit backoff to avoid burning refresh tokens.
// Skips proactive refresh if Claude Code is running to avoid competing for the
// same refresh token (onWatch refreshes would invalidate Claude Code's pending
// refresh and cause re-authentication).
func (a *AnthropicAgent) proactiveRefresh(ctx context.Context, creds *api.AnthropicCredentials) {
	// Skip if Claude Code is running - avoid competing for the same refresh token.
	// onWatch refreshing burns Claude Code's scheduled refresh, causing invalid_grant.
	checkFn := IsClaudeCodeRunning // package-level default
	if a.isClaudeCodeRunning != nil {
		checkFn = a.isClaudeCodeRunning
	}
	if checkFn() {
		return
	}

	// Skip if in rate limit backoff
	if a.rateLimitPaused && time.Now().Before(a.rateLimitResumeAt) {
		a.logger.Debug("Skipping proactive OAuth refresh - in rate limit backoff",
			"resume_at", a.rateLimitResumeAt)
		return
	}

	a.logger.Info("Token expiring soon, attempting proactive OAuth refresh",
		"expires_in", creds.ExpiresIn.Round(time.Second))

	newTokens, err := api.RefreshAnthropicToken(ctx, creds.RefreshToken)
	if err != nil {
		if errors.Is(err, api.ErrOAuthRateLimited) {
			a.rateLimitFailCount++
			backoff := api.RetryAfter(err)
			if backoff > 0 {
				a.rateLimitPaused = true
				a.rateLimitResumeAt = time.Now().Add(backoff)
				a.logger.Warn("Proactive OAuth refresh rate limited - using server Retry-After",
					"fail_count", a.rateLimitFailCount,
					"retry_after", backoff)
			} else {
				backoff = rateLimitBackoff(a.rateLimitFailCount)
				a.rateLimitPaused = true
				a.rateLimitResumeAt = time.Now().Add(backoff)
				a.logger.Warn("Proactive OAuth refresh rate limited - backing off",
					"fail_count", a.rateLimitFailCount,
					"backoff", backoff)
			}
		} else if errors.Is(err, api.ErrOAuthInvalidGrant) {
			a.authPaused = true
			a.authFailCount = maxAuthFailures
			a.lastFailedToken = a.lastToken
			a.logger.Error("Proactive OAuth refresh - invalid_grant, polling PAUSED",
				"error", err,
				"action", "Re-authenticate with 'claude auth' to resume polling")
		} else {
			a.logger.Error("Proactive OAuth refresh failed", "error", err)
		}
		return
	}

	// Proactive refresh succeeded - reset all backoff state
	a.rateLimitFailCount = 0
	a.rateLimitPaused = false
	a.rateLimitResumeAt = time.Time{}

	// CRITICAL: Save new tokens to disk IMMEDIATELY
	if err := api.WriteAnthropicCredentials(newTokens.AccessToken, newTokens.RefreshToken, newTokens.ExpiresIn); err != nil {
		a.logger.Error("Failed to save refreshed credentials", "error", err)
	} else {
		a.client.SetToken(newTokens.AccessToken)
		a.lastToken = newTokens.AccessToken
		a.logger.Info("Proactively refreshed OAuth token",
			"expires_in_hours", newTokens.ExpiresIn/3600)

		// Reset auth failures since we have fresh credentials
		if a.authPaused {
			a.authPaused = false
			a.authFailCount = 0
			a.lastFailedToken = ""
			a.logger.Info("Auth failure pause lifted - token refreshed via OAuth")
		}
	}
}

// poll performs a single Anthropic poll cycle: fetch quotas, store snapshot, process with tracker.
func (a *AnthropicAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	// Statusline bridge: try to read rate limit data from Claude Code's statusline
	// output file. If fresh and valid, use it and skip the rate-limited OAuth usage API.
	// Falls back to API polling if data is stale, missing, corrupt, or out of range.
	if a.statuslinePath != "" && isStatuslineFresh(a.statuslinePath, a.statuslineStaleness) {
		rl, err := readStatuslineData(a.statuslinePath)
		if err != nil {
			a.logger.Info("Statusline read error, falling back to API polling", "error", err)
		} else if !isValidStatuslineData(rl) {
			a.logger.Warn("Statusline data invalid, falling back to API polling")
		} else {
			now := time.Now().UTC()
			snapshot := statuslineToSnapshot(rl, now)
			if _, err := a.store.InsertAnthropicSnapshot(snapshot); err != nil {
				a.logger.Error("Failed to insert statusline snapshot", "error", err)
				return // don't fall through to API polling on DB error
			}
			if a.tracker != nil {
				if err := a.tracker.Process(snapshot); err != nil {
					a.logger.Error("Anthropic tracker processing failed", "error", err)
				}
			}
			a.pollCycleCount++
			a.logger.Info("Anthropic poll complete",
				"source", "statusline",
				"quota_count", len(snapshot.Quotas),
				"cycle", a.pollCycleCount)
			if a.rateLimitFailCount > 0 {
				a.rateLimitFailCount--
			}
			// Hybrid: periodically do a full API poll for supplementary quotas
			// (seven_day_sonnet, extra_usage, etc.) that statusline doesn't provide.
			if a.apiPollCycleInterval > 0 && a.pollCycleCount%a.apiPollCycleInterval == 0 {
				a.logger.Info("Hybrid API poll triggered",
					"cycle", a.pollCycleCount,
					"interval", a.apiPollCycleInterval)
				// Fall through to the API polling path below
			} else {
				return // Statusline only - skip API polling this cycle
			}
		}
	}

	// Proactive OAuth refresh
	if a.credsRefresh != nil {
		if creds := a.credsRefresh(); creds != nil {
			// Check if token is expiring soon or already expired
			if creds.IsExpiringSoon(tokenRefreshThreshold) && creds.RefreshToken != "" {
				a.proactiveRefresh(ctx, creds)
			}
		}
	}

	// Refresh token before each poll (picks up rotated credentials from disk)
	var newToken string
	if a.tokenRefresh != nil {
		newToken = a.tokenRefresh()
		if newToken != "" && newToken != a.lastToken {
			a.client.SetToken(newToken)
			a.lastToken = newToken
			a.logger.Info("Anthropic token refreshed from credentials")

			// If we were paused due to auth failures and credentials changed, resume
			if a.authPaused && newToken != a.lastFailedToken {
				a.authPaused = false
				a.authFailCount = 0
				a.lastFailedToken = ""
				a.logger.Info("Auth failure pause lifted - new credentials detected")
			}

			// If we were in rate limit backoff and credentials changed, resume
			if a.rateLimitPaused {
				a.rateLimitPaused = false
				a.rateLimitFailCount = 0
				a.rateLimitResumeAt = time.Time{}
				a.logger.Info("Rate limit backoff lifted - new credentials detected")
			}
		}
	}

	// If auth is paused, skip polling until credentials change
	if a.authPaused {
		// Only log periodically to avoid spamming logs
		return
	}

	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		// Rate limited (429) - attempt token refresh to get fresh rate limit window.
		//
		// WORKAROUND for Anthropic API rate limiting (GitHub issue #16):
		// Anthropic's /api/oauth/usage endpoint has aggressive rate limits (~5 requests
		// per token before 429). However, each NEW access token gets a fresh rate limit
		// window. By refreshing the OAuth token when rate limited, we can bypass the
		// limit and continue polling without waiting 5+ minutes.
		//
		// Key insight: Rate limits are per-access-token, not per-account. Refresh tokens
		// are one-time use (OAuth refresh token rotation), so we MUST save both the new
		// access token AND new refresh token after each refresh.
		//
		// See: https://github.com/anthropics/claude-code/issues/31021
		if errors.Is(err, api.ErrAnthropicRateLimited) {
			a.logger.Warn("Anthropic rate limited (429), attempting token refresh bypass")

			// If in rate limit backoff, skip the OAuth refresh to avoid burning tokens
			if a.rateLimitPaused {
				if time.Now().Before(a.rateLimitResumeAt) {
					a.logger.Warn("OAuth refresh in backoff, skipping token refresh attempt",
						"resume_at", a.rateLimitResumeAt,
						"fail_count", a.rateLimitFailCount)
					return
				}
				// Backoff expired - decay failCount so retries don't escalate forever.
				// If retry succeeds, failCount resets to 0. If retry fails,
				// failCount stays flat (decremented here, incremented below).
				if a.rateLimitFailCount > 0 {
					a.rateLimitFailCount--
				}
				a.rateLimitPaused = false
				a.logger.Info("OAuth rate limit backoff expired, retrying refresh",
					"fail_count", a.rateLimitFailCount)
			}

			// Try to refresh token to get fresh rate limit window.
			// Skip if Claude Code is running - refreshing burns CC's token.
			checkFn := IsClaudeCodeRunning
			if a.isClaudeCodeRunning != nil {
				checkFn = a.isClaudeCodeRunning
			}
			if checkFn() {
				a.logger.Debug("Claude Code running, skipping 429 bypass refresh")
				return
			}
			if a.credsRefresh != nil {
				if creds := a.credsRefresh(); creds != nil && creds.RefreshToken != "" {
					newTokens, refreshErr := api.RefreshAnthropicToken(ctx, creds.RefreshToken)
					if refreshErr != nil {
						// Classify the OAuth refresh failure
						if errors.Is(refreshErr, api.ErrOAuthRateLimited) {
							// OAuth endpoint itself is rate limited - apply backoff.
							// Prefer server-provided Retry-After header if available.
							a.rateLimitFailCount++
							backoff := api.RetryAfter(refreshErr)
							if backoff > 0 {
								a.rateLimitPaused = true
								a.rateLimitResumeAt = time.Now().Add(backoff)
								a.logger.Warn("OAuth refresh rate limited - using server Retry-After",
									"fail_count", a.rateLimitFailCount,
									"retry_after", backoff,
									"resume_at", a.rateLimitResumeAt)
							} else {
								backoff = rateLimitBackoff(a.rateLimitFailCount)
								a.rateLimitResumeAt = time.Now().Add(backoff)
								if a.rateLimitFailCount >= maxRateLimitFailures {
									a.rateLimitPaused = true
									a.logger.Error("OAuth refresh rate limited - entering extended backoff",
										"fail_count", a.rateLimitFailCount,
										"backoff", backoff,
										"resume_at", a.rateLimitResumeAt)
								} else {
									a.rateLimitPaused = true
									a.logger.Warn("OAuth refresh rate limited - backing off",
										"fail_count", a.rateLimitFailCount,
										"backoff", backoff,
										"resume_at", a.rateLimitResumeAt)
								}
							}
						} else if errors.Is(refreshErr, api.ErrOAuthInvalidGrant) {
							// Terminal: refresh token revoked/expired - pause like auth errors
							a.authPaused = true
							a.authFailCount = maxAuthFailures
							a.lastFailedToken = a.lastToken
							a.logger.Error("OAuth refresh token invalid (invalid_grant) - polling PAUSED",
								"error", refreshErr,
								"action", "Re-authenticate with 'claude auth' to resume polling")
							a.sendAuthErrorNotification(
								"OAuth refresh token expired",
								"Refresh token is invalid or revoked. Re-authenticate with 'claude auth' to resume polling.",
								false,
							)
						} else {
							// Transient OAuth error - apply mild backoff
							a.rateLimitFailCount++
							if a.rateLimitFailCount >= maxRateLimitFailures {
								backoff := rateLimitBackoff(a.rateLimitFailCount)
								a.rateLimitPaused = true
								a.rateLimitResumeAt = time.Now().Add(backoff)
								a.logger.Warn("OAuth refresh failed repeatedly - backing off",
									"error", refreshErr,
									"fail_count", a.rateLimitFailCount,
									"backoff", backoff)
							} else {
								a.logger.Warn("Rate limit bypass failed - token refresh error",
									"error", refreshErr,
									"fail_count", a.rateLimitFailCount)
							}
						}
						return
					}

					// OAuth refresh succeeded - reset backoff state
					a.rateLimitFailCount = 0
					a.rateLimitPaused = false
					a.rateLimitResumeAt = time.Time{}

					// Save new tokens immediately (refresh tokens are one-time use!)
					if saveErr := api.WriteAnthropicCredentials(newTokens.AccessToken, newTokens.RefreshToken, newTokens.ExpiresIn); saveErr != nil {
						a.logger.Error("Failed to save refreshed credentials", "error", saveErr)
						// Continue anyway - we have the new token in memory
					}

					// Update client with new token and retry
					a.client.SetToken(newTokens.AccessToken)
					a.lastToken = newTokens.AccessToken
					a.logger.Info("Token refreshed to bypass rate limit, retrying...")

					// Retry with fresh token
					resp, err = a.client.FetchQuotas(ctx)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						if errors.Is(err, api.ErrAnthropicRateLimited) {
							a.logger.Warn("Still rate limited after token refresh, will retry next poll")
						} else {
							a.logger.Error("Retry after token refresh failed", "error", err)
						}
						return
					}
					// Success! Fall through to process the response
					a.logger.Info("Rate limit bypassed successfully with refreshed token")
				} else {
					a.logger.Warn("Rate limit bypass unavailable - no refresh token")
					return
				}
			} else {
				a.logger.Warn("Rate limit bypass unavailable - no credentials refresh configured")
				return
			}
		}

		// Skip remaining error handling if rate limit was successfully bypassed (err is now nil)
		if err == nil {
			goto processResponse
		}

		// On auth error (401 or 403), force token re-read and retry once
		if isAuthError(err) && a.tokenRefresh != nil {
			a.logger.Warn("Anthropic auth error, forcing credential re-read", "error", err)
			a.lastToken = "" // force re-read even if token hasn't changed on disk
			if retryToken := a.tokenRefresh(); retryToken != "" {
				a.client.SetToken(retryToken)
				a.lastToken = retryToken
				a.logger.Info("Retrying with refreshed token")
				resp, err = a.client.FetchQuotas(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					// Retry also failed - count this as an auth failure
					if isAuthError(err) {
						a.authFailCount++
						a.logger.Error("Anthropic auth retry failed",
							"error", err,
							"failure_count", a.authFailCount,
							"max_failures", maxAuthFailures)

						if a.authFailCount >= maxAuthFailures {
							a.authPaused = true
							a.lastFailedToken = retryToken
							a.logger.Error("Anthropic polling PAUSED due to repeated auth failures",
								"failure_count", a.authFailCount,
								"action", "Re-authenticate with 'claude auth' to resume polling")
							a.sendAuthErrorNotification(
								"Anthropic polling paused",
								fmt.Sprintf("Repeated auth failures (%d). Re-authenticate with 'claude auth' to resume.", a.authFailCount),
								false,
							)
						}
					} else {
						a.logger.Error("Anthropic retry failed with non-auth error", "error", err)
					}
					return
				}
				// Retry succeeded - reset auth failure count and fall through
				a.authFailCount = 0
			} else {
				a.logger.Error("No Anthropic token available after re-read")
				return
			}
		} else {
			a.logger.Error("Failed to fetch Anthropic quotas", "error", err)
			return
		}
	} else {
		// Success - reset auth failure count and decay rate limit backoff
		a.authFailCount = 0
		if a.rateLimitFailCount > 0 {
			a.rateLimitFailCount--
		}
	}

processResponse:
	// Convert to snapshot and store
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertAnthropicSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Anthropic snapshot", "error", err)
		return
	}

	// Process with tracker (log error but don't stop)
	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Anthropic tracker processing failed", "error", err)
		}
	}

	// Check notification thresholds
	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "anthropic",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
			})
		}
	}

	// Report to session manager - extract utilization values for change detection.
	// Use fixed order matching UI columns: five_hour, seven_day, seven_day_sonnet
	// (alphabetical sort would put monthly_limit between them, breaking the mapping).
	if a.sm != nil {
		// Build a map for O(1) lookup
		quotaMap := make(map[string]float64, len(snapshot.Quotas))
		for _, q := range snapshot.Quotas {
			quotaMap[q.Name] = q.Utilization
		}
		// Report in fixed order matching session columns (sub, search, tool)
		values := []float64{
			quotaMap["five_hour"],        // Column 0: 5-Hour %
			quotaMap["seven_day"],        // Column 1: Weekly %
			quotaMap["seven_day_sonnet"], // Column 2: Sonnet %
		}
		a.sm.ReportPoll(values)
	}

	// Log poll completion
	quotaCount := len(snapshot.Quotas)
	var maxUtil float64
	for _, q := range snapshot.Quotas {
		if q.Utilization > maxUtil {
			maxUtil = q.Utilization
		}
	}

	a.logger.Info("Anthropic poll complete",
		"source", "api",
		"quota_count", quotaCount,
		"max_utilization", maxUtil,
	)
}
