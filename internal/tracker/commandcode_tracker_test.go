package tracker

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// commandCodeSnapshot builds a snapshot with reset timestamps relative to now.
// Pinned epochs would silently start reporting elapsed cycles once real time
// passed them, turning these into time bombs.
func commandCodeSnapshot(now time.Time, fiveHourUsed, fiveHourCap float64, fiveHourReset time.Time) *api.CommandCodeSnapshot {
	snap := &api.CommandCodeSnapshot{CapturedAt: now}
	quota := api.CommandCodeQuota{
		Name:     api.CommandCodeQuotaFiveHour,
		Used:     fiveHourUsed,
		Limit:    fiveHourCap,
		Format:   api.CommandCodeQuotaFormatCredits,
		ResetsAt: &fiveHourReset,
	}
	if fiveHourCap > 0 {
		quota.Utilization = fiveHourUsed / fiveHourCap * 100
	}
	snap.Quotas = append(snap.Quotas, quota)
	return snap
}

func newCommandCodeTrackerStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCommandCodeTrackerCreatesCycles(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, now.Add(4*time.Hour))); err != nil {
		t.Fatalf("process: %v", err)
	}

	cycle, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatalf("active cycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("no active cycle created")
	}
	if cycle.PeakUtilization == 0 {
		t.Error("initial peak must be set from the first reading")
	}
}

func TestCommandCodeTrackerAccumulatesDeltaWithinACycle(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 3, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(commandCodeSnapshot(now.Add(2*time.Minute), 5, 14, reset)); err != nil {
		t.Fatal(err)
	}

	cycle, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if cycle == nil {
		t.Fatal("active cycle missing")
	}
	// Utilization is the percentage of the cap: 1/14 -> 7.14%, 3/14 -> 21.43%,
	// 5/14 -> 35.71%, so the accumulated delta is 28.57 percentage points.
	wantDelta := (5.0 - 1.0) / 14 * 100
	if diff := cycle.TotalDelta - wantDelta; diff > 0.001 || diff < -0.001 {
		t.Fatalf("total delta = %v, want %v", cycle.TotalDelta, wantDelta)
	}
	if cycle.ID == 0 {
		t.Fatal("cycle must be persisted")
	}
}

func TestCommandCodeTrackerDetectsAPIBasedReset(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()

	var resets []string
	tr.SetOnReset(func(name string) { resets = append(resets, name) })

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, now.Add(4*time.Hour))); err != nil {
		t.Fatal(err)
	}
	// A reset timestamp far from the stored one means a new window opened.
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 0.1, 14, now.Add(9*time.Hour))); err != nil {
		t.Fatal(err)
	}

	if len(resets) != 1 || resets[0] != api.CommandCodeQuotaFiveHour {
		t.Fatalf("resets = %v, want one five_hour reset", resets)
	}

	history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want the closed cycle", len(history))
	}
	active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if active == nil {
		t.Fatal("a fresh active cycle must be opened after the reset")
	}
}

func TestCommandCodeTrackerDetectsTimeBasedReset(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()
	oldReset := now.Add(30 * time.Minute)

	if err := tr.Process(commandCodeSnapshot(now, 10, 14, oldReset)); err != nil {
		t.Fatal(err)
	}
	// The stored reset is now in the past and the API has not published a new
	// future one yet, which is exactly the offline-across-a-reset case.
	later := now.Add(2 * time.Hour)
	if err := tr.Process(commandCodeSnapshot(later, 0.2, 14, oldReset)); err != nil {
		t.Fatal(err)
	}

	history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1 closed cycle", len(history))
	}
}

func TestCommandCodeTrackerNoResetForSmallDrift(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, reset)); err != nil {
		t.Fatal(err)
	}
	// A few seconds of jitter on the reported reset is not a new window.
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 2, 14, reset.Add(30*time.Second))); err != nil {
		t.Fatal(err)
	}

	history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history = %d, want no reset for sub-tolerance drift", len(history))
	}
}

func TestCommandCodeTrackerNewResetsAtAppearingIsAReset(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()

	// First reading has no reset time (a window the API did not annotate).
	if err := tr.Process(&api.CommandCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.CommandCodeQuota{{
			Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14,
			Utilization: 7.14, Format: api.CommandCodeQuotaFormatCredits,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// A reset time appearing afterwards means a new window.
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 0.5, 14, now.Add(4*time.Hour))); err != nil {
		t.Fatal(err)
	}

	history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
}

func TestCommandCodeTrackerSummary(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)

	// A completed cycle so the summary has history to aggregate.
	completedStart := now.Add(-3 * time.Hour)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, completedStart, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-time.Hour), 20, 20); err != nil {
		t.Fatal(err)
	}

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertCommandCodeSnapshot(commandCodeSnapshot(now, 1, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 3, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertCommandCodeSnapshot(commandCodeSnapshot(now.Add(time.Minute), 3, 14, reset)); err != nil {
		t.Fatal(err)
	}

	summary, err := tr.UsageSummary(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary == nil {
		t.Fatal("summary is nil")
	}
	if summary.QuotaName != api.CommandCodeQuotaFiveHour {
		t.Errorf("quota name = %q", summary.QuotaName)
	}
	if summary.ResetsAt == nil || !summary.ResetsAt.Equal(reset) {
		t.Errorf("resets at = %v, want %v", summary.ResetsAt, reset)
	}
	if diff := summary.CurrentUtil - (3.0 / 14 * 100); diff > 0.01 || diff < -0.01 {
		t.Errorf("current util = %v", summary.CurrentUtil)
	}
	if summary.CompletedCycles != 1 {
		t.Errorf("completed cycles = %d, want 1", summary.CompletedCycles)
	}
	// Peak is the maximum across closed cycles and the active one, and the
	// active cycle (3/14 -> 21.43%) is higher than the closed cycle's 20.
	if summary.PeakCycle != (3.0 / 14 * 100) {
		t.Errorf("peak cycle = %v, want the active cycle's %v", summary.PeakCycle, 3.0/14*100)
	}
	if summary.AvgPerCycle != 20 {
		t.Errorf("avg per cycle = %v, want 20 from the one closed cycle", summary.AvgPerCycle)
	}
	if !summary.TrackingSince.Equal(completedStart) {
		t.Errorf("tracking since = %v, want %v", summary.TrackingSince, completedStart)
	}
}

func TestCommandCodeTrackerSummaryForUnknownQuota(t *testing.T) {
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)

	summary, err := tr.UsageSummary("does_not_exist")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary == nil || summary.QuotaName != "does_not_exist" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.CompletedCycles != 0 || summary.CurrentUtil != 0 {
		t.Errorf("empty summary has data: %+v", summary)
	}
}

func TestCommandCodeTrackerSummarySkipsProjectionBelowThirtyMinutes(t *testing.T) {
	// A 5h window that has only been watched for a couple of minutes would
	// project off noise, so the rate must stay zero.
	s := newCommandCodeTrackerStore(t)
	tr := NewCommandCodeTracker(s, nil)
	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)

	if err := tr.Process(commandCodeSnapshot(now, 1, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(commandCodeSnapshot(now.Add(time.Minute), 4, 14, reset)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertCommandCodeSnapshot(commandCodeSnapshot(now.Add(time.Minute), 4, 14, reset)); err != nil {
		t.Fatal(err)
	}

	summary, err := tr.UsageSummary(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CurrentRate != 0 || summary.ProjectedUtil != 0 {
		t.Fatalf("projection ran on a window younger than 30m: %+v", summary)
	}
}
