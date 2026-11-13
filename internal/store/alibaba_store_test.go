package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func newAlibabaTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func alibabaTestSnapshot(at time.Time, five, weekly, monthly float64) *api.AlibabaSnapshot {
	reset5 := at.Add(3 * time.Hour)
	resetW := at.Add(72 * time.Hour)
	resetM := at.Add(20 * 24 * time.Hour)
	start := at.Add(-10 * 24 * time.Hour)
	end := at.Add(20 * 24 * time.Hour)
	return &api.AlibabaSnapshot{
		CapturedAt:    at,
		RawJSON:       `{"usage":{}}`,
		SpecCode:      "lite",
		Status:        "VALID",
		RemainingDays: 20,
		AutoRenew:     true,
		StartTime:     &start,
		EndTime:       &end,
		InstanceID:    "inst-1",
		Quotas: []api.AlibabaQuota{
			{Name: api.AlibabaQuotaFiveHour, Utilization: five, ResetsAt: &reset5},
			{Name: api.AlibabaQuotaWeekly, Utilization: weekly, ResetsAt: &resetW},
			{Name: api.AlibabaQuotaMonthly, Utilization: monthly, ResetsAt: &resetM},
		},
	}
}

func TestAlibabaSchemaAndDefaultAccount(t *testing.T) {
	s := newAlibabaTestStore(t)

	for _, table := range []string{"alibaba_snapshots", "alibaba_quota_values", "alibaba_reset_cycles"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	accounts, err := s.QueryActiveProviderAccounts("alibaba")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "default" {
		t.Fatalf("accounts = %+v, want one default account", accounts)
	}
}

func TestAlibabaStoreInsertAndQueryLatest(t *testing.T) {
	s := newAlibabaTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if got, err := s.QueryLatestAlibaba(1); err != nil || got != nil {
		t.Fatalf("empty latest = %v, %v; want nil, nil", got, err)
	}

	id, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(now, 10, 40, 5), 1)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero id")
	}

	got, err := s.QueryLatestAlibaba(1)
	if err != nil || got == nil {
		t.Fatalf("latest = %v, %v", got, err)
	}
	if got.SpecCode != "lite" || got.Status != "VALID" || got.RemainingDays != 20 ||
		!got.AutoRenew || got.InstanceID != "inst-1" || got.RawJSON != `{"usage":{}}` {
		t.Errorf("unexpected snapshot: %+v", got)
	}
	if got.StartTime == nil || got.EndTime == nil {
		t.Error("start/end time must round-trip")
	}
	if !got.CapturedAt.Equal(now) {
		t.Errorf("CapturedAt = %v, want %v", got.CapturedAt, now)
	}
	if len(got.Quotas) != 3 {
		t.Fatalf("quotas = %d, want 3", len(got.Quotas))
	}
	// Quotas come back ordered by name.
	byName := map[string]api.AlibabaQuota{}
	for _, q := range got.Quotas {
		byName[q.Name] = q
	}
	if byName[api.AlibabaQuotaWeekly].Utilization != 40 || byName[api.AlibabaQuotaWeekly].ResetsAt == nil {
		t.Errorf("weekly = %+v", byName[api.AlibabaQuotaWeekly])
	}
}

func TestAlibabaStoreNilResetAndTimes(t *testing.T) {
	s := newAlibabaTestStore(t)
	snap := &api.AlibabaSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas:     []api.AlibabaQuota{{Name: api.AlibabaQuotaWeekly, Utilization: 3}},
	}
	if _, err := s.InsertAlibabaSnapshot(snap, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.QueryLatestAlibaba(1)
	if err != nil || got == nil {
		t.Fatalf("latest = %v, %v", got, err)
	}
	if got.StartTime != nil || got.EndTime != nil || got.Quotas[0].ResetsAt != nil {
		t.Errorf("nil times must stay nil: %+v", got)
	}
}

func TestAlibabaStoreInsertNil(t *testing.T) {
	s := newAlibabaTestStore(t)
	if _, err := s.InsertAlibabaSnapshot(nil, 1); err == nil {
		t.Error("expected error for nil snapshot")
	}
}

