package tracker

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func makeAntigravitySnapshot(capturedAt time.Time, models []api.AntigravityModelQuota) *api.AntigravitySnapshot {
	return &api.AntigravitySnapshot{
		CapturedAt: capturedAt,
		Models:     models,
	}
}

func makeModel(id, label string, remaining float64, resetTime *time.Time) api.AntigravityModelQuota {
	return api.AntigravityModelQuota{
		ModelID:           id,
		Label:             label,
		RemainingFraction: remaining,
		IsExhausted:       remaining <= 0,
		ResetTime:         resetTime,
	}
}

func TestAntigravityTracker_FirstSnapshot_CreatesCycle(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	resetTime := time.Now().Add(24 * time.Hour)
	snapshot := makeAntigravitySnapshot(time.Now(), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &resetTime),
	})

	err := tr.Process(snapshot)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-a")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle failed: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle for model-a")
	}
}

func TestAntigravityTracker_UsageIncrement_UpdatesDelta(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime := baseTime.Add(24 * time.Hour)

	// First snapshot: 80% remaining
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &resetTime),
	})
	tr.Process(s1)

	// Second snapshot: 60% remaining (usage increased by 0.2)
	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.6, &resetTime),
	})
	err := tr.Process(s2)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	cycle, _ := s.QueryActiveAntigravityCycle("model-a")
	if cycle.TotalDelta < 0.19 || cycle.TotalDelta > 0.21 {
		t.Errorf("TotalDelta = %v, want ~0.2", cycle.TotalDelta)
	}
	// Peak usage should be 0.4 (1.0 - 0.6)
	if cycle.PeakUsage < 0.39 || cycle.PeakUsage > 0.41 {
		t.Errorf("PeakUsage = %v, want ~0.4", cycle.PeakUsage)
	}
}

func TestAntigravityTracker_DetectsReset_ResetTimeChanged(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime1 := baseTime.Add(24 * time.Hour)

	// First snapshot
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.5, &resetTime1),
	})
	tr.Process(s1)

	// Second snapshot with different reset time (>10 min difference)
	resetTime2 := baseTime.Add(48 * time.Hour)
	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.9, &resetTime2),
	})
	err := tr.Process(s2)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Old cycle should be closed
	history, _ := s.QueryAntigravityCycleHistory("model-a")
	if len(history) != 1 {
		t.Errorf("Expected 1 closed cycle, got %d", len(history))
	}

	// New cycle should exist
	cycle, _ := s.QueryActiveAntigravityCycle("model-a")
	if cycle == nil {
		t.Fatal("Expected new active cycle")
	}
}

func TestAntigravityTracker_DetectsReset_FractionIncreased(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime := baseTime.Add(24 * time.Hour)

	// First snapshot: 30% remaining
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.3, &resetTime),
	})
	tr.Process(s1)

	// Second snapshot: remaining fraction increased by >0.1 (reset)
	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.95, &resetTime),
	})
	err := tr.Process(s2)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	history, _ := s.QueryAntigravityCycleHistory("model-a")
	if len(history) != 1 {
		t.Errorf("Expected 1 closed cycle, got %d", len(history))
	}
}

func TestAntigravityTracker_DetectsReset_TimeBasedResetTimePassed(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime := baseTime.Add(1 * time.Hour)

	// First snapshot
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.5, &resetTime),
	})
	tr.Process(s1)

	// Second snapshot after reset time passed, with slight increase in remaining
	s2 := makeAntigravitySnapshot(baseTime.Add(2*time.Hour), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.55, &resetTime),
	})
	err := tr.Process(s2)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	history, _ := s.QueryAntigravityCycleHistory("model-a")
	if len(history) != 1 {
		t.Errorf("Expected 1 closed cycle, got %d", len(history))
	}
}

