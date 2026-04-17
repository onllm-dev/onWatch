package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func newTestMiniMaxSnapshot(capturedAt time.Time, usedM2, usedM25 int) *api.MiniMaxSnapshot {
	resetAt := capturedAt.Add(5 * time.Hour)
	start := capturedAt.Add(-1 * time.Hour)
	return &api.MiniMaxSnapshot{
		CapturedAt: capturedAt,
		RawJSON:    `{"ok":true}`,
		Models: []api.MiniMaxModelQuota{
			{
				ModelName:      "MiniMax-M2",
				Total:          15000,
				Used:           usedM2,
				Remain:         15000 - usedM2,
				UsedPercent:    float64(usedM2) / 150,
				ResetAt:        &resetAt,
				WindowStart:    &start,
				WindowEnd:      &resetAt,
				TimeUntilReset: 5 * time.Hour,
			},
			{
				ModelName:      "MiniMax-M2.5-highspeed",
				Total:          8000,
				Used:           usedM25,
				Remain:         8000 - usedM25,
				UsedPercent:    float64(usedM25) / 80,
				ResetAt:        &resetAt,
				WindowStart:    &start,
				WindowEnd:      &resetAt,
				TimeUntilReset: 5 * time.Hour,
			},
		},
	}
}

func TestMiniMaxStore_InsertAndQueryLatest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	snap := newTestMiniMaxSnapshot(now, 1000, 200)
	id, err := s.InsertMiniMaxSnapshot(snap, 2)
	if err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}
	if id <= 0 {
		t.Fatalf("invalid snapshot id %d", id)
	}

	latest, err := s.QueryLatestMiniMax(2)
	if err != nil {
		t.Fatalf("QueryLatestMiniMax: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest snapshot")
	}
	if len(latest.Models) != 2 {
		t.Fatalf("models=%d", len(latest.Models))
	}
	if latest.Models[0].ModelName != "MiniMax-M2" {
		t.Fatalf("first model=%q", latest.Models[0].ModelName)
	}
}

func TestMiniMaxStore_QueryRange(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second)
	for i := range 3 {
		snap := newTestMiniMaxSnapshot(base.Add(time.Duration(i)*time.Minute), 1000+(i*100), 200+(i*20))
		if _, err := s.InsertMiniMaxSnapshot(snap, 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot[%d]: %v", i, err)
		}
	}

	all, err := s.QueryMiniMaxRange(base.Add(-time.Minute), base.Add(5*time.Minute), 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxRange: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all)=%d", len(all))
	}

	limited, err := s.QueryMiniMaxRange(base.Add(-time.Minute), base.Add(5*time.Minute), 2, 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxRange(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len(limited)=%d", len(limited))
	}
	if !limited[0].CapturedAt.Equal(base.Add(1 * time.Minute)) {
		t.Fatalf("first limited capture=%s", limited[0].CapturedAt)
	}
	if !limited[1].CapturedAt.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("second limited capture=%s", limited[1].CapturedAt)
	}
}

func TestMiniMaxStore_Cycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	start := time.Now().UTC().Truncate(time.Second)
	resetAt := start.Add(5 * time.Hour)

	id, err := s.CreateMiniMaxCycle("MiniMax-M2", start, &resetAt, 2)
	if err != nil {
		t.Fatalf("CreateMiniMaxCycle: %v", err)
	}
	if id <= 0 {
		t.Fatalf("invalid cycle id %d", id)
	}

	active, err := s.QueryActiveMiniMaxCycle("MiniMax-M2", 2)
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected active cycle")
	}

	if err := s.UpdateMiniMaxCycle("MiniMax-M2", 1400, 400, 2); err != nil {
		t.Fatalf("UpdateMiniMaxCycle: %v", err)
	}

	active, err = s.QueryActiveMiniMaxCycle("MiniMax-M2", 2)
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle(update): %v", err)
	}
	if active.PeakUsed != 1400 || active.TotalDelta != 400 {
		t.Fatalf("unexpected active values peak=%d delta=%d", active.PeakUsed, active.TotalDelta)
	}

	if err := s.CloseMiniMaxCycle("MiniMax-M2", start.Add(30*time.Minute), 1500, 500, 2); err != nil {
		t.Fatalf("CloseMiniMaxCycle: %v", err)
	}

	active, err = s.QueryActiveMiniMaxCycle("MiniMax-M2", 2)
	if err != nil {
		t.Fatalf("QueryActiveMiniMaxCycle(closed): %v", err)
	}
	if active != nil {
		t.Fatal("expected no active cycle after close")
	}

	history, err := s.QueryMiniMaxCycleHistory("MiniMax-M2", 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len=%d", len(history))
	}
	if history[0].PeakUsed != 1500 || history[0].TotalDelta != 500 {
		t.Fatalf("unexpected history peak=%d delta=%d", history[0].PeakUsed, history[0].TotalDelta)
	}
}

func TestMiniMaxStore_UsageSeriesAndModelNames(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second)
	for i := range 2 {
		snap := newTestMiniMaxSnapshot(base.Add(time.Duration(i)*time.Minute), 800+(i*100), 120+(i*10))
		if _, err := s.InsertMiniMaxSnapshot(snap, 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot[%d]: %v", i, err)
		}
	}

	series, err := s.QueryMiniMaxUsageSeries("MiniMax-M2", base.Add(-1*time.Minute), 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxUsageSeries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("series len=%d", len(series))
	}
	if series[0].Used != 800 || series[1].Used != 900 {
		t.Fatalf("unexpected used values: %+v", series)
	}

	names, err := s.QueryAllMiniMaxModelNames(2)
	if err != nil {
		t.Fatalf("QueryAllMiniMaxModelNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names len=%d", len(names))
	}
	if names[0] != "MiniMax-M2" || names[1] != "MiniMax-M2.5-highspeed" {
		t.Fatalf("unexpected names=%v", names)
	}
}

