// Package agent provides the background polling agent for onWatch.
package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// ZaiAgent manages the background polling loop for Z.ai quota tracking.
type ZaiAgent struct {
	client       *api.ZaiClient
	store        *store.Store
	tracker      *tracker.ZaiTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled).
func (a *ZaiAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *ZaiAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewZaiAgent creates a new ZaiAgent with the given dependencies.
func NewZaiAgent(client *api.ZaiClient, store *store.Store, tr *tracker.ZaiTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *ZaiAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &ZaiAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Z.ai agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *ZaiAgent) Run(ctx context.Context) error {
	a.logger.Info("Z.ai agent started", "interval", a.interval)

	// Ensure any active session is closed on exit
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Z.ai agent stopped")
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
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// poll performs a single Z.ai poll cycle: fetch quotas, store snapshot.
func (a *ZaiAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Z.ai quotas", "error", err)
		return
	}

	// Convert to snapshot and store
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertZaiSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Z.ai snapshot", "error", err)
		return
	}

	// Process with tracker (log error but don't stop)
	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Z.ai tracker processing failed", "error", err)
		}
	}

	// Check notification thresholds
	if a.notifier != nil {
		if snapshot.TokensUsage > 0 {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "zai",
				QuotaKey:    "tokens",
				Utilization: float64(snapshot.TokensPercentage),
				Limit:       snapshot.TokensUsage,
			})
		}
		if snapshot.TimeUsage > 0 {
			pct := (snapshot.TimeCurrentValue / snapshot.TimeUsage) * 100
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "zai",
				QuotaKey:    "time",
				Utilization: pct,
				Limit:       snapshot.TimeUsage,
			})
		}
	}

	// Report to session manager for usage-based session detection
	if a.sm != nil {
		a.sm.ReportPoll([]float64{
			snapshot.TokensCurrentValue,
			snapshot.TimeCurrentValue,
		})
	}

	// Log poll completion.
	//
	// Field names follow what the numbers mean, not what Z.ai calls them: the
	// API's "usage" is the total budget and its "currentValue" is the amount
	// actually consumed (see the dashboard mapping in web/handlers.go). Logging
	// them under the API's own names made the line read as self-contradictory -
	// a zero "usage" next to a 68% utilization - which is what prompted the
	// secondary report in issue #112. Unit*Number is the window descriptor
	// rather than a budget, so it is logged as such.
	a.logger.Info("Z.ai poll complete",
		"time_budget", snapshot.TimeUsage,
		"time_used", snapshot.TimeCurrentValue,
		"time_percentage", snapshot.TimePercentage,
		"time_window", snapshot.TimeLimit,
		"tokens_budget", snapshot.TokensUsage,
		"tokens_used", snapshot.TokensCurrentValue,
		"tokens_percentage", snapshot.TokensPercentage,
		"tokens_window", snapshot.TokensLimit,
		"time_limit_type", snapshot.TimeLimitType,
		"tokens_limit_type", snapshot.TokensLimitType,
	)
}
