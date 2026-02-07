// Package agent provides the background polling agent for SynTrack.
package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/onllm-dev/syntrack/internal/api"
	"github.com/onllm-dev/syntrack/internal/store"
)

// ZaiAgent manages the background polling loop for Z.ai quota tracking.
type ZaiAgent struct {
	client   *api.ZaiClient
	store    *store.Store
	interval time.Duration
	logger   *slog.Logger
}

// NewZaiAgent creates a new ZaiAgent with the given dependencies.
func NewZaiAgent(client *api.ZaiClient, store *store.Store, interval time.Duration, logger *slog.Logger) *ZaiAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &ZaiAgent{
		client:   client,
		store:    store,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the Z.ai agent's polling loop. It polls immediately,
// then continues at the configured interval until the context is cancelled.
func (a *ZaiAgent) Run(ctx context.Context) error {
	a.logger.Info("Z.ai agent started", "interval", a.interval)

	defer func() {
		a.logger.Info("Z.ai agent stopped")
	}()

	// Poll immediately on start
	a.poll(ctx)

	// Create ticker for periodic polling
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Main polling loop
	for {
		select {
		case <-ticker.C:
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// poll performs a single Z.ai poll cycle: fetch quotas, store snapshot.
func (a *ZaiAgent) poll(ctx context.Context) {
	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("Failed to fetch Z.ai quotas", "error", err)
		return
	}

	// Convert to snapshot and store
	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if _, err := a.store.InsertZaiSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Z.ai snapshot", "error", err)
		return
	}

	// Log poll completion
	a.logger.Info("Z.ai poll complete",
		"time_usage", snapshot.TimeUsage,
		"time_limit", snapshot.TimeLimit,
		"tokens_usage", snapshot.TokensUsage,
		"tokens_limit", snapshot.TokensLimit,
		"tokens_percentage", snapshot.TokensPercentage,
	)
}
