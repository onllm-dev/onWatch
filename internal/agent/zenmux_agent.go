package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type zenmuxFetcher interface {
	FetchSnapshot(context.Context) (*api.ZenmuxSnapshot, error)
}

type zenmuxNotifier interface {
	Check(notify.QuotaStatus)
}

type ZenmuxAgent struct {
	client       zenmuxFetcher
	store        *store.Store
	interval     time.Duration
	logger       *slog.Logger
	notifier     zenmuxNotifier
	pollingCheck func() bool
}

func NewZenmuxAgent(client zenmuxFetcher, store *store.Store, interval time.Duration, logger *slog.Logger, notifier zenmuxNotifier) *ZenmuxAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &ZenmuxAgent{client: client, store: store, interval: interval, logger: logger, notifier: notifier}
}
func (a *ZenmuxAgent) SetPollingCheck(check func() bool) { a.pollingCheck = check }
func (a *ZenmuxAgent) Run(ctx context.Context) error {
	a.poll(ctx)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}
func (a *ZenmuxAgent) poll(ctx context.Context) {
	if a.client == nil || a.store == nil || (a.pollingCheck != nil && !a.pollingCheck()) {
		return
	}
	snap, err := a.client.FetchSnapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			if errors.Is(err, api.ErrZenmuxRateLimited) {
				a.logger.Warn("Zenmux rate-limited")
			} else {
				a.logger.Error("Zenmux polling failed", "error", err)
			}
			pollFailed("zenmux", "", err)
		}
		return
	}
	var previous *api.ZenmuxSnapshot
	if a.notifier != nil {
		previous, err = a.store.QueryLatestZenmux()
		if err != nil {
			a.logger.Warn("Could not read previous Zenmux snapshot", "error", err)
		}
	}
	if _, err := a.store.InsertZenmuxSnapshot(snap); err != nil {
		a.logger.Error("Zenmux snapshot storage failed", "error", err)
		return
	}
	pollSucceeded("zenmux", "")
	if a.notifier != nil {
		var oldFiveHour, oldWeekly *api.ZenmuxWindow
		if previous != nil {
			oldFiveHour, oldWeekly = previous.Quota5Hour, previous.Quota7Day
		}
		for _, item := range []struct {
			name     string
			window   *api.ZenmuxWindow
			previous *api.ZenmuxWindow
		}{{"five_hour", snap.Quota5Hour, oldFiveHour}, {"weekly", snap.Quota7Day, oldWeekly}} {
			if item.window == nil || item.window.UsagePercentage == nil {
				continue
			}
			if item.previous != nil && item.previous.ResetsAt != nil && item.window.ResetsAt != nil &&
				item.window.ResetsAt.After(*item.previous.ResetsAt) &&
				(!snap.CapturedAt.Before(*item.previous.ResetsAt) || (item.previous.UsagePercentage != nil && *item.window.UsagePercentage < *item.previous.UsagePercentage)) {
				a.notifier.Check(notify.QuotaStatus{Provider: "zenmux", QuotaKey: item.name, ResetOccurred: true})
			}
			limit := 0.0
			if item.window.MaxFlows != nil {
				limit = *item.window.MaxFlows
			}
			a.notifier.Check(notify.QuotaStatus{Provider: "zenmux", QuotaKey: item.name, Utilization: *item.window.UsagePercentage * 100, Limit: limit, ResetAt: derefTime(item.window.ResetsAt)})
		}
	}
}
