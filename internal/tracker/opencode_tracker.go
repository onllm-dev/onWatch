package tracker

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

const openCodeResetDriftTolerance = 90 * time.Minute

// windowRolloverGrace bounds how long a time-only rollover may wait for the
// API to publish its new window. At the default 120s poll it covers seven
// polls of API lag; after it, samples are real usage that must keep counting
// (for example a monthly reset that the subscription lookup stopped returning).
const windowRolloverGrace = 15 * time.Minute

type OpenCodeTracker struct {
	store  *store.Store
	logger *slog.Logger
	// mu guards the in-memory cycle state below. One tracker is shared by every
	// OpenCode agent - each runtime restart builds a fresh agent over the same
	// tracker - so concurrent Process calls are possible and the maps would
	// otherwise be a "concurrent map writes" panic.
	mu         sync.Mutex
	lastValues map[string]float64
	lastResets map[string]string
	hasLast    bool
	onReset    func(quotaName string)
}

func (t *OpenCodeTracker) SetOnReset(fn func(string)) {
	t.mu.Lock()
	t.onReset = fn
	t.mu.Unlock()
}

type OpenCodeSummary struct {
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

func NewOpenCodeTracker(store *store.Store, logger *slog.Logger) *OpenCodeTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenCodeTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[string]float64),
		lastResets: make(map[string]string),
	}
}

func (t *OpenCodeTracker) Process(snapshot *api.OpenCodeSnapshot) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, quota := range snapshot.Quotas {
		if err := t.processQuota(quota, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("opencode tracker: %s: %w", quota.Name, err)
		}
	}

	t.hasLast = true
	return nil
}

