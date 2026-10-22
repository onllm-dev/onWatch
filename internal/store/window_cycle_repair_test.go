package store

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// Repair samples deliberately disagree with stored lifetime statistics.
func insertCommandCodeRepairSample(t *testing.T, s *Store, at time.Time, quota string, util float64, reset *time.Time) {
	t.Helper()
	snap := &api.CommandCodeSnapshot{
		CapturedAt: at,
		Quotas: []api.CommandCodeQuota{{
			Name: quota, Used: util, Limit: 100, Utilization: util,
			Format: api.CommandCodeQuotaFormatCredits, ResetsAt: reset,
		}},
	}
	if _, err := s.InsertCommandCodeSnapshot(snap); err != nil {
		t.Fatalf("insert commandcode sample: %v", err)
	}
}

func insertOpenCodeRepairSample(t *testing.T, s *Store, at time.Time, quota string, util float64, reset *time.Time) {
	t.Helper()
	snap := &api.OpenCodeSnapshot{
		CapturedAt: at,
		Quotas: []api.OpenCodeQuota{{
			Name: quota, Utilization: util, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: reset,
		}},
	}
	if _, err := s.InsertOpenCodeSnapshot(snap); err != nil {
		t.Fatalf("insert opencode sample: %v", err)
	}
}

func mustCreateClosedCommandCodeCycle(t *testing.T, s *Store, quota string, start, end time.Time, reset *time.Time, peak, delta float64) {
	t.Helper()
	if _, err := s.CreateCommandCodeCycle(quota, start, reset); err != nil {
		t.Fatalf("create commandcode cycle: %v", err)
	}
	if err := s.CloseCommandCodeCycle(quota, end, peak, delta); err != nil {
		t.Fatalf("close commandcode cycle: %v", err)
	}
}

func mustCreateClosedOpenCodeCycle(t *testing.T, s *Store, quota string, start, end time.Time, reset *time.Time, peak, delta float64) {
	t.Helper()
	if _, err := s.CreateOpenCodeCycle(quota, start, reset); err != nil {
		t.Fatalf("create opencode cycle: %v", err)
	}
	if err := s.CloseOpenCodeCycle(quota, end, peak, delta); err != nil {
		t.Fatalf("close opencode cycle: %v", err)
	}
}

func countCommandCodeCycles(t *testing.T, s *Store, quota string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM commandcode_reset_cycles WHERE quota_name = ?`, quota).Scan(&n); err != nil {
		t.Fatalf("count commandcode cycles: %v", err)
	}
	return n
}

func countOpenCodeCycles(t *testing.T, s *Store, quota string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM opencode_reset_cycles WHERE quota_name = ?`, quota).Scan(&n); err != nil {
		t.Fatalf("count opencode cycles: %v", err)
	}
	return n
}

// TestRepairWindowResetCycles_InvertedCycle is the live shape: a cycle that
// ends before it starts, with its peak borrowed from the next window.
func TestRepairWindowResetCycles_InvertedCycle(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-6 * time.Hour)
	reset := base.Add(5 * time.Hour)

	// Window 1 samples: 10 -> 30 -> 45, all before its reset.
	insertCommandCodeRepairSample(t, s, base, api.CommandCodeQuotaFiveHour, 10, &reset)
	insertCommandCodeRepairSample(t, s, base.Add(time.Hour), api.CommandCodeQuotaFiveHour, 30, &reset)
	insertCommandCodeRepairSample(t, s, base.Add(2*time.Hour), api.CommandCodeQuotaFiveHour, 45, &reset)
	// Window 2 sample, which the inverted cycle stole its peak from.
	insertCommandCodeRepairSample(t, s, base.Add(6*time.Hour), api.CommandCodeQuotaFiveHour, 70, nil)

	// The inverted anchor start is the autonomic quirk this repair targets.
	// No successor starts within the merge cutoff, so it must be clamped.
	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour,
		base.Add(10*time.Minute), base.Add(5*time.Minute), &reset, 70, 70)

	results, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if results.InvertedClamped != 1 {
		t.Fatalf("invertedClamped = %d, want 1", results.InvertedClamped)
	}

	var start, end string
	if err := s.db.QueryRow(
		`SELECT cycle_start, cycle_end FROM commandcode_reset_cycles WHERE quota_name = ?`,
		api.CommandCodeQuotaFiveHour,
	).Scan(&start, &end); err != nil {
		t.Fatalf("read repaired cycle: %v", err)
	}
	startAt, _ := time.Parse(time.RFC3339Nano, start)
	endAt, _ := time.Parse(time.RFC3339Nano, end)
	if endAt.Before(startAt) {
		t.Fatalf("cycle still inverted: %v -> %v", startAt, endAt)
	}
	if !endAt.Equal(base.Add(2 * time.Hour)) {
		t.Fatalf("cycle end = %v, want the last sample %v", endAt, base.Add(2*time.Hour))
	}
}

