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

func createTestConfigWithOpenCode() *config.Config {
	return &config.Config{
		OpenCodeGoWorkspaceID: "ws-test",
		OpenCodeGoAuthCookie:  "cookie-test",
		PollInterval:          60 * time.Second,
		Port:                  9211,
		AdminUser:             "admin",
		AdminPass:             "test",
		DBPath:                "./test.db",
	}
}

func insertTestOpenCodeSnapshot(t *testing.T, s *store.Store, capturedAt time.Time, quotas []api.OpenCodeQuota) {
	t.Helper()
	snap := &api.OpenCodeSnapshot{
		CapturedAt:  capturedAt,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas:      quotas,
	}
	if _, err := s.InsertOpenCodeSnapshot(snap); err != nil {
		t.Fatalf("failed to insert test OpenCode snapshot: %v", err)
	}
}

func TestBuildOpenCodeCurrent_UsesLatestSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	insertTestOpenCodeSnapshot(t, s, now, []api.OpenCodeQuota{
		{Name: "five_hour", Utilization: 15, Used: 15, Limit: 100, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	current := h.buildOpenCodeCurrent()

	if got := current["planName"]; got != "OpenCode Go" {
		t.Fatalf("planName = %v, want OpenCode Go", got)
	}
	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) != 1 {
		t.Fatalf("quotas = %#v, want one quota", current["quotas"])
	}
}

func TestCyclesOpenCode_DefaultsToFiveHour(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	if _, err := s.CreateOpenCodeCycle("five_hour", now, &reset); err != nil {
		t.Fatalf("CreateOpenCodeCycle: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	req := httptest.NewRequest(http.MethodGet, "/api/opencode/cycles", nil)
	rec := httptest.NewRecorder()
	h.cyclesOpenCode(rec, req)

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
	if got := cycles[0]["quotaName"]; got != "five_hour" {
		t.Fatalf("quotaName = %v, want five_hour", got)
	}
}

func TestCycleOverviewOpenCode_DefaultsToFiveHour(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	if _, err := s.CreateOpenCodeCycle("five_hour", now, &reset); err != nil {
		t.Fatalf("CreateOpenCodeCycle: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	req := httptest.NewRequest(http.MethodGet, "/api/opencode/cycle-overview", nil)
	rec := httptest.NewRecorder()
	h.cycleOverviewOpenCode(rec, req)

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
	if got := overview[0]["QuotaType"]; got != "five_hour" {
		t.Fatalf("QuotaType = %v, want five_hour", got)
	}
}

func TestBuildOpenCodeSummary_WithTracker(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	snap := &api.OpenCodeSnapshot{
		CapturedAt:  now,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 30, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
		},
	}
	if _, err := s.InsertOpenCodeSnapshot(snap); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tr := tracker.NewOpenCodeTracker(s, slog.Default())
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	h.SetOpenCodeTracker(tr)

	summary := h.buildOpenCodeSummaryMap()
	entry, ok := summary["five_hour"].(map[string]interface{})
	if !ok {
		t.Fatalf("summary missing five_hour: %#v", summary)
	}
	if got := entry["currentUtil"]; got != 30.0 {
		t.Fatalf("currentUtil = %v, want 30", got)
	}
}

func TestBuildOpenCodeCurrent_QuotaUsesLimitKey(t *testing.T) {
	// The dashboard JS reads `limit` (not `limitValue`). Lock that contract so a
	// future rename does not silently break the cards (regression: PR #102 frontend
	// showed "/ 0" because JS read a non-existent limitValue key).
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	insertTestOpenCodeSnapshot(t, s, now, []api.OpenCodeQuota{
		{Name: "five_hour", Utilization: 15, Used: 15, Limit: 100, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithOpenCode())
	current := h.buildOpenCodeCurrent()

	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatalf("quotas = %#v, want at least one quota", current["quotas"])
	}
	for _, raw := range quotas {
		q, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("quota entry is not a map: %#v", raw)
		}
		if _, hasLimit := q["limit"]; !hasLimit {
			t.Fatalf("quota map missing 'limit' key: %v", q)
		}
		if _, hasLimitValue := q["limitValue"]; hasLimitValue {
			t.Fatalf("quota map must not use 'limitValue' key: %v", q)
		}
	}
}