func TestAntigravityTracker_MultipleModels_IndependentCycles(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime1 := baseTime.Add(24 * time.Hour)
	resetTime2 := baseTime.Add(12 * time.Hour)

	// First snapshot with two models
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &resetTime1),
		makeModel("model-b", "Model B", 0.6, &resetTime2),
	})
	tr.Process(s1)

	// Second snapshot: only model-a resets
	resetTime1New := baseTime.Add(48 * time.Hour)
	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.95, &resetTime1New),
		makeModel("model-b", "Model B", 0.5, &resetTime2),
	})
	tr.Process(s2)

	// Model A should have 1 closed cycle
	historyA, _ := s.QueryAntigravityCycleHistory("model-a")
	if len(historyA) != 1 {
		t.Errorf("model-a: expected 1 closed cycle, got %d", len(historyA))
	}

	// Model B should have 0 closed cycles
	historyB, _ := s.QueryAntigravityCycleHistory("model-b")
	if len(historyB) != 0 {
		t.Errorf("model-b: expected 0 closed cycles, got %d", len(historyB))
	}
}

func TestAntigravityTracker_EmptyModelID_Skipped(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	resetTime := time.Now().Add(24 * time.Hour)
	snapshot := makeAntigravitySnapshot(time.Now(), []api.AntigravityModelQuota{
		makeModel("", "No ID Model", 0.8, &resetTime),
	})

	err := tr.Process(snapshot)
	if err != nil {
		t.Fatalf("Process should not fail for empty model ID: %v", err)
	}
}

func TestAntigravityTracker_SetOnReset_Called(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	var resetModelID string
	tr.SetOnReset(func(modelID string) {
		resetModelID = modelID
	})

	baseTime := time.Now()
	resetTime1 := baseTime.Add(24 * time.Hour)

	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-x", "Model X", 0.5, &resetTime1),
	})
	tr.Process(s1)

	// Trigger reset
	resetTime2 := baseTime.Add(48 * time.Hour)
	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-x", "Model X", 0.95, &resetTime2),
	})
	tr.Process(s2)

	if resetModelID != "model-x" {
		t.Errorf("onReset called with %q, want %q", resetModelID, "model-x")
	}
}

func TestAntigravityTracker_PeakTracking(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime := baseTime.Add(24 * time.Hour)

	// Remaining fractions: 0.9, 0.7, 0.8, 0.5, 0.6, 0.3
	// Usage:               0.1, 0.3, 0.2, 0.5, 0.4, 0.7
	fractions := []float64{0.9, 0.7, 0.8, 0.5, 0.6, 0.3}

	for i, f := range fractions {
		snap := makeAntigravitySnapshot(baseTime.Add(time.Duration(i)*time.Minute), []api.AntigravityModelQuota{
			makeModel("model-a", "Model A", f, &resetTime),
		})
		tr.Process(snap)
	}

	cycle, _ := s.QueryActiveAntigravityCycle("model-a")
	// Peak usage should be 0.7 (1.0 - 0.3)
	if cycle.PeakUsage < 0.69 || cycle.PeakUsage > 0.71 {
		t.Errorf("PeakUsage = %v, want ~0.7", cycle.PeakUsage)
	}
}

func TestAntigravityTracker_UsageSummary_NoCycles(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)

	summary, err := tr.UsageSummary("model-a")
	if err != nil {
		t.Fatalf("UsageSummary failed: %v", err)
	}

	if summary.ModelID != "model-a" {
		t.Errorf("ModelID = %q, want 'model-a'", summary.ModelID)
	}
	if summary.CompletedCycles != 0 {
		t.Errorf("CompletedCycles = %d, want 0", summary.CompletedCycles)
	}
}

