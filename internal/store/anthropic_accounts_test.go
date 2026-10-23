package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestAnthropicStore_AccountIsolation(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(3 * time.Hour)
	mk := func(util float64, at time.Time) *api.AnthropicSnapshot {
		return &api.AnthropicSnapshot{
			CapturedAt: at,
			RawJSON:    `{}`,
			Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: util, ResetsAt: &reset}},
		}
	}

	// Primary account via the legacy API, extra account 7 via ForAccount.
	if _, err := s.InsertAnthropicSnapshot(mk(10, now.Add(-2*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertAnthropicSnapshotForAccount(7, mk(70, now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}

	primary, err := s.QueryLatestAnthropic()
	if err != nil || primary == nil || primary.Quotas[0].Utilization != 10 {
		t.Fatalf("primary latest = %+v err=%v", primary, err)
	}
	extra, err := s.QueryLatestAnthropicForAccount(7)
	if err != nil || extra == nil || extra.Quotas[0].Utilization != 70 {
		t.Fatalf("extra latest = %+v err=%v", extra, err)
	}
	if none, _ := s.QueryLatestAnthropicForAccount(8); none != nil {
		t.Fatalf("unknown account should be empty, got %+v", none)
	}

	rng, err := s.QueryAnthropicRange(now.Add(-time.Hour), now)
	if err != nil || len(rng) != 1 {
		t.Fatalf("primary range len=%d err=%v", len(rng), err)
	}
	rng, _ = s.QueryAnthropicRangeForAccount(7, now.Add(-time.Hour), now, 10)
	if len(rng) != 1 || rng[0].Quotas[0].Utilization != 70 {
		t.Fatalf("extra range = %+v", rng)
	}

	latest, err := s.QueryAnthropicLatestPerQuotaForAccount(7)
	if err != nil || len(latest) != 1 || latest[0].Utilization != 70 {
		t.Fatalf("extra latest-per-quota = %+v err=%v", latest, err)
	}
	latest, _ = s.QueryAnthropicLatestPerQuota()
	if len(latest) != 1 || latest[0].Utilization != 10 {
		t.Fatalf("primary latest-per-quota = %+v", latest)
	}

	series, _ := s.QueryAnthropicUtilizationSeriesForAccount(7, "five_hour", now.Add(-time.Hour))
	if len(series) != 1 || series[0].Utilization != 70 {
		t.Fatalf("extra series = %+v", series)
	}

	// Cycles are per account.
	if _, err := s.CreateAnthropicCycleForAccount(7, "five_hour", now, &reset); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.QueryActiveAnthropicCycle("five_hour"); c != nil {
		t.Fatal("primary must not see extra account cycle")
	}
	if err := s.UpdateAnthropicCycleForAccount(7, "five_hour", 70, 5); err != nil {
		t.Fatal(err)
	}
	c, _ := s.QueryActiveAnthropicCycleForAccount(7, "five_hour")
	if c == nil || c.PeakUtilization != 70 || c.TotalDelta != 5 {
		t.Fatalf("extra cycle = %+v", c)
	}
	if err := s.CloseAnthropicCycleForAccount(7, "five_hour", now.Add(time.Hour), 70, 5); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.QueryAnthropicCycleHistoryForAccount(7, "five_hour", 10)
	if len(hist) != 1 {
		t.Fatalf("extra history len = %d", len(hist))
	}
	if hist, _ := s.QueryAnthropicCycleHistory("five_hour"); len(hist) != 0 {
		t.Fatalf("primary history should be empty, got %d", len(hist))
	}

	if _, ok, _ := s.LastAnthropicAPISnapshotForAccount(7); !ok {
		t.Fatal("extra account API snapshot should exist")
	}
	if _, ok, _ := s.LastAnthropicAPISnapshotForAccount(9); ok {
		t.Fatal("unknown account should have no API snapshot")
	}
}
