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

// MoonshotAgent manages the background polling loop for Moonshot usage tracking.
type MoonshotAgent struct {
	client       *api.MoonshotClient
	store        *store.Store
	tracker      *tracker.MoonshotTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *MoonshotAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *MoonshotAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewMoonshotAgent creates a new MoonshotAgent with the given dependencies.
func NewMoonshotAgent(client *api.MoonshotClient, store *store.Store, tr *tracker.MoonshotTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *MoonshotAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &MoonshotAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Moonshot agent's polling loop.
func (a *MoonshotAgent) Run(ctx context.Context) error {
	a.logger.Info("Moonshot agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Moonshot agent stopped")
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

// poll performs a single Moonshot poll cycle.
func (a *MoonshotAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}

	resp, err := a.client.FetchBalance(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Moonshot balance", "error", err)
		return
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertMoonshotSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Moonshot snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Moonshot tracker processing failed", "error", err)
		}
	}

	// Report to session manager for usage-based session detection
	// Inverting for balance: smaller balance means usage
	if a.sm != nil {
		a.sm.ReportPoll([]float64{
			-snapshot.AvailableBalance,
		})
	}

	a.logger.Info("Moonshot poll complete",
		"available_balance", snapshot.AvailableBalance,
	)
}