// TestRepairWindowResetCycles_MergesAdjacentChurn covers the per-poll churn
// cycles (a few minutes each, no reset) that sit directly before a real one.
func TestRepairWindowResetCycles_MergesAdjacentChurn(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)

	// Three churn cycles one minute apart, all inverted, followed by a real
	// window opening after them.
	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour,
		base.Add(time.Minute), base, nil, 1, 1)
	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour,
		base.Add(2*time.Minute), base.Add(time.Minute), nil, 2, 1)
	reset := base.Add(2*time.Hour + 3*time.Minute)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, base.Add(3*time.Minute), &reset); err != nil {
		t.Fatalf("create successor: %v", err)
	}
	insertCommandCodeRepairSample(t, s, base, api.CommandCodeQuotaFiveHour, 10, nil)
	insertCommandCodeRepairSample(t, s, base.Add(time.Minute), api.CommandCodeQuotaFiveHour, 15, nil)
	insertCommandCodeRepairSample(t, s, base.Add(2*time.Minute), api.CommandCodeQuotaFiveHour, 20, &reset)

	results, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	// The first churn cycle merges into the second, which then becomes a
	// short null-reset cycle and folds into the successor.
	if results.InvertedMerged+results.ShortFolded < 2 {
		t.Fatalf("repair folded %d cycles, want 2 (%+v)", results.InvertedMerged+results.ShortFolded, results)
	}
	if n := countCommandCodeCycles(t, s, api.CommandCodeQuotaFiveHour); n != 1 {
		t.Fatalf("cycles = %d, want the churn folded into one", n)
	}

	var start string
	if err := s.db.QueryRow(
		`SELECT cycle_start FROM commandcode_reset_cycles WHERE quota_name = ?`,
		api.CommandCodeQuotaFiveHour,
	).Scan(&start); err != nil {
		t.Fatalf("read successor: %v", err)
	}
	startAt, _ := time.Parse(time.RFC3339Nano, start)
	if !startAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("merged cycle start = %v, want the earliest churn start %v", startAt, base.Add(time.Minute))
	}
}

// Retained samples are not evidence that stored lifetime statistics are wrong.
func TestRepairWindowResetCycles_PreservesStats(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-8 * time.Hour)
	reset := base.Add(5 * time.Hour)
	insertCommandCodeRepairSample(t, s, base, api.CommandCodeQuotaFiveHour, 10, &reset)
	insertCommandCodeRepairSample(t, s, base.Add(30*time.Minute), api.CommandCodeQuotaFiveHour, 25, &reset)
	insertCommandCodeRepairSample(t, s, base.Add(time.Hour), api.CommandCodeQuotaFiveHour, 20, &reset) // drop, no delta
	insertCommandCodeRepairSample(t, s, base.Add(2*time.Hour), api.CommandCodeQuotaFiveHour, 40, &reset)
	// Outside the cycle window; must not count.
	insertCommandCodeRepairSample(t, s, base.Add(6*time.Hour), api.CommandCodeQuotaFiveHour, 90, &reset)

	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour,
		base, base.Add(5*time.Hour), &reset, 90, 90)

	results, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if results.total() != 0 {
		t.Fatalf("non-structural repair changed rows: %+v", results)
	}

	var peak, delta float64
	if err := s.db.QueryRow(
		`SELECT peak_utilization, total_delta FROM commandcode_reset_cycles WHERE quota_name = ?`,
		api.CommandCodeQuotaFiveHour,
	).Scan(&peak, &delta); err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if peak != 90 || delta != 90 {
		t.Fatalf("stored stats = %v/%v, want 90/90 despite disagreeing samples", peak, delta)
	}

	// A second pass changes nothing.
	again, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if again.total() != 0 {
		t.Fatalf("second pass changed rows: %+v", again)
	}
}

// TestRepairWindowResetCycles_FoldsShortNullResetCycle covers the 1-minute
// churn cycles with no reset, which cannot describe a window.
func TestRepairWindowResetCycles_FoldsShortNullResetCycle(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	reset := base.Add(2 * time.Hour)
	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour,
		base, base.Add(time.Minute), nil, 3, 3)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, base.Add(2*time.Minute), &reset); err != nil {
		t.Fatalf("create successor: %v", err)
	}

	results, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if results.ShortFolded != 1 {
		t.Fatalf("shortFolded = %d, want 1", results.ShortFolded)
	}
	if n := countCommandCodeCycles(t, s, api.CommandCodeQuotaFiveHour); n != 1 {
		t.Fatalf("cycles = %d, want 1 after folding", n)
	}
}

