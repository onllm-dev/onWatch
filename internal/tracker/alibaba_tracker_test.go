package tracker

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newAlibabaTrackerTest(t *testing.T) (*AlibabaTracker, *store.Store) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewAlibabaTracker(s, nil), s
}

func alibabaSnap(at time.Time, name string, util float64, resetsAt *time.Time) *api.AlibabaSnapshot {
	return &api.AlibabaSnapshot{
		CapturedAt: at,
		Quotas:     []api.AlibabaQuota{{Name: name, Utilization: util, ResetsAt: resetsAt}},
	}
}

func TestAlibabaTracker_CreatesCycleAndTracksPeakDelta(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)
	q := api.AlibabaQuotaFiveHour

	for i, util := range []float64{10, 25, 22, 40} {
		if err := tr.Process(alibabaSnap(now.Add(time.Duration(i)*time.Minute), q, util, &reset), 1); err != nil {
			t.Fatalf("Process #%d: %v", i, err)
		}
	}

	cycle, err := s.QueryActiveAlibabaCycle(q, 1)
	if err != nil || cycle == nil {
		t.Fatalf("active cycle = %v, %v", cycle, err)
	}
	if cycle.PeakUtilization != 40 {
		t.Errorf("peak = %v, want 40", cycle.PeakUtilization)
	}
	// Increases only: 15 + 18 = 33. The 3 point dip is noise while the
	// reset time confirms the same window.
	if cycle.TotalDelta != 33 {
		t.Errorf("delta = %v, want 33", cycle.TotalDelta)
	}
	if cycle.ResetsAt == nil || !cycle.ResetsAt.Equal(reset) {
		t.Errorf("resets_at = %v, want %v", cycle.ResetsAt, reset)
	}
	if history, _ := s.QueryAlibabaCycleHistory(q, 1); len(history) != 0 {
		t.Errorf("history = %d, want 0", len(history))
	}
}

