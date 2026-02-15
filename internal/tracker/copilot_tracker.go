package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/internal/api"
	"github.com/onllm-dev/onwatch/internal/store"
)

// CopilotTracker manages reset cycle detection and usage calculation for Copilot quotas.
// Like AnthropicTracker, it supports dynamic quota names via maps.
type CopilotTracker struct {
	store         *store.Store
	logger        *slog.Logger
	lastValues    map[string]int    // quota_name → last remaining count
	lastResets    map[string]string // quota_name → last reset date string
	hasLastValues bool

	onReset func(quotaName string) // called when a quota reset is detected
}

// SetOnReset registers a callback that is invoked when a quota reset is detected.
func (t *CopilotTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// CopilotSummary contains computed usage statistics for a Copilot quota.
type CopilotSummary struct {
	QuotaName        string
	Entitlement      int
	CurrentRemaining int
	CurrentUsed      int
	UsagePercent     float64 // 100 - percent_remaining
	Unlimited        bool
	ResetDate        *time.Time
	TimeUntilReset   time.Duration
	CurrentRate      float64 // used per hour
	ProjectedUsage   int
	CompletedCycles  int
	AvgPerCycle      float64
	PeakCycle        int
	TotalTracked     int
	TrackingSince    time.Time
}

// NewCopilotTracker creates a new CopilotTracker.
func NewCopilotTracker(store *store.Store, logger *slog.Logger) *CopilotTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &CopilotTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[string]int),
		lastResets: make(map[string]string),
	}
}

// Process iterates over all quotas in the snapshot, detects resets, and updates cycles.
func (t *CopilotTracker) Process(snapshot *api.CopilotSnapshot) error {
	resetDateStr := ""
	if snapshot.ResetDate != nil {
		resetDateStr = snapshot.ResetDate.Format(time.RFC3339Nano)
	}

	for _, quota := range snapshot.Quotas {
		if err := t.processQuota(quota, snapshot.CapturedAt, snapshot.ResetDate, resetDateStr); err != nil {
			return fmt.Errorf("copilot tracker: %s: %w", quota.Name, err)
		}
	}

	t.hasLastValues = true
	return nil
}

