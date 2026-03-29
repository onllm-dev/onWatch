package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// MiniMaxTracker manages reset cycle detection and usage calculation for MiniMax models.
type MiniMaxTracker struct {
	store        *store.Store
	logger       *slog.Logger
	lastUsed     map[string]int
	lastResetAt  map[string]time.Time
	hasLastValue bool

	onReset func(modelName string)
}

// MiniMaxSummary contains computed usage statistics for a MiniMax model.
type MiniMaxSummary struct {
	ModelName       string
	Total           int
	CurrentUsed     int
	CurrentRemain   int
	UsagePercent    float64
	ResetAt         *time.Time
	TimeUntilReset  time.Duration
	CurrentRate     float64
	ProjectedUsage  int
	CompletedCycles int
	AvgPerCycle     float64
	PeakCycle       int
	TotalTracked    int
	TrackingSince   time.Time
}

// NewMiniMaxTracker creates a new MiniMax tracker.
func NewMiniMaxTracker(store *store.Store, logger *slog.Logger) *MiniMaxTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &MiniMaxTracker{
		store:       store,
		logger:      logger,
		lastUsed:    make(map[string]int),
		lastResetAt: make(map[string]time.Time),
	}
}

// SetOnReset registers reset callback.
func (t *MiniMaxTracker) SetOnReset(fn func(modelName string)) {
	t.onReset = fn
}

// Process processes one snapshot and updates/reset-cycles per model.
func (t *MiniMaxTracker) Process(snapshot *api.MiniMaxSnapshot) error {
	for _, model := range snapshot.Models {
		// Skip models with no quota allocation
		if model.Total == 0 && model.Used == 0 {
			continue
		}
		if err := t.processModel(model, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("minimax tracker: %s: %w", model.ModelName, err)
		}
	}
	t.hasLastValue = true
	return nil
}

func (t *MiniMaxTracker) processModel(model api.MiniMaxModelQuota, capturedAt time.Time) error {
	if model.ModelName == "" {
		return nil
	}

	cycle, err := t.store.QueryActiveMiniMaxCycle(model.ModelName)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		if _, err := t.store.CreateMiniMaxCycle(model.ModelName, capturedAt, model.ResetAt); err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateMiniMaxCycle(model.ModelName, model.Used, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastUsed[model.ModelName] = model.Used
		if model.ResetAt != nil {
			t.lastResetAt[model.ModelName] = *model.ResetAt
		}
		return nil
	}

	resetDetected := false
	if model.ResetAt != nil {
		if last, ok := t.lastResetAt[model.ModelName]; ok {
			d := model.ResetAt.Sub(last)
			if d < 0 {
				d = -d
			}
			if d > 10*time.Minute {
				resetDetected = true
			}
		}
	}

	if !resetDetected && t.hasLastValue {
		if last, ok := t.lastUsed[model.ModelName]; ok && last > 0 && model.Used < int(float64(last)*0.6) {
			resetDetected = true
		}
	}

	if resetDetected {
		cycleEnd := capturedAt
		if cycle.ResetAt != nil && capturedAt.After(*cycle.ResetAt) {
			cycleEnd = *cycle.ResetAt
		}
		if err := t.store.CloseMiniMaxCycle(model.ModelName, cycleEnd, cycle.PeakUsed, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}
		if _, err := t.store.CreateMiniMaxCycle(model.ModelName, capturedAt, model.ResetAt); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateMiniMaxCycle(model.ModelName, model.Used, 0); err != nil {
			return fmt.Errorf("failed to initialize new cycle: %w", err)
		}
		t.lastUsed[model.ModelName] = model.Used
		if model.ResetAt != nil {
			t.lastResetAt[model.ModelName] = *model.ResetAt
		}
		if t.onReset != nil {
			t.onReset(model.ModelName)
		}
		return nil
	}

	if t.hasLastValue {
		if last, ok := t.lastUsed[model.ModelName]; ok {
			delta := model.Used - last
			if delta > 0 {
				cycle.TotalDelta += delta
			}
		}
	}
	if model.Used > cycle.PeakUsed {
		cycle.PeakUsed = model.Used
	}
	if err := t.store.UpdateMiniMaxCycle(model.ModelName, cycle.PeakUsed, cycle.TotalDelta); err != nil {
		return fmt.Errorf("failed to update cycle: %w", err)
	}

	t.lastUsed[model.ModelName] = model.Used
	if model.ResetAt != nil {
		t.lastResetAt[model.ModelName] = *model.ResetAt
	}
	return nil
}

// UsageSummary returns computed stats for one MiniMax model.
func (t *MiniMaxTracker) UsageSummary(modelName string) (*MiniMaxSummary, error) {
	active, err := t.store.QueryActiveMiniMaxCycle(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}
	history, err := t.store.QueryMiniMaxCycleHistory(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &MiniMaxSummary{
		ModelName:       modelName,
		CompletedCycles: len(history),
	}

	if len(history) > 0 {
		var total int
		summary.TrackingSince = history[len(history)-1].CycleStart
		for _, c := range history {
			total += c.TotalDelta
			if c.PeakUsed > summary.PeakCycle {
				summary.PeakCycle = c.PeakUsed
			}
		}
		summary.AvgPerCycle = float64(total) / float64(len(history))
		summary.TotalTracked = total
	}

	if active != nil {
		summary.TotalTracked += active.TotalDelta
		if active.PeakUsed > summary.PeakCycle {
			summary.PeakCycle = active.PeakUsed
		}
		if active.ResetAt != nil {
			summary.ResetAt = active.ResetAt
			summary.TimeUntilReset = time.Until(*active.ResetAt)
		}
	}

	latest, err := t.store.QueryLatestMiniMax()
	if err != nil {
		return nil, fmt.Errorf("failed to query latest minimax: %w", err)
	}
	if latest != nil {
		for _, m := range latest.Models {
			if m.ModelName != modelName {
				continue
			}
			summary.Total = m.Total
			summary.CurrentUsed = m.Used
			summary.CurrentRemain = m.Remain
			summary.UsagePercent = m.UsedPercent
			if summary.ResetAt == nil && m.ResetAt != nil {
				summary.ResetAt = m.ResetAt
				summary.TimeUntilReset = time.Until(*m.ResetAt)
			}
			break
		}
	}

	if active != nil {
		elapsed := time.Since(active.CycleStart)
		if elapsed.Minutes() >= 30 && active.TotalDelta > 0 {
			rate := float64(active.TotalDelta) / elapsed.Hours()
			summary.CurrentRate = rate
			if summary.ResetAt != nil && summary.TimeUntilReset > 0 {
				remainingHours := summary.TimeUntilReset.Hours()
				projection := float64(summary.CurrentUsed) + (rate * remainingHours)
				if summary.Total > 0 && projection > float64(summary.Total) {
					projection = float64(summary.Total)
				}
				if projection < 0 {
					projection = 0
				}
				summary.ProjectedUsage = int(projection)
			}
		}
	}

	return summary, nil
}