// TestRepairWindowResetCycles_ClearsStaleActiveReset covers an active cycle
// carrying a reset that is already behind its own start.
func TestRepairWindowResetCycles_ClearsStaleActiveReset(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second)
	stale := base.Add(-time.Hour)
	if _, err := s.CreateOpenCodeCycle("weekly", base, &stale); err != nil {
		t.Fatalf("create active cycle: %v", err)
	}
	fresh := base.Add(24 * time.Hour)
	if _, err := s.CreateOpenCodeCycle("five_hour", base, &fresh); err != nil {
		t.Fatalf("create active cycle: %v", err)
	}

	results, err := s.repairWindowResetCycles(windowCycleTableSet("opencode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if results.ActiveResetCleared != 1 {
		t.Fatalf("activeResetCleared = %d, want 1", results.ActiveResetCleared)
	}

	var weekly, five sql.NullString
	if err := s.db.QueryRow(`SELECT resets_at FROM opencode_reset_cycles WHERE quota_name = 'weekly'`).Scan(&weekly); err != nil {
		t.Fatalf("read weekly: %v", err)
	}
	if err := s.db.QueryRow(`SELECT resets_at FROM opencode_reset_cycles WHERE quota_name = 'five_hour'`).Scan(&five); err != nil {
		t.Fatalf("read five_hour: %v", err)
	}
	if weekly.Valid {
		t.Fatalf("weekly reset = %q, want cleared", weekly.String)
	}
	if !five.Valid {
		t.Fatal("five_hour reset was cleared even though it is in the future")
	}
}

// TestRepairWindowResetCycles_OnceFlagAndIdempotency runs both tables through
// the public entry point, then verifies the marker makes a second call a no-op
// and that raw snapshots are untouched.
func TestRepairWindowResetCycles_OnceFlagAndIdempotency(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-4 * time.Hour)
	reset := base.Add(3 * time.Hour)
	insertCommandCodeRepairSample(t, s, base, api.CommandCodeQuotaFiveHour, 10, &reset)
	insertCommandCodeRepairSample(t, s, base.Add(time.Hour), api.CommandCodeQuotaFiveHour, 60, &reset)
	mustCreateClosedCommandCodeCycle(t, s, api.CommandCodeQuotaFiveHour, base, base.Add(30*time.Minute), &reset, 99, 99)

	insertOpenCodeRepairSample(t, s, base, "weekly", 20, &reset)
	insertOpenCodeRepairSample(t, s, base.Add(time.Hour), "weekly", 50, &reset)
	mustCreateClosedOpenCodeCycle(t, s, "weekly", base.Add(time.Minute), base, &reset, 77, 77)

	snapshotCount := func() (int, int) {
		var cc, oc int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM commandcode_snapshots`).Scan(&cc); err != nil {
			t.Fatalf("count commandcode snapshots: %v", err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM opencode_snapshots`).Scan(&oc); err != nil {
			t.Fatalf("count opencode snapshots: %v", err)
		}
		return cc, oc
	}
	ccBefore, ocBefore := snapshotCount()

	if err := s.RepairWindowResetCyclesIfNeeded(nil); err != nil {
		t.Fatalf("RepairWindowResetCyclesIfNeeded: %v", err)
	}
	marker, err := s.GetSetting(windowCycleRepairKey)
	if err != nil || marker != "done" {
		t.Fatalf("marker = %q, err %v; want done", marker, err)
	}

	ccAfterFirst := countCommandCodeCycles(t, s, api.CommandCodeQuotaFiveHour)
	ocAfterFirst := countOpenCodeCycles(t, s, "weekly")
	if err := s.RepairWindowResetCyclesIfNeeded(nil); err != nil {
		t.Fatalf("second RepairWindowResetCyclesIfNeeded: %v", err)
	}
	if got := countCommandCodeCycles(t, s, api.CommandCodeQuotaFiveHour); got != ccAfterFirst {
		t.Fatalf("second run changed commandcode cycles: %d -> %d", ccAfterFirst, got)
	}
	if got := countOpenCodeCycles(t, s, "weekly"); got != ocAfterFirst {
		t.Fatalf("second run changed opencode cycles: %d -> %d", ocAfterFirst, got)
	}

	ccAfter, ocAfter := snapshotCount()
	if ccAfter != ccBefore || ocAfter != ocBefore {
		t.Fatalf("snapshots changed: commandcode %d->%d, opencode %d->%d", ccBefore, ccAfter, ocBefore, ocAfter)
	}
}

