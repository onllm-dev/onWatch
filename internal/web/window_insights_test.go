package web

import (
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// insertWindowHistory writes increasing utilization across one window so the
// rate lookups have a dependable climb to measure.
func insertWindowHistory(t *testing.T, s *store.Store, provider string, reset time.Time, start time.Time) {
	t.Helper()
	for i := 0; i < 12; i++ {
		at := start.Add(time.Duration(i) * 5 * time.Minute)
		util := float64(i*2) + 2
		if provider == "opencode" {
			_, err := s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{CapturedAt: at, Quotas: []api.OpenCodeQuota{
				{Name: "five_hour", Utilization: util, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
				{Name: "weekly", Utilization: 20, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
				{Name: "monthly", Utilization: 40, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
			}})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		_, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{CapturedAt: at, Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: util, Limit: 14, Utilization: util / 14 * 100, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
			{Name: api.CommandCodeQuotaWeekly, Used: 20, Limit: 35, Utilization: 20.0 / 35 * 100, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
			{Name: api.CommandCodeQuotaMonthly, Used: 18, Limit: 70, Utilization: 18.0 / 70 * 100, Format: api.CommandCodeQuotaFormatCurrency, ResetsAt: &reset},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildOpenCodeInsightsAreMeaningful(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)
	insertWindowHistory(t, s, "opencode", reset, now.Add(-time.Hour))

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	h.SetOpenCodeTracker(tracker.NewOpenCodeTracker(s, nil))
	resp := h.buildOpenCodeInsights(map[string]bool{}, time.Hour)

	if len(resp.Stats)+len(resp.Insights) > 50 {
		t.Fatalf("insights unbounded: %d", len(resp.Stats)+len(resp.Insights))
	}
	if len(resp.Insights) == 0 {
		t.Fatal("expected provider insight cards")
	}
	for _, stat := range resp.Stats {
		if stat.Key == "" {
			continue
		}
		if stat.Metric == "0.0%/hr" || stat.Metric == "Idle" && stat.Value == "0.0%/hr" {
			t.Fatalf("meaningless burn metric for %s: %+v", stat.Label, stat)
		}
		if stat.Desc == "" {
			t.Fatalf("forecast without explanation: %+v", stat)
		}
	}
	seen := map[string]bool{}
	for _, item := range resp.Insights {
		if item.Key == "" || seen[item.Key] || item.Title == "" || item.Desc == "" {
			t.Fatalf("insight card is empty, duplicated or unexplained: %+v", item)
		}
		seen[item.Key] = true
	}
}

func TestBuildCommandCodeInsightsExcludeUncappedMonthly(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)
	// A monthly dollar balance with no cap cannot produce a meaningful percent
	// burn forecast and must not be presented as one.
	snap := &api.CommandCodeSnapshot{CapturedAt: now, MonthlyCredits: 50, RemainingCredits: 32, PeriodEnd: &reset, Quotas: []api.CommandCodeQuota{
		{Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14, Utilization: 7, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
		{Name: api.CommandCodeQuotaWeekly, Used: 10, Limit: 35, Utilization: 28, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
		{Name: api.CommandCodeQuotaMonthly, Used: 18, Limit: 0, Utilization: 0, Format: api.CommandCodeQuotaFormatCurrency, ResetsAt: &reset},
	}}
	if _, err := s.InsertCommandCodeSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	h.SetCommandCodeTracker(tracker.NewCommandCodeTracker(s, nil))
	resp := h.buildCommandCodeInsights(map[string]bool{}, time.Hour)

	for _, stat := range resp.Stats {
		if strings.Contains(stat.Label, "Monthly") && strings.Contains(stat.Label, "Burn Rate") {
			t.Fatalf("uncapped monthly balance presented as percent burn: %+v", stat)
		}
	}
	if len(resp.Insights)+len(resp.Stats) == 0 {
		t.Fatal("expected Command Code insights")
	}
}
