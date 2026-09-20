package store

import (
	"testing"
	"time"
)

// A limit fully consumed by the active cycle must yield history-free output,
// not every cycle ever recorded.
func TestQueryMuseCycleOverviewRespectsLimitOfOne(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	const quota = "window_5h"
	base := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < 5; i++ {
		start := base.Add(time.Duration(i) * time.Hour)
		reset := start.Add(time.Hour)
		if _, err := s.CreateMuseCycle(quota, start, &reset); err != nil {
			t.Fatalf("create cycle %d: %v", i, err)
		}
		if err := s.CloseMuseCycle(quota, start.Add(time.Hour), 10, 5); err != nil {
			t.Fatalf("close cycle %d: %v", i, err)
		}
	}
	// One active cycle on top of the five closed ones.
	active := time.Now().UTC()
	activeReset := active.Add(time.Hour)
	if _, err := s.CreateMuseCycle(quota, active, &activeReset); err != nil {
		t.Fatalf("create active cycle: %v", err)
	}

	rows, err := s.QueryMuseCycleOverview(quota, 1)
	if err != nil {
		t.Fatalf("QueryMuseCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the active cycle alone)", len(rows))
	}

	rows, err = s.QueryMuseCycleOverview(quota, 3)
	if err != nil {
		t.Fatalf("QueryMuseCycleOverview: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
}

// The history query filters on cycle_end IS NOT NULL, but an empty string is
// not NULL: such a row must not yield a nil CycleEnd that handlers dereference.
func TestScanMuseCycleEmptyTimestamps(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(
		`INSERT INTO muse_reset_cycles (quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta)
		 VALUES (?, ?, '', '', 0, 0)`,
		"window_5h", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	history, err := s.QueryMuseCycleHistory("window_5h")
	if err != nil {
		t.Fatalf("QueryMuseCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d rows, want 1 (empty string passes IS NOT NULL)", len(history))
	}
	if history[0].ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil: an empty timestamp must not become a zero time", history[0].ResetsAt)
	}
}
