package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestOpenCodeStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCodeTracker_Process_FirstSnapshot(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	snapshot := &api.OpenCodeSnapshot{
		CapturedAt:  now,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 12.5, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}

	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveOpenCodeCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOpenCodeCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle after first snapshot")
	}
	if cycle.PeakUtilization != 12.5 {
		t.Errorf("PeakUtilization = %f, want 12.5", cycle.PeakUtilization)
	}
}

func TestOpenCodeTracker_Process_UsageIncrease(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	snap1 := &api.OpenCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 12.5, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	snap2 := &api.OpenCodeSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 25.0, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveOpenCodeCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOpenCodeCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle")
	}
	if cycle.PeakUtilization != 25.0 {
		t.Errorf("PeakUtilization = %f, want 25.0", cycle.PeakUtilization)
	}
	if cycle.TotalDelta != 12.5 {
		t.Errorf("TotalDelta = %f, want 12.5", cycle.TotalDelta)
	}
}

func TestOpenCodeTracker_Process_ResetDetection(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	oldReset := now.Add(1 * time.Hour)
	newReset := now.Add(6 * time.Hour)

	snap1 := &api.OpenCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 40, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &oldReset},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	snap2 := &api.OpenCodeSnapshot{
		CapturedAt: now.Add(2 * time.Hour),
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 5, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &newReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOpenCodeCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOpenCodeCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("completed cycles = %d, want 1", len(history))
	}

	active, err := s.QueryActiveOpenCodeCycle("weekly")
	if err != nil {
		t.Fatalf("QueryActiveOpenCodeCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected new active cycle after reset")
	}
}

func TestOpenCodeTracker_Process_IgnoresFallbackResetTimeDrift(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(7 * 24 * time.Hour)
	snap1 := &api.OpenCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 30, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	driftedReset := resetsAt.Add(-59 * time.Minute)
	snap2 := &api.OpenCodeSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 31, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &driftedReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOpenCodeCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOpenCodeCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("completed cycles = %d, want 0", len(history))
	}
}

func TestOpenCodeTracker_Process_IgnoresExpiredStoredResetWithinDriftTolerance(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	storedReset := now.Add(time.Hour)
	snap1 := &api.OpenCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 30, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &storedReset},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	currentReset := storedReset.Add(59 * time.Minute)
	snap2 := &api.OpenCodeSnapshot{
		CapturedAt: storedReset.Add(3 * time.Minute),
		Quotas: []api.OpenCodeQuota{
			{Name: "weekly", Utilization: 31, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &currentReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOpenCodeCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOpenCodeCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("completed cycles = %d, want 0", len(history))
	}
}

func TestOpenCodeTracker_UsageSummary(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)
	snap := &api.OpenCodeSnapshot{
		CapturedAt:  now,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 20, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if _, err := s.InsertOpenCodeSnapshot(snap); err != nil {
		t.Fatalf("InsertOpenCodeSnapshot: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	summary, err := tr.UsageSummary("five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected summary")
	}
	if summary.CurrentUtil != 20 {
		t.Errorf("CurrentUtil = %f, want 20", summary.CurrentUtil)
	}
}

// Usage-API five_hour: idle (no ResetsAt) → session opens → session expires.
func TestOpenCodeTracker_Process_IdleCycleAdoptsSessionReset(t *testing.T) {
	s := newTestOpenCodeStore(t)
	tr := NewOpenCodeTracker(s, slog.Default())
	now := time.Now().UTC().Truncate(time.Second)
	sessionEnd := now.Add(5 * time.Hour)
	step := func(at time.Time, util float64, reset *time.Time) {
		t.Helper()
		snap := &api.OpenCodeSnapshot{CapturedAt: at, Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: util, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: reset},
		}}
		if err := tr.Process(snap); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}
	history := func() int {
		t.Helper()
		h, err := s.QueryOpenCodeCycleHistory("five_hour")
		if err != nil {
			t.Fatalf("QueryOpenCodeCycleHistory: %v", err)
		}
		return len(h)
	}

	step(now, 0, nil)
	step(now.Add(time.Minute), 3, &sessionEnd)
	if n := history(); n != 0 {
		t.Fatalf("session start closed %d empty cycle(s), want 0", n)
	}
	active, err := s.QueryActiveOpenCodeCycle("five_hour")
	if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(sessionEnd) ||
		!active.CycleStart.Equal(now.Add(time.Minute)) {
		t.Fatalf("active cycle = %+v (err %v), want start %v and ResetsAt %v", active, err, now.Add(time.Minute), sessionEnd)
	}
	step(now.Add(2*time.Hour), 9, &sessionEnd)
	step(sessionEnd.Add(10*time.Minute), 0, nil)
	h, err := s.QueryOpenCodeCycleHistory("five_hour")
	if err != nil || len(h) != 1 || h[0].PeakUtilization != 9 {
		t.Fatalf("history = %+v (err %v), want one closed cycle with peak 9", h, err)
	}
}
