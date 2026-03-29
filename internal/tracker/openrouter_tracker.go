package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// OpenRouterTracker manages reset cycle detection and usage calculation for OpenRouter.
type OpenRouterTracker struct {
	store  *store.Store
	logger *slog.Logger

	// Cache last seen values for delta calculation
	lastUsage     float64
	hasLastValues bool

	onReset func(quotaName string) // called when a usage reset is detected
}

// SetOnReset registers a callback that is invoked when a usage reset is detected.
func (t *OpenRouterTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// OpenRouterSummary contains computed usage statistics for OpenRouter.
type OpenRouterSummary struct {
	QuotaType       string
	CurrentUsage    float64
	CurrentLimit    float64  // 0 if no limit (unlimited)
	LimitRemaining  *float64 // nil if no limit
	UsagePercent    float64
	IsFreeTier      bool
	CurrentRate     float64 // per hour
	ProjectedUsage  float64
	CompletedCycles int
	AvgPerCycle     float64
	PeakCycle       float64
	TotalTracked    float64
	TrackingSince   time.Time
}

// NewOpenRouterTracker creates a new OpenRouterTracker.
func NewOpenRouterTracker(store *store.Store, logger *slog.Logger) *OpenRouterTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenRouterTracker{
		store:  store,
		logger: logger,
	}
}

// Process compares current snapshot with previous, detects resets, updates cycles.
func (t *OpenRouterTracker) Process(snapshot *api.OpenRouterSnapshot) error {
	if err := t.processCredits(snapshot); err != nil {
		return fmt.Errorf("openrouter tracker: credits: %w", err)
	}

	t.hasLastValues = true
	return nil
}

// processCredits tracks the credits/usage quota cycle.
// Reset detection: usage drops significantly (indicates a new billing period or credit top-up).
func (t *OpenRouterTracker) processCredits(snapshot *api.OpenRouterSnapshot) error {
	quotaType := "credits"
	currentUsage := snapshot.Usage

	cycle, err := t.store.QueryActiveOpenRouterCycle(quotaType)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		// First snapshot - create new cycle
		_, err := t.store.CreateOpenRouterCycle(quotaType, snapshot.CapturedAt)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateOpenRouterCycle(quotaType, currentUsage, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastUsage = currentUsage
		t.logger.Info("Created new OpenRouter credits cycle",
			"initialUsage", currentUsage,
		)
		return nil
	}

	// Check for reset: detect significant drop in usage value
	resetDetected := false
	if t.hasLastValues && t.lastUsage > 0 && currentUsage < t.lastUsage*0.5 {
		resetDetected = true
	}

	if resetDetected {
		// Close old cycle with final stats
		if err := t.store.CloseOpenRouterCycle(quotaType, snapshot.CapturedAt, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		// Create new cycle
		if _, err := t.store.CreateOpenRouterCycle(quotaType, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateOpenRouterCycle(quotaType, currentUsage, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		t.lastUsage = currentUsage
		t.logger.Info("Detected OpenRouter credits reset",
			"lastUsage", t.lastUsage,
			"newUsage", currentUsage,
			"totalDelta", cycle.TotalDelta,
		)
		if t.onReset != nil {
			t.onReset(quotaType)
		}
		return nil
	}

	// Same cycle - update stats
	if t.hasLastValues {
		delta := currentUsage - t.lastUsage
		if delta > 0 {
			cycle.TotalDelta += delta
		}
		if currentUsage > cycle.PeakUsage {
			cycle.PeakUsage = currentUsage
		}
		if err := t.store.UpdateOpenRouterCycle(quotaType, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to update cycle: %w", err)
		}
	} else {
		// First snapshot after restart - update peak if higher
		if currentUsage > cycle.PeakUsage {
			cycle.PeakUsage = currentUsage
			if err := t.store.UpdateOpenRouterCycle(quotaType, cycle.PeakUsage, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	}

	t.lastUsage = currentUsage
	return nil
}

// UsageSummary returns computed stats for OpenRouter usage.
func (t *OpenRouterTracker) UsageSummary() (*OpenRouterSummary, error) {
	quotaType := "credits"

	activeCycle, err := t.store.QueryActiveOpenRouterCycle(quotaType)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryOpenRouterCycleHistory(quotaType)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &OpenRouterSummary{
		QuotaType:       quotaType,
		CompletedCycles: len(history),
	}

	// Calculate stats from completed cycles
	if len(history) > 0 {
		var totalDelta float64
		summary.TrackingSince = history[len(history)-1].CycleStart // oldest cycle (history is DESC)

		for _, cycle := range history {
			totalDelta += cycle.TotalDelta
			if cycle.TotalDelta > summary.PeakCycle {
				summary.PeakCycle = cycle.TotalDelta
			}
		}
		summary.AvgPerCycle = totalDelta / float64(len(history))
		summary.TotalTracked = totalDelta
	}

	// Add active cycle data
	if activeCycle != nil {
		summary.TotalTracked += activeCycle.TotalDelta

		// Get latest snapshot for current usage
		latest, err := t.store.QueryLatestOpenRouter()
		if err != nil {
			return nil, fmt.Errorf("failed to query latest: %w", err)
		}

		if latest != nil {
			summary.CurrentUsage = latest.Usage
			summary.IsFreeTier = latest.IsFreeTier
			summary.LimitRemaining = latest.LimitRemaining

			if latest.Limit != nil && *latest.Limit > 0 {
				summary.CurrentLimit = *latest.Limit
				summary.UsagePercent = (latest.Usage / *latest.Limit) * 100
			}

			// Calculate rate from active cycle timing
			elapsed := time.Since(activeCycle.CycleStart)
			if elapsed.Hours() > 0 && summary.CurrentUsage > 0 {
				summary.CurrentRate = summary.CurrentUsage / elapsed.Hours()
			}
		}
	}

	return summary, nil
}
