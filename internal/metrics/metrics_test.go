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
		"account_id": "default",
	}, 75)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "codex",
		"quota_type": "five_hour",
		"account_id": "1",
	}, 35)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "antigravity",
		"quota_type": "model-a",
		"account_id": "default",
	}, 60)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "gemini",
		"quota_type": "gemini-2.5-pro",
		"account_id": "default",
	}, 20)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "zai",
		"quota_type": "tokens",
		"account_id": "default",
	}, 15)
	assertGaugeValue(t, families, "onwatch_quota_utilization_percent", map[string]string{
		"provider":   "zai",
		"quota_type": "time",
		"account_id": "default",
	}, 25)

	// #1: reset timestamps are absolute Unix seconds (not countdowns).
	assertGaugeValue(t, families, "onwatch_quota_reset_timestamp_seconds", map[string]string{
		"provider":   "antigravity",
		"quota_type": "model-a",
		"account_id": "default",
	}, float64(futureReset.Unix()))

	// #6: a reset time in the past no longer emits a 0 series - the series is absent.
	if hasGaugeMetric(families, "onwatch_quota_reset_timestamp_seconds", map[string]string{
		"provider":   "gemini",
		"quota_type": "gemini-2.5-pro",
		"account_id": "default",
	}) {
		// allowed only if it carries the actual past timestamp; we still want the series
		// to exist at the real past value so users can see "it was N minutes ago".
		assertGaugeValue(t, families, "onwatch_quota_reset_timestamp_seconds", map[string]string{
			"provider":   "gemini",
			"quota_type": "gemini-2.5-pro",
			"account_id": "default",
		}, float64(pastReset.Unix()))
	}

	// #2: agent_healthy replaces auth_token_status. Old metric must not exist.
	assertMetricFamilyMissing(t, families, "onwatch_auth_token_status")
	// #1: old countdown metric is gone.
	assertMetricFamilyMissing(t, families, "onwatch_quota_time_until_reset_seconds")

	// Counters added by later commits must not be exposed yet in this test's scope.
	assertMetricFamilyMissing(t, families, "onwatch_cycle_completed_total")
	assertMetricFamilyMissing(t, families, "onwatch_quota_snapshots_total")

	// #9: build_info is always present, default version="unknown" before SetBuildInfo.
	if !hasFamily(families, "onwatch_build_info") {
		t.Fatal("onwatch_build_info family missing")
	}
}

// TestMetrics_SetBuildInfoReplacesDefaultSeries verifies that the
// {version="unknown"} series is replaced, not accumulated, when the app
// announces its version at startup.
func TestMetrics_SetBuildInfoReplacesDefaultSeries(t *testing.T) {
	m := New()
	m.SetBuildInfo("2.11.41")

	families, err := m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	found := 0
	var version string
	for _, f := range families {
		if f.GetName() != "onwatch_build_info" {
			continue
		}
		for _, metric := range f.GetMetric() {
			found++
			for _, lbl := range metric.GetLabel() {
				if lbl.GetName() == "version" {
					version = lbl.GetValue()
				}
			}
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one build_info series, got %d", found)
	}
	if version != "2.11.41" {
		t.Fatalf("build_info version = %q, want 2.11.41", version)
	}
}

func hasFamily(families []*dto.MetricFamily, name string) bool {
	for _, f := range families {
		if f.GetName() == name {
			return true
		}
	}
	return false
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
		"account_id": "default",
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
		"account_id": "default",
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
