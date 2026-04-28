package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// MoonshotTracker manages reset cycle detection and usage calculation for Moonshot.
type MoonshotTracker struct {
	store  *store.Store
	logger *slog.Logger

	// Cache last seen values for delta calculation
	lastBalance   float64
	hasLastValues bool

	onReset func(quotaName string) // called when a usage reset is detected
}

// SetOnReset registers a callback that is invoked when a usage reset is detected.
func (t *MoonshotTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// MoonshotSummary contains computed usage statistics for Moonshot.
type MoonshotSummary struct {
	QuotaType       string
	CurrentBalance  float64
	CurrentRate     float64 // per hour (burn rate)
	CompletedCycles int
	AvgPerCycle     float64
	PeakCycle       float64
	TotalTracked    float64
	TrackingSince   time.Time
}

// NewMoonshotTracker creates a new MoonshotTracker.
func NewMoonshotTracker(store *store.Store, logger *slog.Logger) *MoonshotTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &MoonshotTracker{
		store:  store,
		logger: logger,
	}
}

// Process compares current snapshot with previous, detects resets, updates cycles.
func (t *MoonshotTracker) Process(snapshot *api.MoonshotSnapshot) error {
	if err := t.processBalance(snapshot); err != nil {
		return fmt.Errorf("moonshot tracker: balance: %w", err)
	}

	t.hasLastValues = true
	return nil
}

// processBalance tracks the balance cycle.
// Reset detection: balance grows by 50% or more (indicates a recharge/top-up).
func (t *MoonshotTracker) processBalance(snapshot *api.MoonshotSnapshot) error {
	quotaType := "balance"
	currentBalance := snapshot.AvailableBalance

	cycle, err := t.store.QueryActiveMoonshotCycle(quotaType)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		// First snapshot - create new cycle
		_, err := t.store.CreateMoonshotCycle(quotaType, snapshot.CapturedAt)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateMoonshotCycle(quotaType, currentBalance, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastBalance = currentBalance
		t.logger.Info("Created new Moonshot balance cycle",
			"initialBalance", currentBalance,
		)
		return nil
	}

	// Check for reset: detect significant jump in balance (e.g., recharge)
	resetDetected := false
	if t.hasLastValues && t.lastBalance > 0 && currentBalance >= t.lastBalance*1.5 {
		resetDetected = true
	} else if t.hasLastValues && t.lastBalance == 0 && currentBalance > 0 {
		resetDetected = true
	}

	if resetDetected {
		// Close old cycle with final stats
		if err := t.store.CloseMoonshotCycle(quotaType, snapshot.CapturedAt, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		// Create new cycle
		if _, err := t.store.CreateMoonshotCycle(quotaType, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateMoonshotCycle(quotaType, currentBalance, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		t.lastBalance = currentBalance
		t.logger.Info("Detected Moonshot balance recharge (reset)",
			"lastBalance", t.lastBalance,
			"newBalance", currentBalance,
			"totalDelta", cycle.TotalDelta,
		)
		if t.onReset != nil {
			t.onReset(quotaType)
		}
		return nil
	}

	// Same cycle - update stats
	// For balance, delta is the amount *spent*, so we add to TotalDelta when balance drops
	if t.hasLastValues {
		drop := t.lastBalance - currentBalance
		if drop > 0 {
			cycle.TotalDelta += drop
		}
		// PeakUsage represents the highest balance seen in the cycle
		if currentBalance > cycle.PeakUsage {
			cycle.PeakUsage = currentBalance
		}
		if err := t.store.UpdateMoonshotCycle(quotaType, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to update cycle: %w", err)
		}
	} else {
		// First snapshot after restart - update peak if higher
		if currentBalance > cycle.PeakUsage {
			cycle.PeakUsage = currentBalance
			if err := t.store.UpdateMoonshotCycle(quotaType, cycle.PeakUsage, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	}

	t.lastBalance = currentBalance
	return nil
}

// UsageSummary returns computed stats for Moonshot balance.
func (t *MoonshotTracker) UsageSummary() (*MoonshotSummary, error) {
	quotaType := "balance"

	activeCycle, err := t.store.QueryActiveMoonshotCycle(quotaType)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryMoonshotCycleHistory(quotaType)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &MoonshotSummary{
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
		latest, err := t.store.QueryLatestMoonshot()
		if err != nil {
			return nil, fmt.Errorf("failed to query latest: %w", err)
		}

		if latest != nil {
			summary.CurrentBalance = latest.AvailableBalance

			// Calculate rate from active cycle timing (burn rate)
			elapsed := time.Since(activeCycle.CycleStart)
			if elapsed.Hours() > 0 && activeCycle.TotalDelta > 0 {
				summary.CurrentRate = activeCycle.TotalDelta / elapsed.Hours()
			}
		}
	}

	return summary, nil
}