func TestMiniMaxStore_QueryCycleOverview(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second)
	snap := newTestMiniMaxSnapshot(base, 900, 130)
	if _, err := s.InsertMiniMaxSnapshot(snap, 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	if _, err := s.CreateMiniMaxCycle("MiniMax-M2", base, nil, 2); err != nil {
		t.Fatalf("CreateMiniMaxCycle: %v", err)
	}
	if err := s.UpdateMiniMaxCycle("MiniMax-M2", 1100, 300, 2); err != nil {
		t.Fatalf("UpdateMiniMaxCycle: %v", err)
	}
	if err := s.CloseMiniMaxCycle("MiniMax-M2", base.Add(20*time.Minute), 1200, 350, 2); err != nil {
		t.Fatalf("CloseMiniMaxCycle: %v", err)
	}

	rows, err := s.QueryMiniMaxCycleOverview("MiniMax-M2", 10, 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleOverview: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected overview rows")
	}
	if len(rows[0].CrossQuotas) == 0 {
		t.Fatal("expected cross quota data")
	}
}

func TestMiniMaxStore_AccountIsolation(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Create two accounts
	acc1, _ := s.GetOrCreateProviderAccount("minimax", "account1")
	acc2, _ := s.GetOrCreateProviderAccount("minimax", "account2")

	// Insert different data for each account
	snap1 := newTestMiniMaxSnapshot(now, 500, 200)
	snap2 := newTestMiniMaxSnapshot(now, 900, 400)

	if _, err := s.InsertMiniMaxSnapshot(snap1, acc1.ID); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot(acc1): %v", err)
	}
	if _, err := s.InsertMiniMaxSnapshot(snap2, acc2.ID); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot(acc2): %v", err)
	}

	// QueryLatest for account 1 should NOT see account 2's data
	latest1, err := s.QueryLatestMiniMax(acc1.ID)
	if err != nil {
		t.Fatalf("QueryLatestMiniMax(acc1): %v", err)
	}
	if latest1 == nil {
		t.Fatal("expected non-nil latest for acc1")
	}
	for _, m := range latest1.Models {
		if m.ModelName == "MiniMax-M2" && m.Used != 500 {
			t.Errorf("acc1 MiniMax-M2 used=%d, want 500", m.Used)
		}
	}

	latest2, err := s.QueryLatestMiniMax(acc2.ID)
	if err != nil {
		t.Fatalf("QueryLatestMiniMax(acc2): %v", err)
	}
	if latest2 == nil {
		t.Fatal("expected non-nil latest for acc2")
	}
	for _, m := range latest2.Models {
		if m.ModelName == "MiniMax-M2" && m.Used != 900 {
			t.Errorf("acc2 MiniMax-M2 used=%d, want 900", m.Used)
		}
	}

	// QueryRange should isolate by account
	range1, err := s.QueryMiniMaxRange(now.Add(-time.Hour), now.Add(time.Hour), acc1.ID)
	if err != nil {
		t.Fatalf("QueryMiniMaxRange(acc1): %v", err)
	}
	range2, err := s.QueryMiniMaxRange(now.Add(-time.Hour), now.Add(time.Hour), acc2.ID)
	if err != nil {
		t.Fatalf("QueryMiniMaxRange(acc2): %v", err)
	}
	if len(range1) != 1 || len(range2) != 1 {
		t.Fatalf("expected 1 snapshot per account, got acc1=%d acc2=%d", len(range1), len(range2))
	}

	// Cycles should isolate by account
	if _, err := s.CreateMiniMaxCycle("MiniMax-M2", now, nil, acc1.ID); err != nil {
		t.Fatalf("CreateMiniMaxCycle(acc1): %v", err)
	}
	if _, err := s.CreateMiniMaxCycle("MiniMax-M2", now, nil, acc2.ID); err != nil {
		t.Fatalf("CreateMiniMaxCycle(acc2): %v", err)
	}

	cycle1, err := s.QueryActiveMiniMaxCycle("MiniMax-M2", acc1.ID)
	if err != nil || cycle1 == nil {
		t.Fatalf("QueryActiveMiniMaxCycle(acc1): err=%v cycle=%v", err, cycle1)
	}
	cycle2, err := s.QueryActiveMiniMaxCycle("MiniMax-M2", acc2.ID)
	if err != nil || cycle2 == nil {
		t.Fatalf("QueryActiveMiniMaxCycle(acc2): err=%v cycle=%v", err, cycle2)
	}
	if cycle1.ID == cycle2.ID {
		t.Error("expected different cycle IDs for different accounts")
	}

	// Model names should be per-account
	names1, _ := s.QueryAllMiniMaxModelNames(acc1.ID)
	names2, _ := s.QueryAllMiniMaxModelNames(acc2.ID)
	if len(names1) == 0 || len(names2) == 0 {
		t.Errorf("expected model names for both accounts: acc1=%v acc2=%v", names1, names2)
	}

	// Non-existent account should return no data
	latestNone, err := s.QueryLatestMiniMax(99999)
	if err != nil {
		t.Fatalf("QueryLatestMiniMax(99999): %v", err)
	}
	if latestNone != nil {
		t.Error("expected nil for non-existent account")
	}
}
