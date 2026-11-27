package metrics

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestScrapeOpenCode_WindowsStatusAndFreshness(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New()
	labels := func(window string) map[string]string {
		return map[string]string{"provider": "opencode", "quota_type": window, "account_id": "default"}
	}
	health := map[string]string{"provider": "opencode", "account_id": "default"}
	m.Scrape(s, 2*time.Minute)
	f, err := m.Gather().Gather()
	if err != nil {
		t.Fatal(err)
	}
	if hasGaugeMetric(f, "onwatch_agent_healthy", health) {
		t.Fatal("no snapshot must not fabricate health")
	}
	now := time.Now().UTC()
	reset := now.Add(time.Hour)
	if _, err := s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{
		CapturedAt: now.Add(-10 * time.Minute),
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 25, Format: api.OpenCodeQuotaFormatPercent, Status: "exhausted", ResetsAt: &reset},
			{Name: "weekly", Utilization: 0, Format: api.OpenCodeQuotaFormatCurrency},
			{Name: "monthly", Utilization: 50, Format: api.OpenCodeQuotaFormatPercent, Status: "OK"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	m.Scrape(s, 2*time.Minute)
	f, err = m.Gather().Gather()
	if err != nil {
		t.Fatal(err)
	}
	assertGaugeValue(t, f, "onwatch_quota_utilization_percent", labels("five_hour"), 25)
	assertGaugeValue(t, f, "onwatch_quota_blocked", labels("five_hour"), 1)
	assertGaugeValue(t, f, "onwatch_quota_reset_timestamp_seconds", labels("five_hour"), float64(reset.Unix()))
	assertGaugeValue(t, f, "onwatch_quota_utilization_percent", labels("weekly"), 0)
	assertGaugeValue(t, f, "onwatch_quota_utilization_percent", labels("monthly"), 50)
	assertGaugeValue(t, f, "onwatch_quota_blocked", labels("monthly"), 0)
	assertGaugeValue(t, f, "onwatch_agent_healthy", health, 0)
	if hasGaugeMetric(f, "onwatch_quota_blocked", labels("weekly")) {
		t.Fatal("unknown status must not be explicit ok")
	}
	if _, err := s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{
		CapturedAt: now,
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 17, Format: api.OpenCodeQuotaFormatPercent, Status: "ok"},
			{Name: "weekly", Utilization: 125, Format: api.OpenCodeQuotaFormatCurrency, ResetsAt: &reset},
		},
	}); err != nil {
		t.Fatal(err)
	}
	m.Scrape(s, 2*time.Minute)
	f, err = m.Gather().Gather()
	if err != nil {
		t.Fatal(err)
	}
	assertGaugeValue(t, f, "onwatch_quota_utilization_percent", labels("five_hour"), 17)
	assertGaugeValue(t, f, "onwatch_quota_blocked", labels("five_hour"), 0)
	assertGaugeValue(t, f, "onwatch_agent_healthy", health, 1)
	assertGaugeValue(t, f, "onwatch_quota_utilization_percent", labels("weekly"), 125)
	assertGaugeValue(t, f, "onwatch_quota_reset_timestamp_seconds", labels("weekly"), float64(reset.Unix()))
	if hasGaugeMetric(f, "onwatch_quota_utilization_percent", labels("monthly")) ||
		hasGaugeMetric(f, "onwatch_quota_blocked", labels("monthly")) ||
		hasGaugeMetric(f, "onwatch_quota_reset_timestamp_seconds", labels("five_hour")) {
		t.Fatal("removed window/reset must not retain stale series")
	}
}
