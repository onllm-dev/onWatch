package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestOllamaStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOllamaTracker_Process_FirstSnapshot(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	snapshot := &api.OllamaSnapshot{
		CapturedAt:  now,
		Plan:        "pro",
		AccountName: "Ollama Go",
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Utilization: 12.5, Format: api.OllamaQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}

	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveOllamaCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOllamaCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle after first snapshot")
	}
	if cycle.PeakUtilization != 12.5 {
		t.Errorf("PeakUtilization = %f, want 12.5", cycle.PeakUtilization)
	}
}

func TestOllamaTracker_Process_UsageIncrease(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	snap1 := &api.OllamaSnapshot{
		CapturedAt: now,
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Utilization: 12.5, Format: api.OllamaQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	snap2 := &api.OllamaSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Utilization: 25.0, Format: api.OllamaQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveOllamaCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOllamaCycle: %v", err)
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

func TestOllamaTracker_Process_ResetDetection(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	oldReset := now.Add(1 * time.Hour)
	newReset := now.Add(6 * time.Hour)

	snap1 := &api.OllamaSnapshot{
		CapturedAt: now,
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 40, Format: api.OllamaQuotaFormatPercent, ResetsAt: &oldReset},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	snap2 := &api.OllamaSnapshot{
		CapturedAt: now.Add(2 * time.Hour),
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 5, Format: api.OllamaQuotaFormatPercent, ResetsAt: &newReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOllamaCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOllamaCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("completed cycles = %d, want 1", len(history))
	}

	active, err := s.QueryActiveOllamaCycle("weekly")
	if err != nil {
		t.Fatalf("QueryActiveOllamaCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected new active cycle after reset")
	}
}

func TestOllamaTracker_Process_IgnoresFallbackResetTimeDrift(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(7 * 24 * time.Hour)
	snap1 := &api.OllamaSnapshot{
		CapturedAt: now,
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 30, Format: api.OllamaQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	driftedReset := resetsAt.Add(-59 * time.Minute)
	snap2 := &api.OllamaSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 31, Format: api.OllamaQuotaFormatPercent, ResetsAt: &driftedReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOllamaCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOllamaCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("completed cycles = %d, want 0", len(history))
	}
}

func TestOllamaTracker_Process_IgnoresExpiredStoredResetWithinDriftTolerance(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	storedReset := now.Add(time.Hour)
	snap1 := &api.OllamaSnapshot{
		CapturedAt: now,
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 30, Format: api.OllamaQuotaFormatPercent, ResetsAt: &storedReset},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	currentReset := storedReset.Add(59 * time.Minute)
	snap2 := &api.OllamaSnapshot{
		CapturedAt: storedReset.Add(3 * time.Minute),
		Quotas: []api.OllamaQuota{
			{Name: "weekly", Utilization: 31, Format: api.OllamaQuotaFormatPercent, ResetsAt: &currentReset},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryOllamaCycleHistory("weekly")
	if err != nil {
		t.Fatalf("QueryOllamaCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("completed cycles = %d, want 0", len(history))
	}
}

func TestOllamaTracker_UsageSummary(t *testing.T) {
	s := newTestOllamaStore(t)
	tr := NewOllamaTracker(s, slog.Default())

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)
	snap := &api.OllamaSnapshot{
		CapturedAt:  now,
		Plan:        "pro",
		AccountName: "Ollama Go",
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Utilization: 20, Format: api.OllamaQuotaFormatPercent, ResetsAt: &resetsAt},
		},
	}
	if _, err := s.InsertOllamaSnapshot(snap); err != nil {
		t.Fatalf("InsertOllamaSnapshot: %v", err)
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
