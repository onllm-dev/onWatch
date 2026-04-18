package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func createTestConfigWithCursor() *config.Config {
	return &config.Config{
		CursorToken:  "cursor_test_token",
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func createTestConfigWithCursorAndAll() *config.Config {
	return &config.Config{
		SyntheticAPIKey:    "syn_test_key",
		ZaiAPIKey:          "zai_test_key",
		ZaiBaseURL:         "https://api.z.ai/api",
		AnthropicToken:     "test_anthropic_token",
		CopilotToken:       "ghp_test_copilot_token",
		CodexToken:         "codex_test_token",
		AntigravityEnabled: true,
		MiniMaxAPIKey:      "minimax_test_key",
		GeminiEnabled:      true,
		CursorToken:        "cursor_test_token",
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

func insertTestCursorSnapshot(t *testing.T, s *store.Store, capturedAt time.Time, accountType api.CursorAccountType, planName string, quotas []api.CursorQuota) {
	t.Helper()
	snap := &api.CursorSnapshot{
		CapturedAt:  capturedAt,
		AccountType: accountType,
		PlanName:    planName,
		Quotas:      quotas,
	}
	if _, err := s.InsertCursorSnapshot(snap); err != nil {
		t.Fatalf("failed to insert test Cursor snapshot: %v", err)
	}
}

func insertTrackedCursorSnapshot(t *testing.T, s *store.Store, tr *tracker.CursorTracker, capturedAt time.Time, quotas []api.CursorQuota) {
	t.Helper()
	snap := &api.CursorSnapshot{
		CapturedAt:  capturedAt,
		AccountType: api.CursorAccountIndividual,
		PlanName:    "Pro+",
		Quotas:      quotas,
	}
	if _, err := s.InsertCursorSnapshot(snap); err != nil {
		t.Fatalf("failed to insert tracked Cursor snapshot: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("failed to process tracked Cursor snapshot: %v", err)
	}
}

func TestBuildCursorCurrent_UsesLatestSnapshotMetadata(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	oldReset := now.Add(24 * time.Hour)

	oldSnap := &api.CursorSnapshot{
		CapturedAt:  now.Add(-2 * time.Hour),
		AccountType: api.CursorAccountIndividual,
		PlanName:    "Pro",
		Quotas: []api.CursorQuota{
			{Name: "api_usage", Utilization: 12, Format: api.CursorFormatPercent, ResetsAt: &oldReset},
		},
	}
	newSnap := &api.CursorSnapshot{
		CapturedAt:  now.Add(-10 * time.Minute),
		AccountType: api.CursorAccountEnterprise,
		PlanName:    "Enterprise",
		Quotas: []api.CursorQuota{
			{Name: "requests_gpt-4.1", Used: 15, Limit: 100, Utilization: 15, Format: api.CursorFormatCount},
		},
	}

	if _, err := s.InsertCursorSnapshot(oldSnap); err != nil {
		t.Fatalf("insert old snapshot: %v", err)
	}
	if _, err := s.InsertCursorSnapshot(newSnap); err != nil {
		t.Fatalf("insert new snapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	current := h.buildCursorCurrent()

	if got := current["accountType"]; got != string(api.CursorAccountEnterprise) {
		t.Fatalf("accountType = %v, want %q", got, api.CursorAccountEnterprise)
	}
	if got := current["planName"]; got != "Enterprise" {
		t.Fatalf("planName = %v, want Enterprise", got)
	}
}

func TestCursorInsights_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(3 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-20*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 45, Limit: 100, Utilization: 45, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=cursor&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := resp["stats"].([]interface{}); !ok {
		t.Fatalf("expected stats array, got: %v", resp)
	}
	if _, ok := resp["insights"].([]interface{}); !ok {
		t.Fatalf("expected insights array, got: %v", resp)
	}
}

func TestBuildCursorInsights_ShowsQuotaBurnRates(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(3 * time.Hour)
	tr := tracker.NewCursorTracker(s, slog.Default())
	insertTrackedCursorSnapshot(t, s, tr, now.Add(-40*time.Minute), []api.CursorQuota{
		{Name: "total_usage", Used: 30, Limit: 100, Utilization: 30, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 10, Limit: 100, Utilization: 10, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "api_usage", Used: 5, Limit: 100, Utilization: 5, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})
	insertTrackedCursorSnapshot(t, s, tr, now.Add(-10*time.Minute), []api.CursorQuota{
		{Name: "total_usage", Used: 45, Limit: 100, Utilization: 45, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 25, Limit: 100, Utilization: 25, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "api_usage", Used: 15, Limit: 100, Utilization: 15, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	h.cursorTracker = tr
	resp := h.buildCursorInsights(map[string]bool{}, 7*24*time.Hour)

	labels := map[string]bool{}
	for _, stat := range resp.Stats {
		labels[stat.Label] = true
	}

	for _, want := range []string{"Total Usage Burn Rate", "Auto + Composer Burn Rate", "API Usage Burn Rate"} {
		if !labels[want] {
			t.Fatalf("missing stat %q in %+v", want, resp.Stats)
		}
	}
}

func TestBuildCursorInsights_IncludesPlanStat(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-10*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 12, Limit: 100, Utilization: 12, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	resp := h.buildCursorInsights(map[string]bool{}, 7*24*time.Hour)

	var planStat *cursorInsightStat
	for i := range resp.Stats {
		if resp.Stats[i].Label == "Plan" {
			planStat = &resp.Stats[i]
			break
		}
	}
	if planStat == nil {
		t.Fatal("missing Plan stat")
	}
	if planStat.Value != "Pro+" {
		t.Fatalf("Plan stat value = %q, want %q", planStat.Value, "Pro+")
	}
}

func TestBuildCursorInsights_ShowsProjectedBurnRateInsights(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(3 * time.Hour)
	tr := tracker.NewCursorTracker(s, slog.Default())
	insertTrackedCursorSnapshot(t, s, tr, now.Add(-40*time.Minute), []api.CursorQuota{
		{Name: "total_usage", Used: 25, Limit: 100, Utilization: 25, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 10, Limit: 100, Utilization: 10, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "api_usage", Used: 15, Limit: 100, Utilization: 15, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})
	insertTrackedCursorSnapshot(t, s, tr, now.Add(-10*time.Minute), []api.CursorQuota{
		{Name: "total_usage", Used: 65, Limit: 100, Utilization: 65, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 25, Limit: 100, Utilization: 25, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "api_usage", Used: 18, Limit: 100, Utilization: 18, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	h.cursorTracker = tr
	resp := h.buildCursorInsights(map[string]bool{}, 7*24*time.Hour)

	statByLabel := map[string]cursorInsightStat{}
	for _, s := range resp.Stats {
		statByLabel[s.Label] = s
	}

	totalBurn, ok := statByLabel["Total Usage Burn Rate"]
	if !ok {
		t.Fatalf("missing Total Usage Burn Rate stat in %+v", resp.Stats)
	}
	if totalBurn.Severity != "negative" {
		t.Fatalf("Total Usage Burn Rate severity = %q, want %q", totalBurn.Severity, "negative")
	}
	if totalBurn.Metric == "" || totalBurn.Metric == "Analyzing..." {
		t.Fatalf("expected burn rate metric, got %q", totalBurn.Metric)
	}
	if totalBurn.Sublabel == "" {
		t.Fatal("expected projected sublabel for total burn stat")
	}

	autoBurn, ok := statByLabel["Auto + Composer Burn Rate"]
	if !ok {
		t.Fatalf("missing Auto + Composer Burn Rate stat in %+v", resp.Stats)
	}
	if autoBurn.Severity != "warning" {
		t.Fatalf("Auto + Composer Burn Rate severity = %q, want %q", autoBurn.Severity, "warning")
	}

	apiBurn, ok := statByLabel["API Usage Burn Rate"]
	if !ok {
		t.Fatalf("missing API Usage Burn Rate stat in %+v", resp.Stats)
	}
	if apiBurn.Severity != "positive" {
		t.Fatalf("API Usage Burn Rate severity = %q, want %q", apiBurn.Severity, "positive")
	}

	// Burn rate analysis must no longer appear as separate insight cards
	for _, item := range resp.Insights {
		if item.Key == "forecast_total_usage" || item.Key == "forecast_auto_usage" || item.Key == "forecast_api_usage" {
			t.Errorf("burn rate insight %q should be embedded in stats, not in insights", item.Key)
		}
	}
}

func TestBuildCursorInsights_PlanStatFirst(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-10*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 12, Limit: 100, Utilization: 12, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 8, Limit: 100, Utilization: 8, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	resp := h.buildCursorInsights(map[string]bool{}, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Fatal("expected stats")
	}
	if resp.Stats[0].Label != "Plan" {
		t.Fatalf("first stat label = %q, want Plan", resp.Stats[0].Label)
	}
}

func TestBuildCursorCurrent_UsesHealthyStatusBelowWarning(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-10*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 12, Limit: 100, Utilization: 12, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	current := h.buildCursorCurrent()

	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatalf("expected quotas in current response, got %#v", current["quotas"])
	}

	firstQuota, ok := quotas[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected quota map, got %#v", quotas[0])
	}
	if got := firstQuota["status"]; got != "healthy" {
		t.Fatalf("status = %v, want healthy", got)
	}
}

func TestLoggingHistoryCursor_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(7 * 24 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-2*time.Hour), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 8, Limit: 100, Utilization: 8, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})
	insertTestCursorSnapshot(t, s, now.Add(-1*time.Hour), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 35, Limit: 100, Utilization: 35, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "auto_usage", Used: 12, Limit: 100, Utilization: 12, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=cursor&range=1", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Provider   string                   `json:"provider"`
		QuotaNames []string                 `json:"quotaNames"`
		Logs       []map[string]interface{} `json:"logs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp.Provider != "cursor" {
		t.Fatalf("provider = %q, want cursor", resp.Provider)
	}
	if len(resp.QuotaNames) < 2 || resp.QuotaNames[0] != "total_usage" {
		t.Fatalf("unexpected quotaNames: %#v", resp.QuotaNames)
	}
	if len(resp.Logs) != 2 {
		t.Fatalf("logs len = %d, want 2", len(resp.Logs))
	}
}

func TestBothHistory_IncludesCursor(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-20*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 10, Limit: 100, Utilization: 10, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})
	insertTestCursorSnapshot(t, s, now.Add(-10*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 25, Limit: 100, Utilization: 25, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursorAndAll())

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cursor, ok := resp["cursor"].([]interface{})
	if !ok {
		t.Fatalf("expected 'cursor' key in both history response, keys: %v", keysOf(resp))
	}
	if len(cursor) == 0 {
		t.Fatal("expected non-empty cursor history array")
	}
}

func TestBothInsights_IncludesCursor(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-15*time.Minute), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 35, Limit: 100, Utilization: 35, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
		{Name: "api_usage", Used: 10, Limit: 100, Utilization: 10, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursorAndAll())

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=both&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cursor, ok := resp["cursor"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'cursor' key in both insights response, keys: %v", keysOf(resp))
	}
	if _, ok := cursor["stats"]; !ok {
		t.Error("cursor insights missing 'stats'")
	}
	if _, ok := cursor["insights"]; !ok {
		t.Error("cursor insights missing 'insights'")
	}
}

func TestHistoryCursor_ThirtyDayRange(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(31 * 24 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-20*24*time.Hour), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})
	insertTestCursorSnapshot(t, s, now.Add(-2*24*time.Hour), api.CursorAccountIndividual, "Pro+", []api.CursorQuota{
		{Name: "total_usage", Used: 35, Limit: 100, Utilization: 35, Format: api.CursorFormatPercent, ResetsAt: &resetTime},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=cursor&range=30d", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("history len = %d, want 2", len(resp))
	}
}
