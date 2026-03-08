package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestMiniMaxStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func miniMaxTrackerSnapshot(capturedAt time.Time, resetAt *time.Time, used int) *api.MiniMaxSnapshot {
	total := 15000
	return &api.MiniMaxSnapshot{
		CapturedAt: capturedAt,
		Models: []api.MiniMaxModelQuota{
			{
				ModelName:      "MiniMax-M2",
				Total:          total,
				Used:           used,
				Remain:         total - used,
				UsedPercent:    (float64(used) / float64(total)) * 100,
				ResetAt:        resetAt,
				TimeUntilReset: time.Hour,
			},
		},
	}
}

func TestMiniMaxTracker_Process(t *testing.T) {
	s := newTestMiniMaxStore(t)
	tr := NewMiniMaxTracker(s, slog.Default())

	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(2 * time.Hour)
	snap := miniMaxTrackerSnapshot(now, &resetAt, 1200)

	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	active, err := s.QueryActiveMiniMaxCycle("MiniMax-M2")
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected active cycle")
	}
	if active.PeakUsed != 1200 {
		t.Fatalf("peak=%d", active.PeakUsed)
	}
}

func TestMiniMaxTracker_ResetDetection(t *testing.T) {
	s := newTestMiniMaxStore(t)
	tr := NewMiniMaxTracker(s, slog.Default())

	resetCalled := false
	tr.SetOnReset(func(modelName string) {
		if modelName == "MiniMax-M2" {
			resetCalled = true
		}
	})

	now := time.Now().UTC().Truncate(time.Second)
	resetAt1 := now.Add(2 * time.Hour)
	if err := tr.Process(miniMaxTrackerSnapshot(now, &resetAt1, 9000)); err != nil {
		t.Fatalf("Process #1: %v", err)
	}

	// Advance reset window + drop usage to trigger reset detection.
	resetAt2 := now.Add(7 * time.Hour)
	if err := tr.Process(miniMaxTrackerSnapshot(now.Add(3*time.Minute), &resetAt2, 300)); err != nil {
		t.Fatalf("Process #2: %v", err)
	}

	if !resetCalled {
		t.Fatal("expected reset callback")
	}

	history, err := s.QueryMiniMaxCycleHistory("MiniMax-M2")
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len=%d", len(history))
	}

	active, err := s.QueryActiveMiniMaxCycle("MiniMax-M2")
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected active cycle after reset")
	}
}

func TestMiniMaxTracker_CycleManagement(t *testing.T) {
	s := newTestMiniMaxStore(t)
	tr := NewMiniMaxTracker(s, slog.Default())

	now := time.Now().UTC().Truncate(time.Second)
	resetAt := now.Add(2 * time.Hour)

	if err := tr.Process(miniMaxTrackerSnapshot(now, &resetAt, 1000)); err != nil {
		t.Fatalf("Process #1: %v", err)
	}
	if err := tr.Process(miniMaxTrackerSnapshot(now.Add(1*time.Minute), &resetAt, 1300)); err != nil {
		t.Fatalf("Process #2: %v", err)
	}
	if err := tr.Process(miniMaxTrackerSnapshot(now.Add(2*time.Minute), &resetAt, 1800)); err != nil {
		t.Fatalf("Process #3: %v", err)
	}

	active, err := s.QueryActiveMiniMaxCycle("MiniMax-M2")
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected active cycle")
	}
	if active.PeakUsed != 1800 {
		t.Fatalf("peak=%d", active.PeakUsed)
	}
	if active.TotalDelta != 800 {
		t.Fatalf("totalDelta=%d", active.TotalDelta)
	}
}
