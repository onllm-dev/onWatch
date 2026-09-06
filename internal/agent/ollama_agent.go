package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type ollamaFetcher interface {
	FetchSnapshot(ctx context.Context) (*api.OllamaSnapshot, error)
}

type OllamaAgent struct {
	client       ollamaFetcher
	store        *store.Store
	tracker      *tracker.OllamaTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	cfg          *config.Config
}

func (a *OllamaAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

func (a *OllamaAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

func NewOllamaAgent(client ollamaFetcher, store *store.Store, tr *tracker.OllamaTracker, cfg *config.Config, interval time.Duration, logger *slog.Logger, sm *SessionManager) *OllamaAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &OllamaAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		cfg:      cfg,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

func (a *OllamaAgent) Run(ctx context.Context) error {
	a.logger.Info("Ollama agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Ollama agent stopped")
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

func (a *OllamaAgent) poll(ctx context.Context) {
	if a.client == nil || a.cfg == nil {
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
		a.logger.Error("Failed to fetch Ollama quotas", "error", err)
		return
	}

	a.applyLearnedReset(snapshot)

	if _, err := a.store.InsertOllamaSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Ollama snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Ollama tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "ollama",
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

	a.logger.Info("Ollama poll complete",
		"plan", snapshot.Plan,
		"monthly_used_usd", snapshot.MonthlyUsedUSD,
		"quota_count", len(snapshot.Quotas),
	)
}

const (
	// ollamaResetAnchorSetting persists the moment a real monthly reset was
	// observed so the reset day survives restarts.
	ollamaResetAnchorSetting = "ollama_reset_anchor"
	// ollamaResetDropUSD is the drop in included usage that counts as a reset
	// (usage only ever grows within a cycle; half a cent absorbs rounding).
	ollamaResetDropUSD = 0.005
)

// applyLearnedReset detects a real monthly reset (included usage fell since
// the previous snapshot), records that moment, and re-anchors the quota's
// ResetsAt to it. The account-anniversary guess is right for Free accounts
// but paid plans reset on the subscription start day, which the API does
// not expose. An explicit OLLAMA_RESET_DAY always wins.
func (a *OllamaAgent) applyLearnedReset(snapshot *api.OllamaSnapshot) {
	if a.store == nil || snapshot == nil || a.cfg.OllamaResetDay > 0 {
		return
	}
	prev, err := a.store.QueryLatestOllama()
	if err != nil {
		a.logger.Warn("Ollama: could not load previous snapshot for reset detection", "error", err)
	} else if prev != nil && prev.MonthlyUsedUSD-snapshot.MonthlyUsedUSD > ollamaResetDropUSD {
		anchor := snapshot.CapturedAt.UTC().Format(time.RFC3339)
		if err := a.store.SetSetting(ollamaResetAnchorSetting, anchor); err != nil {
			a.logger.Warn("Ollama: failed to persist learned reset anchor", "error", err)
		} else {
			a.logger.Info("Ollama included usage dropped - learned monthly reset day",
				"previous_usd", prev.MonthlyUsedUSD,
				"current_usd", snapshot.MonthlyUsedUSD,
				"reset_day", snapshot.CapturedAt.UTC().Day(),
			)
		}
	}

	stored, err := a.store.GetSetting(ollamaResetAnchorSetting)
	if err != nil || stored == "" {
		return
	}
	anchor, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		a.logger.Warn("Ollama: ignoring malformed learned reset anchor", "value", stored)
		return
	}
	snapshot.ApplyResetAnchor(anchor)
}
