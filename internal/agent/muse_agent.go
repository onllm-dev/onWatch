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

// museFetcher is the usage-probe surface the Muse agent needs.
type museFetcher interface {
	FetchSnapshot(ctx context.Context) (*api.MuseSnapshot, error)
}

// MuseAgent manages the background polling loop for Muse coding-plan usage.
// Each poll sends one minimal streamed probe to the Meta Model API.
type MuseAgent struct {
	client       museFetcher
	store        *store.Store
	tracker      *tracker.MuseTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *MuseAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *MuseAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewMuseAgent creates a new MuseAgent with the given dependencies.
func NewMuseAgent(client museFetcher, store *store.Store, tr *tracker.MuseTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *MuseAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &MuseAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Muse agent's polling loop.
func (a *MuseAgent) Run(ctx context.Context) error {
	a.logger.Info("Muse agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Muse agent stopped")
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

// poll performs a single Muse poll cycle.
func (a *MuseAgent) poll(ctx context.Context) {
	if a.client == nil {
		return
	}
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	snapshot, err := a.client.FetchSnapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Muse quotas", "error", err)
		return
	}

	if _, err := a.store.InsertMuseSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Muse snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Muse tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "muse",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
				Limit:       q.Limit,
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

	a.logger.Info("Muse poll complete",
		"window_used_pct", snapshot.WindowUsedPct,
		"weekly_used_pct", snapshot.WeeklyUsedPct,
		"quotas", len(snapshot.Quotas),
	)
}
