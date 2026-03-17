package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// GeminiTracker manages reset cycle detection and usage calculation for Gemini models.
type GeminiTracker struct {
	store          *store.Store
	logger         *slog.Logger
	lastFractions  map[string]float64   // model_id -> last remaining fraction
	lastResetTimes map[string]time.Time // model_id -> last reset time
	hasLastValues  bool

	onReset func(modelID string)
}

// GeminiSummary contains computed usage statistics for a Gemini model.
type GeminiSummary struct {
	ModelID           string
	RemainingFraction float64
	UsagePercent      float64
	ResetTime         *time.Time
	TimeUntilReset    time.Duration
	CurrentRate       float64 // usage fraction per hour
	ProjectedUsage    float64 // projected usage fraction at reset
	CompletedCycles   int
	AvgPerCycle       float64
	PeakCycle         float64
	TotalTracked      float64
	TrackingSince     time.Time
}

// NewGeminiTracker creates a new GeminiTracker.
func NewGeminiTracker(store *store.Store, logger *slog.Logger) *GeminiTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &GeminiTracker{
		store:          store,
		logger:         logger,
		lastFractions:  make(map[string]float64),
		lastResetTimes: make(map[string]time.Time),
	}
}

// SetOnReset registers a callback invoked when a model reset is detected.
func (t *GeminiTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// Process aggregates snapshot quotas by family, then detects resets per-family.
func (t *GeminiTracker) Process(snapshot *api.GeminiSnapshot) error {
	families := api.AggregateGeminiByFamily(snapshot.Quotas)
	for _, fq := range families {
		q := api.GeminiQuota{
			ModelID:           fq.FamilyID,
			RemainingFraction: fq.RemainingFraction,
			UsagePercent:      fq.UsagePercent,
			ResetTime:         fq.ResetTime,
			TimeUntilReset:    fq.TimeUntilReset,
		}
		if err := t.processModel(q, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("gemini tracker: %s: %w", fq.FamilyID, err)
		}
	}

	t.hasLastValues = true
	return nil
}

func (t *GeminiTracker) processModel(quota api.GeminiQuota, capturedAt time.Time) error {
	modelID := quota.ModelID
	// Usage = 1.0 - remainingFraction
	currentUsage := 1.0 - quota.RemainingFraction

	cycle, err := t.store.QueryActiveGeminiCycle(modelID)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	if cycle == nil {
		_, err := t.store.CreateGeminiCycle(modelID, capturedAt, quota.ResetTime)
		if err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateGeminiCycle(modelID, currentUsage, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastFractions[modelID] = quota.RemainingFraction
		if quota.ResetTime != nil {
			t.lastResetTimes[modelID] = *quota.ResetTime
		}
		return nil
	}

	// Detect reset
	resetDetected := false
	updateCycleResetTime := false

	// Time-based reset detection: if we're past the reset time
	if cycle.ResetTime != nil && capturedAt.After(cycle.ResetTime.Add(2*time.Minute)) {
		resetDetected = true
	}

	// Reset time shift detection
	if !resetDetected && quota.ResetTime != nil && cycle.ResetTime != nil {
		refReset := *cycle.ResetTime
		if lastReset, ok := t.lastResetTimes[modelID]; ok {
			refReset = lastReset
		}

		diff := quota.ResetTime.Sub(refReset)
		if diff < 0 {
			diff = -diff
		}

		// Large reset time shift + usage drop = reset
		if diff > 60*time.Minute {
			if t.hasLastValues {
				if lastFraction, ok := t.lastFractions[modelID]; ok && quota.RemainingFraction > lastFraction+0.02 {
					resetDetected = true
				}
			}
		}

		if !resetDetected {
			updateCycleResetTime = true
		}
	} else if !resetDetected && quota.ResetTime != nil && cycle.ResetTime == nil {
		updateCycleResetTime = true
	}

	if resetDetected {
		cycleEndTime := capturedAt
		if cycle.ResetTime != nil && capturedAt.After(*cycle.ResetTime) {
			cycleEndTime = *cycle.ResetTime
		}

		// Final delta before closing
		if t.hasLastValues {
			if lastFraction, ok := t.lastFractions[modelID]; ok {
				lastUsage := 1.0 - lastFraction
				delta := currentUsage - lastUsage
				if delta > 0 {
					cycle.TotalDelta += delta
				}
				if currentUsage > cycle.PeakUsage {
					cycle.PeakUsage = currentUsage
				}
			}
		}

		if err := t.store.CloseGeminiCycle(modelID, cycleEndTime, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}
		if _, err := t.store.CreateGeminiCycle(modelID, capturedAt, quota.ResetTime); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateGeminiCycle(modelID, currentUsage, 0); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		t.lastFractions[modelID] = quota.RemainingFraction
		if quota.ResetTime != nil {
			t.lastResetTimes[modelID] = *quota.ResetTime
		}
		if t.onReset != nil {
			t.onReset(modelID)
		}
		return nil
	}

	if updateCycleResetTime {
		if err := t.store.UpdateGeminiCycleResetTime(modelID, quota.ResetTime); err != nil {
			return fmt.Errorf("failed to update cycle reset time: %w", err)
		}
		if quota.ResetTime != nil {
			t.lastResetTimes[modelID] = *quota.ResetTime
		}
	}

	// Normal update
	if t.hasLastValues {
		if lastFraction, ok := t.lastFractions[modelID]; ok {
			lastUsage := 1.0 - lastFraction
			delta := currentUsage - lastUsage
			if delta > 0 {
				cycle.TotalDelta += delta
			}
			if currentUsage > cycle.PeakUsage {
				cycle.PeakUsage = currentUsage
			}
			if err := t.store.UpdateGeminiCycle(modelID, cycle.PeakUsage, cycle.TotalDelta); err != nil {
				return fmt.Errorf("failed to update cycle: %w", err)
			}
		}
	} else if currentUsage > cycle.PeakUsage {
		cycle.PeakUsage = currentUsage
		if err := t.store.UpdateGeminiCycle(modelID, cycle.PeakUsage, cycle.TotalDelta); err != nil {
			return fmt.Errorf("failed to update cycle: %w", err)
		}
	}

	t.lastFractions[modelID] = quota.RemainingFraction
	return nil
}

// UsageSummary returns computed stats for a Gemini family ID.
func (t *GeminiTracker) UsageSummary(familyID string) (*GeminiSummary, error) {
	activeCycle, err := t.store.QueryActiveGeminiCycle(familyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryGeminiCycleHistory(familyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &GeminiSummary{ModelID: familyID, CompletedCycles: len(history)}

	if len(history) > 0 {
		var totalDelta float64
		summary.TrackingSince = history[len(history)-1].CycleStart
		for _, cycle := range history {
			totalDelta += cycle.TotalDelta
			if cycle.PeakUsage > summary.PeakCycle {
				summary.PeakCycle = cycle.PeakUsage
			}
		}
		summary.AvgPerCycle = totalDelta / float64(len(history))
		summary.TotalTracked = totalDelta
	}

	if activeCycle != nil {
		summary.TotalTracked += activeCycle.TotalDelta
		if activeCycle.PeakUsage > summary.PeakCycle {
			summary.PeakCycle = activeCycle.PeakUsage
		}
		if activeCycle.ResetTime != nil {
			summary.ResetTime = activeCycle.ResetTime
			summary.TimeUntilReset = time.Until(*activeCycle.ResetTime)
		}

		latest, err := t.store.QueryLatestGemini()
		if err != nil {
			return nil, fmt.Errorf("failed to query latest: %w", err)
		}
		if latest != nil {
			// Aggregate latest quotas by family to find this family's values
			families := api.AggregateGeminiByFamily(latest.Quotas)
			for _, fq := range families {
				if fq.FamilyID == familyID {
					summary.RemainingFraction = fq.RemainingFraction
					summary.UsagePercent = fq.UsagePercent
					if summary.ResetTime == nil && fq.ResetTime != nil {
						summary.ResetTime = fq.ResetTime
						summary.TimeUntilReset = time.Until(*fq.ResetTime)
					}
					break
				}
			}

			elapsed := time.Since(activeCycle.CycleStart)
			if elapsed.Minutes() >= 30 && activeCycle.TotalDelta > 0 {
				summary.CurrentRate = activeCycle.TotalDelta / elapsed.Hours()
				if summary.ResetTime != nil {
					hoursLeft := time.Until(*summary.ResetTime).Hours()
					if hoursLeft > 0 {
						projected := (1.0 - summary.RemainingFraction) + (summary.CurrentRate * hoursLeft)
						if projected > 1.0 {
							projected = 1.0
						}
						summary.ProjectedUsage = projected
					}
				}
			}
		}
	}

	return summary, nil
}
