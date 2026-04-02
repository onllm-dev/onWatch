package store

import (
	"testing"
	"time"
)

func TestClosedDB_MiniMaxStoreFunctions(t *testing.T) {
	s := closedStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("InsertMiniMaxSnapshot", func(t *testing.T) {
		_, err := s.InsertMiniMaxSnapshot(newTestMiniMaxSnapshot(now, 100, 20), 2)
		if err == nil {
			t.Fatal("expected error from InsertMiniMaxSnapshot on closed DB")
		}
	})

	t.Run("QueryLatestMiniMax", func(t *testing.T) {
		_, err := s.QueryLatestMiniMax(2)
		if err == nil {
			t.Fatal("expected error from QueryLatestMiniMax on closed DB")
		}
	})

	t.Run("QueryMiniMaxRange", func(t *testing.T) {
		_, err := s.QueryMiniMaxRange(now.Add(-time.Hour), now.Add(time.Hour), 2)
		if err == nil {
			t.Fatal("expected error from QueryMiniMaxRange on closed DB")
		}
	})

	t.Run("CreateMiniMaxCycle", func(t *testing.T) {
		_, err := s.CreateMiniMaxCycle("MiniMax-M2", now, nil, 2)
		if err == nil {
			t.Fatal("expected error from CreateMiniMaxCycle on closed DB")
		}
	})

	t.Run("CloseMiniMaxCycle", func(t *testing.T) {
		err := s.CloseMiniMaxCycle("MiniMax-M2", now, 10, 5, 2)
		if err == nil {
			t.Fatal("expected error from CloseMiniMaxCycle on closed DB")
		}
	})

	t.Run("UpdateMiniMaxCycle", func(t *testing.T) {
		err := s.UpdateMiniMaxCycle("MiniMax-M2", 10, 5, 2)
		if err == nil {
			t.Fatal("expected error from UpdateMiniMaxCycle on closed DB")
		}
	})

	t.Run("QueryActiveMiniMaxCycle", func(t *testing.T) {
		_, err := s.QueryActiveMiniMaxCycle("MiniMax-M2", 2) 
		if err == nil {
			t.Fatal("expected error from QueryActiveMiniMaxCycle on closed DB")
		}
	})

	t.Run("QueryMiniMaxCycleHistory", func(t *testing.T) {
		_, err := s.QueryMiniMaxCycleHistory("MiniMax-M2", 2)
		if err == nil {
			t.Fatal("expected error from QueryMiniMaxCycleHistory on closed DB")
		}
	})

	t.Run("QueryMiniMaxUsageSeries", func(t *testing.T) {
		_, err := s.QueryMiniMaxUsageSeries("MiniMax-M2", now.Add(-time.Hour), 2)
		if err == nil {
			t.Fatal("expected error from QueryMiniMaxUsageSeries on closed DB")
		}
	})

	t.Run("QueryAllMiniMaxModelNames", func(t *testing.T) {
		_, err := s.QueryAllMiniMaxModelNames(2)
		if err == nil {
			t.Fatal("expected error from QueryAllMiniMaxModelNames on closed DB")
		}
	})

	t.Run("queryMiniMaxSnapshotAtOrBefore", func(t *testing.T) {
		_, err := s.queryMiniMaxSnapshotAtOrBefore(now, 2)
		if err == nil {
			t.Fatal("expected error from queryMiniMaxSnapshotAtOrBefore on closed DB")
		}
	})

	t.Run("minimaxCrossQuotasAt", func(t *testing.T) {
		_, err := s.minimaxCrossQuotasAt(now, 2)
		if err == nil {
			t.Fatal("expected error from minimaxCrossQuotasAt on closed DB")
		}
	})

	t.Run("QueryMiniMaxCycleOverview", func(t *testing.T) {
		_, err := s.QueryMiniMaxCycleOverview("MiniMax-M2", 10, 2)
		if err == nil {
			t.Fatal("expected error from QueryMiniMaxCycleOverview on closed DB")
		}
	})

	t.Run("QueryMiniMaxCycleOverview default-group query error", func(t *testing.T) {
		_, err := s.QueryMiniMaxCycleOverview("", 10, 2)
		if err == nil {
			t.Fatal("expected error from QueryMiniMaxCycleOverview(groupBy empty) on closed DB")
		}
	})
}
