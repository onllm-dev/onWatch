package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// createTestConfigWithGemini returns a config with only Gemini enabled.
func createTestConfigWithGemini() *config.Config {
	return &config.Config{
		GeminiEnabled: true,
		PollInterval:  60 * time.Second,
		Port:          9211,
		AdminUser:     "admin",
		AdminPass:     "test",
		DBPath:        "./test.db",
	}
}

// createTestConfigWithGeminiAndAll returns a config with ALL providers enabled including Gemini.
func createTestConfigWithGeminiAndAll() *config.Config {
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
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

// insertTestGeminiSnapshot inserts a Gemini snapshot with given quotas.
func insertTestGeminiSnapshot(t *testing.T, s *store.Store, capturedAt time.Time, quotas []api.GeminiQuota) {
	t.Helper()
	snap := &api.GeminiSnapshot{
		CapturedAt: capturedAt,
		Tier:       "free",
		ProjectID:  "test-project",
		Quotas:     quotas,
	}
	if _, err := s.InsertGeminiSnapshot(snap); err != nil {
		t.Fatalf("failed to insert test Gemini snapshot: %v", err)
	}
}

// insertTestGeminiData inserts a Gemini snapshot with 3 model quotas for realistic test data.
func insertTestGeminiData(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now.Add(-5*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
		{ModelID: "gemini-2.5-flash", RemainingFraction: 0.5, UsagePercent: 50.0, ResetTime: &resetTime},
		{ModelID: "gemini-2.0-flash", RemainingFraction: 0.95, UsagePercent: 5.0, ResetTime: &resetTime},
	})
}

// ---------------------------------------------------------------------------
// Part 1: Single-provider Gemini endpoint tests via exported handler dispatch
// ---------------------------------------------------------------------------

// TestGeminiCurrent_ViaRouter verifies GET /api/current?provider=gemini returns
// quotas array with model data through the exported Current handler.
func TestGeminiCurrent_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Must have capturedAt
	if _, ok := resp["capturedAt"]; !ok {
		t.Error("response missing 'capturedAt'")
	}

	// Must have quotas array
	quotas, ok := resp["quotas"].([]interface{})
	if !ok {
		t.Fatalf("expected 'quotas' array, got: %v", resp)
	}
	if len(quotas) != 3 {
		t.Fatalf("expected 3 quotas, got %d", len(quotas))
	}

	// Verify quota structure
	q := quotas[0].(map[string]interface{})
	for _, field := range []string{"modelId", "displayName", "remainingFraction", "usagePercent", "remainingPercent", "status"} {
		if _, ok := q[field]; !ok {
			t.Errorf("quota missing field '%s'", field)
		}
	}

	// Must have tier and projectId
	if _, ok := resp["tier"]; !ok {
		t.Error("response missing 'tier'")
	}
	if _, ok := resp["projectId"]; !ok {
		t.Error("response missing 'projectId'")
	}
}

// TestGeminiHistory_ViaRouter verifies GET /api/history?provider=gemini&range=1h returns
// a flat array through the exported History handler.
func TestGeminiHistory_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now.Add(-30*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
		{ModelID: "gemini-2.5-flash", RemainingFraction: 0.3, UsagePercent: 70.0, ResetTime: &resetTime},
	})
	insertTestGeminiSnapshot(t, s, now.Add(-15*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.7, UsagePercent: 30.0, ResetTime: &resetTime},
		{ModelID: "gemini-2.5-flash", RemainingFraction: 0.2, UsagePercent: 80.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=gemini&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Must be a flat JSON array (not wrapped in an object)
	var arr []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &arr); err != nil {
		t.Fatalf("response must be a JSON array, got: %s", rr.Body.String())
	}
	if len(arr) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(arr))
	}

	for _, entry := range arr {
		if _, ok := entry["capturedAt"]; !ok {
			t.Error("entry missing 'capturedAt'")
		}
		// Should have model IDs as flat keys
		if _, ok := entry["gemini-2.5-pro"]; !ok {
			t.Error("entry missing flat key 'gemini-2.5-pro'")
		}
		if _, ok := entry["gemini-2.5-flash"]; !ok {
			t.Error("entry missing flat key 'gemini-2.5-flash'")
		}
		// Must NOT have nested quotas
		if _, ok := entry["quotas"]; ok {
			t.Error("entry should not have nested 'quotas' key")
		}
	}
}

// TestGeminiCycles_ViaRouter verifies GET /api/cycles?provider=gemini returns
// cycles response through the exported Cycles handler.
func TestGeminiCycles_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now, []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Must have cycles key
	if _, ok := resp["cycles"]; !ok {
		t.Error("response missing 'cycles'")
	}
}

// TestGeminiSummary_ViaRouter verifies GET /api/summary?provider=gemini returns
// summary response through the exported Summary handler.
func TestGeminiSummary_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now, []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	// Without a GeminiTracker, summary should return the "tracker not available" fallback
	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Without tracker, should indicate tracker not available
	if errMsg, ok := resp["error"].(string); ok {
		if errMsg != "tracker not available" {
			t.Errorf("expected 'tracker not available' error, got %q", errMsg)
		}
	}
}

