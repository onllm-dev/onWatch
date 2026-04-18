package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestCursorStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCursorTracker_Process_FirstSnapshot(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	snapshot := &api.CursorSnapshot{
		CapturedAt:  now,
		AccountType: api.CursorAccountIndividual,
		PlanName:    "Pro",
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Used: 50.0, Limit: 400.0, Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		},
	}

	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle after first snapshot")
	}
	if cycle.PeakUtilization != 12.5 {
		t.Errorf("PeakUtilization = %f, want 12.5", cycle.PeakUtilization)
	}
}

func TestCursorTracker_Process_UsageIncrease(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	snap1 := &api.CursorSnapshot{
		CapturedAt: now,
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	snap2 := &api.CursorSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 25.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.PeakUtilization != 25.0 {
		t.Errorf("PeakUtilization = %f, want 25.0", cycle.PeakUtilization)
	}
	if cycle.TotalDelta != 12.5 {
		t.Errorf("TotalDelta = %f, want 12.5", cycle.TotalDelta)
	}
}

func TestCursorTracker_Process_ResetDetection(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	now := time.Now().UTC()

	// First cycle with resetsAt
	resetsAt1 := now.Add(24 * time.Hour)
	snap1 := &api.CursorSnapshot{
		CapturedAt: now,
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 80.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt1},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Reset: resetsAt jumps forward significantly (>10min diff)
	resetsAt2 := now.Add(7 * 24 * time.Hour)
	snap2 := &api.CursorSnapshot{
		CapturedAt: now.Add(25 * time.Hour),
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 5.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt2},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Old cycle should be closed, new cycle started
	history, err := s.QueryCursorCycleHistory("total_usage", 10)
	if err != nil {
		t.Fatalf("QueryCursorCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("History len = %d, want 1 (old cycle closed)", len(history))
	}
	if history[0].PeakUtilization != 80.0 {
		t.Errorf("Closed cycle PeakUtilization = %f, want 80.0", history[0].PeakUtilization)
	}

	// New cycle should be active
	active, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle: %v", err)
	}
	if active == nil {
		t.Fatal("Expected new active cycle after reset")
	}
	if active.PeakUtilization != 5.0 {
		t.Errorf("New cycle PeakUtilization = %f, want 5.0", active.PeakUtilization)
	}
}

func TestCursorTracker_Process_MultipleQuotas(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	snapshot := &api.CursorSnapshot{
		CapturedAt: now,
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 15.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
			{Name: "auto_usage", Utilization: 3.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
			{Name: "api_usage", Utilization: 12.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		},
	}

	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycleTotal, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle total_usage: %v", err)
	}
	if cycleTotal == nil {
		t.Fatal("Expected active cycle for total_usage")
	}

	cycleAuto, err := s.QueryActiveCursorCycle("auto_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle auto_usage: %v", err)
	}
	if cycleAuto == nil {
		t.Fatal("Expected active cycle for auto_usage")
	}

	cycleApi, err := s.QueryActiveCursorCycle("api_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle api_usage: %v", err)
	}
	if cycleApi == nil {
		t.Fatal("Expected active cycle for api_usage")
	}
}

func TestCursorTracker_UsageSummary(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	snapshot := &api.CursorSnapshot{
		CapturedAt: now,
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Used: 50.0, Limit: 400.0, Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		},
	}

	if _, err := s.InsertCursorSnapshot(snapshot); err != nil {
		t.Fatalf("InsertCursorSnapshot: %v", err)
	}
	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	summary, err := tr.UsageSummary("total_usage")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("UsageSummary returned nil")
	}
	if summary.QuotaName != "total_usage" {
		t.Errorf("QuotaName = %q, want total_usage", summary.QuotaName)
	}
	if summary.CurrentUtil != 12.5 {
		t.Errorf("CurrentUtil = %f, want 12.5", summary.CurrentUtil)
	}
	if summary.ResetsAt == nil {
		t.Error("ResetsAt should not be nil")
	}
}

func TestCursorTracker_SetOnReset(t *testing.T) {
	s := newTestCursorStore(t)
	tr := NewCursorTracker(s, slog.Default())

	resetCalled := false
	resetQuota := ""
	tr.SetOnReset(func(quotaName string) {
		resetCalled = true
		resetQuota = quotaName
	})

	now := time.Now().UTC()

	// First cycle
	resetsAt1 := now.Add(24 * time.Hour)
	snap1 := &api.CursorSnapshot{
		CapturedAt: now,
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 80.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt1},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Trigger reset
	resetsAt2 := now.Add(7 * 24 * time.Hour)
	snap2 := &api.CursorSnapshot{
		CapturedAt: now.Add(25 * time.Hour),
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Utilization: 5.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt2},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	if !resetCalled {
		t.Error("OnReset callback was not called")
	}
	if resetQuota != "total_usage" {
		t.Errorf("OnReset quotaName = %q, want total_usage", resetQuota)
	}
}