func (t *OpenCodeTracker) processQuota(quota api.OpenCodeQuota, capturedAt time.Time) error {
	quotaName := quota.Name
	currentUtil := quota.Utilization

	// A reset that is not after the sample was already passed when the sample
	// was taken. Storing it would give the new cycle a reset in the past, and
	// the next poll would mistake that stale value for a fresh window.
	quota.ResetsAt = futureResetsAt(quota.ResetsAt, capturedAt)

	cycle, err := t.store.QueryActiveOpenCodeCycle(quotaName)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		_, err := t.store.CreateOpenCodeCycle(quotaName, capturedAt, quota.ResetsAt)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateOpenCodeCycle(quotaName, currentUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastValues[quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
		}
		t.logger.Info("Created new OpenCode cycle",
			"quota", quotaName,
			"resetsAt", quota.ResetsAt,
			"initialUtil", currentUtil,
		)
		return nil
	}
	if cycle.ResetsAt != nil && !cycle.ResetsAt.After(cycle.CycleStart) {
		cycle.ResetsAt = nil
	}

	// A time-only rollover opens at the old boundary with no reset or
	// statistics. The matching predecessor makes this state restart-safe
	// without a column, and values pending acknowledgment are held for at most
	// windowRolloverGrace.
	if predecessor := t.awaitingRolloverPredecessor(quotaName, cycle); predecessor != nil {
		if capturedAt.Sub(cycle.CycleStart) < windowRolloverGrace {
			// A low previous peak cannot prove a drop: seed the first sample
			// anyway so usage in this window is not lost.
			dropped := currentUtil+2 < predecessor.PeakUtilization || predecessor.PeakUtilization <= 2
			if quota.ResetsAt != nil {
				if err := t.store.UpdateOpenCodeCycleResetsAt(quotaName, quota.ResetsAt); err != nil {
					return fmt.Errorf("failed to attach acknowledged reset: %w", err)
				}
				t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
			}
			if !dropped {
				t.lastValues[quotaName] = currentUtil
				return nil
			}
			// The first sample below the old window's peak starts the new
			// window; earlier samples after the boundary cannot prove which
			// side of the reset they belong to.
			if err := t.store.UpdateOpenCodeCycle(quotaName, currentUtil, currentUtil); err != nil {
				return fmt.Errorf("failed to seed acknowledged rollover: %w", err)
			}
			t.lastValues[quotaName] = currentUtil
			return nil
		}
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
			if diff > openCodeResetDriftTolerance {
				resetDetected = true
				resetReason = "api-based (ResetsAt changed)"
			}
		} else if quota.ResetsAt != nil && cycle.ResetsAt == nil {
			lastUtil, hasLast := t.lastValues[quotaName]
			if t.hasLast && hasLast && currentUtil+2 < lastUtil {
				resetDetected = true
				resetReason = "api-based (new ResetsAt appeared with a usage drop)"
			} else if cycle.PeakUtilization == 0 && cycle.TotalDelta == 0 {
				// An idle cycle (e.g. no open 5-hour session) just learned its
				// reset time: restart it now instead of closing an empty cycle.
				if err := t.store.RestartOpenCodeCycle(quotaName, capturedAt, *quota.ResetsAt); err != nil {
					return fmt.Errorf("failed to restart idle cycle: %w", err)
				}
				cycle.CycleStart, cycle.ResetsAt = capturedAt, quota.ResetsAt
				t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
			} else {
				// A reset published mid-window belongs to the window already
				// open; only a usage drop proves a new window started.
				if err := t.store.UpdateOpenCodeCycleResetsAt(quotaName, quota.ResetsAt); err != nil {
					return fmt.Errorf("failed to attach reset to cycle: %w", err)
				}
				cycle.ResetsAt = quota.ResetsAt
				t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
			}
		}
	}

	if resetDetected {
		cycleEndTime := capturedAt
		if cycle.ResetsAt != nil && capturedAt.After(*cycle.ResetsAt) {
			cycleEndTime = *cycle.ResetsAt
		}
		if cycleEndTime.Before(cycle.CycleStart) {
			cycleEndTime = cycle.CycleStart
		}

		// The sample that revealed the reset belongs to the new cycle.
		if err := t.store.CloseOpenCodeCycle(quotaName, cycleEndTime, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		newStart, initialUtil := capturedAt, currentUtil
		if storedResetPassed && !currentResetIsFuture {
			// Do not assign a possibly pre-reset sample to the new window.
			newStart, initialUtil = cycleEndTime, 0
		}
		if _, err := t.store.CreateOpenCodeCycle(quotaName, newStart, nextWindowReset(quota.ResetsAt, capturedAt)); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateOpenCodeCycle(quotaName, initialUtil, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		t.lastValues[quotaName] = currentUtil
		if quota.ResetsAt != nil {
			t.lastResets[quotaName] = quota.ResetsAt.Format(time.RFC3339Nano)
		}
		t.logger.Info("Detected OpenCode quota reset",
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
			if err := t.store.UpdateOpenCodeCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		} else {
			if currentUtil > cycle.PeakUtilization {
				cycle.PeakUtilization = currentUtil
				if err := t.store.UpdateOpenCodeCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
					return fmt.Errorf("failed to update cycle: %w", err)
				}
			}
		}
	} else {
		if currentUtil > cycle.PeakUtilization {
			cycle.PeakUtilization = currentUtil
			if err := t.store.UpdateOpenCodeCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
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

// awaitingRolloverPredecessor returns the closed cycle whose boundary opened
// an empty active cycle. Matching boundaries identify the state across
// restarts without persisting a separate flag. It ends once the first
// post-boundary sample has seeded the cycle, not when a reset is attached:
// the API can publish the new reset several polls before utilization drops.
func (t *OpenCodeTracker) awaitingRolloverPredecessor(quotaName string, cycle *store.OpenCodeResetCycle) *store.OpenCodeResetCycle {
	if cycle.PeakUtilization != 0 || cycle.TotalDelta != 0 {
		return nil
	}
	history, err := t.store.QueryOpenCodeCycleHistory(quotaName, 1)
	if err != nil || len(history) != 1 {
		return nil
	}
	prev := history[0]
	if prev.CycleEnd == nil || prev.ResetsAt == nil ||
		!prev.CycleEnd.Equal(cycle.CycleStart) || !prev.ResetsAt.Equal(cycle.CycleStart) {
		return nil
	}
	return prev
}

func (t *OpenCodeTracker) UsageSummary(quotaName string) (*OpenCodeSummary, error) {
	activeCycle, err := t.store.QueryActiveOpenCodeCycle(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryOpenCodeCycleHistory(quotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &OpenCodeSummary{
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

		latest, err := t.store.QueryLatestOpenCode()
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

// futureResetsAt drops a reset timestamp that is not after the sample it came
// with. Such a value was already passed when the sample was captured, so it
// describes the window that just ended, not the open one.
func futureResetsAt(resetsAt *time.Time, capturedAt time.Time) *time.Time {
	if resetsAt == nil || !resetsAt.After(capturedAt) {
		return nil
	}
	return resetsAt
}

// nextWindowReset drops a reset timestamp the API has not advanced past the
// sample that revealed the reset. Storing it would give the new cycle a reset
// in the past and can produce an inverted cycle window.
func nextWindowReset(resetsAt *time.Time, capturedAt time.Time) *time.Time {
	return futureResetsAt(resetsAt, capturedAt)
}
