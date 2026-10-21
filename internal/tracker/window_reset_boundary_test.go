package tracker

import (
	"math"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
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
