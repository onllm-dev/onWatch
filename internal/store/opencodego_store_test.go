package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func testOpenCodeGoSnapshot(ts time.Time, rolling float64) *api.OpenCodeGoSnapshot {
	return &api.OpenCodeGoSnapshot{
		CapturedAt: ts,
		RawJSON:    `{"ok":true}`,
		Windows: []api.OpenCodeGoWindowValue{
			{WindowName: "rolling", UsagePercent: rolling, ResetInSec: 3600, Status: "ok"},
			{WindowName: "weekly", UsagePercent: 10, ResetInSec: 7200, Status: "ok"},
		},
	}
}

func TestOpenCodeGoStore_InsertAndQueryLatest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.InsertOpenCodeGoSnapshot(testOpenCodeGoSnapshot(now, 12.5)); err != nil {
		t.Fatalf("InsertOpenCodeGoSnapshot: %v", err)
	}

	latest, err := s.QueryLatestOpenCodeGo()
	if err != nil {
		t.Fatalf("QueryLatestOpenCodeGo: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest snapshot")
	}
	if len(latest.Windows) != 2 {
		t.Fatalf("len(latest.Windows) = %d", len(latest.Windows))
	}
}

func TestOpenCodeGoStore_QuerySeries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	for i := range 3 {
		ts := now.Add(time.Duration(i) * time.Minute)
		if _, err := s.InsertOpenCodeGoSnapshot(testOpenCodeGoSnapshot(ts, float64(20+i))); err != nil {
			t.Fatalf("insert[%d]: %v", i, err)
		}
	}

	series, err := s.QueryOpenCodeGoUsageSeries("rolling", now.Add(-time.Minute), now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("QueryOpenCodeGoUsageSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("len(series) = %d", len(series))
	}
}

func TestOpenCodeGoStore_CycleLifecycleAndOverview(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(2 * time.Hour)

	if _, err := s.CreateOpenCodeGoCycle("rolling", now, reset); err != nil {
		t.Fatalf("CreateOpenCodeGoCycle: %v", err)
	}
	if err := s.UpdateOpenCodeGoCycle("rolling", 45, 12); err != nil {
		t.Fatalf("UpdateOpenCodeGoCycle: %v", err)
	}

	active, err := s.QueryActiveOpenCodeGoCycle("rolling")
	if err != nil {
		t.Fatalf("QueryActiveOpenCodeGoCycle: %v", err)
	}
	if active == nil || active.PeakUsage != 45 {
		t.Fatalf("active cycle invalid: %+v", active)
	}

	if err := s.CloseOpenCodeGoCycle("rolling", now.Add(time.Hour), 60, 20); err != nil {
		t.Fatalf("CloseOpenCodeGoCycle: %v", err)
	}

	history, err := s.QueryOpenCodeGoCycleHistory("rolling", 0)
	if err != nil {
		t.Fatalf("QueryOpenCodeGoCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d", len(history))
	}

	overview, err := s.QueryOpenCodeGoCycleOverview("rolling", 10)
	if err != nil {
		t.Fatalf("QueryOpenCodeGoCycleOverview: %v", err)
	}
	if len(overview) != 1 {
		t.Fatalf("len(overview) = %d", len(overview))
	}
}
