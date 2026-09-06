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

func createTestConfigWithOllama() *config.Config {
	return &config.Config{
		OllamaAPIKey: "sk-ollama-test",
		PollInterval: 60 * time.Second,
		Port:         9212,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func insertTestOllamaSnapshot(t *testing.T, s *store.Store, capturedAt time.Time, snap *api.OllamaSnapshot) {
	t.Helper()
	snap.CapturedAt = capturedAt
	if _, err := s.InsertOllamaSnapshot(snap); err != nil {
		t.Fatalf("failed to insert test Ollama snapshot: %v", err)
	}
}

func TestBuildOllamaCurrent_UsesLatestSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(72 * time.Hour)
	insertTestOllamaSnapshot(t, s, now, &api.OllamaSnapshot{
		Plan:            "pro",
		AccountName:     "Ada Lovelace",
		AccountEmail:    "ada@example.com",
		MonthlyUsedUSD:  12,
		MonthlyLimitUSD: 60,
		ExtraCostUSD:    3.5,
		Models: []api.OllamaModelUsage{
			{Name: "qwen3-coder", RequestCount: 120, Cost: 2.0},
			{Name: "gpt-oss", RequestCount: 30, Cost: 1.5},
		},
		Quotas: []api.OllamaQuota{
			{Name: "monthly", Used: 12, Limit: 60, Utilization: 20, Format: api.OllamaQuotaFormatCurrency, ResetsAt: &reset},
		},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	current := h.buildOllamaCurrent()

	if got := current["plan"]; got != "pro" {
		t.Fatalf("plan = %v, want pro", got)
	}
	if got := current["accountName"]; got != "Ada Lovelace" {
		t.Fatalf("accountName = %v, want Ada Lovelace", got)
	}
	if got := current["extraCostUsd"]; got != 3.5 {
		t.Fatalf("extraCostUsd = %v, want 3.5", got)
	}

	models, ok := current["models"].([]interface{})
	if !ok || len(models) != 2 {
		t.Fatalf("models = %#v, want two models", current["models"])
	}

	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) != 1 {
		t.Fatalf("quotas = %#v, want one quota", current["quotas"])
	}
	q := quotas[0].(map[string]interface{})
	if _, hasLimit := q["limit"]; !hasLimit {
		t.Fatalf("quota map missing 'limit' key: %v", q)
	}
	if _, unknown := q["limitUnknown"]; unknown {
		t.Fatalf("quota with a known cap must not set limitUnknown: %v", q)
	}
	if got := q["displayName"]; got != "Monthly Included Usage" {
		t.Fatalf("displayName = %v, want Monthly Included Usage", got)
	}
}

func TestBuildOllamaCurrent_LimitUnknown(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestOllamaSnapshot(t, s, now, &api.OllamaSnapshot{
		Plan:            "free",
		AccountName:     "Grace Hopper",
		MonthlyUsedUSD:  4.25,
		MonthlyLimitUSD: 0,
		Quotas: []api.OllamaQuota{
			{Name: "monthly", Used: 4.25, Limit: 0, Utilization: 0, Format: api.OllamaQuotaFormatCurrency},
		},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	current := h.buildOllamaCurrent()

	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) != 1 {
		t.Fatalf("quotas = %#v, want one quota", current["quotas"])
	}
	q := quotas[0].(map[string]interface{})
	unknown, ok := q["limitUnknown"].(bool)
	if !ok || !unknown {
		t.Fatalf("limitUnknown = %v, want true when cap is unknown", q["limitUnknown"])
	}
	if got := q["used"]; got != 4.25 {
		t.Fatalf("used = %v, want 4.25", got)
	}
}

func TestCyclesOllama_DefaultsToMonthly(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(72 * time.Hour)
	if _, err := s.CreateOllamaCycle("monthly", now, &reset); err != nil {
		t.Fatalf("CreateOllamaCycle: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	req := httptest.NewRequest(http.MethodGet, "/api/ollama/cycles", nil)
	rec := httptest.NewRecorder()
	h.cyclesOllama(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var cycles []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &cycles); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("cycles = %d, want 1", len(cycles))
	}
	if got := cycles[0]["quotaName"]; got != "monthly" {
		t.Fatalf("quotaName = %v, want monthly", got)
	}
}

func TestCycleOverviewOllama_DefaultsToMonthly(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(72 * time.Hour)
	if _, err := s.CreateOllamaCycle("monthly", now, &reset); err != nil {
		t.Fatalf("CreateOllamaCycle: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	req := httptest.NewRequest(http.MethodGet, "/api/ollama/cycle-overview", nil)
	rec := httptest.NewRecorder()
	h.cycleOverviewOllama(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var overview []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(overview) != 1 {
		t.Fatalf("overview = %d, want 1", len(overview))
	}
	if got := overview[0]["QuotaType"]; got != "monthly" {
		t.Fatalf("QuotaType = %v, want monthly", got)
	}
}

func TestBuildOllamaSummary_WithTracker(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(72 * time.Hour)
	snap := &api.OllamaSnapshot{
		CapturedAt:      now,
		Plan:            "pro",
		MonthlyUsedUSD:  18,
		MonthlyLimitUSD: 60,
		Quotas: []api.OllamaQuota{
			{Name: "monthly", Used: 18, Limit: 60, Utilization: 30, Format: api.OllamaQuotaFormatCurrency, ResetsAt: &reset},
		},
	}
	if _, err := s.InsertOllamaSnapshot(snap); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tr := tracker.NewOllamaTracker(s, slog.Default())
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	h.SetOllamaTracker(tr)

	summary := h.buildOllamaSummaryMap()
	entry, ok := summary["monthly"].(map[string]interface{})
	if !ok {
		t.Fatalf("summary missing monthly: %#v", summary)
	}
	if got := entry["currentUtil"]; got != 30.0 {
		t.Fatalf("currentUtil = %v, want 30", got)
	}
}

func TestBuildOllamaInsights_StatsFromSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(72 * time.Hour)
	insertTestOllamaSnapshot(t, s, now, &api.OllamaSnapshot{
		Plan:            "max",
		AccountName:     "Alan Turing",
		MonthlyUsedUSD:  40,
		MonthlyLimitUSD: 300,
		ExtraCostUSD:    5,
		Models: []api.OllamaModelUsage{
			{Name: "qwen3-coder", RequestCount: 200, Cost: 4},
		},
		Quotas: []api.OllamaQuota{
			{Name: "monthly", Used: 40, Limit: 300, Utilization: 13.3, Format: api.OllamaQuotaFormatCurrency, ResetsAt: &reset},
		},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOllama())
	resp := h.buildOllamaInsights(map[string]bool{}, 24*time.Hour)

	var hasPlan, hasAccount, hasExtra, hasModel bool
	for _, st := range resp.Stats {
		switch st.Label {
		case "Plan":
			hasPlan = st.Value == "Max"
		case "Account":
			hasAccount = st.Value == "Alan Turing"
		case "Extra Usage":
			hasExtra = st.Value == "$5.00"
		case "qwen3-coder":
			hasModel = st.Value == "200 req"
		}
	}
	if !hasPlan {
		t.Fatalf("missing title-cased Plan stat: %#v", resp.Stats)
	}
	if !hasAccount {
		t.Fatalf("missing Account stat: %#v", resp.Stats)
	}
	if !hasExtra {
		t.Fatalf("missing Extra Usage stat: %#v", resp.Stats)
	}
	if !hasModel {
		t.Fatalf("missing per-model breakdown stat: %#v", resp.Stats)
	}
}