// processQuota handles cycle detection and tracking for a single Copilot quota.
// Reset detection: compare the reset_date string — if it changed, a quota reset occurred.
func (t *CopilotTracker) processQuota(quota api.CopilotQuota, capturedAt time.Time, resetDate *time.Time, resetDateStr string) error {
	quotaName := quota.Name
	currentUsed := quota.Entitlement - quota.Remaining

	cycle, err := t.store.QueryActiveCopilotCycle(quotaName)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		// First snapshot for this quota — create new cycle
		_, err := t.store.CreateCopilotCycle(quotaName, capturedAt, resetDate)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateCopilotCycle(quotaName, currentUsed, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastValues[quotaName] = quota.Remaining
		t.lastResets[quotaName] = resetDateStr
		t.logger.Info("Created new Copilot cycle",
			"quota", quotaName,
			"resetDate", resetDateStr,
			"initialUsed", currentUsed,
		)
		return nil
	}

	// Reset detection: compare reset date strings
	resetDetected := false
	resetReason := ""

	if lastResetStr, ok := t.lastResets[quotaName]; ok && lastResetStr != "" && resetDateStr != "" && resetDateStr != lastResetStr {
		resetDetected = true
		resetReason = "reset_date changed"
	}

	// Also detect reset via time-based check: if resetDate passed and remaining went up
	if !resetDetected && cycle.ResetDate != nil && capturedAt.After(*cycle.ResetDate) {
		if lastRemaining, ok := t.lastValues[quotaName]; ok && quota.Remaining > lastRemaining {
			resetDetected = true
			resetReason = "time-based (reset date passed + remaining increased)"
		}
	}

	if resetDetected {
		// Close old cycle
		cycleEndTime := capturedAt
		if cycle.ResetDate != nil && capturedAt.After(*cycle.ResetDate) {
			cycleEndTime = *cycle.ResetDate
		}

		if err := t.store.CloseCopilotCycle(quotaName, cycleEndTime, cycle.PeakUsed, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		// Create new cycle
		if _, err := t.store.CreateCopilotCycle(quotaName, capturedAt, resetDate); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateCopilotCycle(quotaName, currentUsed, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		t.lastValues[quotaName] = quota.Remaining
		t.lastResets[quotaName] = resetDateStr
		t.logger.Info("Detected Copilot quota reset",
			"quota", quotaName,
			"reason", resetReason,
			"oldResetDate", cycle.ResetDate,
			"newResetDate", resetDateStr,
		)
		if t.onReset != nil {
			t.onReset(quotaName)
		}
		return nil
	}

	// Same cycle — update stats
	if t.hasLastValues {
		if lastRemaining, ok := t.lastValues[quotaName]; ok {
			usageDelta := lastRemaining - quota.Remaining
			if usageDelta > 0 {
				cycle.TotalDelta += usageDelta
			}
			if currentUsed > cycle.PeakUsed {
				cycle.PeakUsed = currentUsed
			}
			if err := t.store.UpdateCopilotCycle(quotaName, cycle.PeakUsed, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		} else {
			if currentUsed > cycle.PeakUsed {
				cycle.PeakUsed = currentUsed
				if err := t.store.UpdateCopilotCycle(quotaName, cycle.PeakUsed, cycle.TotalDelta); err != nil {
					return fmt.Errorf("failed to update cycle: %w", err)
				}
			}
		}
	} else {
		if currentUsed > cycle.PeakUsed {
			cycle.PeakUsed = currentUsed
			if err := t.store.UpdateCopilotCycle(quotaName, cycle.PeakUsed, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	}

	t.lastValues[quotaName] = quota.Remaining
	t.lastResets[quotaName] = resetDateStr
	return nil
}

// UsageSummary returns computed stats for a specific Copilot quota.
func (t *CopilotTracker) UsageSummary(quotaName string) (*CopilotSummary, error) {
	activeCycle, err := t.store.QueryActiveCopilotCycle(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryCopilotCycleHistory(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &CopilotSummary{
		QuotaName:       quotaName,
		CompletedCycles: len(history),
	}

	// Calculate stats from completed cycles
	if len(history) > 0 {
		var totalDelta int
		summary.TrackingSince = history[len(history)-1].CycleStart

		for _, cycle := range history {
			totalDelta += cycle.TotalDelta
			if cycle.PeakUsed > summary.PeakCycle {
				summary.PeakCycle = cycle.PeakUsed
			}
		}
		summary.AvgPerCycle = float64(totalDelta) / float64(len(history))
		summary.TotalTracked = totalDelta
	}

	// Add active cycle data
	if activeCycle != nil {
		summary.TotalTracked += activeCycle.TotalDelta
		if activeCycle.PeakUsed > summary.PeakCycle {
			summary.PeakCycle = activeCycle.PeakUsed
		}
		if activeCycle.ResetDate != nil {
			summary.ResetDate = activeCycle.ResetDate
			summary.TimeUntilReset = time.Until(*activeCycle.ResetDate)
		}

		// Get latest snapshot for current values
		latest, err := t.store.QueryLatestCopilot()
		if err != nil {
			return nil, fmt.Errorf("failed to query latest: %w", err)
		}

		if latest != nil {
			for _, q := range latest.Quotas {
				if q.Name == quotaName {
					summary.Entitlement = q.Entitlement
					summary.CurrentRemaining = q.Remaining
					summary.CurrentUsed = q.Entitlement - q.Remaining
					summary.UsagePercent = 100.0 - q.PercentRemaining
					summary.Unlimited = q.Unlimited
					if summary.ResetDate == nil && latest.ResetDate != nil {
						summary.ResetDate = latest.ResetDate
						summary.TimeUntilReset = time.Until(*latest.ResetDate)
					}
					break
				}
			}

			// Calculate rate from tracked usage within this cycle
			elapsed := time.Since(activeCycle.CycleStart)
			if elapsed.Minutes() >= 30 && activeCycle.TotalDelta > 0 {
				summary.CurrentRate = float64(activeCycle.TotalDelta) / elapsed.Hours()
				if summary.ResetDate != nil && summary.Entitlement > 0 {
					hoursLeft := time.Until(*summary.ResetDate).Hours()
					if hoursLeft > 0 {
						projected := summary.CurrentUsed + int(summary.CurrentRate*hoursLeft)
						if projected > summary.Entitlement {
							projected = summary.Entitlement
						}
						summary.ProjectedUsage = projected
					}
				}
			}
		}
	}

	return summary, nil
}
