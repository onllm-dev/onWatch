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

// GrokAgent manages the background polling loop for Grok credits tracking.
type GrokAgent struct {
	client       *api.GrokClient
	store        *store.Store
	tracker      *tracker.GrokTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled via settings).
func (a *GrokAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *GrokAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewGrokAgent creates a new GrokAgent.
func NewGrokAgent(client *api.GrokClient, store *store.Store, tr *tracker.GrokTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *GrokAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrokAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Grok agent's polling loop.
func (a *GrokAgent) Run(ctx context.Context) error {
	a.logger.Info("Grok agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Grok agent stopped")
	}()

	// Poll immediately
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

// poll performs one fetch -> store -> track -> notify -> session cycle.
func (a *GrokAgent) poll(ctx context.Context) {
	if a.client == nil {
		return
	}
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	snapshot, err := a.client.FetchSnapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Grok usage", "error", err)
		return
	}

	if _, err := a.store.InsertGrokSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Grok snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Grok tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil && len(snapshot.Quotas) > 0 {
		q := snapshot.Quotas[0] // credits primary
		a.notifier.Check(notify.QuotaStatus{
			Provider:    "grok",
			QuotaKey:    q.Name,
			Utilization: q.Utilization,
		})
	}

	if a.sm != nil {
		vals := []float64{0}
		if len(snapshot.Quotas) > 0 {
			vals[0] = snapshot.Quotas[0].Utilization
		}
		a.sm.ReportPoll(vals)
	}

	a.logger.Info("Grok poll complete",
		"email", snapshot.Email,
		"util", func() float64 {
			if len(snapshot.Quotas) > 0 {
				return snapshot.Quotas[0].Utilization
			}
			return 0
		}(),
		"resets", func() interface{} {
			if len(snapshot.Quotas) > 0 && snapshot.Quotas[0].ResetsAt != nil {
				return snapshot.Quotas[0].ResetsAt.Format(time.RFC3339)
			}
			return nil
		}(),
	)
}
