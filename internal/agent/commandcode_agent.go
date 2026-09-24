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

// commandCodeFetcher is the usage surface the agent needs.
type commandCodeFetcher interface {
	FetchSnapshot(ctx context.Context) (*api.CommandCodeSnapshot, error)
}

// CommandCodeAgent manages the background polling loop for Command Code.
// Every poll is a set of read-only GETs, so it costs no credits.
type CommandCodeAgent struct {
	client       commandCodeFetcher
	store        *store.Store
	tracker      *tracker.CommandCodeTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *CommandCodeAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *CommandCodeAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewCommandCodeAgent creates a new CommandCodeAgent.
func NewCommandCodeAgent(client commandCodeFetcher, store *store.Store, tr *tracker.CommandCodeTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *CommandCodeAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommandCodeAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the agent's polling loop.
func (a *CommandCodeAgent) Run(ctx context.Context) error {
	a.logger.Info("Command Code agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Command Code agent stopped")
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

func (a *CommandCodeAgent) poll(ctx context.Context) {
	if a.client == nil || a.store == nil {
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
		if api.IsCommandCodeRateLimited(err) {
			a.logger.Warn("Command Code API rate-limited; skipping this cycle")
			return
		}
		a.logger.Error("Failed to fetch Command Code quotas", "error", err)
		return
	}

	if _, err := a.store.InsertCommandCodeSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Command Code snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Command Code tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "commandcode",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
				Limit:       q.Limit,
				ResetAt:     derefTime(q.ResetsAt),
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

	a.logger.Info("Command Code poll complete",
		"quotas", len(snapshot.Quotas),
		"remaining_credits", snapshot.RemainingCredits,
		"period_cost_usd", snapshot.PeriodCostUSD,
	)
}
