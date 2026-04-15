package metrics

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	dto "github.com/prometheus/client_model/go"
)

func TestMetrics_ScrapeExportsUsedPercentagesAndNoMisleadingCounters(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	futureReset := now.Add(2 * time.Hour)
	pastReset := now.Add(-5 * time.Minute)

	if _, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
		CapturedAt:  now,
		CopilotPlan: "individual_pro",
		Quotas: []api.CopilotQuota{{
			Name:             "premium_interactions",
			Entitlement:      100,
			Remaining:        25,
			PercentRemaining: 25,
		}},
	}); err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: now,
		PlanType:   "pro",
		Quotas: []api.CodexQuota{{
			Name:        "five_hour",
			Utilization: 35,
		}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{{
			ModelID:           "model-a",
			Label:             "Model A",
			RemainingFraction: 0.4,
			RemainingPercent:  40,
			ResetTime:         &futureReset,
		}},
	}); err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	if _, err := s.InsertGeminiSnapshot(&api.GeminiSnapshot{
		CapturedAt: now,
		Tier:       "pro",
		ProjectID:  "project-1",
		Quotas: []api.GeminiQuota{{
			ModelID:           "gemini-2.5-pro",
			RemainingFraction: 0.8,
			UsagePercent:      20,
			ResetTime:         &pastReset,
		}},
	}); err != nil {
		t.Fatalf("InsertGeminiSnapshot: %v", err)
	}

	if _, err := s.InsertZaiSnapshot(&api.ZaiSnapshot{
		CapturedAt:          now,
		TokensUsage:         100,
		TokensCurrentValue:  15,
		TokensPercentage:    15,
		TokensNextResetTime: &futureReset,
		TimeUsage:           80,
		TimeCurrentValue:    20,
		TimePercentage:      25,
	}); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	m := New()
	m.Scrape(s, time.Minute)
	families, err := m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "copilot",
		"quota_type": "premium_interactions",
		"account_id": "",
	}, 75)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "codex",
		"quota_type": "five_hour",
		"account_id": "1",
	}, 35)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "antigravity",
		"quota_type": "model-a",
		"account_id": "",
	}, 60)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "gemini",
		"quota_type": "gemini-2.5-pro",
		"account_id": "",
	}, 20)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "zai",
		"quota_type": "tokens",
		"account_id": "",
	}, 15)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "zai",
		"quota_type": "time",
		"account_id": "",
	}, 25)
	assertGaugeValue(t, families, "onwatch_quota_time_until_reset_seconds", map[string]string{
		"provider":   "gemini",
		"quota_type": "gemini-2.5-pro",
		"account_id": "",
	}, 0)

	assertMetricFamilyMissing(t, families, "onwatch_cycle_completed_total")
	assertMetricFamilyMissing(t, families, "onwatch_quota_snapshots_total")
}

func TestMetrics_ScrapeResetsStaleSeriesBetweenScrapes(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if _, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
		CapturedAt:  now,
		CopilotPlan: "individual_pro",
		Quotas: []api.CopilotQuota{{
			Name:             "premium_interactions",
			Entitlement:      100,
			Remaining:        25,
			PercentRemaining: 25,
		}},
	}); err != nil {
		t.Fatalf("InsertCopilotSnapshot first: %v", err)
	}

	m := New()
	m.Scrape(s, time.Minute)
	families, err := m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather first scrape: %v", err)
	}
	if !hasGaugeMetric(families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "copilot",
		"quota_type": "premium_interactions",
		"account_id": "",
	}) {
		t.Fatal("expected copilot quota metric after first scrape")
	}

	if _, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
		CapturedAt:  now.Add(time.Second),
		CopilotPlan: "individual_pro",
		Quotas:      nil,
	}); err != nil {
		t.Fatalf("InsertCopilotSnapshot second: %v", err)
	}

	m.Scrape(s, time.Minute)
	families, err = m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather second scrape: %v", err)
	}
	if hasGaugeMetric(families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "copilot",
		"quota_type": "premium_interactions",
		"account_id": "",
	}) {
		t.Fatal("stale copilot quota metric persisted after later scrape")
	}
}

func assertGaugeValue(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string, want float64) {
	t.Helper()
	metric := findMetric(t, families, name, labels)
	if metric.GetGauge() == nil {
		t.Fatalf("metric %s with labels %v is not a gauge", name, labels)
	}
	got := metric.GetGauge().GetValue()
	if got != want {
		t.Fatalf("metric %s with labels %v = %v, want %v", name, labels, got, want)
	}
}

func assertMetricFamilyMissing(t *testing.T, families []*dto.MetricFamily, name string) {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			t.Fatalf("unexpected metric family %s present", name)
		}
	}
}

func hasGaugeMetric(families []*dto.MetricFamily, name string, labels map[string]string) bool {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsEqual(metric, labels) {
				return true
			}
		}
	}
	return false
}

func findMetric(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsEqual(metric, labels) {
				return metric
			}
		}
		t.Fatalf("metric %s found but labels %v missing", name, labels)
	}
	t.Fatalf("metric family %s missing", name)
	return nil
}

func metricLabelsEqual(metric *dto.Metric, want map[string]string) bool {
	if len(metric.GetLabel()) != len(want) {
		return false
	}
	for _, label := range metric.GetLabel() {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