func TestAlibabaStoreAccountIsolation(t *testing.T) {
	s := newAlibabaTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(now, 10, 10, 10), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(now.Add(time.Minute), 90, 90, 90), 2); err != nil {
		t.Fatal(err)
	}

	a1, _ := s.QueryLatestAlibaba(1)
	a2, _ := s.QueryLatestAlibaba(2)
	if a1 == nil || a2 == nil {
		t.Fatal("both accounts must have a snapshot")
	}
	if a1.Quotas[0].Utilization != 10 || a2.Quotas[0].Utilization != 90 {
		t.Errorf("accounts mixed: %v vs %v", a1.Quotas[0].Utilization, a2.Quotas[0].Utilization)
	}
	if got, _ := s.QueryLatestAlibaba(3); got != nil {
		t.Error("unknown account must have no snapshot")
	}

	r1, err := s.QueryAlibabaRange(now.Add(-time.Hour), now.Add(time.Hour), 1)
	if err != nil || len(r1) != 1 {
		t.Fatalf("range account 1 = %d, %v", len(r1), err)
	}
	c2, err := s.QueryAlibabaChartRange(now.Add(-time.Hour), now.Add(time.Hour), 2)
	if err != nil || len(c2) != 1 {
		t.Fatalf("chart account 2 = %d, %v", len(c2), err)
	}

	series1, _ := s.QueryAlibabaUtilizationSeries(api.AlibabaQuotaWeekly, now.Add(-time.Hour), 1)
	if len(series1) != 1 || series1[0].Utilization != 10 {
		t.Errorf("series account 1 = %+v", series1)
	}

	latestQuotas, err := s.QueryLatestAlibabaQuotas(2)
	if err != nil || len(latestQuotas) != 3 {
		t.Fatalf("latest quotas = %v, %v", latestQuotas, err)
	}
	for _, q := range latestQuotas {
		if q.Utilization != 90 || q.SpecCode != "lite" || !q.AutoRenew || q.EndTime == nil {
			t.Errorf("unexpected latest quota: %+v", q)
		}
	}
	if got, err := s.QueryLatestAlibabaQuotas(3); err != nil || len(got) != 0 {
		t.Errorf("unknown account latest quotas = %v, %v", got, err)
	}
}

func TestAlibabaStoreRangeLimitKeepsNewest(t *testing.T) {
	s := newAlibabaTestStore(t)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i := 0; i < 5; i++ {
		if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(base.Add(time.Duration(i)*time.Minute), float64(i), 0, 0), 1); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.QueryAlibabaRange(base.Add(-time.Minute), base.Add(time.Hour), 1)
	if err != nil || len(all) != 5 {
		t.Fatalf("all = %d, %v", len(all), err)
	}
	if !all[0].CapturedAt.Before(all[4].CapturedAt) {
		t.Error("range must be ascending")
	}

	limited, err := s.QueryAlibabaRange(base.Add(-time.Minute), base.Add(time.Hour), 1, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limited = %d, %v", len(limited), err)
	}
	if !limited[1].CapturedAt.Equal(all[4].CapturedAt) || !limited[0].CapturedAt.Equal(all[3].CapturedAt) {
		t.Errorf("limit must keep the newest rows ascending: %v %v", limited[0].CapturedAt, limited[1].CapturedAt)
	}
	if len(limited[0].Quotas) != 3 {
		t.Errorf("quotas must be attached, got %d", len(limited[0].Quotas))
	}
}