func TestRepairWindowResetCycles_StoredStatisticsAndCollapsedReset(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			s, err := New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tables := windowCycleTableSet(provider)
			base := time.Now().UTC().Truncate(time.Second).Add(-10 * time.Hour)
			future := base.Add(5 * time.Hour)
			stale := base.Add(-time.Hour)
			insert := func(quota string, start, end time.Time, reset *time.Time, peak, delta float64) {
				t.Helper()
				var r interface{}
				if reset != nil {
					r = reset.Format(time.RFC3339Nano)
				}
				_, err := s.db.Exec(fmt.Sprintf(`INSERT INTO %s (quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta) VALUES (?, ?, ?, ?, ?, ?)`, tables.cycles), quota, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), r, peak, delta)
				if err != nil {
					t.Fatal(err)
				}
			}
			insert("merge", base, base.Add(-time.Minute), &stale, 10, 3)
			insert("merge", base.Add(2*time.Minute), base.Add(time.Hour), &future, 30, 7)
			insert("fold", base, base.Add(time.Minute), nil, 20, 8)
			insert("fold", base.Add(2*time.Minute), base.Add(time.Hour), &future, 15, 4)
			insert("preserve", base, base.Add(time.Hour), &future, 100, 100)
			insert("retained", base, base.Add(time.Hour), &future, 100, 100)
			insert("collapsed", base, base, &stale, 12, 9)
			insert("clamped", base, base.Add(-time.Minute), &stale, 13, 6)
			for _, quota := range []string{"merge", "fold", "preserve", "retained"} {
				if provider == "commandcode" {
					if quota != "retained" {
						insertCommandCodeRepairSample(t, s, base.Add(time.Minute), quota, 99, &future)
					}
					insertCommandCodeRepairSample(t, s, base.Add(45*time.Minute), quota, 0, &future)
					insertCommandCodeRepairSample(t, s, base.Add(time.Hour), quota, 99, &future)
				} else {
					if quota != "retained" {
						insertOpenCodeRepairSample(t, s, base.Add(time.Minute), quota, 99, &future)
					}
					insertOpenCodeRepairSample(t, s, base.Add(45*time.Minute), quota, 0, &future)
					insertOpenCodeRepairSample(t, s, base.Add(time.Hour), quota, 99, &future)
				}
			}
			if _, err := s.repairWindowResetCycles(tables); err != nil {
				t.Fatal(err)
			}
			for _, want := range []struct {
				quota       string
				peak, delta float64
			}{
				{"merge", 30, 10}, {"fold", 20, 12}, {"preserve", 100, 100}, {"retained", 100, 100}, {"collapsed", 12, 9}, {"clamped", 13, 6},
			} {
				var peak, delta float64
				var reset sql.NullString
				if err := s.db.QueryRow(fmt.Sprintf(`SELECT peak_utilization, total_delta, resets_at FROM %s WHERE quota_name = ?`, tables.cycles), want.quota).Scan(&peak, &delta, &reset); err != nil {
					t.Fatal(err)
				}
				if peak != want.peak || delta != want.delta {
					t.Fatalf("%s stats=%v/%v want %v/%v", want.quota, peak, delta, want.peak, want.delta)
				}
				if (want.quota == "collapsed" || want.quota == "clamped") && reset.Valid {
					t.Fatalf("%s stale reset was not cleared", want.quota)
				}
			}
			again, err := s.repairWindowResetCycles(tables)
			if err != nil || again.total() != 0 {
				t.Fatalf("second pass=%+v err=%v", again, err)
			}
		})
	}
}

// TestRepairWindowResetCycles_FreshDatabaseIsNoop keeps a new install free of
// repair work and settings churn beyond the marker.
func TestRepairWindowResetCycles_FreshDatabaseIsNoop(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	for _, provider := range []string{"commandcode", "opencode"} {
		results, err := s.repairWindowResetCycles(windowCycleTableSet(provider))
		if err != nil {
			t.Fatalf("%s repair: %v", provider, err)
		}
		if results.total() != 0 {
			t.Fatalf("%s fresh DB repair changed %d rows", provider, results.total())
		}
	}
}

// TestRepairWindowResetCycles_BoundsNewestCycles checks that only the newest
// 200 cycles per quota are considered.
func TestRepairWindowResetCycles_BoundsNewestCycles(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Truncate(time.Second).Add(-300 * time.Hour)
	for i := 0; i < 210; i++ {
		start := base.Add(time.Duration(i) * time.Hour)
		// The oldest 10 cycles are inverted; they must stay outside the bound.
		end := start.Add(30 * time.Minute)
		if i < 10 {
			end = start.Add(-time.Minute)
		}
		if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, start, nil); err != nil {
			t.Fatalf("create cycle: %v", err)
		}
		if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, end, 5, 5); err != nil {
			t.Fatalf("close cycle: %v", err)
		}
	}

	results, err := s.repairWindowResetCycles(windowCycleTableSet("commandcode"))
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if results.InvertedMerged != 0 && results.InvertedClamped != 0 {
		t.Fatalf("repair touched cycles outside the newest 200: %+v", results)
	}
}
