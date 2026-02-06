// Package agent provides the background polling agent for SynTrack.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/onllm-dev/syntrack/internal/api"
	"github.com/onllm-dev/syntrack/internal/store"
	"github.com/onllm-dev/syntrack/internal/tracker"
)

// Agent manages the background polling loop for quota tracking.
type Agent struct {
	client    *api.Client
	store     *store.Store
	tracker   *tracker.Tracker
	interval  time.Duration
	logger    *slog.Logger
	sessionID string
}

// New creates a new Agent with the given dependencies.
func New(client *api.Client, store *store.Store, tracker *tracker.Tracker, interval time.Duration, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{
		client:   client,
		store:    store,
		tracker:  tracker,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the agent's polling loop. It creates a session, polls immediately,
// then continues at the configured interval until the context is cancelled.
// On context cancellation, it gracefully shuts down and closes the session.
func (a *Agent) Run(ctx context.Context) error {
	// Close any orphaned sessions from previous runs (e.g., process was killed)
	if closed, err := a.store.CloseOrphanedSessions(); err != nil {
		a.logger.Warn("Failed to close orphaned sessions", "error", err)
	} else if closed > 0 {
		a.logger.Info("Closed orphaned sessions", "count", closed)
	}

	// Generate session ID
	a.sessionID = uuid.New().String()

	// Create session in database
	if err := a.store.CreateSession(a.sessionID, time.Now().UTC(), int(a.interval.Milliseconds())); err != nil {
		return fmt.Errorf("agent: failed to create session: %w", err)
	}

	a.logger.Info("Agent started",
		"session_id", a.sessionID,
		"interval", a.interval,
	)

	// Ensure session is closed on exit
	defer func() {
		if err := a.store.CloseSession(a.sessionID, time.Now().UTC()); err != nil {
			a.logger.Error("Failed to close session", "error", err)
		} else {
			a.logger.Info("Agent stopped", "session_id", a.sessionID)
		}
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

// SessionID returns the current session ID. Returns empty string if Run() hasn't been called.
func (a *Agent) SessionID() string {
	return a.sessionID
}

// poll performs a single poll cycle: fetch quotas, store snapshot, update tracker.
func (a *Agent) poll(ctx context.Context) {
	// Fetch quotas from API
	resp, err := a.client.FetchQuotas(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Context cancelled during request - this is expected during shutdown
			return
		}
		a.logger.Error("Failed to fetch quotas", "error", err)
		return
	}

	// Create snapshot from response
	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        resp.Subscription,
		Search:     resp.Search.Hourly,
		ToolCall:   resp.ToolCallDiscounts,
	}

	// Store snapshot (always do this, even if tracker fails)
	if _, err := a.store.InsertSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert snapshot", "error", err)
		// Continue to try updating tracker and session even if storage failed
	} else {
		// Increment snapshot count for successful storage
		if err := a.store.IncrementSnapshotCount(a.sessionID); err != nil {
			a.logger.Error("Failed to increment snapshot count", "error", err)
		}
	}

	// Process with tracker (log error but don't stop)
	if err := a.tracker.Process(snapshot); err != nil {
		a.logger.Error("Tracker processing failed", "error", err)
	}

	// Update session max values
	if err := a.store.UpdateSessionMaxRequests(
		a.sessionID,
		snapshot.Sub.Requests,
		snapshot.Search.Requests,
		snapshot.ToolCall.Requests,
	); err != nil {
		a.logger.Error("Failed to update session max", "error", err)
	}

	// Log poll completion with key metrics
	a.logger.Info("Poll complete",
		"session_id", a.sessionID,
		"sub_requests", resp.Subscription.Requests,
		"sub_limit", resp.Subscription.Limit,
		"search_requests", resp.Search.Hourly.Requests,
		"tool_requests", resp.ToolCallDiscounts.Requests,
		"sub_renews_at", resp.Subscription.RenewsAt,
	)
}
