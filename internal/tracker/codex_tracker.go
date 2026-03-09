package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// CodexTracker manages reset cycle detection and usage calculation for Codex quotas.
type CodexTracker struct {
	store      *store.Store
	logger     *slog.Logger
	lastValues map[int64]map[string]float64   // accountID -> quotaName -> value
	lastResets map[int64]map[string]time.Time // accountID -> quotaName -> resetTime
	hasLast    map[int64]bool                 // accountID -> hasLast

	onReset func(quotaName string)
}

// DefaultCodexAccountID is the default account ID for single-account setups.
const DefaultCodexAccountID int64 = 1

const codexResetShiftThreshold = 60 * time.Minute

// CodexSummary contains computed usage statistics for a Codex quota.
type CodexSummary struct {
	QuotaName       string
	CurrentUtil     float64
	ResetsAt        *time.Time
	TimeUntilReset  time.Duration
	CurrentRate     float64
	ProjectedUtil   float64
	CompletedCycles int
	AvgPerCycle     float64
	PeakCycle       float64
	TotalTracked    float64
	TrackingSince   time.Time
}

// NewCodexTracker creates a new CodexTracker.
func NewCodexTracker(store *store.Store, logger *slog.Logger) *CodexTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &CodexTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[int64]map[string]float64),
		lastResets: make(map[int64]map[string]time.Time),
		hasLast:    make(map[int64]bool),
	}
}

// SetOnReset registers a callback invoked when a quota reset is detected.
func (t *CodexTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// Process iterates over all quotas in the snapshot, detects resets, and updates cycles.
func (t *CodexTracker) Process(snapshot *api.CodexSnapshot) error {
	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}

	for _, quota := range snapshot.Quotas {
		if err := t.processQuota(accountID, quota, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("codex tracker: %s: %w", quota.Name, err)
		}
	}
	t.hasLast[accountID] = true
	return nil
}

