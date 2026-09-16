package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// retentionIntervalDefault is how often the retention policy is enforced.
// Hourly is frequent enough for a day-scale policy and cheap when none is set:
// the pass then costs two settings reads and nothing else.
const retentionIntervalDefault = time.Hour

// RetentionAgent enforces the operator's data retention policy.
//
// It exists so that storage limitation (GDPR Art. 5(1)(e)) is something onWatch
// actually does rather than something the operator has to remember. Before
// this, only api_integration_usage_events was ever pruned;
// ClearOldSystemAlerts was written but never called from anywhere, and the
// provider snapshot tables holding account emails grew without bound.
//
// The policy is re-read from settings on every pass, so a change made in the
// dashboard takes effect within the hour without a restart.
type RetentionAgent struct {
	store *store.Store
	// envDefault is the policy from the environment, used for whichever
	// period the operator has not set in the dashboard.
	envDefault store.RetentionPolicy
	interval   time.Duration
	logger     *slog.Logger
}

// NewRetentionAgent creates the retention enforcer. envDefault may be the zero
// policy, which means "keep everything" - the default for every install that
// has not chosen otherwise.
func NewRetentionAgent(st *store.Store, envDefault store.RetentionPolicy, logger *slog.Logger) *RetentionAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &RetentionAgent{
		store:      st,
		envDefault: envDefault,
		interval:   retentionIntervalDefault,
		logger:     logger,
	}
}

// SetInterval overrides the enforcement interval. Used in tests.
func (a *RetentionAgent) SetInterval(interval time.Duration) {
	if interval > 0 {
		a.interval = interval
	}
}

// EffectivePolicy returns the policy currently in force.
func (a *RetentionAgent) EffectivePolicy() store.RetentionPolicy {
	return a.store.RetentionPolicyWithDefault(a.envDefault)
}

// RunOnce enforces the policy a single time and reports what changed.
func (a *RetentionAgent) RunOnce(ctx context.Context, now time.Time) (store.PruneResult, error) {
	policy := a.EffectivePolicy()
	if policy.IsZero() {
		return store.PruneResult{Scrubbed: map[string]int64{}, Deleted: map[string]int64{}}, nil
	}

	res, err := a.store.ApplyRetention(ctx, policy, now)
	if err != nil {
		return res, err
	}
	if scrubbed, deleted := res.TotalScrubbed(), res.TotalDeleted(); scrubbed > 0 || deleted > 0 {
		a.logger.Info("retention policy applied",
			"scrubbed_rows", scrubbed,
			"deleted_rows", deleted,
			"scrub_after", policy.ScrubAfter,
			"delete_after", policy.DeleteAfter)
	}
	return res, nil
}

// Run enforces the policy on a timer until the context is cancelled.
func (a *RetentionAgent) Run(ctx context.Context) error {
	policy := a.EffectivePolicy()
	a.logger.Info("retention agent started",
		"interval", a.interval, "scrub_after", policy.ScrubAfter, "delete_after", policy.DeleteAfter)
	defer a.logger.Info("retention agent stopped")

	// A first pass at startup, so a policy set while the daemon was down is
	// applied without waiting out the interval.
	if _, err := a.RunOnce(ctx, time.Now()); err != nil {
		// A retention failure must not take the daemon down: the data is still
		// there and the next pass will retry.
		a.logger.Error("retention pass failed", "error", err)
	}

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := a.RunOnce(ctx, time.Now()); err != nil {
				a.logger.Error("retention pass failed", "error", err)
			}
		}
	}
}