func TestAlibabaStoreChartRangeSamples(t *testing.T) {
	s := newAlibabaTestStore(t)
	base := time.Now().UTC().Truncate(time.Second).Add(-10 * time.Hour)
	for i := 0; i < 450; i++ {
		if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(base.Add(time.Duration(i)*time.Minute), 1, 1, 1), 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.QueryAlibabaChartRange(base.Add(-time.Minute), base.Add(12*time.Hour), 1)
	if err != nil {
		t.Fatalf("chart: %v", err)
	}
	if len(got) == 0 || len(got) > 201 {
		t.Fatalf("sampled rows = %d, want 1..201", len(got))
	}
	if !got[0].CapturedAt.Equal(base) || !got[len(got)-1].CapturedAt.Equal(base.Add(449*time.Minute)) {
		t.Error("first and last snapshots must be kept")
	}
}

func TestAlibabaStoreCycleLifecycle(t *testing.T) {
	s := newAlibabaTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(5 * time.Hour)
	q := api.AlibabaQuotaFiveHour

	if c, err := s.QueryActiveAlibabaCycle(q, 1); err != nil || c != nil {
		t.Fatalf("no active cycle expected, got %v, %v", c, err)
	}

	id, err := s.CreateAlibabaCycle(q, now, &reset, 1)
	if err != nil || id == 0 {
		t.Fatalf("create: %d, %v", id, err)
	}
	if _, err := s.CreateAlibabaCycle(q, now, nil, 2); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateAlibabaCycle(q, 42, 40, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	active, _ := s.QueryActiveAlibabaCycle(q, 1)
	if active == nil || active.PeakUtilization != 42 || active.TotalDelta != 40 || active.ResetsAt == nil || active.AccountID != 1 {
		t.Fatalf("active = %+v", active)
	}
	other, _ := s.QueryActiveAlibabaCycle(q, 2)
	if other == nil || other.PeakUtilization != 0 || other.ResetsAt != nil {
		t.Errorf("update leaked into account 2: %+v", other)
	}

	if err := s.UpdateAlibabaCycleResetsAt(q, nil, 1); err != nil {
		t.Fatalf("clear resets: %v", err)
	}
	if active, _ = s.QueryActiveAlibabaCycle(q, 1); active.ResetsAt != nil {
		t.Error("resets_at must be cleared")
	}
	newReset := now.Add(4 * time.Hour)
	if err := s.UpdateAlibabaCycleResetsAt(q, &newReset, 1); err != nil {
		t.Fatalf("set resets: %v", err)
	}

	restartAt := now.Add(time.Minute)
	if err := s.RestartAlibabaCycle(q, restartAt, restartAt.Add(5*time.Hour), 1); err != nil {
		t.Fatalf("restart: %v", err)
	}
	active, _ = s.QueryActiveAlibabaCycle(q, 1)
	if !active.CycleStart.Equal(restartAt) || active.PeakUtilization != 0 || active.TotalDelta != 0 ||
		active.ResetsAt == nil || !active.ResetsAt.Equal(restartAt.Add(5*time.Hour)) {
		t.Errorf("restarted cycle = %+v", active)
	}

	if err := s.UpdateAlibabaCycle(q, 55, 50, 1); err != nil {
		t.Fatal(err)
	}
	end := now.Add(2 * time.Minute)
	if err := s.CloseAlibabaCycle(q, end, 55, 50, 1); err != nil {
		t.Fatalf("close: %v", err)
	}
	if c, _ := s.QueryActiveAlibabaCycle(q, 1); c != nil {
		t.Error("cycle must be closed")
	}
	if c, _ := s.QueryActiveAlibabaCycle(q, 2); c == nil {
		t.Error("closing account 1 must not close account 2")
	}

	history, err := s.QueryAlibabaCycleHistory(q, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %d, %v", len(history), err)
	}
	if history[0].CycleEnd == nil || history[0].PeakUtilization != 55 || history[0].TotalDelta != 50 {
		t.Errorf("history[0] = %+v", history[0])
	}
	if h2, _ := s.QueryAlibabaCycleHistory(q, 2); len(h2) != 0 {
		t.Errorf("account 2 history = %d, want 0", len(h2))
	}

	names, err := s.QueryAllAlibabaQuotaNames(1)
	if err != nil || len(names) != 1 || names[0] != q {
		t.Errorf("names = %v, %v", names, err)
	}
	if names, _ := s.QueryAllAlibabaQuotaNames(9); len(names) != 0 {
		t.Errorf("unknown account names = %v", names)
	}
}

func TestAlibabaStoreCycleHistoryLimit(t *testing.T) {
	s := newAlibabaTestStore(t)
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	q := api.AlibabaQuotaWeekly
	for i := 0; i < 4; i++ {
		start := base.Add(time.Duration(i) * time.Hour)
		if _, err := s.CreateAlibabaCycle(q, start, nil, 1); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseAlibabaCycle(q, start.Add(30*time.Minute), float64(i), float64(i), 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.QueryAlibabaCycleHistory(q, 1, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("limited history = %d, %v", len(got), err)
	}
	if got[0].PeakUtilization != 3 || got[1].PeakUtilization != 2 {
		t.Errorf("history must be newest first: %+v %+v", got[0], got[1])
	}
}

func TestAlibabaStoreCycleOverview(t *testing.T) {
	s := newAlibabaTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	q := api.AlibabaQuotaFiveHour

	// A closed cycle spanning two snapshots, then an active cycle.
	start1 := now.Add(-10 * time.Hour)
	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(start1.Add(time.Minute), 20, 5, 1), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(start1.Add(2*time.Hour), 80, 9, 2), 1); err != nil {
		t.Fatal(err)
	}
	// Account 2 data inside the same window must not be picked as a peak.
	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(start1.Add(3*time.Hour), 99, 99, 99), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAlibabaCycle(q, start1, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseAlibabaCycle(q, now.Add(-5*time.Hour), 80, 60, 1); err != nil {
		t.Fatal(err)
	}

	start2 := now.Add(-5 * time.Hour)
	if _, err := s.InsertAlibabaSnapshot(alibabaTestSnapshot(now.Add(-time.Hour), 30, 12, 3), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAlibabaCycle(q, start2, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAlibabaCycle(q, 30, 30, 1); err != nil {
		t.Fatal(err)
	}

	rows, err := s.QueryAlibabaCycleOverview(q, 10, 1)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].CycleEnd != nil || rows[0].PeakValue != 30 {
		t.Errorf("first row must be the active cycle: %+v", rows[0])
	}
	closed := rows[1]
	if closed.CycleEnd == nil || closed.PeakValue != 80 {
		t.Fatalf("closed row = %+v", closed)
	}
	if len(closed.CrossQuotas) != 3 {
		t.Fatalf("cross quotas = %d, want 3", len(closed.CrossQuotas))
	}
	for _, cq := range closed.CrossQuotas {
		if cq.Name == api.AlibabaQuotaFiveHour && cq.Percent != 80 {
			t.Errorf("five_hour cross percent = %v, want 80", cq.Percent)
		}
		if cq.Limit != 100 {
			t.Errorf("limit = %v, want 100", cq.Limit)
		}
	}

	// A limit consumed by the active cycle returns no history.
	one, err := s.QueryAlibabaCycleOverview(q, 1, 1)
	if err != nil || len(one) != 1 || one[0].CycleEnd != nil {
		t.Errorf("limit 1 overview = %+v, %v", one, err)
	}
	none, err := s.QueryAlibabaCycleOverview(q, 10, 2)
	if err != nil || len(none) != 0 {
		t.Errorf("account 2 overview = %+v, %v", none, err)
	}
}
