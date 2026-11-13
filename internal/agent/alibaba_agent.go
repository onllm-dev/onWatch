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

// alibabaFetcher is the usage surface the agent needs.
type alibabaFetcher interface {
	FetchSnapshot(ctx context.Context) (*api.AlibabaSnapshot, error)
}

// AlibabaAgent manages background polling for one Alibaba Token Plan account.
// Every poll is a set of read-only console calls, so it costs no quota.
type AlibabaAgent struct {
	client       alibabaFetcher
	store        *store.Store
	tracker      *tracker.AlibabaTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	accountID    int64
}

// NewAlibabaAgent creates a new Alibaba polling agent.
func NewAlibabaAgent(client alibabaFetcher, store *store.Store, tr *tracker.AlibabaTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *AlibabaAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &AlibabaAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// NewAlibabaAgentWithAccount creates a new Alibaba polling agent scoped to a specific account.
func NewAlibabaAgentWithAccount(client alibabaFetcher, store *store.Store, tr *tracker.AlibabaTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager, accountID int64) *AlibabaAgent {
	ag := NewAlibabaAgent(client, store, tr, interval, logger, sm)
	ag.accountID = accountID
	return ag
}

// SetPollingCheck sets a provider polling guard function.
func (a *AlibabaAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for quota notifications.
func (a *AlibabaAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// Run starts the polling loop until context cancellation.
func (a *AlibabaAgent) Run(ctx context.Context) error {
	a.logger.Info("Alibaba agent started", "interval", a.interval, "account", a.accountID)
	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Alibaba agent stopped", "account", a.accountID)
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

func (a *AlibabaAgent) poll(ctx context.Context) {
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
		if api.IsAlibabaRateLimited(err) {
			a.logger.Warn("Alibaba console rate-limited; skipping this cycle", "account", a.accountID)
		} else {
			a.logger.Error("Failed to fetch Alibaba quotas", "account", a.accountID, "error", err)
		}
		pollFailed("alibaba", pollStatusAccountKey(a.accountID), err)
		return
	}

	pollSucceeded("alibaba", pollStatusAccountKey(a.accountID))
	if _, err := a.store.InsertAlibabaSnapshot(snapshot, a.accountID); err != nil {
		a.logger.Error("Failed to insert Alibaba snapshot", "account", a.accountID, "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot, a.accountID); err != nil {
			a.logger.Error("Alibaba tracker processing failed", "account", a.accountID, "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "alibaba",
				QuotaKey:    q.Name,
				AccountID:   pollStatusAccountKey(a.accountID),
				Utilization: q.Utilization,
				Limit:       100,
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

	a.logger.Info("Alibaba poll complete",
		"account", a.accountID,
		"quotas", len(snapshot.Quotas),
		"spec", snapshot.SpecCode,
		"remaining_days", snapshot.RemainingDays,
	)
}
