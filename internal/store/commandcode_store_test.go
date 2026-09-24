package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func commandCodeQuotas(reset time.Time) []api.CommandCodeQuota {
	return []api.CommandCodeQuota{
		{Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14, Utilization: 7.14, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
		{Name: api.CommandCodeQuotaWeekly, Used: 19, Limit: 35, Utilization: 54.29, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
		{Name: api.CommandCodeQuotaMonthly, Used: 19, Limit: 70, Utilization: 27.14, Format: api.CommandCodeQuotaFormatCurrency, Remaining: 50.99, ResetsAt: &reset},
	}
}

func newCommandCodeTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCommandCodeStoreInsertAndQueryLatest(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(72 * time.Hour)
	periodEnd := now.Add(25 * 24 * time.Hour)

	snap := &api.CommandCodeSnapshot{
		CapturedAt:       now,
		RawJSON:          `{"whoami":{}}`,
		AccountName:      "prakersh",
		AccountID:        "u_1",
		OrgID:            "org_1",
		Plan:             "individual-goat",
		Status:           "active",
		MonthlyCredits:   50.99,
		PurchasedCredits: 0,
		FreeCredits:      0,
		RemainingCredits: 50.99,
		PeriodStart:      ptrTime(now.Add(-5 * 24 * time.Hour)),
		PeriodEnd:        &periodEnd,
		PeriodCostUSD:    18.99,
		PeriodReqs:       6190,
		PeriodTokens:     1_100_000_000,
		Quotas:           commandCodeQuotas(reset),
	}

	id, err := s.InsertCommandCodeSnapshot(snap)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero id")
	}

	latest, err := s.QueryLatestCommandCode()
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest == nil {
		t.Fatal("latest is nil")
	}
	if latest.AccountName != "prakersh" || latest.Plan != "individual-goat" || latest.Status != "active" {
		t.Errorf("identity = %+v", latest)
	}
	if latest.RemainingCredits != 50.99 || latest.PeriodCostUSD != 18.99 {
		t.Errorf("credits/cost = %v/%v", latest.RemainingCredits, latest.PeriodCostUSD)
	}
	if latest.PeriodReqs != 6190 || latest.PeriodTokens != 1_100_000_000 {
		t.Errorf("usage = %d/%d", latest.PeriodReqs, latest.PeriodTokens)
	}
	if latest.PeriodEnd == nil || !latest.PeriodEnd.Equal(periodEnd) {
		t.Errorf("period end = %v, want %v", latest.PeriodEnd, periodEnd)
	}
	if len(latest.Quotas) != 3 {
		t.Fatalf("quotas = %d", len(latest.Quotas))
	}

	var monthly api.CommandCodeQuota
	for _, q := range latest.Quotas {
		if q.Name == api.CommandCodeQuotaMonthly {
			monthly = q
		}
	}
	if monthly.Remaining != 50.99 {
		t.Errorf("monthly remaining = %v, want 50.99", monthly.Remaining)
	}
	if monthly.Format != api.CommandCodeQuotaFormatCurrency {
		t.Errorf("monthly format = %q", monthly.Format)
	}
	if monthly.ResetsAt == nil || !monthly.ResetsAt.Equal(reset) {
		t.Errorf("monthly reset = %v", monthly.ResetsAt)
	}
}

func TestCommandCodeStoreNilSnapshot(t *testing.T) {
	s := newCommandCodeTestStore(t)
	if _, err := s.InsertCommandCodeSnapshot(nil); err == nil {
		t.Fatal("expected an error for a nil snapshot")
	}
}

func TestCommandCodeStoreLatestWhenEmpty(t *testing.T) {
	s := newCommandCodeTestStore(t)
	latest, err := s.QueryLatestCommandCode()
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest != nil {
		t.Fatalf("latest = %+v, want nil", latest)
	}
}

func TestCommandCodeStoreRangeLoadsQuotasAndRespectsLimit(t *testing.T) {
	s := newCommandCodeTestStore(t)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	reset := base.Add(24 * time.Hour)

	for i := 0; i < 5; i++ {
		snap := &api.CommandCodeSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Quotas: []api.CommandCodeQuota{
				{Name: api.CommandCodeQuotaFiveHour, Utilization: float64(i + 1), Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
			},
		}
		if _, err := s.InsertCommandCodeSnapshot(snap); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	all, err := s.QueryCommandCodeRange(base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("all = %d, want 5", len(all))
	}
	for _, snap := range all {
		if len(snap.Quotas) != 1 {
			t.Fatalf("snapshot %d has %d quotas", snap.ID, len(snap.Quotas))
		}
	}
	// Rows come back oldest first so charts read left to right.
	if !all[0].CapturedAt.Before(all[len(all)-1].CapturedAt) {
		t.Error("range must be ordered ascending by captured_at")
	}

	// A limit keeps the newest rows, still returned ascending.
	limited, err := s.QueryCommandCodeRange(base.Add(-time.Minute), base.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("limited range: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited = %d, want 2", len(limited))
	}
	if limited[0].Quotas[0].Utilization != 4 || limited[1].Quotas[0].Utilization != 5 {
		t.Errorf("limited kept the wrong rows: %v/%v",
			limited[0].Quotas[0].Utilization, limited[1].Quotas[0].Utilization)
	}
}

func TestCommandCodeStoreLatestPerQuota(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	// First snapshot only has two windows; the second adds the third. Per-quota
	// latest reads the newest snapshot, so the third must appear once it lands.
	if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14, Utilization: 7, Format: api.CommandCodeQuotaFormatCredits},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 2, Limit: 14, Utilization: 14, Format: api.CommandCodeQuotaFormatCredits},
			{Name: api.CommandCodeQuotaWeekly, Used: 19, Limit: 35, Utilization: 54, Format: api.CommandCodeQuotaFormatCredits},
		},
	}); err != nil {
		t.Fatal(err)
	}

	perQuota, err := s.QueryCommandCodeLatestPerQuota()
	if err != nil {
		t.Fatalf("latest per quota: %v", err)
	}
	if len(perQuota) != 2 {
		t.Fatalf("per quota = %d, want 2", len(perQuota))
	}
	byName := map[string]CommandCodeLatestQuota{}
	for _, q := range perQuota {
		byName[q.Name] = q
	}
	if byName[api.CommandCodeQuotaFiveHour].Utilization != 14 {
		t.Errorf("five hour util = %v, want the newer 14", byName[api.CommandCodeQuotaFiveHour].Utilization)
	}
	if _, ok := byName[api.CommandCodeQuotaWeekly]; !ok {
		t.Error("weekly missing from the latest snapshot")
	}
}

