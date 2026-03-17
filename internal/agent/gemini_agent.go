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

// maxGeminiAuthFailures is the number of consecutive auth failures before pausing polling.
const maxGeminiAuthFailures = 3

// geminiTokenRefreshThreshold is how soon before expiry we proactively refresh.
// Google tokens expire in ~1hr, so 15 minutes provides a comfortable buffer.
const geminiTokenRefreshThreshold = 15 * time.Minute

// GeminiCredentialsRefreshFunc returns fresh credentials from disk.
type GeminiCredentialsRefreshFunc func() *api.GeminiCredentials

// isGeminiAuthError returns true if the error is an authentication/authorization error.
func isGeminiAuthError(err error) bool {
	return errors.Is(err, api.ErrGeminiUnauthorized) || errors.Is(err, api.ErrGeminiForbidden)
}

// GeminiAgent manages the background polling loop for Gemini quota tracking.
type GeminiAgent struct {
	client       *api.GeminiClient
	store        *store.Store
	tracker      *tracker.GeminiTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	credsRefresh GeminiCredentialsRefreshFunc
	clientCreds  *api.GeminiClientCredentials
	lastToken    string

	// Auth failure rate limiting
	authFailCount   int
	authPaused      bool
	lastFailedToken string

	// Tier caching
	tierFetched bool
}