func TestAntigravityTracker_UsageSummary_WithCompletedCycle(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	baseTime := time.Now()
	resetTime1 := baseTime.Add(24 * time.Hour)

	// Create cycle
	s1 := makeAntigravitySnapshot(baseTime, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &resetTime1),
	})
	tr.Process(s1)
	s.InsertAntigravitySnapshot(s1)

	s2 := makeAntigravitySnapshot(baseTime.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.5, &resetTime1),
	})
	tr.Process(s2)
	s.InsertAntigravitySnapshot(s2)

	// Trigger reset
	resetTime2 := baseTime.Add(48 * time.Hour)
	s3 := makeAntigravitySnapshot(baseTime.Add(2*time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.9, &resetTime2),
	})
	tr.Process(s3)
	s.InsertAntigravitySnapshot(s3)

	summary, err := tr.UsageSummary("model-a")
	if err != nil {
		t.Fatalf("UsageSummary failed: %v", err)
	}

	if summary.CompletedCycles != 1 {
		t.Errorf("CompletedCycles = %d, want 1", summary.CompletedCycles)
	}
	if summary.Label != "Model A" {
		t.Errorf("Label = %q, want 'Model A'", summary.Label)
	}
}

func TestAntigravityTracker_Process_ExistingCycleAfterRestart_UpdatesPeakWithoutDelta(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Date(2026, 3, 4, 13, 0, 0, 0, time.UTC)
	reset := base.Add(24 * time.Hour)

	if _, err := s.CreateAntigravityCycle("model-a", base, &reset); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("model-a", 0.2, 0.1); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	model := makeModel("model-a", "Model A", 0.6, &reset) // usage=0.4
	if err := tr.processModel(model, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("processModel: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-a")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle: %v", err)
	}
	if cycle.PeakUsage < 0.39 || cycle.PeakUsage > 0.41 {
		t.Fatalf("PeakUsage = %v, want ~0.4", cycle.PeakUsage)
	}
	if cycle.TotalDelta < 0.099 || cycle.TotalDelta > 0.101 {
		t.Fatalf("TotalDelta = %v, want ~0.1", cycle.TotalDelta)
	}
}

func TestAntigravityTracker_UsageSummary_UsesSnapshotResetFallbackAndCapsNegativeDuration(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)
	pastReset := base.Add(-2 * time.Hour)

	if _, err := s.CreateAntigravityCycle("model-a", base, nil); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}

	snap := makeAntigravitySnapshot(base.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.75, &pastReset),
	})
	if _, err := s.InsertAntigravitySnapshot(snap); err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	summary, err := tr.UsageSummary("model-a")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.ResetTime == nil {
		t.Fatal("expected ResetTime fallback from latest snapshot")
	}
	if !summary.ResetTime.Equal(pastReset) {
		t.Fatalf("ResetTime = %v, want %v", summary.ResetTime, pastReset)
	}
	if summary.TimeUntilReset != 0 {
		t.Fatalf("TimeUntilReset = %v, want 0 when reset is in the past", summary.TimeUntilReset)
	}
	if summary.Label != "Model A" {
		t.Fatalf("Label = %q, want Model A", summary.Label)
	}
	if summary.RemainingFraction != 0.75 {
		t.Fatalf("RemainingFraction = %v, want 0.75", summary.RemainingFraction)
	}
}

func TestAntigravityTracker_UsageSummary_CalculatesRateAndClampsProjection(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	cycleStart := time.Now().UTC().Add(-2 * time.Hour)
	resetTime := time.Now().UTC().Add(2 * time.Hour)

	if _, err := s.CreateAntigravityCycle("model-a", cycleStart, &resetTime); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("model-a", 0.9, 0.4); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	snap := makeAntigravitySnapshot(time.Now().UTC(), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.2, &resetTime), // current usage 0.8
	})
	if _, err := s.InsertAntigravitySnapshot(snap); err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	summary, err := tr.UsageSummary("model-a")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CurrentRate <= 0 {
		t.Fatalf("CurrentRate = %v, want > 0", summary.CurrentRate)
	}
	if summary.ProjectedUsage != 1.0 {
		t.Fatalf("ProjectedUsage = %v, want 1.0 (clamped)", summary.ProjectedUsage)
	}
}