func TestCommandCodeStoreCycleLifecycle(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(5 * time.Hour)

	id, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now, &reset)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero cycle id")
	}

	active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active == nil || active.ID != id || active.QuotaName != api.CommandCodeQuotaFiveHour {
		t.Fatalf("active = %+v", active)
	}
	if active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
		t.Errorf("active reset = %v", active.ResetsAt)
	}

	if err := s.UpdateCommandCodeCycle(api.CommandCodeQuotaFiveHour, 42, 12); err != nil {
		t.Fatalf("update: %v", err)
	}
	active, err = s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if active.PeakUtilization != 42 || active.TotalDelta != 12 {
		t.Fatalf("update not applied: %+v", active)
	}

	end := now.Add(time.Hour)
	if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, end, 42, 12); err != nil {
		t.Fatalf("close: %v", err)
	}
	active, err = s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatalf("active after close = %+v, want nil", active)
	}

	history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].PeakUtilization != 42 {
		t.Fatalf("history = %+v", history)
	}
	if history[0].CycleEnd == nil || !history[0].CycleEnd.Equal(end) {
		t.Errorf("cycle end = %v, want %v", history[0].CycleEnd, end)
	}
}

func TestCommandCodeStoreActiveCycleIsScopedByQuota(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaWeekly, now, nil); err != nil {
		t.Fatal(err)
	}
	active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
	if err != nil {
		t.Fatal(err)
	}
	if active != nil {
		t.Fatalf("five-hour active = %+v, want nil for an unrelated window", active)
	}
}

