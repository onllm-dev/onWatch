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

// MiniMaxAgent manages background polling for MiniMax remains.
type MiniMaxAgent struct {
	client       *api.MiniMaxClient
	store        *store.Store
	tracker      *tracker.MiniMaxTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// NewMiniMaxAgent creates a new MiniMax polling agent.
func NewMiniMaxAgent(client *api.MiniMaxClient, store *store.Store, tr *tracker.MiniMaxTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *MiniMaxAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &MiniMaxAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// SetPollingCheck sets a provider polling guard function.
func (a *MiniMaxAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for quota notifications.
func (a *MiniMaxAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// Run starts the polling loop until context cancellation.
func (a *MiniMaxAgent) Run(ctx context.Context) error {
	a.logger.Info("MiniMax agent started", "interval", a.interval)
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("MiniMax agent stopped")
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

func (a *MiniMaxAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	resp, err := a.client.FetchRemains(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch MiniMax remains", "error", err)
		return
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertMiniMaxSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert MiniMax snapshot", "error", err)
	}

	if err := a.tracker.Process(snapshot); err != nil {
		a.logger.Error("MiniMax tracker processing failed", "error", err)
	}

	if a.notifier != nil {
		if snapshot.IsSharedQuota() {
			// Shared pool: send one notification for the entire plan
			if merged := snapshot.MergedQuota(); merged != nil && merged.Total > 0 {
				a.notifier.Check(notify.QuotaStatus{
					Provider:    "minimax",
					QuotaKey:    "coding_plan",
					Utilization: merged.UsedPercent,
					Limit:       float64(merged.Total),
				})
			}
		} else {
			for _, m := range snapshot.Models {
				if m.Total == 0 {
					continue
				}
				a.notifier.Check(notify.QuotaStatus{
					Provider:    "minimax",
					QuotaKey:    m.ModelName,
					Utilization: m.UsedPercent,
					Limit:       float64(m.Total),
				})
			}
		}
	}

	if a.sm != nil {
		values := make([]float64, 0, len(snapshot.Models))
		for _, m := range snapshot.Models {
			values = append(values, float64(m.Used))
		}
		a.sm.ReportPoll(values)
	}

	for _, m := range snapshot.Models {
		a.logger.Info("MiniMax poll complete",
			"model", m.ModelName,
			"used", m.Used,
			"total", m.Total,
			"remain", m.Remain,
		)
	}
}
