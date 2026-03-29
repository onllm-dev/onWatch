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

// OpenRouterAgent manages the background polling loop for OpenRouter usage tracking.
type OpenRouterAgent struct {
	client       *api.OpenRouterClient
	store        *store.Store
	tracker      *tracker.OpenRouterTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled).
func (a *OpenRouterAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *OpenRouterAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewOpenRouterAgent creates a new OpenRouterAgent with the given dependencies.
func NewOpenRouterAgent(client *api.OpenRouterClient, store *store.Store, tr *tracker.OpenRouterTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *OpenRouterAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenRouterAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the OpenRouter agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *OpenRouterAgent) Run(ctx context.Context) error {
	a.logger.Info("OpenRouter agent started", "interval", a.interval)

	// Ensure any active session is closed on exit
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("OpenRouter agent stopped")
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

// poll performs a single OpenRouter poll cycle: fetch usage, store snapshot.
func (a *OpenRouterAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	resp, err := a.client.FetchUsage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch OpenRouter usage", "error", err)
		return
	}

	// Convert to snapshot and store
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertOpenRouterSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert OpenRouter snapshot", "error", err)
		return
	}

	// Process with tracker (log error but don't stop)
	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("OpenRouter tracker processing failed", "error", err)
		}
	}

	// Check notification thresholds
	if a.notifier != nil {
		if snapshot.Limit != nil && *snapshot.Limit > 0 {
			pct := (snapshot.Usage / *snapshot.Limit) * 100
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "openrouter",
				QuotaKey:    "credits",
				Utilization: pct,
				Limit:       *snapshot.Limit,
			})
		}
	}

	// Report to session manager for usage-based session detection
	if a.sm != nil {
		a.sm.ReportPoll([]float64{
			snapshot.Usage,
		})
	}

	// Log poll completion
	a.logger.Info("OpenRouter poll complete",
		"usage", snapshot.Usage,
		"usage_daily", snapshot.UsageDaily,
		"limit", snapshot.Limit,
		"is_free_tier", snapshot.IsFreeTier,
	)
}
