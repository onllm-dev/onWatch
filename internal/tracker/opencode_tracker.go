package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type OpenCodeTracker struct {
	store      *store.Store
	logger     *slog.Logger
	lastValues map[string]float64
	lastResets map[string]string
	hasLast    bool
	onReset    func(quotaName string)
}

func (t *OpenCodeTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
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

	resetDetected := false
	resetReason := ""
	if cycle.ResetsAt != nil && capturedAt.After(cycle.ResetsAt.Add(2*time.Minute)) {
		resetDetected = true
		resetReason = "time-based (stored ResetsAt passed)"
	}

	if !resetDetected {
		if quota.ResetsAt != nil && cycle.ResetsAt != nil {
			diff := quota.ResetsAt.Sub(*cycle.ResetsAt)
			if diff < 0 {
				diff = -diff
			}
			if diff > 10*time.Minute {
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

		if err := t.store.CloseOpenCodeCycle(quotaName, cycleEndTime, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}

		if _, err := t.store.CreateOpenCodeCycle(quotaName, capturedAt, quota.ResetsAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateOpenCodeCycle(quotaName, currentUtil, 0); err != nil {
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
