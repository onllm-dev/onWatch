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