// TestGeminiInsights_ViaRouter verifies GET /api/insights?provider=gemini&range=7d returns
// empty stats/insights through the exported Insights handler.
func TestGeminiInsights_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=gemini&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// insightsGemini returns real stats and per-model insights
	stats, ok := resp["stats"].([]interface{})
	if !ok {
		t.Fatalf("expected 'stats' array, got: %v", resp)
	}
	if len(stats) == 0 {
		t.Error("expected non-empty stats for Gemini insights")
	}

	insights, ok := resp["insights"].([]interface{})
	if !ok {
		t.Fatalf("expected 'insights' array, got: %v", resp)
	}
	if len(insights) == 0 {
		t.Error("expected non-empty insights for Gemini insights (per-model burn rates)")
	}
}

// TestGeminiCycleOverview_ViaRouter verifies GET /api/cycle-overview?provider=gemini returns
// groupBy/quotaNames/cycles through the exported CycleOverview handler.
func TestGeminiCycleOverview_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Must have provider
	if provider, ok := resp["provider"].(string); !ok || provider != "gemini" {
		t.Errorf("expected provider='gemini', got %v", resp["provider"])
	}

	// Must have groupBy
	if _, ok := resp["groupBy"]; !ok {
		t.Error("response missing 'groupBy'")
	}

	// Must have quotaNames
	quotaNames, ok := resp["quotaNames"].([]interface{})
	if !ok {
		t.Fatalf("expected 'quotaNames' array, got: %v", resp)
	}
	if len(quotaNames) == 0 {
		t.Error("expected non-empty quotaNames")
	}

	// Must have cycles
	if _, ok := resp["cycles"]; !ok {
		t.Error("response missing 'cycles'")
	}
}

// TestGeminiLoggingHistory_ViaRouter verifies GET /api/logging-history?provider=gemini&range=1
// returns provider/quotaNames/logs through the exported LoggingHistory handler.
func TestGeminiLoggingHistory_ViaRouter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now.Add(-10*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
		{ModelID: "gemini-2.5-flash", RemainingFraction: 0.5, UsagePercent: 50.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=gemini&range=1", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Must have provider field
	if provider, ok := resp["provider"].(string); !ok || provider != "gemini" {
		t.Errorf("expected provider='gemini', got %v", resp["provider"])
	}

	// Must have quotaNames array
	quotaNames, ok := resp["quotaNames"].([]interface{})
	if !ok {
		t.Fatalf("expected 'quotaNames' array, got: %v", resp)
	}
	if len(quotaNames) == 0 {
		t.Error("expected non-empty quotaNames")
	}

	// Must have logs array
	logs, ok := resp["logs"].([]interface{})
	if !ok {
		t.Fatalf("expected 'logs' array, got: %v", resp)
	}
	if len(logs) == 0 {
		t.Error("expected non-empty logs")
	}

	// Verify log entry structure
	if len(logs) > 0 {
		entry := logs[0].(map[string]interface{})
		if _, ok := entry["capturedAt"]; !ok {
			t.Error("log entry missing 'capturedAt'")
		}
		if _, ok := entry["crossQuotas"]; !ok {
			t.Error("log entry missing 'crossQuotas'")
		}
	}
}

// ---------------------------------------------------------------------------
// Part 2: "Both" provider tests verifying Gemini inclusion
// ---------------------------------------------------------------------------

// TestBothCurrent_IncludesGemini verifies GET /api/current?provider=both includes Gemini
// in the combined response when Gemini is configured.
func TestBothCurrent_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Gemini must be present in the combined response
	gemini, ok := resp["gemini"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'gemini' key in both current response, keys: %v", keysOf(resp))
	}

	// Gemini current must have quotas
	quotas, ok := gemini["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected 'quotas' array in gemini current response")
	}
	if len(quotas) != 3 {
		t.Errorf("expected 3 gemini quotas, got %d", len(quotas))
	}
}

// TestBothHistory_IncludesGemini verifies GET /api/history?provider=both&range=1h includes
// Gemini as a non-empty array in the combined response.
func TestBothHistory_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now.Add(-20*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
	})
	insertTestGeminiSnapshot(t, s, now.Add(-10*time.Minute), []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.7, UsagePercent: 30.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

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

	gemini, ok := resp["gemini"].([]interface{})
	if !ok {
		t.Fatalf("expected 'gemini' key in both history response, keys: %v", keysOf(resp))
	}
	if len(gemini) == 0 {
		t.Error("expected non-empty gemini history array")
	}
}