func TestCommandCodeStoreUtilizationSeries(t *testing.T) {
	s := newCommandCodeTestStore(t)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Quotas: []api.CommandCodeQuota{
				{Name: api.CommandCodeQuotaFiveHour, Utilization: float64(i+1) * 10, Format: api.CommandCodeQuotaFormatCredits},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	points, err := s.QueryCommandCodeUtilizationSeries(api.CommandCodeQuotaFiveHour, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("points = %d, want 3", len(points))
	}
	if points[0].Utilization != 10 || points[2].Utilization != 30 {
		t.Errorf("points = %+v", points)
	}

	// A `since` after every row yields nothing rather than an error.
	points, err = s.QueryCommandCodeUtilizationSeries(api.CommandCodeQuotaFiveHour, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("future since: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("points = %d, want 0", len(points))
	}
}

func TestCommandCodeStoreQuotaNames(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC()

	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaWeekly, now, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now, nil); err != nil {
		t.Fatal(err)
	}
	// A duplicate cycle must not duplicate the name.
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}

	names, err := s.QueryAllCommandCodeQuotaNames()
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if len(names) != 2 || names[0] != api.CommandCodeQuotaFiveHour || names[1] != api.CommandCodeQuotaWeekly {
		t.Fatalf("names = %v", names)
	}
}

func TestCommandCodeStoreCycleOverview(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	// A completed cycle plus an active one.
	cycleStart := now.Add(-6 * time.Hour)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, cycleStart, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-time.Hour), 40, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
		CapturedAt: now.Add(-2 * time.Hour),
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 30, Limit: 14, Utilization: 40, Format: api.CommandCodeQuotaFormatCredits},
		},
	}); err != nil {
		t.Fatal(err)
	}

	overview, err := s.QueryCommandCodeCycleOverview(api.CommandCodeQuotaFiveHour, 50)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview) != 2 {
		t.Fatalf("overview rows = %d, want active + history", len(overview))
	}
	// The active cycle is emitted first by QueryCommandCodeCycleOverview.
	if overview[0].CycleEnd != nil {
		t.Error("first row must be the active cycle")
	}
	var completed CycleOverviewRow
	for _, row := range overview {
		if row.CycleEnd != nil {
			completed = row
		}
	}
	if completed.CycleID == 0 {
		t.Fatal("completed cycle missing from the overview")
	}
	if completed.PeakValue != 40 {
		t.Errorf("peak = %v, want 40", completed.PeakValue)
	}
	if completed.PeakTime.IsZero() {
		t.Error("peak snapshot time must be resolved")
	}
	if len(completed.CrossQuotas) != 1 || completed.CrossQuotas[0].Name != api.CommandCodeQuotaFiveHour {
		t.Errorf("cross quotas = %+v", completed.CrossQuotas)
	}
}

func TestCommandCodeStoreCycleOverviewLimitConsumedByActive(t *testing.T) {
	s := newCommandCodeTestStore(t)
	now := time.Now().UTC()

	// Three completed cycles plus an active one, with a limit of 1. The
	// remaining budget after the active cycle is zero, so no history may be
	// appended - passing 0 through would drop the LIMIT clause entirely.
	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(-i-1) * time.Hour)
		if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaWeekly, start, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaWeekly, start.Add(time.Minute), 5, 5); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaWeekly, now, nil); err != nil {
		t.Fatal(err)
	}

	overview, err := s.QueryCommandCodeCycleOverview(api.CommandCodeQuotaWeekly, 1)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview) != 1 {
		t.Fatalf("overview rows = %d, want 1", len(overview))
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
