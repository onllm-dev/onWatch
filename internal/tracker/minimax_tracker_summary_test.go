package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func insertAndProcessMiniMaxSnapshot(t *testing.T, s *store.Store, tr *MiniMaxTracker, snap *api.MiniMaxSnapshot) {
	t.Helper()
	if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}
}

func TestMiniMaxTracker_UsageSummary(t *testing.T) {
	s := newTestMiniMaxStore(t)
	tr := NewMiniMaxTracker(s, nil)
	if tr.logger == nil {
		t.Fatal("expected default logger when nil")
	}

	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	firstReset := base.Add(2 * time.Hour)
	secondReset := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	insertAndProcessMiniMaxSnapshot(t, s, tr, miniMaxTrackerSnapshot(base, &firstReset, 1000))
	insertAndProcessMiniMaxSnapshot(t, s, tr, miniMaxTrackerSnapshot(base.Add(30*time.Minute), &firstReset, 1800))
	insertAndProcessMiniMaxSnapshot(t, s, tr, miniMaxTrackerSnapshot(base.Add(65*time.Minute), &secondReset, 200))
	insertAndProcessMiniMaxSnapshot(t, s, tr, miniMaxTrackerSnapshot(base.Add(95*time.Minute), &secondReset, 700))

	summary, err := tr.UsageSummary("MiniMax-M2")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.ModelName != "MiniMax-M2" {
		t.Fatalf("ModelName = %q, want MiniMax-M2", summary.ModelName)
	}
	if summary.CompletedCycles != 1 {
		t.Fatalf("CompletedCycles = %d, want 1", summary.CompletedCycles)
	}
	if summary.TotalTracked != 1300 {
		t.Fatalf("TotalTracked = %d, want 1300", summary.TotalTracked)
	}
	if summary.PeakCycle != 1800 {
		t.Fatalf("PeakCycle = %d, want 1800", summary.PeakCycle)
	}
	if summary.CurrentUsed != 700 || summary.CurrentRemain != 14300 || summary.Total != 15000 {
		t.Fatalf("unexpected current state: %+v", summary)
	}
	if summary.UsagePercent <= 0 {
		t.Fatalf("expected positive UsagePercent, got %f", summary.UsagePercent)
	}
	if summary.CurrentRate <= 0 {
		t.Fatalf("expected positive CurrentRate, got %f", summary.CurrentRate)
	}
	if summary.ProjectedUsage < summary.CurrentUsed {
		t.Fatalf("ProjectedUsage = %d, want >= CurrentUsed %d", summary.ProjectedUsage, summary.CurrentUsed)
	}
	if summary.ResetAt == nil || summary.TimeUntilReset <= 0 {
		t.Fatalf("expected future reset time, got ResetAt=%v TimeUntilReset=%v", summary.ResetAt, summary.TimeUntilReset)
	}
	if summary.TrackingSince.IsZero() {
		t.Fatal("expected TrackingSince to be populated")
	}
}

func TestMiniMaxTracker_UsageSummary_NoDataAndEmptyModel(t *testing.T) {
	s := newTestMiniMaxStore(t)
	tr := NewMiniMaxTracker(s, slog.Default())

	if err := tr.Process(&api.MiniMaxSnapshot{
		CapturedAt: time.Now().UTC(),
		Models: []api.MiniMaxModelQuota{
			{ModelName: "", Total: 100, Used: 5, Remain: 95, UsedPercent: 5},
		},
	}); err != nil {
		t.Fatalf("Process(empty model): %v", err)
	}

	summary, err := tr.UsageSummary("missing-model")
	if err != nil {
		t.Fatalf("UsageSummary(missing-model): %v", err)
	}
	if summary == nil {
		t.Fatal("expected zero-value summary for missing model")
	}
	if summary.ModelName != "missing-model" || summary.TotalTracked != 0 || summary.CompletedCycles != 0 {
		t.Fatalf("unexpected zero-value summary: %+v", summary)
	}
}
