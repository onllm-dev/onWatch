package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// MuseResetDriftTolerance bounds ResetsAt jitter before a change counts as a
// new quota window. The API reports whole-second epochs; 90 minutes matches
// the other percent-window providers.
const museResetDriftTolerance = 90 * time.Minute

// MuseTracker manages reset cycle detection for Muse quota windows.
type MuseTracker struct {
	store      *store.Store
	logger     *slog.Logger
	lastValues map[string]float64
	lastResets map[string]string
	hasLast    bool
	onReset    func(quotaName string)
}

// SetOnReset registers a callback invoked when a quota reset is detected.
func (t *MuseTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// MuseSummary contains computed usage statistics for a Muse quota window.
type MuseSummary struct {
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

// NewMuseTracker creates a new MuseTracker.
func NewMuseTracker(store *store.Store, logger *slog.Logger) *MuseTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &MuseTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[string]float64),
		lastResets: make(map[string]string),
	}
}

// Process compares the snapshot quotas with previous state and maintains cycles.
func (t *MuseTracker) Process(snapshot *api.MuseSnapshot) error {
	for _, quota := range snapshot.Quotas {
		if err := t.processQuota(quota, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("muse tracker: %s: %w", quota.Name, err)
		}
	}

	t.hasLast = true
	return nil
}

func (t *MuseTracker) processQuota(quota api.MuseQuota, capturedAt time.Time) error {
	quotaName := quota.Name
	currentUtil := quota.Utilization

	cycle, err := t.store.QueryActiveMuseCycle(quotaName)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		_, err := t.store.CreateMuseCycle(quotaName, capturedAt, quota.ResetsAt)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateMuseCycle(quotaName, currentUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastValues[quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
		}
		t.logger.Info("Created new Muse cycle",
			"quota", quotaName,
			"resetsAt", quota.ResetsAt,
			"initialUtil", currentUtil,
		)
		return nil
	}

	resetDetected := false
	resetReason := ""
	storedResetPassed := cycle.ResetsAt != nil && capturedAt.After(cycle.ResetsAt.Add(2*time.Minute))
	currentResetIsFuture := quota.ResetsAt != nil && quota.ResetsAt.After(capturedAt)
	if storedResetPassed && !currentResetIsFuture {
		resetDetected = true
		resetReason = "time-based (stored ResetsAt passed)"
	}

	if !resetDetected {
		if quota.ResetsAt != nil && cycle.ResetsAt != nil {
			diff := quota.ResetsAt.Sub(*cycle.ResetsAt)
			if diff < 0 {
				diff = -diff
			}
			if diff > museResetDriftTolerance {
				resetDetected = true
				resetReason = "api-based (ResetsAt changed)"
			}
		} else if quota.ResetsAt != nil && cycle.ResetsAt == nil {
			resetDetected = true
			resetReason = "api-based (new ResetsAt appeared)"
		}
	}

	if resetDetected {
		cycleEndTime := capturedAt
		if cycle.ResetsAt != nil && capturedAt.After(*cycle.ResetsAt) {
			cycleEndTime = *cycle.ResetsAt
		}

		if t.hasLast {
			if lastUtil, ok := t.lastValues[quotaName]; ok {
				delta := currentUtil - lastUtil
				if delta > 0 {
					cycle.TotalDelta += delta
				}
				if currentUtil > cycle.PeakUtilization {
					cycle.PeakUtilization = currentUtil
				}
			}
		}

		if err := t.store.CloseMuseCycle(quotaName, cycleEndTime, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		if _, err := t.store.CreateMuseCycle(quotaName, capturedAt, quota.ResetsAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateMuseCycle(quotaName, currentUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		t.lastValues[quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
		}
		t.logger.Info("Detected Muse quota reset",
			"quota", quotaName,
			"reason", resetReason,
			"oldResetsAt", cycle.ResetsAt,
			"newResetsAt", quota.ResetsAt,
			"cycleEndTime", cycleEndTime,
			"totalDelta", cycle.TotalDelta,
		)
		if t.onReset != nil {
			t.onReset(quotaName)
		}
		return nil
	}

	if t.hasLast {
		if lastUtil, ok := t.lastValues[quotaName]; ok {
			delta := currentUtil - lastUtil
			if delta > 0 {
				cycle.TotalDelta += delta
			}
			if currentUtil > cycle.PeakUtilization {
				cycle.PeakUtilization = currentUtil
			}
			if err := t.store.UpdateMuseCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		} else {
			if currentUtil > cycle.PeakUtilization {
				cycle.PeakUtilization = currentUtil
				if err := t.store.UpdateMuseCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
					return fmt.Errorf("failed to update cycle: %w", err)
				}
			}
		}
	} else {
		if currentUtil > cycle.PeakUtilization {
			cycle.PeakUtilization = currentUtil
			if err := t.store.UpdateMuseCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	}

	t.lastValues[quotaName] = currentUtil
	if quota.ResetsAt != nil {
		t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
	}
	return nil
}

// UsageSummary computes statistics for a Muse quota window.
func (t *MuseTracker) UsageSummary(quotaName string) (*MuseSummary, error) {
	activeCycle, err := t.store.QueryActiveMuseCycle(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryMuseCycleHistory(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &MuseSummary{
		QuotaName:       quotaName,
		CompletedCycles: len(history),
	}

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

		latest, err := t.store.QueryLatestMuse()
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
