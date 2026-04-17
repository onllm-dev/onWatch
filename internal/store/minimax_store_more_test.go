package store

import (
	"testing"
	"time"
)

func TestMiniMaxStore_QueryMiniMaxCycleOverview_EmptyDefaultGroup(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryMiniMaxCycleOverview("", 10, 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleOverview(empty): %v", err)
	}
	if rows != nil {
		t.Fatalf("expected nil rows for empty store, got %+v", rows)
	}
}

func TestMiniMaxStore_QueryMiniMaxCycleOverview_ActiveHistorySortAndLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second)
	if _, err := s.InsertMiniMaxSnapshot(newTestMiniMaxSnapshot(base.Add(-20*time.Minute), 500, 100), 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot(old): %v", err)
	}
	if _, err := s.InsertMiniMaxSnapshot(newTestMiniMaxSnapshot(base.Add(-5*time.Minute), 900, 180), 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot(new): %v", err)
	}

	closedStart := base.Add(-2 * time.Hour)
	if _, err := s.CreateMiniMaxCycle("MiniMax-M2", closedStart, nil, 2); err != nil {
		t.Fatalf("CreateMiniMaxCycle(closed): %v", err)
	}
	if err := s.CloseMiniMaxCycle("MiniMax-M2", closedStart.Add(30*time.Minute), 700, 250, 2); err != nil {
		t.Fatalf("CloseMiniMaxCycle(closed): %v", err)
	}

	activeReset := base.Add(2 * time.Hour)
	if _, err := s.CreateMiniMaxCycle("MiniMax-M2", base.Add(-10*time.Minute), &activeReset, 2); err != nil {
		t.Fatalf("CreateMiniMaxCycle(active): %v", err)
	}
	if err := s.UpdateMiniMaxCycle("MiniMax-M2", 950, 320, 2); err != nil {
		t.Fatalf("UpdateMiniMaxCycle(active): %v", err)
	}

	rows, err := s.QueryMiniMaxCycleOverview("", 10, 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleOverview(default group): %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("overview rows = %d, want at least 2", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Fatalf("expected active row first, got cycle_end=%v", rows[0].CycleEnd)
	}
	if rows[0].QuotaType != "MiniMax-M2" {
		t.Fatalf("active row model = %q, want MiniMax-M2", rows[0].QuotaType)
	}
	if len(rows[0].CrossQuotas) == 0 {
		t.Fatal("expected active row cross quotas")
	}
	if rows[1].CycleEnd == nil {
		t.Fatalf("expected history row second, got cycle_end=%v", rows[1].CycleEnd)
	}

	limited, err := s.QueryMiniMaxCycleOverview("MiniMax-M2", 1, 2)
	if err != nil {
		t.Fatalf("QueryMiniMaxCycleOverview(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited rows = %d, want 1", len(limited))
	}
	if limited[0].CycleEnd != nil {
		t.Fatal("expected the active row to survive limit=1")
	}
}

func TestMiniMaxStore_MiniMaxCrossQuotasAt_FallbackToLatest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	entries, err := s.minimaxCrossQuotasAt(time.Now().UTC(), 2)
	if err != nil {
		t.Fatalf("minimaxCrossQuotasAt(empty): %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries for empty store, got %+v", entries)
	}

	captured := time.Now().UTC().Truncate(time.Second)
	if _, err := s.InsertMiniMaxSnapshot(newTestMiniMaxSnapshot(captured, 1100, 220), 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	beforeAllSnapshots := captured.Add(-24 * time.Hour)
	entries, err = s.minimaxCrossQuotasAt(beforeAllSnapshots, 2)
	if err != nil {
		t.Fatalf("minimaxCrossQuotasAt(fallback): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].Name != "MiniMax-M2" || entries[1].Name != "MiniMax-M2.5-highspeed" {
		t.Fatalf("entries not sorted by name: %+v", entries)
	}
	if entries[0].Value != 1100 || entries[0].Limit != 15000 {
		t.Fatalf("unexpected first entry values: %+v", entries[0])
	}
}
