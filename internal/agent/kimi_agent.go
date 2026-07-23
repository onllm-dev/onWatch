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

// KimiAgent manages the background polling loop for Kimi Code quotas.
type KimiAgent struct {
	client       *api.KimiClient
	store        *store.Store
	tracker      *tracker.KimiTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *KimiAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *KimiAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewKimiAgent creates a new KimiAgent.
func NewKimiAgent(client *api.KimiClient, store *store.Store, tr *tracker.KimiTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *KimiAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &KimiAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Kimi agent's polling loop.
func (a *KimiAgent) Run(ctx context.Context) error {
	a.logger.Info("Kimi Code agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Kimi Code agent stopped")
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

func (a *KimiAgent) poll(ctx context.Context) {
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
		a.logger.Error("Failed to fetch Kimi Code usages", "error", err)
		return
	}

	if _, err := a.store.InsertKimiSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Kimi snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Kimi tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil && len(snapshot.Quotas) > 0 {
		q := snapshot.Quotas[0]
		a.notifier.Check(notify.QuotaStatus{
			Provider:    "kimi",
			QuotaKey:    q.Name,
			Utilization: q.Utilization,
		})
	}

	if a.sm != nil {
		vals := make([]float64, len(snapshot.Quotas))
		for i, q := range snapshot.Quotas {
			vals[i] = q.Utilization
		}
		if len(vals) == 0 {
			vals = []float64{0}
		}
		a.sm.ReportPoll(vals)
	}

	primaryUtil := 0.0
	var resets interface{}
	if len(snapshot.Quotas) > 0 {
		primaryUtil = snapshot.Quotas[0].Utilization
		if snapshot.Quotas[0].ResetsAt != nil {
			resets = snapshot.Quotas[0].ResetsAt.Format(time.RFC3339)
		}
	}
	a.logger.Info("Kimi Code poll complete",
		"user_id", snapshot.UserID,
		"membership", snapshot.Membership,
		"quota_count", len(snapshot.Quotas),
		"util", primaryUtil,
		"resets", resets,
	)
}