func TestAlibabaTracker_DetectsResetByResetsAtChange(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaWeekly
	reset1 := now.Add(24 * time.Hour)
	reset2 := reset1.Add(7 * 24 * time.Hour)

	var resets []string
	tr.SetOnReset(func(name string) { resets = append(resets, name) })

	if err := tr.Process(alibabaSnap(now, q, 60, &reset1), 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(alibabaSnap(now.Add(time.Minute), q, 65, &reset1), 1); err != nil {
		t.Fatal(err)
	}
	// Window rolled over: new reset time, usage barely moved above the old one.
	if err := tr.Process(alibabaSnap(now.Add(2*time.Minute), q, 66, &reset2), 1); err != nil {
		t.Fatal(err)
	}

	if len(resets) != 1 || resets[0] != q {
		t.Fatalf("onReset calls = %v", resets)
	}
	history, _ := s.QueryAlibabaCycleHistory(q, 1)
	if len(history) != 1 || history[0].PeakUtilization != 65 {
		t.Fatalf("history = %+v", history)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || active.PeakUtilization != 66 || active.TotalDelta != 0 {
		t.Errorf("active = %+v", active)
	}
	if active.ResetsAt == nil || !active.ResetsAt.Equal(reset2) {
		t.Errorf("active resets_at = %v, want %v", active.ResetsAt, reset2)
	}
}

func TestAlibabaTracker_DetectsResetByUtilizationDrop(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaMonthly

	resets := 0
	tr.SetOnReset(func(string) { resets++ })

	// No reset time reported at all: only the drop can reveal the reset.
	for i, util := range []float64{50, 70, 3} {
		if err := tr.Process(alibabaSnap(now.Add(time.Duration(i)*time.Minute), q, util, nil), 1); err != nil {
			t.Fatal(err)
		}
	}
	if resets != 1 {
		t.Fatalf("resets = %d, want 1", resets)
	}
	history, _ := s.QueryAlibabaCycleHistory(q, 1)
	if len(history) != 1 || history[0].PeakUtilization != 70 || history[0].TotalDelta != 20 {
		t.Fatalf("history = %+v", history)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || active.PeakUtilization != 3 {
		t.Errorf("active = %+v", active)
	}
}

func TestAlibabaTracker_SharpDropWithSameResetIsAReset(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	reset := now.Add(3 * time.Hour)

	for i, util := range []float64{80, 5} {
		if err := tr.Process(alibabaSnap(now.Add(time.Duration(i)*time.Minute), q, util, &reset), 1); err != nil {
			t.Fatal(err)
		}
	}
	if history, _ := s.QueryAlibabaCycleHistory(q, 1); len(history) != 1 {
		t.Errorf("history = %d, want 1", len(history))
	}
}

func TestAlibabaTracker_SmallDropIsNotAReset(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	reset := now.Add(3 * time.Hour)

	// Within the noise threshold.
	for i, util := range []float64{50, 49} {
		if err := tr.Process(alibabaSnap(now.Add(time.Duration(i)*time.Minute), q, util, &reset), 1); err != nil {
			t.Fatal(err)
		}
	}
	if history, _ := s.QueryAlibabaCycleHistory(q, 1); len(history) != 0 {
		t.Errorf("history = %d, want 0", len(history))
	}
}

func TestAlibabaTracker_DetectsResetByStoredResetPassed(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	base := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	reset := base.Add(5 * time.Hour)

	if err := tr.Process(alibabaSnap(base, q, 80, &reset), 1); err != nil {
		t.Fatal(err)
	}
	// Past the stored reset with no future reset reported: the pre-reset
	// sample must not be assigned to the new window.
	after := reset.Add(10 * time.Minute)
	if err := tr.Process(alibabaSnap(after, q, 80, nil), 1); err != nil {
		t.Fatal(err)
	}
	history, _ := s.QueryAlibabaCycleHistory(q, 1)
	if len(history) != 1 || !history[0].CycleEnd.Equal(reset) {
		t.Fatalf("history = %+v", history)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || !active.CycleStart.Equal(reset) || active.PeakUtilization != 0 {
		t.Errorf("active = %+v", active)
	}
}

func TestAlibabaTracker_PastResetIsNotStored(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	past := now.Add(-time.Minute)

	if err := tr.Process(alibabaSnap(now, q, 10, &past), 1); err != nil {
		t.Fatal(err)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || active.ResetsAt != nil {
		t.Errorf("a reset in the past must not be stored: %+v", active)
	}
}

func TestAlibabaTracker_AttachesResetToOpenCycle(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	reset := now.Add(4 * time.Hour)

	if err := tr.Process(alibabaSnap(now, q, 10, nil), 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(alibabaSnap(now.Add(time.Minute), q, 12, &reset), 1); err != nil {
		t.Fatal(err)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
		t.Errorf("active = %+v", active)
	}
	if history, _ := s.QueryAlibabaCycleHistory(q, 1); len(history) != 0 {
		t.Errorf("attaching a reset must not close the cycle")
	}
}

func TestAlibabaTracker_AccountsAreIndependent(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour
	reset := now.Add(3 * time.Hour)

	if err := tr.Process(alibabaSnap(now, q, 80, &reset), 1); err != nil {
		t.Fatal(err)
	}
	// Account 2 starts low; it must not look like a drop from account 1.
	if err := tr.Process(alibabaSnap(now.Add(time.Minute), q, 5, &reset), 2); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(alibabaSnap(now.Add(2*time.Minute), q, 85, &reset), 1); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{1, 2} {
		if history, _ := s.QueryAlibabaCycleHistory(q, id); len(history) != 0 {
			t.Errorf("account %d history = %d, want 0", id, len(history))
		}
	}
	c1, _ := s.QueryActiveAlibabaCycle(q, 1)
	c2, _ := s.QueryActiveAlibabaCycle(q, 2)
	if c1 == nil || c2 == nil || c1.PeakUtilization != 85 || c2.PeakUtilization != 5 {
		t.Errorf("cycles mixed: %+v / %+v", c1, c2)
	}
}

func TestAlibabaTracker_NilSnapshot(t *testing.T) {
	tr, _ := newAlibabaTrackerTest(t)
	if err := tr.Process(nil, 1); err == nil {
		t.Error("expected error for nil snapshot")
	}
}

func TestAlibabaTracker_UsageSummary(t *testing.T) {
	tr, s := newAlibabaTrackerTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaWeekly
	reset := now.Add(48 * time.Hour)

	// One closed cycle and one active cycle, started two hours ago.
	closedStart := now.Add(-72 * time.Hour)
	if _, err := s.CreateAlibabaCycle(q, closedStart, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseAlibabaCycle(q, closedStart.Add(24*time.Hour), 90, 70, 1); err != nil {
		t.Fatal(err)
	}

	activeStart := now.Add(-2 * time.Hour)
	snap := alibabaSnap(activeStart, q, 10, &reset)
	if _, err := s.InsertAlibabaSnapshot(snap, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(snap, 1); err != nil {
		t.Fatal(err)
	}
	later := alibabaSnap(now, q, 30, &reset)
	if _, err := s.InsertAlibabaSnapshot(later, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Process(later, 1); err != nil {
		t.Fatal(err)
	}

	sum, err := tr.UsageSummary(q, 1)
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if sum.CompletedCycles != 1 || sum.PeakCycle != 90 || sum.AvgPerCycle != 70 {
		t.Errorf("history stats = %+v", sum)
	}
	if sum.CurrentUtil != 30 {
		t.Errorf("CurrentUtil = %v, want 30", sum.CurrentUtil)
	}
	if sum.TotalTracked != 90 { // 70 closed + 20 active delta
		t.Errorf("TotalTracked = %v, want 90", sum.TotalTracked)
	}
	if sum.ResetsAt == nil || sum.TimeUntilReset <= 0 {
		t.Errorf("ResetsAt = %v / %v", sum.ResetsAt, sum.TimeUntilReset)
	}
	if sum.CurrentRate <= 0 || sum.ProjectedUtil <= 30 || sum.ProjectedUtil > 100 {
		t.Errorf("rate/projection = %v / %v", sum.CurrentRate, sum.ProjectedUtil)
	}
	if !sum.TrackingSince.Equal(closedStart) {
		t.Errorf("TrackingSince = %v, want %v", sum.TrackingSince, closedStart)
	}

	// Another account has nothing.
	empty, err := tr.UsageSummary(q, 2)
	if err != nil || empty.CompletedCycles != 0 || empty.TotalTracked != 0 {
		t.Errorf("account 2 summary = %+v, %v", empty, err)
	}
}