// NewGeminiAgent creates a new GeminiAgent with the given dependencies.
func NewGeminiAgent(client *api.GeminiClient, st *store.Store, tracker *tracker.GeminiTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *GeminiAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &GeminiAgent{
		client:   client,
		store:    st,
		tracker:  tracker,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// SetPollingCheck sets a function called before each poll.
func (a *GeminiAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets notification engine for sending alerts.
func (a *GeminiAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// SetCredentialsRefresh sets a function that returns fresh credentials for proactive OAuth refresh.
func (a *GeminiAgent) SetCredentialsRefresh(fn GeminiCredentialsRefreshFunc) {
	a.credsRefresh = fn
}

// SetClientCredentials sets the OAuth client credentials for token refresh.
func (a *GeminiAgent) SetClientCredentials(creds *api.GeminiClientCredentials) {
	a.clientCreds = creds
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *GeminiAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "gemini",
		Title:       title,
		Message:     message,
		IsRecovable: isRecoverable,
	})
}

// Run starts the agent polling loop.
func (a *GeminiAgent) Run(ctx context.Context) error {
	a.logger.Info("Gemini agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Gemini agent stopped")
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

func (a *GeminiAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	// Proactive OAuth refresh
	if a.credsRefresh != nil && a.clientCreds != nil {
		if creds := a.credsRefresh(); creds != nil {
			if creds.IsExpiringSoon(geminiTokenRefreshThreshold) && creds.RefreshToken != "" {
				a.logger.Info("Gemini token expiring soon, attempting proactive OAuth refresh",
					"expires_in", creds.ExpiresIn.Round(time.Second))

				newTokens, err := api.RefreshGeminiToken(ctx,
					creds.RefreshToken,
					a.clientCreds.ClientID,
					a.clientCreds.ClientSecret,
				)
				if err != nil {
					a.logger.Error("Proactive Gemini OAuth refresh failed", "error", err)
				} else {
					// Google does NOT rotate refresh tokens - only save access_token + expiry
					if err := api.WriteGeminiCredentials(newTokens.AccessToken, newTokens.ExpiresIn); err != nil {
						a.logger.Error("Failed to save refreshed Gemini credentials", "error", err)
					} else {
						a.client.SetToken(newTokens.AccessToken)
						a.lastToken = newTokens.AccessToken
						a.logger.Info("Proactively refreshed Gemini OAuth token",
							"expires_in_seconds", newTokens.ExpiresIn)

						if a.authPaused {
							a.authPaused = false
							a.authFailCount = 0
							a.lastFailedToken = ""
							a.logger.Info("Gemini auth failure pause lifted - token refreshed via OAuth")
						}
					}
				}
			}

			// Check if credentials changed on disk (user re-authed via CLI)
			if creds.AccessToken != "" && creds.AccessToken != a.lastToken {
				a.client.SetToken(creds.AccessToken)
				a.lastToken = creds.AccessToken
				a.logger.Info("Gemini token refreshed from credentials file")

				if a.authPaused && creds.AccessToken != a.lastFailedToken {
					a.authPaused = false
					a.authFailCount = 0
					a.lastFailedToken = ""
					a.logger.Info("Gemini auth failure pause lifted - new credentials detected")
				}
			}
		}
	}

	if a.authPaused {
		return
	}

	// Fetch tier on first poll to get project ID
	if !a.tierFetched {
		tierResp, err := a.client.FetchTier(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.logger.Warn("Failed to fetch Gemini tier", "error", err)
		} else {
			if tierResp.CloudAICompanionProject != "" {
				a.client.SetProjectID(tierResp.CloudAICompanionProject)
				a.logger.Info("Gemini tier detected",
					"tier", tierResp.Tier,
					"project", tierResp.CloudAICompanionProject)
			}
			a.tierFetched = true
		}
	}

	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		if isGeminiAuthError(err) && a.credsRefresh != nil && a.clientCreds != nil {
			a.logger.Warn("Gemini auth error, attempting token refresh", "error", err)

			if creds := a.credsRefresh(); creds != nil && creds.RefreshToken != "" {
				newTokens, refreshErr := api.RefreshGeminiToken(ctx,
					creds.RefreshToken,
					a.clientCreds.ClientID,
					a.clientCreds.ClientSecret,
				)
				if refreshErr == nil {
					if err := api.WriteGeminiCredentials(newTokens.AccessToken, newTokens.ExpiresIn); err != nil {
						a.logger.Error("Failed to save refreshed Gemini credentials", "error", err)
					}
					a.client.SetToken(newTokens.AccessToken)
					a.lastToken = newTokens.AccessToken
					a.logger.Info("Retrying Gemini poll with refreshed token")

					resp, err = a.client.FetchQuotas(ctx)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						if isGeminiAuthError(err) {
							a.authFailCount++
							a.logger.Error("Gemini auth retry failed",
								"error", err,
								"failure_count", a.authFailCount,
								"max_failures", maxGeminiAuthFailures)

							if a.authFailCount >= maxGeminiAuthFailures {
								a.authPaused = true
								a.lastFailedToken = newTokens.AccessToken
								a.logger.Error("Gemini polling PAUSED due to repeated auth failures")
								a.sendAuthErrorNotification(
									"Authentication Failed",
									"Gemini polling has been paused due to repeated authentication failures. Please re-authenticate via 'gemini auth' to resume.",
									false,
								)
							}
						} else {
							a.logger.Error("Gemini retry failed with non-auth error", "error", err)
						}
						return
					}
					a.authFailCount = 0
				} else {
					a.logger.Error("Gemini OAuth refresh failed on auth error", "error", refreshErr)
					a.authFailCount++
					if a.authFailCount >= maxGeminiAuthFailures {
						a.authPaused = true
						a.logger.Error("Gemini polling PAUSED due to repeated auth failures")
						a.sendAuthErrorNotification(
							"Authentication Failed",
							"Gemini polling has been paused. Please re-authenticate via 'gemini auth' to resume.",
							false,
						)
					}
					return
				}
			} else {
				a.logger.Error("No Gemini refresh token available for retry")
				return
			}
		} else {
			a.logger.Error("Failed to fetch Gemini quotas", "error", err)
			return
		}
	} else {
		a.authFailCount = 0
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertGeminiSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Gemini snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Gemini tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "gemini",
				QuotaKey:    q.ModelID,
				Utilization: q.UsagePercent,
				Limit:       100,
			})
		}
	}

	if a.sm != nil {
		values := make([]float64, 0, len(snapshot.Quotas))
		for _, q := range snapshot.Quotas {
			values = append(values, q.UsagePercent)
		}
		a.sm.ReportPoll(values)
	}

	for _, q := range snapshot.Quotas {
		a.logger.Info("Gemini poll complete",
			"model", q.ModelID,
			"remaining", fmt.Sprintf("%.1f%%", q.RemainingFraction*100),
			"usage", fmt.Sprintf("%.1f%%", q.UsagePercent))
	}
}
