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

// maxOpenCodeGoAuthFailures is the number of consecutive auth failures before pausing polling.
const maxOpenCodeGoAuthFailures = 5

// isOpenCodeGoAuthError returns true if the error is an authentication/authorization error.
func isOpenCodeGoAuthError(err error) bool {
	return errors.Is(err, api.ErrOpenCodeGoUnauthorized) || errors.Is(err, api.ErrOpenCodeGoNotSignedIn)
}

// OpenCodeGoAgent manages the background polling loop for OpenCode Go quota tracking.
type OpenCodeGoAgent struct {
	client       *api.OpenCodeGoClient
	store        *store.Store
	tracker      *tracker.OpenCodeGoTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool

	// Auth failure rate limiting
	authFailCount   int
	authPaused      bool
	lastFailedCookie string
}

// NewOpenCodeGoAgent creates a new OpenCodeGoAgent with the given dependencies.
func NewOpenCodeGoAgent(client *api.OpenCodeGoClient, st *store.Store, track *tracker.OpenCodeGoTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *OpenCodeGoAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenCodeGoAgent{
		client:   client,
		store:    st,
		tracker:  track,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// SetPollingCheck sets a function called before each poll.
func (a *OpenCodeGoAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets notification engine for sending alerts.
func (a *OpenCodeGoAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *OpenCodeGoAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "opencodego",
		Title:       title,
		Message:     message,
		IsRecovable: isRecoverable,
	})
}

// Run starts the agent polling loop.
func (a *OpenCodeGoAgent) Run(ctx context.Context) error {
	a.logger.Info("OpenCode Go agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("OpenCode Go agent stopped")
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

func (a *OpenCodeGoAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	if a.authPaused {
		return
	}

	snapshot, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		if isOpenCodeGoAuthError(err) {
			currentCookie := a.client.GetCookie()
			a.authFailCount++
			a.logger.Error("OpenCode Go auth error",
				"error", err,
				"failure_count", a.authFailCount,
				"max_failures", maxOpenCodeGoAuthFailures)

			if a.authFailCount >= maxOpenCodeGoAuthFailures {
				a.authPaused = true
				a.lastFailedCookie = currentCookie
				a.logger.Error("OpenCode Go polling PAUSED due to repeated auth failures")
				a.sendAuthErrorNotification(
					"Authentication Failed",
					"OpenCode Go polling has been paused due to repeated authentication failures. Please update your OPENCODEGO_COOKIE to resume.",
					false,
				)
			}
		} else {
			a.logger.Error("Failed to fetch OpenCode Go quotas", "error", err)
		}
		return
	}

	// Success - reset auth failure count
	a.authFailCount = 0

	if _, err := a.store.InsertOpenCodeGoSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert OpenCode Go snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("OpenCode Go tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, w := range snapshot.Windows {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "opencodego",
				QuotaKey:    w.WindowName,
				Utilization: w.UsagePercent,
				Limit:       100,
			})
		}
	}

	if a.sm != nil {
		values := make([]float64, 0, len(snapshot.Windows))
		for _, w := range snapshot.Windows {
			values = append(values, w.UsagePercent)
		}
		a.sm.ReportPoll(values)
	}

	for _, w := range snapshot.Windows {
		a.logger.Info("OpenCode Go poll complete",
			"window", w.WindowName,
			"usage", fmt.Sprintf("%.1f%%", w.UsagePercent),
			"reset_in_sec", w.ResetInSec)
	}
}
