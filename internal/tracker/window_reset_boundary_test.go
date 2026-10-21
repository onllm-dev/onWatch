package tracker

import (
	"math"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func commandCodeSnapshotAt(now time.Time, util float64, reset *time.Time) *api.CommandCodeSnapshot {
	quota := api.CommandCodeQuota{
		Name:        api.CommandCodeQuotaFiveHour,
		Used:        util / 100 * 14,
		Limit:       14,
		Utilization: util,
		Format:      api.CommandCodeQuotaFormatCredits,
		ResetsAt:    reset,
	}
	return &api.CommandCodeSnapshot{CapturedAt: now, Quotas: []api.CommandCodeQuota{quota}}
}

func openCodeSnapshotAt(now time.Time, util float64, reset *time.Time) *api.OpenCodeSnapshot {
	return &api.OpenCodeSnapshot{CapturedAt: now, Quotas: []api.OpenCodeQuota{
		{Name: "weekly", Utilization: util, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: reset},
	}}
}

// TestWindowResetSampleBelongsToNewCycle guards the live Command Code defect
// where the poll that revealed a reset was credited to the cycle it closed:
// a completed cycle was stored with the peak of the first sample of the next
// window.
func TestWindowResetSampleBelongsToNewCycle(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, now time.Time, oldReset, newReset *time.Time)
	}{
		{
			name: "commandcode",
			run: func(t *testing.T, now time.Time, oldReset, newReset *time.Time) {
				s := newCommandCodeTrackerStore(t)
				tr := NewCommandCodeTracker(s, nil)
				if err := tr.Process(commandCodeSnapshotAt(now, 10, oldReset)); err != nil {
					t.Fatal(err)
				}
				if err := tr.Process(commandCodeSnapshotAt(now.Add(time.Minute), 20, newReset)); err != nil {
					t.Fatal(err)
				}
				history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
				if err != nil || len(history) != 1 {
					t.Fatalf("history = %d, err %v; want 1", len(history), err)
				}
				if math.Abs(history[0].PeakUtilization-10) > 0.01 || math.Abs(history[0].TotalDelta) > 0.01 {
					t.Fatalf("closed cycle peak/delta = %.2f/%.2f, want 10/0; the reset sample belongs to the new cycle", history[0].PeakUtilization, history[0].TotalDelta)
				}
				active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
				if err != nil || active == nil || math.Abs(active.PeakUtilization-20) > 0.01 {
					t.Fatalf("active = %+v, err %v; want peak 20", active, err)
				}
			},
		},
		{
			name: "opencode",
			run: func(t *testing.T, now time.Time, oldReset, newReset *time.Time) {
				s := newTestOpenCodeStore(t)
				tr := NewOpenCodeTracker(s, nil)
				if err := tr.Process(openCodeSnapshotAt(now, 10, oldReset)); err != nil {
					t.Fatal(err)
				}
				if err := tr.Process(openCodeSnapshotAt(now.Add(time.Minute), 20, newReset)); err != nil {
					t.Fatal(err)
				}
				history, err := s.QueryOpenCodeCycleHistory("weekly")
				if err != nil || len(history) != 1 {
					t.Fatalf("history = %d, err %v; want 1", len(history), err)
				}
				if math.Abs(history[0].PeakUtilization-10) > 0.01 || math.Abs(history[0].TotalDelta) > 0.01 {
					t.Fatalf("closed cycle peak/delta = %.2f/%.2f, want 10/0; the reset sample belongs to the new cycle", history[0].PeakUtilization, history[0].TotalDelta)
				}
				active, err := s.QueryActiveOpenCodeCycle("weekly")
				if err != nil || active == nil || math.Abs(active.PeakUtilization-20) > 0.01 {
					t.Fatalf("active = %+v, err %v; want peak 20", active, err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			oldReset := now.Add(time.Hour)
			newReset := now.Add(6 * time.Hour)
			tc.run(t, now, &oldReset, &newReset)
		})
	}
}

// TestWindowStaleStaleStaleFreshFresh exercises the churn reproduced by both
// reviewers: a cycle created while the API still reported the old reset stored
// no reset, so every later sample carrying that stale reset was read as a fresh
// window. The sequence must close exactly one cycle and fire one notification.
func TestWindowStaleStaleStaleFreshFresh(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	stale := now.Add(time.Hour)
	fresh := now.Add(3 * time.Hour)
	steps := []struct {
		at time.Time
		u  float64
		r  *time.Time
	}{
		{now, 10, &stale},
		{now.Add(2 * time.Hour), 1, &stale},
		{now.Add(2*time.Hour + time.Minute), 2, &stale},
		{now.Add(2*time.Hour + 2*time.Minute), 3, &fresh},
		{now.Add(2*time.Hour + 3*time.Minute), 4, &fresh},
	}

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		for _, st := range steps {
			if err := tr.Process(commandCodeSnapshotAt(st.at, st.u, st.r)); err != nil {
				t.Fatal(err)
			}
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		assertOneClosedCycle(t, "commandcode", len(history), resets, history)
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active resets_at = %v, want the fresh reset %v attached", active.ResetsAt, fresh)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		for _, st := range steps {
			if err := tr.Process(openCodeSnapshotAt(st.at, st.u, st.r)); err != nil {
				t.Fatal(err)
			}
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		assertOneClosedCycleGeneric(t, "opencode", len(history), resets, openCodeCycleBounds(history))
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active resets_at = %v, want the fresh reset %v attached", active.ResetsAt, fresh)
		}
	})
}