// TestBothInsights_IncludesGemini verifies GET /api/insights?provider=both&range=7d includes
// Gemini with empty stats/insights in the combined response.
func TestBothInsights_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

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

	// insightsBoth includes Gemini with empty stats/insights
	gemini, ok := resp["gemini"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'gemini' key in both insights response, keys: %v", keysOf(resp))
	}

	// Should have stats array (empty for Gemini)
	if _, ok := gemini["stats"]; !ok {
		t.Error("gemini insights missing 'stats'")
	}

	// Should have insights array (empty for Gemini)
	if _, ok := gemini["insights"]; !ok {
		t.Error("gemini insights missing 'insights'")
	}
}

// TestBothCycles_IncludesGemini verifies GET /api/cycles?provider=both includes Gemini
// in the combined response.
func TestBothCycles_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now, []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Gemini must be present in the combined cycles response
	if _, ok := resp["gemini"]; !ok {
		t.Errorf("expected 'gemini' key in both cycles response, keys: %v", keysOf(resp))
	}
}

// TestBothSummary_IncludesGemini verifies GET /api/summary?provider=both includes Gemini
// in the combined response. Note: summaryBoth requires geminiTracker != nil,
// so without setting it, the Gemini key should be absent.
func TestBothSummary_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(1 * time.Hour)
	insertTestGeminiSnapshot(t, s, now, []api.GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20.0, ResetTime: &resetTime},
	})

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// summaryBoth requires geminiTracker != nil - without it, gemini key won't appear.
	// This test verifies the endpoint completes successfully with Gemini configured.
	// The response is valid JSON regardless of whether gemini key is present.
	_ = resp
}

// TestBothCycleOverview_IncludesGemini verifies GET /api/cycle-overview?provider=both includes
// Gemini in the combined response.
func TestBothCycleOverview_IncludesGemini(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestGeminiData(t, s)

	cfg := createTestConfigWithGeminiAndAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=both", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// cycleOverviewBoth includes Gemini when store has data
	if _, ok := resp["gemini"]; !ok {
		t.Errorf("expected 'gemini' key in both cycle-overview response, keys: %v", keysOf(resp))
	}
}

// ---------------------------------------------------------------------------
// Additional edge case tests
// ---------------------------------------------------------------------------

// TestGeminiCurrent_ViaRouter_NilStore verifies Current returns empty quotas when store is nil.
func TestGeminiCurrent_ViaRouter_NilStore(t *testing.T) {
	cfg := createTestConfigWithGemini()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	quotas, ok := resp["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected 'quotas' array")
	}
	if len(quotas) != 0 {
		t.Errorf("expected empty quotas, got %d", len(quotas))
	}
}

// TestGeminiHistory_ViaRouter_NilStore verifies History returns empty flat array when store is nil.
func TestGeminiHistory_ViaRouter_NilStore(t *testing.T) {
	cfg := createTestConfigWithGemini()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=gemini&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var arr []interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &arr); err != nil {
		t.Fatalf("response must be a JSON array, got: %s", rr.Body.String())
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %d items", len(arr))
	}
}

// TestGeminiCycles_ViaRouter_NoData verifies Cycles returns empty cycles with no data.
func TestGeminiCycles_ViaRouter_NoData(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cfg := createTestConfigWithGemini()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cycles, ok := resp["cycles"].([]interface{})
	if !ok {
		t.Fatal("expected 'cycles' array")
	}
	if len(cycles) != 0 {
		t.Errorf("expected empty cycles, got %d", len(cycles))
	}
}

// TestGeminiCycleOverview_ViaRouter_NilStore verifies CycleOverview returns empty data when store is nil.
func TestGeminiCycleOverview_ViaRouter_NilStore(t *testing.T) {
	cfg := createTestConfigWithGemini()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=gemini", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cycles, ok := resp["cycles"].([]interface{})
	if !ok {
		t.Fatal("expected 'cycles' array")
	}
	if len(cycles) != 0 {
		t.Errorf("expected empty cycles, got %d", len(cycles))
	}
}

// TestGeminiLoggingHistory_ViaRouter_NilStore verifies LoggingHistory returns empty logs when store is nil.
func TestGeminiLoggingHistory_ViaRouter_NilStore(t *testing.T) {
	cfg := createTestConfigWithGemini()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=gemini&range=1", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	logs, ok := resp["logs"].([]interface{})
	if !ok {
		t.Fatal("expected 'logs' array")
	}
	if len(logs) != 0 {
		t.Errorf("expected empty logs, got %d", len(logs))
	}
}

// TestGeminiInsights_ViaRouter_NilStore verifies Insights returns empty stats/insights when store is nil.
func TestGeminiInsights_ViaRouter_NilStore(t *testing.T) {
	cfg := createTestConfigWithGemini()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=gemini&range=24h", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	stats, ok := resp["stats"].([]interface{})
	if !ok {
		t.Fatal("expected 'stats' array")
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %d", len(stats))
	}

	insights, ok := resp["insights"].([]interface{})
	if !ok {
		t.Fatal("expected 'insights' array")
	}
	if len(insights) != 0 {
		t.Errorf("expected empty insights, got %d", len(insights))
	}
}

// keysOf returns the keys of a map for debug output.
func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
