package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type openCodeFetcher interface {
	FetchSnapshot(ctx context.Context, workspaceID, authCookie string) (*api.OpenCodeSnapshot, error)
	FetchUsageSnapshot(ctx context.Context, apiKey string) (*api.OpenCodeSnapshot, error)
}

type OpenCodeAgent struct {
	client       openCodeFetcher
	store        *store.Store
	tracker      *tracker.OpenCodeTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
	// Credentials are captured by value when the agent is built so a poll never
	// reads the live config. The settings save path mutates the shared config
	// while agents poll (reconcileProviderAgents), so reading it here would be a
	// data race. A key or cookie change restarts the agent with a fresh copy.
	apiKey      string
	workspaceID string
	authCookie  string
}

func (a *OpenCodeAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

func (a *OpenCodeAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

func NewOpenCodeAgent(client openCodeFetcher, store *store.Store, tr *tracker.OpenCodeTracker, cfg *config.Config, interval time.Duration, logger *slog.Logger, sm *SessionManager) *OpenCodeAgent {
	if logger == nil {
		logger = slog.Default()
	}
	a := &OpenCodeAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
	if cfg != nil {
		a.apiKey = cfg.OpenCodeGoAPIKey
		a.workspaceID = cfg.OpenCodeGoWorkspaceID
		a.authCookie = cfg.OpenCodeGoAuthCookie
	}
	return a
}

func (a *OpenCodeAgent) Run(ctx context.Context) error {
	a.logger.Info("OpenCode agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("OpenCode agent stopped")
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

func (a *OpenCodeAgent) poll(ctx context.Context) {
	if a.client == nil {
		return
	}
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	snapshot, err := a.fetch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, api.ErrOpenCodeRateLimited) {
			a.logger.Warn("OpenCode Go status API rate-limited; skipping this cycle")
			return
		}
		a.logger.Error("Failed to fetch OpenCode quotas", "error", err)
		pollFailed("opencode", "", err)
		return
	}

	pollSucceeded("opencode", "")
	if _, err := a.store.InsertOpenCodeSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert OpenCode snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("OpenCode tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "opencode",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
				Limit:       q.Limit,
				ResetAt:     derefTime(q.ResetsAt),
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

	a.logger.Info("OpenCode poll complete",
		"plan_name", snapshot.PlanName,
		"quota_count", len(snapshot.Quotas),
	)
}

// fetch reads the Go status API with the service-account key when one is set,
// otherwise with the workspace ID + console session cookie.
func (a *OpenCodeAgent) fetch(ctx context.Context) (*api.OpenCodeSnapshot, error) {
	if a.apiKey != "" {
		return a.client.FetchUsageSnapshot(ctx, a.apiKey)
	}
	return a.client.FetchSnapshot(ctx, a.workspaceID, a.authCookie)
}
