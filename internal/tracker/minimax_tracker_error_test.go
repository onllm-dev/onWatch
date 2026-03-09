package tracker

import (
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestMiniMaxTracker_ProcessAndSummary_ErrorOnClosedStore(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	tr := NewMiniMaxTracker(s, nil)
	resetAt := time.Now().UTC().Add(2 * time.Hour)
	err = tr.Process(&api.MiniMaxSnapshot{
		CapturedAt: time.Now().UTC(),
		Models: []api.MiniMaxModelQuota{
			{ModelName: "MiniMax-M2", Total: 1500, Used: 100, Remain: 1400, UsedPercent: 6.7, ResetAt: &resetAt},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "minimax tracker: MiniMax-M2") {
		t.Fatalf("Process(closed store) error = %v", err)
	}

	_, err = tr.UsageSummary("MiniMax-M2")
	if err == nil || !strings.Contains(err.Error(), "failed to query active cycle") {
		t.Fatalf("UsageSummary(closed store) error = %v", err)
	}
}

func TestMiniMaxTracker_Process_ResetByUsageDrop(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewMiniMaxTracker(s, nil)
	resetAt := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	base := time.Now().UTC().Add(-40 * time.Minute).Truncate(time.Second)

	resetCalls := 0
	tr.SetOnReset(func(model string) {
		if model == "MiniMax-M2" {
			resetCalls++
		}
	})

	s1 := &api.MiniMaxSnapshot{
		CapturedAt: base,
		Models: []api.MiniMaxModelQuota{
			{ModelName: "MiniMax-M2", Total: 15000, Used: 1000, Remain: 14000, UsedPercent: 6.67, ResetAt: &resetAt},
		},
	}
	s2 := &api.MiniMaxSnapshot{
		CapturedAt: base.Add(5 * time.Minute),
		Models: []api.MiniMaxModelQuota{
			{ModelName: "MiniMax-M2", Total: 15000, Used: 1600, Remain: 13400, UsedPercent: 10.67, ResetAt: &resetAt},
		},
	}
	s3 := &api.MiniMaxSnapshot{
		CapturedAt: base.Add(10 * time.Minute),
		Models: []api.MiniMaxModelQuota{
			{ModelName: "MiniMax-M2", Total: 15000, Used: 500, Remain: 14500, UsedPercent: 3.33, ResetAt: &resetAt},
		},
	}

	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process(s1): %v", err)
	}
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process(s2): %v", err)
	}
	if err := tr.Process(s3); err != nil {
		t.Fatalf("Process(s3): %v", err)
	}

	history, err := s.QueryMiniMaxCycleHistory("MiniMax-M2")
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("closed cycles = %d, want 1", len(history))
	}
	if resetCalls != 1 {
		t.Fatalf("reset callback calls = %d, want 1", resetCalls)
	}
}

func TestMiniMaxTracker_UsageSummary_ProjectionAndLatestFallback(t *testing.T) {
	t.Run("projection clamps to total", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		now := time.Now().UTC().Truncate(time.Second)
		resetAt := now.Add(90 * time.Minute)
		cycleStart := now.Add(-2 * time.Hour)

		if _, err := s.CreateMiniMaxCycle("MiniMax-M2", cycleStart, &resetAt); err != nil {
			t.Fatalf("CreateMiniMaxCycle: %v", err)
		}
		if err := s.UpdateMiniMaxCycle("MiniMax-M2", 1400, 3000); err != nil {
			t.Fatalf("UpdateMiniMaxCycle: %v", err)
		}

		snap := &api.MiniMaxSnapshot{
			CapturedAt: now,
			Models: []api.MiniMaxModelQuota{
				{
					ModelName:   "MiniMax-M2",
					Total:       1500,
					Used:        1200,
					Remain:      300,
					UsedPercent: 80,
					ResetAt:     &resetAt,
				},
			},
		}
		if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot: %v", err)
		}

		tr := NewMiniMaxTracker(s, nil)
		summary, err := tr.UsageSummary("MiniMax-M2")
		if err != nil {
			t.Fatalf("UsageSummary: %v", err)
		}
		if summary.CurrentRate <= 0 {
			t.Fatalf("CurrentRate = %f, want > 0", summary.CurrentRate)
		}
		if summary.ProjectedUsage != summary.Total {
			t.Fatalf("ProjectedUsage = %d, want clamped total %d", summary.ProjectedUsage, summary.Total)
		}
	})

	t.Run("latest snapshot supplies reset when no active cycle", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		resetAt := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Second)
		snap := &api.MiniMaxSnapshot{
			CapturedAt: time.Now().UTC().Truncate(time.Second),
			Models: []api.MiniMaxModelQuota{
				{
					ModelName:   "MiniMax-M2.5",
					Total:       8000,
					Used:        6400,
					Remain:      1600,
					UsedPercent: 80,
					ResetAt:     &resetAt,
				},
			},
		}
		if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot: %v", err)
		}

		tr := NewMiniMaxTracker(s, nil)
		summary, err := tr.UsageSummary("MiniMax-M2.5")
		if err != nil {
			t.Fatalf("UsageSummary: %v", err)
		}
		if summary.ResetAt == nil {
			t.Fatal("expected reset time from latest snapshot")
		}
		if summary.CurrentUsed != 6400 || summary.CurrentRemain != 1600 {
			t.Fatalf("unexpected latest values: %+v", summary)
		}
	})
}

func TestMiniMaxTracker_processModel_HasLastValueWithoutModelCache(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewMiniMaxTracker(s, nil)
	tr.hasLastValue = true // simulate post-restart state

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.CreateMiniMaxCycle("MiniMax-M2.5", now.Add(-10*time.Minute), nil); err != nil {
		t.Fatalf("CreateMiniMaxCycle: %v", err)
	}

	model := api.MiniMaxModelQuota{
		ModelName:   "MiniMax-M2.5",
		Total:       8000,
		Used:        1500,
		Remain:      6500,
		UsedPercent: 18.75,
	}
	if err := tr.processModel(model, now); err != nil {
		t.Fatalf("processModel: %v", err)
	}

	active, err := s.QueryActiveMiniMaxCycle("MiniMax-M2.5")
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle: %v", err)
	}
	if active == nil || active.PeakUsed != 1500 {
		t.Fatalf("unexpected active cycle after processModel: %+v", active)
	}
}
