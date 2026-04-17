package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestCopilotStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCopilotTracker_Process_FirstSnapshot(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	now := time.Now().UTC()
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	snap := &api.CopilotSnapshot{
		CapturedAt:  now,
		CopilotPlan: "individual_pro",
		ResetDate:   &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000, PercentRemaining: 66.667},
		},
	}

	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle after first snapshot")
	}
	if cycle.PeakUsed != 500 { // 1500 - 1000
		t.Errorf("PeakUsed = %d, want 500", cycle.PeakUsed)
	}
}

func TestCopilotTracker_Process_UsageIncrease(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	now := time.Now().UTC()
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// First snapshot
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot with usage (remaining decreased)
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 900}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.PeakUsed != 600 { // 1500 - 900
		t.Errorf("PeakUsed = %d, want 600", cycle.PeakUsed)
	}
	if cycle.TotalDelta != 100 { // 1000 - 900
		t.Errorf("TotalDelta = %d, want 100", cycle.TotalDelta)
	}
}

func TestCopilotTracker_Process_ResetDetection(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	resetDetected := false
	tr.SetOnReset(func(quotaName string) {
		resetDetected = true
	})

	now := time.Now().UTC()
	resetDate1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// First snapshot
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate1,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 500}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot with different reset date → quota reset
	resetDate2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate2,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1500}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	if !resetDetected {
		t.Error("Expected reset callback to fire")
	}

	// Should have a completed cycle and a new active cycle
	history, err := s.QueryCopilotCycleHistory("premium_interactions")
	if err != nil {
		t.Fatalf("QueryCopilotCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("Expected 1 completed cycle, got %d", len(history))
	}

	active, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if active == nil {
		t.Error("Expected new active cycle after reset")
	}
}

func TestCopilotTracker_Process_MultipleQuotas(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	now := time.Now().UTC()
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	snap := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000, Unlimited: false},
			{Name: "chat", Entitlement: 0, Remaining: 0, Unlimited: true},
			{Name: "completions", Entitlement: 0, Remaining: 0, Unlimited: true},
		},
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Each quota should have its own active cycle
	for _, name := range []string{"premium_interactions", "chat", "completions"} {
		cycle, err := s.QueryActiveCopilotCycle(name)
		if err != nil {
			t.Fatalf("QueryActiveCopilotCycle(%q): %v", name, err)
		}
		if cycle == nil {
			t.Errorf("Expected active cycle for %q", name)
		}
	}
}

func TestCopilotTracker_UsageSummary(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	now := time.Now().UTC()
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	snap := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000, PercentRemaining: 66.667, Unlimited: false},
		},
	}

	// Insert the snapshot into the store
	if _, err := s.InsertCopilotSnapshot(snap); err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("Expected summary")
	}
	if summary.Entitlement != 1500 {
		t.Errorf("Entitlement = %d, want 1500", summary.Entitlement)
	}
	if summary.CurrentUsed != 500 {
		t.Errorf("CurrentUsed = %d, want 500", summary.CurrentUsed)
	}
	if summary.ResetDate == nil {
		t.Error("Expected ResetDate")
	}
}

func TestCopilotTracker_UsageSummary_Empty(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("Expected non-nil summary (even with no data)")
	}
	if summary.CompletedCycles != 0 {
		t.Errorf("CompletedCycles = %d, want 0", summary.CompletedCycles)
	}
}

func TestCopilotTracker_Process_ExistingCycleAfterRestart_UpdatesPeakWithoutDelta(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	base := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	resetDate := base.Add(24 * time.Hour)
	if _, err := s.CreateCopilotCycle("premium_interactions", base, &resetDate); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("premium_interactions", 40, 12); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	quota := api.CopilotQuota{Name: "premium_interactions", Entitlement: 1500, Remaining: 1400}
	if err := tr.processQuota(quota, base.Add(10*time.Minute), &resetDate, resetDate.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("processQuota: %v", err)
	}

	cycle, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if cycle.PeakUsed != 100 {
		t.Fatalf("PeakUsed = %d, want 100", cycle.PeakUsed)
	}
	if cycle.TotalDelta != 12 {
		t.Fatalf("TotalDelta = %d, want 12", cycle.TotalDelta)
	}
}

func TestCopilotTracker_UsageSummary_UsesSnapshotResetDateFallback(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	base := time.Date(2026, 3, 4, 14, 0, 0, 0, time.UTC)
	resetDate := base.Add(48 * time.Hour)

	if _, err := s.CreateCopilotCycle("premium_interactions", base, nil); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("premium_interactions", 500, 0); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	snap := &api.CopilotSnapshot{
		CapturedAt: base.Add(time.Minute),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1200, PercentRemaining: 80, Unlimited: false},
		},
	}
	if _, err := s.InsertCopilotSnapshot(snap); err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.ResetDate == nil {
		t.Fatal("expected ResetDate to fall back to latest snapshot reset date")
	}
	if !summary.ResetDate.Equal(resetDate) {
		t.Fatalf("ResetDate = %v, want %v", summary.ResetDate, resetDate)
	}
	if summary.CurrentUsed != 300 {
		t.Fatalf("CurrentUsed = %d, want 300", summary.CurrentUsed)
	}
	if summary.UsagePercent != 20 {
		t.Fatalf("UsagePercent = %v, want 20", summary.UsagePercent)
	}
}

func TestCopilotTracker_UsageSummary_CalculatesRateAndClampsProjection(t *testing.T) {
	t.Parallel()
	s := newTestCopilotStore(t)
	tr := NewCopilotTracker(s, slog.Default())

	cycleStart := time.Now().UTC().Add(-2 * time.Hour)
	resetDate := time.Now().UTC().Add(2 * time.Hour)
	if _, err := s.CreateCopilotCycle("premium_interactions", cycleStart, &resetDate); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("premium_interactions", 1450, 200); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	snap := &api.CopilotSnapshot{
		CapturedAt: time.Now().UTC(),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 100, PercentRemaining: 6.666, Unlimited: false},
		},
	}
	if _, err := s.InsertCopilotSnapshot(snap); err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CurrentRate <= 0 {
		t.Fatalf("CurrentRate = %v, want > 0", summary.CurrentRate)
	}
	if summary.ProjectedUsage != 1500 {
		t.Fatalf("ProjectedUsage = %d, want 1500 (clamped)", summary.ProjectedUsage)
	}
}