func (t *CodexTracker) processQuota(accountID int64, quota api.CodexQuota, capturedAt time.Time) error {
	quotaName := quota.Name
	currentUtil := quota.Utilization

	// Initialize per-account maps if needed
	if t.lastValues[accountID] == nil {
		t.lastValues[accountID] = make(map[string]float64)
	}
	if t.lastResets[accountID] == nil {
		t.lastResets[accountID] = make(map[string]time.Time)
	}

	cycle, err := t.store.QueryActiveCodexCycle(accountID, quotaName)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		_, err := t.store.CreateCodexCycle(accountID, quotaName, capturedAt, quota.ResetsAt)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateCodexCycle(accountID, quotaName, currentUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastValues[accountID][quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[accountID][quotaName] = *quota.ResetsAt
		}
		return nil
	}

	resetDetected := false
	updateCycleResetAt := false
	if cycle.ResetsAt != nil && capturedAt.After(cycle.ResetsAt.Add(2*time.Minute)) {
		resetDetected = true
	}
	if !resetDetected {
		if quota.ResetsAt != nil && cycle.ResetsAt != nil {
			refReset := *cycle.ResetsAt
			if lastReset, ok := t.lastResets[accountID][quotaName]; ok {
				refReset = lastReset
			}

			diff := quota.ResetsAt.Sub(refReset)
			if diff < 0 {
				diff = -diff
			}

			if diff > codexResetShiftThreshold {
				// Some Codex responses use rolling reset timestamps. Only treat large
				// reset-time shifts as a reset when utilization also drops materially.
				if t.hasLast[accountID] {
					if lastUtil, ok := t.lastValues[accountID][quotaName]; ok && currentUtil+2 < lastUtil {
						resetDetected = true
					}
				}
			}

			if !resetDetected {
				updateCycleResetAt = true
			}
		} else if quota.ResetsAt != nil && cycle.ResetsAt == nil {
			updateCycleResetAt = true
		}
	}

	if resetDetected {
		cycleEndTime := capturedAt
		if cycle.ResetsAt != nil && capturedAt.After(*cycle.ResetsAt) {
			cycleEndTime = *cycle.ResetsAt
		}
		if t.hasLast[accountID] {
			if lastUtil, ok := t.lastValues[accountID][quotaName]; ok {
				delta := currentUtil - lastUtil
				if delta > 0 {
					cycle.TotalDelta += delta
				}
				if currentUtil > cycle.PeakUtilization {
					cycle.PeakUtilization = currentUtil
				}
			}
		}
		if err := t.store.CloseCodexCycle(accountID, quotaName, cycleEndTime, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}
		if _, err := t.store.CreateCodexCycle(accountID, quotaName, capturedAt, quota.ResetsAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateCodexCycle(accountID, quotaName, currentUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastValues[accountID][quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[accountID][quotaName] = *quota.ResetsAt
		}
		if t.onReset != nil {
			t.onReset(quotaName)
		}
		return nil
	}

	if updateCycleResetAt {
		if err := t.store.UpdateCodexCycleResetsAt(accountID, quotaName, quota.ResetsAt); err != nil {
			return fmt.Errorf("failed to update cycle reset timestamp: %w", err)
		}
		if quota.ResetsAt != nil {
			t.lastResets[accountID][quotaName] = *quota.ResetsAt
		}
	}

	if t.hasLast[accountID] {
		if lastUtil, ok := t.lastValues[accountID][quotaName]; ok {
			delta := currentUtil - lastUtil
			if delta > 0 {
				cycle.TotalDelta += delta
			}
			if currentUtil > cycle.PeakUtilization {
				cycle.PeakUtilization = currentUtil
			}
			if err := t.store.UpdateCodexCycle(accountID, quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	} else if currentUtil > cycle.PeakUtilization {
		cycle.PeakUtilization = currentUtil
		if err := t.store.UpdateCodexCycle(accountID, quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to update cycle: %w", err)
		}
	}

	t.lastValues[accountID][quotaName] = currentUtil
	return nil
}

// UsageSummary returns computed stats for a specific Codex quota.
func (t *CodexTracker) UsageSummary(accountID int64, quotaName string) (*CodexSummary, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}

	activeCycle, err := t.store.QueryActiveCodexCycle(accountID, quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryCodexCycleHistory(accountID, quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &CodexSummary{QuotaName: quotaName, CompletedCycles: len(history)}

	if len(history) > 0 {
		var totalDelta float64
		summary.TrackingSince = history[len(history)-1].CycleStart
		for _, cycle := range history {
			totalDelta += cycle.TotalDelta
			if cycle.PeakUtilization > summary.PeakCycle {
				summary.PeakCycle = cycle.PeakUtilization
			}
		}
		summary.AvgPerCycle = totalDelta / float64(len(history))
		summary.TotalTracked = totalDelta
	}

	if activeCycle != nil {
		summary.TotalTracked += activeCycle.TotalDelta
		if activeCycle.PeakUtilization > summary.PeakCycle {
			summary.PeakCycle = activeCycle.PeakUtilization
		}
		if activeCycle.ResetsAt != nil {
			summary.ResetsAt = activeCycle.ResetsAt
			summary.TimeUntilReset = time.Until(*activeCycle.ResetsAt)
		}

		latest, err := t.store.QueryLatestCodex(accountID)
		if err != nil {
			return nil, fmt.Errorf("failed to query latest: %w", err)
		}
		if latest != nil {
			for _, q := range latest.Quotas {
				if q.Name == quotaName {
					summary.CurrentUtil = q.Utilization
					if summary.ResetsAt == nil && q.ResetsAt != nil {
						summary.ResetsAt = q.ResetsAt
						summary.TimeUntilReset = time.Until(*q.ResetsAt)
					}
					break
				}
			}

			elapsed := time.Since(activeCycle.CycleStart)
			if elapsed.Minutes() >= 30 && activeCycle.TotalDelta > 0 {
				summary.CurrentRate = activeCycle.TotalDelta / elapsed.Hours()
				if summary.ResetsAt != nil {
					hoursLeft := time.Until(*summary.ResetsAt).Hours()
					if hoursLeft > 0 {
						projected := summary.CurrentUtil + (summary.CurrentRate * hoursLeft)
						if projected > 100 {
							projected = 100
						}
						summary.ProjectedUtil = projected
					}
				}
			}
		}
	}

	return summary, nil
}
