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
