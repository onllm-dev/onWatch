package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/internal/api"
	"github.com/onllm-dev/onwatch/internal/store"
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
