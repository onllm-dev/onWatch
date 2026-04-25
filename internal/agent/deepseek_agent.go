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

// DeepSeekAgent manages the background polling loop for DeepSeek usage tracking.
type DeepSeekAgent struct {
	client       *api.DeepSeekClient
	store        *store.Store
	tracker      *tracker.DeepSeekTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *DeepSeekAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *DeepSeekAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewDeepSeekAgent creates a new DeepSeekAgent with the given dependencies.
func NewDeepSeekAgent(client *api.DeepSeekClient, store *store.Store, tr *tracker.DeepSeekTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *DeepSeekAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeepSeekAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the DeepSeek agent's polling loop.
func (a *DeepSeekAgent) Run(ctx context.Context) error {
	a.logger.Info("DeepSeek agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("DeepSeek agent stopped")
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

// poll performs a single DeepSeek poll cycle.
func (a *DeepSeekAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	resp, err := a.client.FetchBalance(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch DeepSeek balance", "error", err)
		return
	}
	
	if !resp.IsAvailable {
		a.logger.Info("DeepSeek service is currently not available")
		return
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertDeepSeekSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert DeepSeek snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("DeepSeek tracker processing failed", "error", err)
		}
	}

	// Report to session manager for usage-based session detection
	// Inverting for balance: smaller balance means usage
	if a.sm != nil {
		a.sm.ReportPoll([]float64{
			-snapshot.TotalBalance,
		})
	}

	a.logger.Info("DeepSeek poll complete",
		"total_balance", snapshot.TotalBalance,
		"currency", snapshot.Currency,
	)
}
