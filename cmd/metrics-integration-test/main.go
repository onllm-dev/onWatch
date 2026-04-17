// Package main is a standalone integration test for the /metrics endpoint.
// It seeds a SQLite DB with representative data for every provider, then
// scrapes the Metrics struct directly and validates label/value shapes.
//
// Run with:  go run ./cmd/metrics-integration-test
package main

import (
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/metrics"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type check struct {
	name string
	fn   func(body string) error
}

func main() {
	os.Exit(run())
}

func run() int {
	tmp, err := os.MkdirTemp("", "onwatch-metrics-integration-*")
	if err != nil {
		fail("mkdirtemp: %v", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	dbPath := tmp + "/test.db"
	s, err := store.New(dbPath)
	if err != nil {
		fail("store.New: %v", err)
		return 1
	}
	defer s.Close()

	now := time.Now().UTC()
	future := now.Add(2 * time.Hour)

	if err := seedAll(s, now, future); err != nil {
		fail("seed: %v", err)
		return 1
	}

	m := metrics.New()
	m.SetBuildInfo("2.11.41-integration")
	m.RecordCycleCompleted("synthetic", "")
	m.RecordCycleCompleted("synthetic", "")
	m.RecordCycleFailed("anthropic", "", "fetch_failed")
	// Seed one scrape error so the series is emitted (Prometheus omits
	// zero-valued counter series by default).
	m.RecordScrapeError("test", "synthetic_error")

	handler := m.Handler(s, time.Minute)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/metrics")
	if err != nil {
		fail("GET /metrics: %v", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fail("read body: %v", err)
		return 1
	}
	bodyStr := string(body)

	if resp.StatusCode != 200 {
		fail("status=%d want 200\nbody=%s", resp.StatusCode, bodyStr)
		return 1
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		fail("content-type=%q want text/plain...", ct)
		return 1
	}

	checks := []check{
		// #1: new timestamp metric exists, carries future Unix seconds.
		// Prometheus text format uses scientific notation for large numbers
		// (e.g. 1.776464963e+09), so match the exponent form.
		{"new: onwatch_quota_reset_timestamp_seconds present",
			requireMetric(`onwatch_quota_reset_timestamp_seconds\{[^}]*provider="zai"[^}]*\} 1\.\d+e\+09`)},
		// #1 & #6: old countdown metric must be gone.
		{"old metric gone: onwatch_quota_time_until_reset_seconds",
			requireAbsent("onwatch_quota_time_until_reset_seconds")},
		// #2: old auth_token_status must be gone, agent_healthy takes its place.
		{"old metric gone: onwatch_auth_token_status",
			requireAbsent("onwatch_auth_token_status")},
		{"new: onwatch_agent_healthy present",
			requireMetric(`onwatch_agent_healthy\{[^}]+\} 1`)},
		// #3: account_id="default" on single-account providers.
		{"account_id=\"default\" on anthropic",
			requireMetric(`onwatch_agent_healthy\{account_id="default",provider="anthropic"\}`)},
		{"account_id=\"default\" on copilot",
			requireMetric(`onwatch_agent_healthy\{account_id="default",provider="copilot"\}`)},
		// #3: multi-account still uses numeric IDs.
		{"account_id numeric on codex",
			requireMetric(`onwatch_agent_healthy\{account_id="1",provider="codex"\}`)},
		// #5: unit label on credits_balance.
		{"unit=\"usd\" on openrouter credits_balance",
			requireMetric(`onwatch_credits_balance\{account_id="default",provider="openrouter",unit="usd"\}`)},
		{"unit=\"credits\" on codex credits_balance",
			requireMetric(`onwatch_credits_balance\{account_id="1",provider="codex",unit="credits"\}`)},
		{"unit=\"prompt_credits\" on antigravity credits_balance",
			requireMetric(`onwatch_credits_balance\{account_id="default",provider="antigravity",unit="prompt_credits"\}`)},
		// #7: scrape_errors_total counter is registered (value 0 with no errors).
		{"onwatch_scrape_errors_total present (registered counter)",
			requireContains("# TYPE onwatch_scrape_errors_total counter")},
		// #8: API integrations metrics exist.
		{"onwatch_api_integration_requests per integration",
			requireMetric(`onwatch_api_integration_requests\{integration="claude-api"\} 2`)},
		{"onwatch_api_integration_spend_usd per integration",
			requireMetric(`onwatch_api_integration_spend_usd\{integration="claude-api"\} 0\.08`)},
		{"api_integrations agent_healthy",
			requireMetric(`onwatch_agent_healthy\{account_id="default",provider="api_integrations"\} 1`)},
		// #9: build_info carries SetBuildInfo version.
		{"onwatch_build_info with version",
			requireMetric(`onwatch_build_info\{commit="[^"]*",go_version="[^"]*",version="2\.11\.41-integration"\} 1`)},
		// #10: cycles_completed_total counter counts synthetic cycles.
		{"cycles_completed_total for synthetic",
			requireMetric(`onwatch_cycles_completed_total\{account_id="default",provider="synthetic"\} 2`)},
		{"cycles_failed_total for anthropic",
			requireMetric(`onwatch_cycles_failed_total\{account_id="default",provider="anthropic",reason="fetch_failed"\} 1`)},
		// #15: account_info join-metric for multi-account provider.
		{"onwatch_account_info for codex",
			requireMetric(`onwatch_account_info\{account_id="1",account_name="[^"]+",provider="codex"\} 1`)},
		// Go/process collectors still mounted.
		{"go_goroutines gauge still exposed",
			requireContains("go_goroutines ")},
		{"process_start_time_seconds gauge still exposed",
			requireContains("process_start_time_seconds ")},
	}

	passed := 0
	failed := 0
	failedNames := []string{}

	for _, c := range checks {
		if err := c.fn(bodyStr); err != nil {
			failed++
			failedNames = append(failedNames, c.name)
			fmt.Fprintf(os.Stderr, "  [FAIL] %s\n    %v\n", c.name, err)
			continue
		}
		passed++
		fmt.Printf("  [ OK ] %s\n", c.name)
	}

	fmt.Printf("\nRESULT: %d passed, %d failed (of %d)\n", passed, failed, len(checks))
	if failed > 0 {
		sort.Strings(failedNames)
		fmt.Fprintln(os.Stderr, "\nFailing checks:")
		for _, n := range failedNames {
			fmt.Fprintln(os.Stderr, "  -", n)
		}
		fmt.Fprintln(os.Stderr, "\n--- response body ---")
		fmt.Fprintln(os.Stderr, bodyStr)
		return 1
	}
	return 0
}

func seedAll(s *store.Store, now, future time.Time) error {
	// anthropic
	if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		CapturedAt: now,
		Quotas: []api.AnthropicQuota{{
			Name:        "five_hour",
			Utilization: 40,
			ResetsAt:    &future,
		}},
	}); err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}

	// codex - register account first, then snapshot
	acct, err := s.GetOrCreateProviderAccount("codex", "work-laptop")
	if err != nil {
		return fmt.Errorf("codex account: %w", err)
	}
	credits := 42.5
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:     now,
		AccountID:      acct.ID,
		PlanType:       "pro",
		CreditsBalance: &credits,
		Quotas: []api.CodexQuota{{
			Name:        "five_hour",
			Utilization: 35,
			ResetsAt:    &future,
		}},
	}); err != nil {
		return fmt.Errorf("codex snapshot: %w", err)
	}

	// copilot
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
		return fmt.Errorf("copilot: %w", err)
	}

	// zai - pass future reset so we can assert Unix() value
	if _, err := s.InsertZaiSnapshot(&api.ZaiSnapshot{
		CapturedAt:          now,
		TokensUsage:         100,
		TokensCurrentValue:  15,
		TokensPercentage:    15,
		TokensNextResetTime: &future,
		TimeUsage:           80,
		TimeCurrentValue:    20,
		TimePercentage:      25,
	}); err != nil {
		return fmt.Errorf("zai: %w", err)
	}

	// antigravity
	if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
		CapturedAt:    now,
		PromptCredits: 150,
		Models: []api.AntigravityModelQuota{{
			ModelID:           "model-a",
			Label:             "Model A",
			RemainingFraction: 0.4,
			RemainingPercent:  40,
			ResetTime:         &future,
		}},
	}); err != nil {
		return fmt.Errorf("antigravity: %w", err)
	}

	// gemini
	if _, err := s.InsertGeminiSnapshot(&api.GeminiSnapshot{
		CapturedAt: now,
		Tier:       "pro",
		ProjectID:  "project-1",
		Quotas: []api.GeminiQuota{{
			ModelID:           "gemini-2.5-pro",
			RemainingFraction: 0.8,
			UsagePercent:      20,
			ResetTime:         &future,
		}},
	}); err != nil {
		return fmt.Errorf("gemini: %w", err)
	}

	// openrouter
	limit := 100.0
	remaining := 42.0
	if _, err := s.InsertOpenRouterSnapshot(&api.OpenRouterSnapshot{
		CapturedAt:     now,
		Usage:          58,
		Limit:          &limit,
		LimitRemaining: &remaining,
	}); err != nil {
		return fmt.Errorf("openrouter: %w", err)
	}

	// api integrations: two events for "claude-api" totalling $0.08.
	cost1 := 0.05
	cost2 := 0.03
	lat1 := 320
	lat2 := 210
	events := []*apiintegrations.UsageEvent{
		{
			Timestamp:    now,
			Integration:  "claude-api",
			Provider:     "anthropic",
			Account:      "me",
			Model:        "claude-3-5-sonnet",
			RequestID:    "req-1",
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CostUSD: &cost1, LatencyMS: &lat1,
			SourcePath: "/tmp/ingest.jsonl", Fingerprint: "fp-1",
		},
		{
			Timestamp:    now,
			Integration:  "claude-api",
			Provider:     "anthropic",
			Account:      "me",
			Model:        "claude-3-5-sonnet",
			RequestID:    "req-2",
			PromptTokens: 600, CompletionTokens: 400, TotalTokens: 1000,
			CostUSD: &cost2, LatencyMS: &lat2,
			SourcePath: "/tmp/ingest.jsonl", Fingerprint: "fp-2",
		},
	}
	for _, e := range events {
		if _, err := s.InsertAPIIntegrationUsageEvent(e); err != nil {
			return fmt.Errorf("api_integrations event: %w", err)
		}
	}

	return nil
}

func requireMetric(pattern string) func(string) error {
	re := regexp.MustCompile(pattern)
	return func(body string) error {
		if re.MatchString(body) {
			return nil
		}
		return fmt.Errorf("regex %q not found", pattern)
	}
}

func requireContains(substr string) func(string) error {
	return func(body string) error {
		if strings.Contains(body, substr) {
			return nil
		}
		return fmt.Errorf("substring %q not found", substr)
	}
}

func requireAbsent(name string) func(string) error {
	return func(body string) error {
		lines := strings.Split(body, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, name+" ") || strings.HasPrefix(l, name+"{") ||
				strings.HasPrefix(l, "# HELP "+name+" ") ||
				strings.HasPrefix(l, "# TYPE "+name+" ") {
				return fmt.Errorf("found metric %q which should be absent, line: %s", name, l)
			}
		}
		return nil
	}
}

func fail(f string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+f+"\n", args...)
}