type cycleBound struct {
	start time.Time
	end   *time.Time
}

func openCodeCycleBounds(cycles []*store.OpenCodeResetCycle) []cycleBound {
	bounds := make([]cycleBound, 0, len(cycles))
	for _, c := range cycles {
		bounds = append(bounds, cycleBound{start: c.CycleStart, end: c.CycleEnd})
	}
	return bounds
}

func assertOneClosedCycle(t *testing.T, provider string, closed, resets int, history []*store.CommandCodeResetCycle) {
	t.Helper()
	if closed != 1 || resets != 1 {
		t.Fatalf("%s closed cycles=%d onReset=%d, want 1 and 1", provider, closed, resets)
	}
	for _, c := range history {
		if c.CycleEnd != nil && c.CycleEnd.Before(c.CycleStart) {
			t.Fatalf("%s cycle %d ends %v before it starts %v", provider, c.ID, c.CycleEnd, c.CycleStart)
		}
	}
}

func assertOneClosedCycleGeneric(t *testing.T, provider string, closed, resets int, bounds []cycleBound) {
	t.Helper()
	if closed != 1 || resets != 1 {
		t.Fatalf("%s closed cycles=%d onReset=%d, want 1 and 1", provider, closed, resets)
	}
	for _, c := range bounds {
		if c.end != nil && c.end.Before(c.start) {
			t.Fatalf("%s cycle ends %v before it starts %v", provider, c.end, c.start)
		}
	}
}

// TestWindowMissedPollsSpanningReset covers a daemon that was offline across a
// reset: the first sample after the gap is the only one that can reveal it.
func TestWindowMissedPollsSpanningReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	oldReset := now.Add(time.Hour)
	fresh := now.Add(6 * time.Hour)
	later := now.Add(4 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(commandCodeSnapshotAt(now, 10, &oldReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(later, 0.5, &fresh)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
		if history[0].CycleEnd == nil || !history[0].CycleEnd.Equal(oldReset) {
			t.Fatalf("cycle end = %v, want the missed reset %v", history[0].CycleEnd, oldReset)
		}
		if history[0].CycleEnd.Before(history[0].CycleStart) {
			t.Fatalf("cycle end %v before start %v", history[0].CycleEnd, history[0].CycleStart)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active = %+v, err %v", active, err)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 10, &oldReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(later, 0.5, &fresh)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
		if history[0].CycleEnd == nil || !history[0].CycleEnd.Equal(oldReset) {
			t.Fatalf("cycle end = %v, want the missed reset %v", history[0].CycleEnd, oldReset)
		}
		if history[0].CycleEnd.Before(history[0].CycleStart) {
			t.Fatalf("cycle end %v before start %v", history[0].CycleEnd, history[0].CycleStart)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active = %+v, err %v", active, err)
		}
	})
}

// TestWindowRestartMidWindowAttachesReset covers a tracker restart in the
// middle of an open window: the reset the API only starts reporting later is
// the same window, not a new one.
func TestWindowRestartMidWindowAttachesReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(3 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		// No reset reported yet, usage already climbing.
		if err := tr.Process(commandCodeSnapshotAt(now, 10, nil)); err != nil {
			t.Fatal(err)
		}
		// Restart mid-window: the same window now reports its reset.
		if err := tr.Process(commandCodeSnapshotAt(now.Add(10*time.Minute), 15, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 || resets != 0 {
			t.Fatalf("closed cycles=%d onReset=%d, want 0 and 0", len(history), resets)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
			t.Fatalf("active = %+v, err %v; want the reset attached", active, err)
		}
		if !active.CycleStart.Equal(now) {
			t.Fatalf("cycle start moved to %v, want the original %v", active.CycleStart, now)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 10, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(now.Add(10*time.Minute), 15, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 || resets != 0 {
			t.Fatalf("closed cycles=%d onReset=%d, want 0 and 0", len(history), resets)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
			t.Fatalf("active = %+v, err %v; want the reset attached", active, err)
		}
		if !active.CycleStart.Equal(now) {
			t.Fatalf("cycle start moved to %v, want the original %v", active.CycleStart, now)
		}
	})
}

// TestWindowResetDropClosesCycle keeps the genuine reset signal: a new reset
// alongside falling utilization really is a new window.
func TestWindowResetDropClosesCycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(commandCodeSnapshotAt(now, 60, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(now.Add(time.Minute), 2, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 60, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(now.Add(time.Minute), 2, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
	})
}

// TestWindowStaleResetIsNotStored covers the inverted live OpenCode cycle
// (cycle_start 03:38, cycle_end 03:36): a reset was detected while the API
// still reported the previous, already-passed reset, and that stale timestamp
// was written as the new cycle's reset.
func TestWindowStaleResetIsNotStored(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	staleReset := now.Add(time.Hour)
	later := now.Add(2 * time.Hour)

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		if err := tr.Process(openCodeSnapshotAt(now, 10, &staleReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(later, 0.2, &staleReset)); err != nil {
			t.Fatal(err)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt != nil {
			t.Fatalf("new cycle stored stale reset %v", active.ResetsAt)
		}
	})

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		if err := tr.Process(commandCodeSnapshotAt(now, 10, &staleReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(later, 0.2, &staleReset)); err != nil {
			t.Fatal(err)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt != nil {
			t.Fatalf("new cycle stored stale reset %v", active.ResetsAt)
		}
	})
}
