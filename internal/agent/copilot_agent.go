package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/internal/api"
	"github.com/onllm-dev/onwatch/internal/notify"
	"github.com/onllm-dev/onwatch/internal/store"
	"github.com/onllm-dev/onwatch/internal/tracker"
)

// CopilotAgent manages the background polling loop for Copilot quota tracking.
type CopilotAgent struct {
	client       *api.CopilotClient
	store        *store.Store
	tracker      *tracker.CopilotTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
// If it returns false, the poll is skipped (provider polling disabled).
func (a *CopilotAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *CopilotAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewCopilotAgent creates a new CopilotAgent with the given dependencies.
func NewCopilotAgent(client *api.CopilotClient, store *store.Store, tracker *tracker.CopilotTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *CopilotAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &CopilotAgent{
		client:   client,
		store:    store,
		tracker:  tracker,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *CopilotAgent) Run(ctx context.Context) error {
	a.logger.Info("Copilot agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Copilot agent stopped")
	}()

	// Poll immediately on start
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

// poll performs a single poll cycle: fetch quotas, store snapshot, update tracker.
func (a *CopilotAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Copilot quotas", "error", err)
		return
	}

	// Convert API response to snapshot
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	// Store snapshot
	if _, err := a.store.InsertCopilotSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Copilot snapshot", "error", err)
	}

	// Process with tracker
	if err := a.tracker.Process(snapshot); err != nil {
		a.logger.Error("Copilot tracker processing failed", "error", err)
	}

	// Check notification thresholds for non-unlimited quotas
	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			if q.Unlimited || q.Entitlement == 0 {
				continue
			}
			used := q.Entitlement - q.Remaining
			utilization := float64(used) / float64(q.Entitlement) * 100
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "copilot",
				QuotaKey:    q.Name,
				Utilization: utilization,
				Limit:       float64(q.Entitlement),
			})
		}
	}

	// Report to session manager for usage-based session detection
	if a.sm != nil {
		var values []float64
		for _, q := range snapshot.Quotas {
			values = append(values, float64(q.Entitlement-q.Remaining))
		}
		a.sm.ReportPoll(values)
	}

	// Log poll completion
	for _, q := range snapshot.Quotas {
		if !q.Unlimited {
			a.logger.Info("Copilot poll complete",
				"quota", q.Name,
				"entitlement", q.Entitlement,
				"remaining", q.Remaining,
				"plan", resp.CopilotPlan,
			)
		}
	}
}
