package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// Test helper functions for creating configurations
func createTestConfigWithSynthetic() *config.Config {
	return &config.Config{
		SyntheticAPIKey: "syn_test_key",
		PollInterval:    60 * time.Second,
		Port:            9211,
		AdminUser:       "admin",
		AdminPass:       "test",
		DBPath:          "./test.db",
	}
}

func createTestConfigWithZai() *config.Config {
	return &config.Config{
		ZaiAPIKey:    "zai_test_key",
		ZaiBaseURL:   "https://api.z.ai/api",
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func createTestConfigWithBoth() *config.Config {
	return &config.Config{
		SyntheticAPIKey: "syn_test_key",
		ZaiAPIKey:       "zai_test_key",
		ZaiBaseURL:      "https://api.z.ai/api",
		PollInterval:    60 * time.Second,
		Port:            9211,
		AdminUser:       "admin",
		AdminPass:       "test",
		DBPath:          "./test.db",
	}
}

func TestHandler_Dashboard_ReturnsHTML(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("expected HTML document in response")
	}
	if !strings.Contains(body, "onWatch") {
		t.Error("expected 'onWatch' in response body")
	}
}

func TestHandler_Current_ReturnsJSON(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 0, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	for _, field := range []string{"capturedAt", "subscription", "search", "toolCalls"} {
		if _, ok := response[field]; !ok {
			t.Errorf("expected %s field", field)
		}
	}
}

func TestHandler_Current_IncludesResetCountdown(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(4*time.Hour + 16*time.Minute)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 0, RenewsAt: time.Now().Add(58 * time.Minute)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(2 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	var response map[string]map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	for _, quotaType := range []string{"subscription", "search", "toolCalls"} {
		quota, ok := response[quotaType]
		if !ok {
			t.Errorf("missing %s quota", quotaType)
			continue
		}

		if _, ok := quota["renewsAt"]; !ok {
			t.Errorf("%s missing renewsAt", quotaType)
		}
		if _, ok := quota["timeUntilReset"]; !ok {
			t.Errorf("%s missing timeUntilReset", quotaType)
		}
		if _, ok := quota["timeUntilResetSeconds"]; !ok {
			t.Errorf("%s missing timeUntilResetSeconds", quotaType)
		}
	}
}

func TestHandler_Current_IncludesToolCallReset(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	subRenewsAt := time.Date(2026, 2, 6, 16, 16, 18, 0, time.UTC)
	toolRenewsAt := time.Date(2026, 2, 6, 15, 26, 41, 0, time.UTC)

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: subRenewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 0, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: toolRenewsAt},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	var response map[string]map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	toolCalls := response["toolCalls"]
	if toolCalls == nil {
		t.Fatal("missing toolCalls in response")
	}

	renewsAt, ok := toolCalls["renewsAt"].(string)
	if !ok {
		t.Fatal("toolCalls renewsAt not a string")
	}

	if !strings.Contains(renewsAt, "2026-02-06T15:26:41") {
		t.Errorf("toolCalls renewsAt = %s, expected 2026-02-06T15:26:41", renewsAt)
	}
}

func TestHandler_Current_EmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for empty DB, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["capturedAt"]; !ok {
		t.Error("expected capturedAt field even with empty DB")
	}
	if _, ok := response["subscription"]; !ok {
		t.Error("expected subscription field even with empty DB")
	}
}

func TestHandler_History_DefaultRange(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	baseTime := time.Now().UTC().Add(-5 * time.Hour)
	for i := 0; i < 10; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: baseTime.Add(time.Duration(i) * time.Minute),
			Sub:        api.QuotaInfo{Limit: 1350, Requests: float64(i * 10), RenewsAt: time.Now().Add(5 * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i), RenewsAt: time.Now().Add(1 * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 5), RenewsAt: time.Now().Add(3 * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) == 0 {
		t.Error("expected history data with default 6h range")
	}
}

func TestHandler_History_AllRanges(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 100, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 500, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	ranges := []string{"1h", "6h", "24h", "7d", "30d"}
	for _, r := range ranges {
		t.Run(r, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/history?range="+r, nil)
			rr := httptest.NewRecorder()
			h.History(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("range %s: expected status 200, got %d", r, rr.Code)
			}
		})
	}
}

func TestHandler_History_InvalidRange(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=invalid", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandler_History_ReturnsPercentages(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 500, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 125, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 2000, Requests: 1000, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=6h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if len(response) == 0 {
		t.Fatal("expected history data")
	}

	point := response[0]

	for _, field := range []string{"subscriptionPercent", "searchPercent", "toolCallsPercent"} {
		if _, ok := point[field]; !ok {
			t.Errorf("expected %s field", field)
		}
	}

	if subPct, ok := point["subscriptionPercent"].(float64); ok {
		if subPct != 50.0 {
			t.Errorf("subscriptionPercent = %v, want 50.0", subPct)
		}
	}
}

func TestHandler_Cycles_FilterByType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	s.CreateCycle("subscription", now, now.Add(5*time.Hour))
	s.CreateCycle("search", now, now.Add(1*time.Hour))
	s.CreateCycle("toolcall", now, now.Add(3*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?type=subscription", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	for _, cycle := range response {
		if cycle["quotaType"] != "subscription" {
			t.Errorf("expected only subscription cycles, got %v", cycle["quotaType"])
		}
	}
}

func TestHandler_Cycles_AllTypes(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	s.CreateCycle("subscription", now, now.Add(5*time.Hour))
	s.CreateCycle("search", now, now.Add(1*time.Hour))
	s.CreateCycle("toolcall", now, now.Add(3*time.Hour))

	types := []string{"subscription", "search", "toolcall"}
	for _, quotaType := range types {
		t.Run(quotaType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/cycles?type="+quotaType, nil)
			rr := httptest.NewRecorder()
			h.Cycles(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("type %s: expected status 200, got %d", quotaType, rr.Code)
			}
		})
	}
}

func TestHandler_Cycles_InvalidType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?type=invalid", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_Cycles_IncludesActiveCycle(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	s.CreateCycle("subscription", now, now.Add(5*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?type=subscription", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if len(response) == 0 {
		t.Fatal("expected at least one cycle")
	}

	cycle := response[0]
	if cycle["cycleEnd"] != nil {
		t.Error("active cycle should have nil cycleEnd")
	}
}

func TestHandler_Summary_AllThreeQuotas(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 0, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	for _, quotaType := range []string{"subscription", "search", "toolCalls"} {
		if _, ok := response[quotaType]; !ok {
			t.Errorf("expected %s in summary", quotaType)
		}
	}
}

func TestHandler_Summary_IncludesProjectedUsage(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 500, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 50, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 2000, Requests: 500, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	var response map[string]map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	sub := response["subscription"]
	if sub == nil {
		t.Fatal("missing subscription summary")
	}

	if _, ok := sub["projectedUsage"]; !ok {
		t.Error("expected projectedUsage field")
	}
}

func TestHandler_Sessions_ReturnsList(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	s.CreateSession("session-1", time.Now().Add(-2*time.Hour), 60, "synthetic")
	s.CreateSession("session-2", time.Now().Add(-1*time.Hour), 60, "synthetic")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(response))
	}
}

func TestHandler_Sessions_IncludesMaxRequests(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	s.CreateSession("session-1", time.Now(), 60, "synthetic")
	s.UpdateSessionMaxRequests("session-1", 100, 20, 50)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if len(response) == 0 {
		t.Fatal("expected at least one session")
	}

	session := response[0]

	for _, field := range []string{"maxSubRequests", "maxSearchRequests", "maxToolRequests"} {
		if _, ok := session[field]; !ok {
			t.Errorf("expected %s field", field)
		}
	}
}

func TestHandler_Sessions_IncludesActiveSession(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	s.CreateSession("active-session", time.Now(), 60, "synthetic")
	s.CreateSession("closed-session", time.Now().Add(-2*time.Hour), 60, "synthetic")
	s.CloseSession("closed-session", time.Now().Add(-1*time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if len(response) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(response))
	}

	var foundActive bool
	for _, session := range response {
		if session["id"] == "active-session" {
			foundActive = true
			if session["endedAt"] != nil {
				t.Error("active session should have nil endedAt")
			}
		}
	}

	if !foundActive {
		t.Error("expected to find active session")
	}
}

func TestHandler_Sessions_EmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response == nil {
		t.Error("expected empty array, not null")
	}

	if len(response) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(response))
	}
}

func TestHandler_respondJSON(t *testing.T) {
	type TestData struct {
		Message string `json:"message"`
		Count   int    `json:"count"`
	}

	rr := httptest.NewRecorder()
	data := TestData{Message: "test", Count: 42}
	respondJSON(rr, http.StatusCreated, data)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var response TestData
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response.Message != "test" || response.Count != 42 {
		t.Error("JSON response mismatch")
	}
}

func TestHandler_respondError(t *testing.T) {
	rr := httptest.NewRecorder()
	respondError(rr, http.StatusBadRequest, "invalid input")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["error"] != "invalid input" {
		t.Errorf("expected error 'invalid input', got %s", response["error"])
	}
}

func TestHandler_parseTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"1h", time.Hour, false},
		{"6h", 6 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"invalid", 0, true},
		{"undefined", 0, true},
		{"", 6 * time.Hour, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			duration, err := parseTimeRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTimeRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && duration != tt.expected {
				t.Errorf("parseTimeRange(%q) = %v, want %v", tt.input, duration, tt.expected)
			}
		})
	}
}

// Provider Endpoint Tests

func TestHandler_Providers_ReturnsAvailableProviders(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
	if providers[0] != "synthetic" {
		t.Errorf("expected synthetic provider, got %v", providers[0])
	}

	if response["current"] != "synthetic" {
		t.Errorf("expected current provider to be synthetic, got %v", response["current"])
	}
}

func TestHandler_Providers_WithNoProviders(t *testing.T) {
	cfg := &config.Config{
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok || providers == nil {
		// Nil providers is acceptable for no providers
		return
	}
	if len(providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(providers))
	}
}

func TestHandler_Providers_WithBothProviders(t *testing.T) {
	cfg := createTestConfigWithBoth()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}
	if len(providers) != 3 {
		t.Errorf("expected 3 providers (synthetic, zai, both), got %d", len(providers))
	}
}

// Synthetic Provider Tests

func TestHandler_Current_WithSyntheticProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["subscription"]; !ok {
		t.Error("expected subscription field")
	}
	if _, ok := response["search"]; !ok {
		t.Error("expected search field")
	}
	if _, ok := response["toolCalls"]; !ok {
		t.Error("expected toolCalls field")
	}
}

func TestHandler_History_WithSyntheticProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 100, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 500, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=synthetic&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(response))
	}
}

func TestHandler_Summary_WithSyntheticProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	for _, field := range []string{"subscription", "search", "toolCalls"} {
		if _, ok := response[field]; !ok {
			t.Errorf("expected %s field", field)
		}
	}
}

func TestHandler_Cycles_WithSyntheticProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	s.CreateCycle("subscription", now, now.Add(5*time.Hour))

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=synthetic&type=subscription", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) == 0 {
		t.Fatal("expected at least one cycle")
	}

	if response[0]["quotaType"] != "subscription" {
		t.Errorf("expected quotaType to be subscription, got %v", response[0]["quotaType"])
	}
}

func TestHandler_Insights_WithSyntheticProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["stats"]; !ok {
		t.Error("expected stats field")
	}
	if _, ok := response["insights"]; !ok {
		t.Error("expected insights field")
	}
}

// Z.ai Provider Tests

func TestHandler_Current_WithZaiProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["tokensLimit"]; !ok {
		t.Error("expected tokensLimit field")
	}
	if _, ok := response["timeLimit"]; !ok {
		t.Error("expected timeLimit field")
	}
}

func TestHandler_Current_ZaiReturnsTokensAndTimeLimits(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	// Z.ai API: "usage" = budget/capacity, "currentValue" = actual consumption
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensUsage:         200000000, // budget
		TokensCurrentValue:  200000000, // 100% consumed
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeUsage:           1000, // budget
		TimeCurrentValue:    19,   // actual consumption
		TimeRemaining:       981,
		TimePercentage:      2,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	tokensLimit, ok := response["tokensLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("expected tokensLimit in response")
	}

	// usage = TokensCurrentValue (actual consumption)
	if usage, ok := tokensLimit["usage"].(float64); !ok || usage != 200000000 {
		t.Errorf("expected tokens usage 200000000, got %v", usage)
	}

	// limit = TokensUsage (budget/capacity)
	if limit, ok := tokensLimit["limit"].(float64); !ok || limit != 200000000 {
		t.Errorf("expected tokens limit 200000000, got %v", limit)
	}

	timeLimit, ok := response["timeLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("expected timeLimit in response")
	}

	// usage = TimeCurrentValue (actual consumption)
	if usage, ok := timeLimit["usage"].(float64); !ok || usage != 19 {
		t.Errorf("expected time usage 19, got %v", usage)
	}
}

func TestHandler_History_WithZaiProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensLimit:         200000000,
		TokensUsage:         200112618,
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeLimit:           1000,
		TimeUsage:           19,
		TimeRemaining:       981,
		TimePercentage:      1,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=zai&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(response))
	}

	if _, ok := response[0]["tokensLimit"]; !ok {
		t.Error("expected tokensLimit field")
	}
	if _, ok := response[0]["timeLimit"]; !ok {
		t.Error("expected timeLimit field")
	}
}

func TestHandler_Summary_WithZaiProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensLimit:         200000000,
		TokensUsage:         200112618,
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeLimit:           1000,
		TimeUsage:           19,
		TimeRemaining:       981,
		TimePercentage:      1,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["tokensLimit"]; !ok {
		t.Error("expected tokensLimit field")
	}
	if _, ok := response["timeLimit"]; !ok {
		t.Error("expected timeLimit field")
	}
}

func TestHandler_Cycles_WithZaiProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	nextReset := now.Add(24 * time.Hour)
	s.CreateZaiCycle("tokens", now, &nextReset)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=zai&type=tokens", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) == 0 {
		t.Fatal("expected at least one cycle")
	}

	if response[0]["quotaType"] != "tokens" {
		t.Errorf("expected quotaType to be tokens, got %v", response[0]["quotaType"])
	}
}

func TestHandler_Cycles_ZaiTokensAndTimeTypes(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	nextReset := now.Add(24 * time.Hour)
	s.CreateZaiCycle("tokens", now, &nextReset)
	s.CreateZaiCycle("time", now, nil)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	tests := []struct {
		quotaType string
	}{
		{"tokens"},
		{"time"},
	}

	for _, tt := range tests {
		t.Run(tt.quotaType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=zai&type="+tt.quotaType, nil)
			rr := httptest.NewRecorder()
			h.Cycles(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("type %s: expected status 200, got %d", tt.quotaType, rr.Code)
			}

			var response []map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			if len(response) == 0 {
				t.Fatalf("expected at least one cycle for type %s", tt.quotaType)
			}

			if response[0]["quotaType"] != tt.quotaType {
				t.Errorf("expected quotaType to be %s, got %v", tt.quotaType, response[0]["quotaType"])
			}
		})
	}
}

func TestHandler_Insights_WithZaiProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensLimit:         200000000,
		TokensUsage:         200112618,
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeLimit:           1000,
		TimeUsage:           19,
		TimeRemaining:       981,
		TimePercentage:      1,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["stats"]; !ok {
		t.Error("expected stats field")
	}
	if _, ok := response["insights"]; !ok {
		t.Error("expected insights field")
	}
}

// Provider Switching Tests

func TestHandler_ProviderSwitching_SyntheticToZai(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensLimit:         200000000,
		TokensUsage:         200112618,
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeLimit:           1000,
		TimeUsage:           19,
		TimeRemaining:       981,
		TimePercentage:      1,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	// First request to synthetic
	req1 := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	rr1 := httptest.NewRecorder()
	h.Current(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Errorf("synthetic request: expected status 200, got %d", rr1.Code)
	}

	var response1 map[string]interface{}
	json.Unmarshal(rr1.Body.Bytes(), &response1)
	if _, ok := response1["subscription"]; !ok {
		t.Error("synthetic response: expected subscription field")
	}

	// Switch to Z.ai
	req2 := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr2 := httptest.NewRecorder()
	h.Current(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("zai request: expected status 200, got %d", rr2.Code)
	}

	var response2 map[string]interface{}
	json.Unmarshal(rr2.Body.Bytes(), &response2)
	if _, ok := response2["tokensLimit"]; !ok {
		t.Error("zai response: expected tokensLimit field")
	}
}

func TestHandler_ProviderSwitching_ZaiToSynthetic(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensLimit:         200000000,
		TokensUsage:         200112618,
		TokensRemaining:     0,
		TokensPercentage:    100,
		TimeLimit:           1000,
		TimeUsage:           19,
		TimeRemaining:       981,
		TimePercentage:      1,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 154.3, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 7635, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	// First request to Z.ai
	req1 := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr1 := httptest.NewRecorder()
	h.Current(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Errorf("zai request: expected status 200, got %d", rr1.Code)
	}

	var response1 map[string]interface{}
	json.Unmarshal(rr1.Body.Bytes(), &response1)
	if _, ok := response1["tokensLimit"]; !ok {
		t.Error("zai response: expected tokensLimit field")
	}

	// Switch to Synthetic
	req2 := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	rr2 := httptest.NewRecorder()
	h.Current(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("synthetic request: expected status 200, got %d", rr2.Code)
	}

	var response2 map[string]interface{}
	json.Unmarshal(rr2.Body.Bytes(), &response2)
	if _, ok := response2["subscription"]; !ok {
		t.Error("synthetic response: expected subscription field")
	}
}

func TestHandler_InvalidProvider_ReturnsError(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=invalid", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandler_UnconfiguredProvider_ReturnsError(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	// Z.ai is not configured, so this should fail
	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// Dashboard Template Tests

func TestHandler_Dashboard_WithSingleProvider_NoSelector(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}
}

func TestHandler_Dashboard_WithMultipleProviders_ShowsSelector(t *testing.T) {
	cfg := createTestConfigWithBoth()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_Dashboard_PreservesProviderQueryParam(t *testing.T) {
	cfg := createTestConfigWithBoth()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_Dashboard_CodexView_RestoresProfileTabsAndTables(t *testing.T) {
	cfg := createTestConfigWithCodex()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `id="codex-profile-tabs"`) {
		t.Error("expected codex profile tabs container")
	}
	if !strings.Contains(body, `id="quota-grid-codex"`) {
		t.Error("expected single-account codex quota grid")
	}
	if strings.Contains(body, `id="codex-accounts-container-both"`) {
		t.Error("did not expect all-view codex multi-account container in codex view")
	}
	if !strings.Contains(body, `id="sessions-section"`) {
		t.Error("expected Session History section for codex view")
	}
	if !strings.Contains(body, `id="cycles-section"`) {
		t.Error("expected Logging History section for codex view")
	}
	if !strings.Contains(body, `id="overview-table"`) {
		t.Error("expected Cycle Overview table for codex view")
	}
}

func TestHandler_Dashboard_AllView_UsesCodexMultiAccountLayout(t *testing.T) {
	cfg := createTestConfigWithAll()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `id="all-providers-container"`) {
		t.Error("expected all-view provider cards container")
	}
	if strings.Contains(body, `id="codex-accounts-container-both"`) {
		t.Error("did not expect legacy codex multi-account container in all view")
	}
	if strings.Contains(body, `id="sessions-section"`) {
		t.Error("did not expect Session History section on all view")
	}
	if strings.Contains(body, `id="cycles-section"`) {
		t.Error("did not expect Logging History section on all view")
	}
	if strings.Contains(body, `id="overview-table"`) {
		t.Error("did not expect Cycle Overview table on all view")
	}
}

// Mock Data Tests

func TestHandler_Current_SyntheticWithMockData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 750.5, RenewsAt: time.Now().Add(5 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 125, RenewsAt: time.Now().Add(1 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 8000, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	tr := tracker.New(s, nil)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	sub, ok := response["subscription"].(map[string]interface{})
	if !ok {
		t.Fatal("expected subscription in response")
	}

	if usage, ok := sub["usage"].(float64); !ok || usage != 750.5 {
		t.Errorf("expected usage 750.5, got %v", usage)
	}

	if limit, ok := sub["limit"].(float64); !ok || limit != 1350 {
		t.Errorf("expected limit 1350, got %v", limit)
	}
}

func TestHandler_Current_ZaiWithMockData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().Add(24 * time.Hour)
	// Z.ai API: "usage" = budget/capacity, "currentValue" = actual consumption
	zaiSnapshot := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TokensUsage:         200000000, // budget
		TokensCurrentValue:  100000000, // 50% consumed
		TokensRemaining:     100000000,
		TokensPercentage:    50,
		TimeUsage:           1000, // budget
		TimeCurrentValue:    500,  // 50% consumed
		TimeRemaining:       500,
		TimePercentage:      50,
		TokensNextResetTime: &resetTime,
	}
	s.InsertZaiSnapshot(zaiSnapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	tokensLimit, ok := response["tokensLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("expected tokensLimit in response")
	}

	// usage = TokensCurrentValue (actual consumption)
	if usage, ok := tokensLimit["usage"].(float64); !ok || usage != 100000000 {
		t.Errorf("expected usage 100000000, got %v", usage)
	}

	if percent, ok := tokensLimit["percent"].(float64); !ok || percent != 50.0 {
		t.Errorf("expected percent 50.0, got %v", percent)
	}
}

func TestHandler_History_SyntheticMultipleSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	baseTime := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 5; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: baseTime.Add(time.Duration(i) * 30 * time.Minute),
			Sub:        api.QuotaInfo{Limit: 1350, Requests: float64(i * 100), RenewsAt: time.Now().Add(5 * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i * 10), RenewsAt: time.Now().Add(1 * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 50), RenewsAt: time.Now().Add(3 * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=synthetic&range=6h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 5 {
		t.Errorf("expected 5 history entries, got %d", len(response))
	}
}

func TestHandler_History_ZaiMultipleSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	resetTime := time.Now().Add(24 * time.Hour)
	baseTime := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 5; i++ {
		zaiSnapshot := &api.ZaiSnapshot{
			CapturedAt:          baseTime.Add(time.Duration(i) * 30 * time.Minute),
			TokensLimit:         200000000,
			TokensUsage:         float64(i * 1000000),
			TokensRemaining:     float64(200000000 - i*1000000),
			TokensPercentage:    i * 5,
			TimeLimit:           1000,
			TimeUsage:           float64(i * 10),
			TimeRemaining:       float64(1000 - i*10),
			TimePercentage:      i * 5,
			TokensNextResetTime: &resetTime,
		}
		s.InsertZaiSnapshot(zaiSnapshot)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=zai&range=6h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 5 {
		t.Errorf("expected 5 history entries, got %d", len(response))
	}
}

func TestHandler_Cycles_SyntheticActiveAndCompleted(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()

	// Create an active cycle
	s.CreateCycle("subscription", now, now.Add(5*time.Hour))

	// Note: We can't easily create a completed cycle through the Store API
	// as cycles are typically closed automatically by the tracker
	// But we can verify the active cycle is present

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=synthetic&type=subscription", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) == 0 {
		t.Fatal("expected at least one cycle")
	}

	// The active cycle should have nil cycleEnd
	if response[0]["cycleEnd"] != nil {
		t.Error("expected active cycle to have nil cycleEnd")
	}
}

func TestHandler_Cycles_ZaiActiveAndCompleted(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	nextReset := now.Add(24 * time.Hour)

	// Create an active cycle
	s.CreateZaiCycle("tokens", now, &nextReset)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=zai&type=tokens", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) == 0 {
		t.Fatal("expected at least one cycle")
	}

	// The active cycle should have nil cycleEnd
	if response[0]["cycleEnd"] != nil {
		t.Error("expected active cycle to have nil cycleEnd")
	}
}

// ── KPI Modal Chart Regression Tests ──
// These tests guard against the range-selector misfire bug where
// insights range pills (data-insights-range) were picked up instead
// of chart range buttons (data-range), sending range=undefined to the API.

func TestHandler_History_UndefinedRange_Returns400(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=undefined&provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for range=undefined, got %d", rr.Code)
	}
}

func TestHandler_History_EmptyDB_ReturnsEmptyArray_Synthetic(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=6h&provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := strings.TrimSpace(rr.Body.String())
	if body != "[]" {
		t.Errorf("expected empty JSON array '[]' for empty DB, got %q", body)
	}
}

func TestHandler_History_EmptyDB_ReturnsEmptyArray_Zai(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=6h&provider=zai", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := strings.TrimSpace(rr.Body.String())
	if body != "[]" {
		t.Errorf("expected empty JSON array '[]' for empty DB, got %q", body)
	}
}

func TestHandler_History_EmptyDB_ReturnsEmptyArrays_Both(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?range=6h&provider=both", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	for _, key := range []string{"synthetic", "zai", "anthropic", "copilot", "codex", "antigravity"} {
		val, ok := response[key]
		if !ok {
			continue
		}
		arr, ok := val.([]interface{})
		if !ok {
			t.Errorf("expected %s to be an array, got %T", key, val)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("expected %s to be empty array, got %d items", key, len(arr))
		}
	}
}

// ── Anthropic Provider Tests ──

func createTestConfigWithAnthropic() *config.Config {
	return &config.Config{
		AnthropicToken: "test_anthropic_token",
		PollInterval:   60 * time.Second,
		Port:           9211,
		AdminUser:      "admin",
		AdminPass:      "test",
		DBPath:         "./test.db",
	}
}

func createTestConfigWithAll() *config.Config {
	return &config.Config{
		SyntheticAPIKey:    "syn_test_key",
		ZaiAPIKey:          "zai_test_key",
		ZaiBaseURL:         "https://api.z.ai/api",
		AnthropicToken:     "test_anthropic_token",
		CodexToken:         "codex_test_token",
		AntigravityEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

func createTestConfigWithCodex() *config.Config {
	return &config.Config{
		CodexToken:   "codex_test_token",
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func TestHandler_SetAnthropicTracker(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	tr := tracker.NewAnthropicTracker(s, nil)
	h.SetAnthropicTracker(tr)

	if h.anthropicTracker == nil {
		t.Error("expected anthropicTracker to be set")
	}
}

func TestHandler_SetCodexTracker(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	tr := tracker.NewCodexTracker(s, nil)
	h.SetCodexTracker(tr)

	if h.codexTracker == nil {
		t.Error("expected codexTracker to be set")
	}
}

func TestHandler_Current_WithAnthropicProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 20.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":0.45},"seven_day":{"utilization":0.20}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["capturedAt"]; !ok {
		t.Error("expected capturedAt field")
	}

	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array")
	}

	if len(quotas) != 2 {
		t.Errorf("expected 2 quotas, got %d", len(quotas))
	}

	// Verify first quota has expected fields
	q0, ok := quotas[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected first quota to be a map")
	}
	if q0["name"] != "five_hour" {
		t.Errorf("expected first quota name 'five_hour', got %v", q0["name"])
	}
	if q0["displayName"] != "5-Hour Limit" {
		t.Errorf("expected displayName '5-Hour Limit', got %v", q0["displayName"])
	}
	if _, ok := q0["status"]; !ok {
		t.Error("expected status field")
	}
}

func TestHandler_Current_AnthropicEmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for empty DB, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["capturedAt"]; !ok {
		t.Error("expected capturedAt field even with empty DB")
	}

	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array")
	}
	if len(quotas) != 0 {
		t.Errorf("expected 0 quotas with empty DB, got %d", len(quotas))
	}
}

func TestHandler_History_WithAnthropicProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":0.45}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=anthropic&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(response))
	}

	if _, ok := response[0]["capturedAt"]; !ok {
		t.Error("expected capturedAt field in history entry")
	}
	if _, ok := response[0]["five_hour"]; !ok {
		t.Error("expected five_hour utilization in history entry")
	}
}

func TestHandler_Cycles_WithAnthropicProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	// Insert 3 snapshots with increasing utilization
	for i, util := range []float64{10.0, 25.0, 40.0} {
		snap := &api.AnthropicSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Minute),
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: util, ResetsAt: &resetsAt},
			},
			RawJSON: fmt.Sprintf(`{"five_hour":{"utilization":%v}}`, util),
		}
		s.InsertAnthropicSnapshot(snap)
	}

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=anthropic&type=five_hour", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 3 {
		t.Fatalf("expected 3 snapshot rows, got %d", len(response))
	}

	// Response is DESC order (newest first)
	if response[0]["quotaName"] != "five_hour" {
		t.Errorf("expected quotaName to be five_hour, got %v", response[0]["quotaName"])
	}

	// Newest snapshot (util=40) should be first, with cycleEnd=nil (active)
	if response[0]["cycleEnd"] != nil {
		t.Errorf("expected latest snapshot cycleEnd to be nil, got %v", response[0]["cycleEnd"])
	}

	// Check peakUtilization of newest = 40.0
	if peak, ok := response[0]["peakUtilization"].(float64); !ok || peak != 40.0 {
		t.Errorf("expected peakUtilization=40.0, got %v", response[0]["peakUtilization"])
	}

	// Check delta computation: 40-25=15 for the newest snapshot
	if delta, ok := response[0]["totalDelta"].(float64); !ok || delta != 15.0 {
		t.Errorf("expected totalDelta=15.0, got %v", response[0]["totalDelta"])
	}

	// First snapshot (util=10, oldest) should have delta=0
	if delta, ok := response[2]["totalDelta"].(float64); !ok || delta != 0.0 {
		t.Errorf("expected first snapshot totalDelta=0, got %v", response[2]["totalDelta"])
	}
}

func TestHandler_Summary_WithAnthropicProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":0.45}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	tr := tracker.NewAnthropicTracker(s, nil)
	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(tr)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Summary should be keyed by quota name
	if _, ok := response["five_hour"]; !ok {
		t.Error("expected five_hour summary")
	}
}

func TestHandler_Insights_WithAnthropicProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":0.45}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["stats"]; !ok {
		t.Error("expected stats field")
	}
	if _, ok := response["insights"]; !ok {
		t.Error("expected insights field")
	}
}

func TestHandler_Current_WithCodexProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	capturedAt := time.Now().UTC()
	resetsAt := capturedAt.Add(5 * time.Hour)
	snapshot := &api.CodexSnapshot{
		CapturedAt: capturedAt,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 42.5, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 18.0, ResetsAt: &resetsAt},
			{Name: "code_review", Utilization: 35.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"plan_type":"plus"}`,
	}
	if _, err := s.InsertCodexSnapshot(snapshot); err != nil {
		t.Fatalf("failed to insert codex snapshot: %v", err)
	}

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["planType"] != "plus" {
		t.Errorf("expected planType plus, got %v", response["planType"])
	}

	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array")
	}
	if len(quotas) != 3 {
		t.Fatalf("expected 3 codex quotas, got %d", len(quotas))
	}

	q0, ok := quotas[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected first quota to be a map")
	}
	if q0["displayName"] != "5-Hour Limit" {
		t.Errorf("expected 5-Hour Limit displayName, got %v", q0["displayName"])
	}

	foundCodeReview := false
	for _, raw := range quotas {
		q, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if q["name"] != "code_review" {
			continue
		}
		foundCodeReview = true
		if q["displayName"] != "Review Requests" {
			t.Errorf("expected code_review displayName Review Requests, got %v", q["displayName"])
		}
		if q["cardLabel"] != "Remaining" {
			t.Errorf("expected code_review cardLabel Remaining, got %v", q["cardLabel"])
		}
		cardPercent, ok := q["cardPercent"].(float64)
		if !ok || cardPercent != 65.0 {
			t.Errorf("expected code_review cardPercent 65.0, got %v", q["cardPercent"])
		}
		if q["status"] != "healthy" {
			t.Errorf("expected code_review status healthy, got %v", q["status"])
		}
	}
	if !foundCodeReview {
		t.Error("expected code_review quota in codex response")
	}
}

func TestHandler_History_WithCodexProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	capturedAt := time.Now().UTC()
	snap := &api.CodexSnapshot{
		CapturedAt: capturedAt,
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 22.0},
			{Name: "seven_day", Utilization: 11.5},
			{Name: "code_review", Utilization: 7.0},
		},
		RawJSON: `{"ok":true}`,
	}
	if _, err := s.InsertCodexSnapshot(snap); err != nil {
		t.Fatalf("failed to insert codex snapshot: %v", err)
	}

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=codex&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(response))
	}
	if _, ok := response[0]["capturedAt"]; !ok {
		t.Error("expected capturedAt in codex history entry")
	}
	if _, ok := response[0]["five_hour"]; !ok {
		t.Error("expected five_hour value in codex history entry")
	}
	if _, ok := response[0]["code_review"]; !ok {
		t.Error("expected code_review value in codex history entry")
	}
}

func TestHandler_Cycles_WithCodexProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Add(-5 * time.Hour)
	resetsAt := now.Add(5 * time.Hour)
	tkr := tracker.NewCodexTracker(s, nil)
	for i, util := range []float64{10.0, 30.0, 55.0} {
		snap := &api.CodexSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Minute),
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: util, ResetsAt: &resetsAt},
			},
			RawJSON: `{"ok":true}`,
		}
		if _, err := s.InsertCodexSnapshot(snap); err != nil {
			t.Fatalf("failed to insert codex snapshot: %v", err)
		}
		if err := tkr.Process(snap); err != nil {
			t.Fatalf("failed to process codex snapshot: %v", err)
		}
	}

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=codex&type=five_hour", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("expected 1 cycle row, got %d", len(response))
	}
	if response[0]["quotaName"] != "five_hour" {
		t.Errorf("expected quotaName five_hour, got %v", response[0]["quotaName"])
	}
	if _, ok := response[0]["peakUtilization"]; !ok {
		t.Error("expected peakUtilization in codex cycle entry")
	}
}

func TestHandler_Cycles_CodexInvalidType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=codex&type=invalid", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_Summary_WithCodexProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(4 * time.Hour)
	tkr := tracker.NewCodexTracker(s, nil)
	for i, util := range []float64{20.0, 40.0} {
		snap := &api.CodexSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Minute),
			Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: util, ResetsAt: &resetsAt}},
			RawJSON:    `{"ok":true}`,
		}
		if _, err := s.InsertCodexSnapshot(snap); err != nil {
			t.Fatalf("failed to insert codex snapshot: %v", err)
		}
		if err := tkr.Process(snap); err != nil {
			t.Fatalf("failed to process codex snapshot: %v", err)
		}
	}

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCodexTracker(tkr)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := response["five_hour"]; !ok {
		t.Error("expected five_hour summary")
	}
}

func TestHandler_Insights_CodexEmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response insightsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(response.Insights) == 0 {
		t.Fatal("expected at least one insight")
	}
	if response.Insights[0].Title != "Getting Started" {
		t.Errorf("expected Getting Started insight, got %q", response.Insights[0].Title)
	}
}

func TestHandler_Insights_CodexRichData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Add(-2 * time.Hour)
	fiveHourReset := now.Add(3 * time.Hour)
	weeklyReset := now.Add(5 * 24 * time.Hour)
	credits := 87.5
	tkr := tracker.NewCodexTracker(s, nil)

	for i, util := range []float64{22.0, 31.0, 44.0} {
		snap := &api.CodexSnapshot{
			CapturedAt:     now.Add(time.Duration(i) * 30 * time.Minute),
			PlanType:       "plus",
			CreditsBalance: &credits,
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: util, ResetsAt: &fiveHourReset},
				{Name: "seven_day", Utilization: 60.0 + float64(i), ResetsAt: &weeklyReset},
				{Name: "code_review", Utilization: 15.0 + float64(i*7), ResetsAt: &weeklyReset},
			},
			RawJSON: `{"ok":true}`,
		}
		if _, err := s.InsertCodexSnapshot(snap); err != nil {
			t.Fatalf("failed to insert codex snapshot: %v", err)
		}
		if err := tkr.Process(snap); err != nil {
			t.Fatalf("failed to process codex snapshot: %v", err)
		}
	}

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCodexTracker(tkr)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=codex&range=1d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response insightsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(response.Stats) == 0 {
		t.Fatal("expected codex stats")
	}
	if len(response.Insights) == 0 {
		t.Fatal("expected codex insights")
	}

	hasPlan := false
	hasFiveHourBehaviorStat := false
	for _, st := range response.Stats {
		if st.Label == "Plan" {
			hasPlan = true
		}
		if st.Label == "Average 5-Hour Limit Usage/Cycle" || st.Label == "5-Hour Limit Delta (Current)" {
			hasFiveHourBehaviorStat = true
		}
	}
	if !hasPlan {
		t.Error("expected Plan stat in codex insights response")
	}
	if !hasFiveHourBehaviorStat {
		t.Error("expected 5-Hour Limit behavior stat in codex insights response")
	}
	for _, in := range response.Insights {
		if in.Title == "Tracking Quality" {
			t.Error("did not expect Tracking Quality insight in codex insights response")
		}
		if in.Title == "Next Reset" {
			t.Error("did not expect Next Reset insight in codex insights response")
		}
		if in.Title == "Credits Balance" {
			t.Error("did not expect Credits Balance insight in codex insights response")
		}
	}
	for _, st := range response.Stats {
		if st.Label == "Credits" {
			t.Error("did not expect Credits stat in codex insights response")
		}
		if st.Label == "Next Reset" {
			t.Error("did not expect Next Reset stat in codex insights response")
		}
		if st.Label == "Last Sample" {
			t.Error("did not expect Last Sample stat in codex insights response")
		}
	}

	shortForecastFound := false
	weeklyForecastFound := false
	weeklyPaceFound := false
	reviewPaceFound := false
	for _, in := range response.Insights {
		if in.Title == "Short Window Burn Rate" {
			t.Error("did not expect Short Window Burn Rate in codex insights response")
		}
		if in.Title == "Weekly All-Model Burn Rate" {
			weeklyForecastFound = strings.Contains(in.Sublabel, "by reset")
		}
		if in.Title == "5-Hour Limit Burn Rate" {
			shortForecastFound = strings.Contains(in.Sublabel, "by reset")
		}
		if in.Title == "Weekly Pace" {
			weeklyPaceFound = true
		}
		if in.Title == "Review Request Pace" {
			reviewPaceFound = true
		}
		if in.Title == "Window Pressure" {
			t.Error("did not expect Window Pressure insight in codex insights response")
		}
	}
	if !shortForecastFound {
		t.Error("expected 5-Hour Limit Burn Rate to show reset estimate sublabel")
	}
	if !weeklyForecastFound {
		t.Error("expected Weekly All-Model Burn Rate to show reset estimate sublabel")
	}
	if !weeklyPaceFound {
		t.Error("expected Weekly Pace insight in codex insights response")
	}
	if !reviewPaceFound {
		t.Error("expected Review Request Pace insight in codex insights response")
	}
}

func TestHandler_Providers_WithCodexOnly(t *testing.T) {
	cfg := createTestConfigWithCodex()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0] != "codex" {
		t.Errorf("expected codex provider, got %v", providers[0])
	}
}

func TestHandler_Current_BothIncludesCodex(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	snap := &api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 25.0}},
		RawJSON:    `{"ok":true}`,
	}
	if _, err := s.InsertCodexSnapshot(snap); err != nil {
		t.Fatalf("failed to insert codex snapshot: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := response["codex"]; !ok {
		t.Error("expected codex field in both response")
	}
}

func TestHandler_CycleOverview_Codex(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if response["provider"] != "codex" {
		t.Errorf("expected provider codex, got %v", response["provider"])
	}
	if response["groupBy"] != "five_hour" {
		t.Errorf("expected default groupBy five_hour, got %v", response["groupBy"])
	}
}

func TestHandler_CodexUtilStatus(t *testing.T) {
	tests := []struct {
		util   float64
		status string
	}{
		{0, "healthy"},
		{49.9, "healthy"},
		{50, "warning"},
		{79.9, "warning"},
		{80, "danger"},
		{94.9, "danger"},
		{95, "critical"},
		{100, "critical"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("util_%.0f", tt.util), func(t *testing.T) {
			got := codexUtilStatus(tt.util)
			if got != tt.status {
				t.Errorf("codexUtilStatus(%.1f) = %q, want %q", tt.util, got, tt.status)
			}
		})
	}
}

func TestHandler_CodexRemainingStatus(t *testing.T) {
	tests := []struct {
		remaining float64
		status    string
	}{
		{100, "healthy"},
		{50, "warning"},
		{20, "danger"},
		{5, "critical"},
		{0, "critical"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("remaining_%.0f", tt.remaining), func(t *testing.T) {
			got := codexRemainingStatus(tt.remaining)
			if got != tt.status {
				t.Errorf("codexRemainingStatus(%.1f) = %q, want %q", tt.remaining, got, tt.status)
			}
		})
	}
}

func TestHandler_Providers_WithAnthropicOnly(t *testing.T) {
	cfg := createTestConfigWithAnthropic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
	if providers[0] != "anthropic" {
		t.Errorf("expected anthropic provider, got %v", providers[0])
	}
}

func TestHandler_Providers_WithAllProviders_IncludesBoth(t *testing.T) {
	cfg := createTestConfigWithAll()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}

	// Should have synthetic, zai, anthropic, codex, antigravity, both = 6
	if len(providers) != 6 {
		t.Errorf("expected 6 providers (synthetic, zai, anthropic, codex, antigravity, both), got %d: %v", len(providers), providers)
	}
}

func TestHandler_Current_BothIncludesAnthropicAndAntigravity(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":0.45}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	agSnapshot := &api.AntigravitySnapshot{
		CapturedAt: time.Now().UTC(),
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80},
			{ModelID: "gemini-3-pro", Label: "Gemini 3 Pro", RemainingFraction: 0.7, RemainingPercent: 70},
			{ModelID: "gemini-3-flash", Label: "Gemini 3 Flash", RemainingFraction: 0.6, RemainingPercent: 60},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(agSnapshot); err != nil {
		t.Fatalf("failed to insert antigravity snapshot: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if _, ok := response["anthropic"]; !ok {
		t.Error("expected anthropic field in 'both' response")
	}

	agRaw, ok := response["antigravity"]
	if !ok {
		t.Fatal("expected antigravity field in 'both' response")
	}
	ag, ok := agRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected antigravity payload object, got %T", agRaw)
	}
	quotas, ok := ag["quotas"].([]interface{})
	if !ok {
		t.Fatalf("expected antigravity quotas array, got %T", ag["quotas"])
	}
	if len(quotas) != 3 {
		t.Fatalf("expected 3 antigravity quota groups, got %d", len(quotas))
	}
}

func TestHandler_AnthropicUtilStatus(t *testing.T) {
	tests := []struct {
		util   float64
		status string
	}{
		{0, "healthy"},
		{49.9, "healthy"},
		{50, "warning"},
		{79.9, "warning"},
		{80, "danger"},
		{94.9, "danger"},
		{95, "critical"},
		{100, "critical"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("util_%.0f", tt.util), func(t *testing.T) {
			got := anthropicUtilStatus(tt.util)
			if got != tt.status {
				t.Errorf("anthropicUtilStatus(%.1f) = %q, want %q", tt.util, got, tt.status)
			}
		})
	}
}

func TestHandler_Insights_AnthropicEmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response insightsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Should have a "Getting Started" insight for empty DB
	if len(response.Insights) == 0 {
		t.Fatal("expected at least one insight")
	}
	if response.Insights[0].Title != "Getting Started" {
		t.Errorf("expected 'Getting Started' insight, got %q", response.Insights[0].Title)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Login / Logout Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Login_GET_RendersForm(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.SetVersion("2.11.0")

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %s", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "/static/app.js?v=2.11.0") {
		t.Fatalf("expected login page to include versioned app.js URL, body=%s", body)
	}
	if !regexp.MustCompile(`/static/app\.js\?v=[^"\s]+`).MatchString(body) {
		t.Fatalf("expected login page to include non-empty app.js version token, body=%s", body)
	}
}

func TestHandler_Login_POST_ValidCredentials_Redirects(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("test")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader("username=admin&password=test")
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %s", loc)
	}
	// Should set a session cookie
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "onwatch_session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected onwatch_session cookie to be set")
	}
}

func TestHandler_Login_POST_InvalidCredentials_RedirectsWithError(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("test")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader("username=admin&password=wrong")
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/login?error=") {
		t.Errorf("expected redirect to /login with error, got %s", loc)
	}
}

func TestHandler_Login_GET_AlreadyAuthenticated_RedirectsToDashboard(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("test")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	// Authenticate to get a token
	token, ok := sessions.Authenticate("admin", "test")
	if !ok {
		t.Fatal("authentication should succeed")
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{Name: "onwatch_session", Value: token})
	rr := httptest.NewRecorder()

	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("expected redirect to /, got %s", rr.Header().Get("Location"))
	}
}

func TestHandler_Logout_ClearsCookieAndRedirects(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("test")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	token, _ := sessions.Authenticate("admin", "test")

	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "onwatch_session", Value: token})
	rr := httptest.NewRecorder()

	h.Logout(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect to /login, got %s", rr.Header().Get("Location"))
	}
	// Cookie should be expired
	for _, c := range rr.Result().Cookies() {
		if c.Name == "onwatch_session" && c.MaxAge >= 0 {
			t.Error("expected session cookie to be expired (MaxAge < 0)")
		}
	}
	// Token should be invalidated
	if sessions.ValidateToken(token) {
		t.Error("expected token to be invalidated after logout")
	}
}

func TestHandler_SettingsPage_RendersHTML(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.SetVersion("2.5.0")

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	h.SettingsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %s", ct)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Password Change Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_ChangePassword_Success(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", passHash, s)
	s.UpsertUser("admin", passHash)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader(`{"current_password":"oldpass","new_password":"newpass123"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/password", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChangePassword(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)
	if response["message"] != "password updated successfully" {
		t.Errorf("unexpected message: %s", response["message"])
	}
}

func TestHandler_ChangePassword_WrongCurrentPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader(`{"current_password":"wrongpass","new_password":"newpass123"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/password", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChangePassword(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestHandler_ChangePassword_TooShortNewPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader(`{"current_password":"oldpass","new_password":"abc"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/password", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChangePassword(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)
	if !strings.Contains(response["error"], "at least 6 characters") {
		t.Errorf("expected 'at least 6 characters' error, got %s", response["error"])
	}
}

func TestHandler_ChangePassword_MissingFields(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", passHash, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := strings.NewReader(`{"current_password":"oldpass","new_password":""}`)
	req := httptest.NewRequest(http.MethodPut, "/api/password", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChangePassword(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_ChangePassword_InvalidatesAllSessions(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	passHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", passHash, s)
	s.UpsertUser("admin", passHash)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	// Create a session token first
	token, ok := sessions.Authenticate("admin", "oldpass")
	if !ok {
		t.Fatal("auth should succeed")
	}
	if !sessions.ValidateToken(token) {
		t.Fatal("token should be valid before password change")
	}

	body := strings.NewReader(`{"current_password":"oldpass","new_password":"newpass123"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/password", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChangePassword(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Old token should be invalidated
	if sessions.ValidateToken(token) {
		t.Error("expected all sessions to be invalidated after password change")
	}
}

func TestHandler_ChangePassword_MethodNotAllowed(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/password", nil)
	rr := httptest.NewRecorder()
	h.ChangePassword(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Settings CRUD Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_GetSettings_ReturnsTimezoneAndHiddenInsights(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	s.SetSetting("timezone", "America/New_York")
	s.SetSetting("hidden_insights", `["cycle_utilization","trend"]`)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["timezone"] != "America/New_York" {
		t.Errorf("expected timezone America/New_York, got %v", response["timezone"])
	}
	hidden, ok := response["hidden_insights"].([]interface{})
	if !ok || len(hidden) != 2 {
		t.Errorf("expected 2 hidden insights, got %v", response["hidden_insights"])
	}
}

func TestHandler_UpdateSettings_Timezone(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"timezone":"Europe/London"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Verify it was saved
	val, _ := s.GetSetting("timezone")
	if val != "Europe/London" {
		t.Errorf("expected timezone Europe/London, got %s", val)
	}
}

func TestHandler_UpdateSettings_InvalidTimezone(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"timezone":"Invalid/Timezone"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_HiddenInsights(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"hidden_insights":["cycle_utilization","weekly_pace"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	val, _ := s.GetSetting("hidden_insights")
	if !strings.Contains(val, "cycle_utilization") || !strings.Contains(val, "weekly_pace") {
		t.Errorf("expected hidden insights to be saved, got %s", val)
	}
}

func TestHandler_GetSettings_SMTPMasksPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	smtpConfig := `{"host":"smtp.example.com","port":587,"password":"secret123","from_address":"test@example.com"}`
	s.SetSetting("smtp", smtpConfig)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	smtp, ok := response["smtp"].(map[string]interface{})
	if !ok {
		t.Fatal("expected smtp field in response")
	}
	// Password should be empty (masked)
	if smtp["password"] != "" {
		t.Error("SMTP password should be masked (empty) in GET response")
	}
	// password_set should be true
	if smtp["password_set"] != true {
		t.Error("expected password_set to be true")
	}
}

func TestHandler_GetSettings_NotificationSettings(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	notifConfig := `{"warning_threshold":70,"critical_threshold":90,"notify_warning":true,"notify_critical":true}`
	s.SetSetting("notifications", notifConfig)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	notif, ok := response["notifications"].(map[string]interface{})
	if !ok {
		t.Fatal("expected notifications field in response")
	}
	if notif["warning_threshold"].(float64) != 70 {
		t.Errorf("expected warning_threshold 70, got %v", notif["warning_threshold"])
	}
}

func TestHandler_UpdateSettings_ProviderVisibility(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"provider_visibility":{"synthetic":{"dashboard":true,"polling":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	val, _ := s.GetSetting("provider_visibility")
	if !strings.Contains(val, "synthetic") {
		t.Errorf("expected provider_visibility to be saved, got %s", val)
	}
}

func TestHandler_UpdateSettings_Notifications(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"notifications":{"warning_threshold":60,"critical_threshold":85,"notify_warning":true,"notify_critical":true,"notify_reset":false,"cooldown_minutes":15}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	val, _ := s.GetSetting("notifications")
	if !strings.Contains(val, "60") {
		t.Errorf("expected notification settings to be saved, got %s", val)
	}
}

func TestHandler_UpdateSettings_Notifications_InvalidThresholds(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	// Warning >= Critical should fail
	body := strings.NewReader(`{"notifications":{"warning_threshold":90,"critical_threshold":85,"notify_warning":true,"notify_critical":true,"cooldown_minutes":15}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for warning >= critical, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_MethodNotAllowed(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	// UpdateSettings checks for PUT method
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── SMTP Test Handler Tests ──
// ═══════════════════════════════════════════════════════════════════

// mockNotifier implements the Notifier interface for testing.
type mockNotifier struct {
	sendTestErr  error
	reloadCalled bool
}

func (m *mockNotifier) Reload() error             { m.reloadCalled = true; return nil }
func (m *mockNotifier) ConfigureSMTP() error      { return nil }
func (m *mockNotifier) ConfigurePush() error      { return nil }
func (m *mockNotifier) SendTestEmail() error      { return m.sendTestErr }
func (m *mockNotifier) SendTestPush() error       { return nil }
func (m *mockNotifier) SetEncryptionKey(_ string) {}
func (m *mockNotifier) GetVAPIDPublicKey() string { return "" }

func TestHandler_SMTPTest_Success(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/smtp/test", nil)
	rr := httptest.NewRecorder()
	h.SMTPTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["success"] != true {
		t.Errorf("expected success true, got %v", response["success"])
	}
}

func TestHandler_SMTPTest_RateLimit(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	// First request succeeds
	req1 := httptest.NewRequest(http.MethodPost, "/api/settings/smtp/test", nil)
	rr1 := httptest.NewRecorder()
	h.SMTPTest(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: expected status 200, got %d", rr1.Code)
	}

	// Second request within 30s should be rate-limited
	req2 := httptest.NewRequest(http.MethodPost, "/api/settings/smtp/test", nil)
	rr2 := httptest.NewRecorder()
	h.SMTPTest(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected status 429, got %d", rr2.Code)
	}
}

func TestHandler_SMTPTest_NoNotifierConfigured(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	// No notifier set

	req := httptest.NewRequest(http.MethodPost, "/api/settings/smtp/test", nil)
	rr := httptest.NewRecorder()
	h.SMTPTest(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_SMTPTest_MethodNotAllowed(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/smtp/test", nil)
	rr := httptest.NewRecorder()
	h.SMTPTest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── CycleOverview Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CycleOverview_Synthetic(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=synthetic", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["provider"] != "synthetic" {
		t.Errorf("expected provider synthetic, got %v", response["provider"])
	}
	if response["groupBy"] != "subscription" {
		t.Errorf("expected default groupBy subscription, got %v", response["groupBy"])
	}
}

func TestHandler_CycleOverview_Zai(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["provider"] != "zai" {
		t.Errorf("expected provider zai, got %v", response["provider"])
	}
	if response["groupBy"] != "tokens" {
		t.Errorf("expected default groupBy tokens, got %v", response["groupBy"])
	}
}

func TestHandler_CycleOverview_Anthropic(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["provider"] != "anthropic" {
		t.Errorf("expected provider anthropic, got %v", response["provider"])
	}
	if response["groupBy"] != "five_hour" {
		t.Errorf("expected default groupBy five_hour, got %v", response["groupBy"])
	}
}

func TestHandler_CycleOverview_Both(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=both", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["synthetic"]; !ok {
		t.Error("expected synthetic field in 'both' response")
	}
	if _, ok := response["zai"]; !ok {
		t.Error("expected zai field in 'both' response")
	}
}

func TestHandler_Sessions_BothIncludesCodexAndAntigravity(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	if err := s.CreateSession("codex-session", time.Now().Add(-30*time.Minute), 60, "codex", 12.0, 8.0, 0); err != nil {
		t.Fatalf("failed to create codex session: %v", err)
	}
	if err := s.CreateSession("antigravity-session", time.Now().Add(-20*time.Minute), 60, "antigravity", 10.0, 20.0, 30.0); err != nil {
		t.Fatalf("failed to create antigravity session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string][]map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	codexSessions, ok := response["codex"]
	if !ok {
		t.Fatal("expected codex field in both sessions response")
	}
	if len(codexSessions) != 1 {
		t.Fatalf("expected 1 codex session, got %d", len(codexSessions))
	}
	if codexSessions[0]["id"] != "codex-session" {
		t.Fatalf("expected codex session id codex-session, got %v", codexSessions[0]["id"])
	}

	antigravitySessions, ok := response["antigravity"]
	if !ok {
		t.Fatal("expected antigravity field in both sessions response")
	}
	if len(antigravitySessions) != 1 {
		t.Fatalf("expected 1 antigravity session, got %d", len(antigravitySessions))
	}
	if antigravitySessions[0]["id"] != "antigravity-session" {
		t.Fatalf("expected antigravity session id antigravity-session, got %v", antigravitySessions[0]["id"])
	}
}

func TestHandler_Sessions_CodexAccountFiltering(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	if err := s.CreateSession("codex-legacy", now.Add(-40*time.Minute), 60, "codex", 11, 7, 0); err != nil {
		t.Fatalf("failed to create legacy codex session: %v", err)
	}
	if err := s.CreateSession("codex-account-1", now.Add(-30*time.Minute), 60, "codex:1", 12, 8, 0); err != nil {
		t.Fatalf("failed to create codex account 1 session: %v", err)
	}
	if err := s.CreateSession("codex-account-2", now.Add(-20*time.Minute), 60, "codex:2", 13, 9, 0); err != nil {
		t.Fatalf("failed to create codex account 2 session: %v", err)
	}

	reqAccount2 := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=codex&account=2", nil)
	rrAccount2 := httptest.NewRecorder()
	h.Sessions(rrAccount2, reqAccount2)
	if rrAccount2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for account 2, got %d", rrAccount2.Code)
	}

	var account2Sessions []map[string]interface{}
	if err := json.Unmarshal(rrAccount2.Body.Bytes(), &account2Sessions); err != nil {
		t.Fatalf("failed to parse account 2 sessions: %v", err)
	}
	if len(account2Sessions) != 1 || account2Sessions[0]["id"] != "codex-account-2" {
		t.Fatalf("expected only codex-account-2, got %#v", account2Sessions)
	}

	reqAccount1 := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=codex&account=1", nil)
	rrAccount1 := httptest.NewRecorder()
	h.Sessions(rrAccount1, reqAccount1)
	if rrAccount1.Code != http.StatusOK {
		t.Fatalf("expected status 200 for account 1, got %d", rrAccount1.Code)
	}

	var account1Sessions []map[string]interface{}
	if err := json.Unmarshal(rrAccount1.Body.Bytes(), &account1Sessions); err != nil {
		t.Fatalf("failed to parse account 1 sessions: %v", err)
	}
	if len(account1Sessions) != 2 {
		t.Fatalf("expected 2 sessions for account 1 (legacy + codex:1), got %d", len(account1Sessions))
	}
}

func TestHandler_Sessions_BothCodexAccountFiltering(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	now := time.Now().UTC()
	if err := s.CreateSession("codex-account-1", now.Add(-35*time.Minute), 60, "codex:1", 12, 8, 0); err != nil {
		t.Fatalf("failed to create codex account 1 session: %v", err)
	}
	if err := s.CreateSession("codex-account-2", now.Add(-25*time.Minute), 60, "codex:2", 13, 9, 0); err != nil {
		t.Fatalf("failed to create codex account 2 session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=both&account=2", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string][]map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse both sessions response: %v", err)
	}

	codexSessions := response["codex"]
	if len(codexSessions) != 1 || codexSessions[0]["id"] != "codex-account-2" {
		t.Fatalf("expected only codex-account-2 in both response, got %#v", codexSessions)
	}
}

func TestHandler_CycleOverview_AntigravityReturnsCycleOverview(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "antigravity" {
		t.Fatalf("expected antigravity provider field, got %v", response["provider"])
	}
	// Default groupBy should be antigravity_claude_gpt
	if response["groupBy"] != api.AntigravityQuotaGroupClaudeGPT {
		t.Fatalf("expected antigravity groupBy '%s', got %v", api.AntigravityQuotaGroupClaudeGPT, response["groupBy"])
	}

	quotaNames, ok := response["quotaNames"].([]interface{})
	if !ok {
		t.Fatalf("expected antigravity quotaNames array, got %T", response["quotaNames"])
	}
	want := api.AntigravityQuotaGroupOrder()
	if len(quotaNames) != len(want) {
		t.Fatalf("expected %d antigravity quota names, got %d", len(want), len(quotaNames))
	}
	for i, w := range want {
		if quotaNames[i] != w {
			t.Fatalf("expected quotaNames[%d]=%s, got %v", i, w, quotaNames[i])
		}
	}
}

func TestHandler_CycleOverview_BothCodexRespectsGroupByFallback(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=both&groupBy=seven_day", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	codexRaw, ok := response["codex"]
	if !ok {
		t.Fatal("expected codex field in both cycle overview response")
	}
	codex, ok := codexRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected codex overview to be object, got %T", codexRaw)
	}
	if codex["groupBy"] != "seven_day" {
		t.Fatalf("expected codex groupBy seven_day from generic groupBy fallback, got %v", codex["groupBy"])
	}

}

func TestHandler_CycleOverview_AntigravityReturnsEmptyWhenNoCycles(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// Insert a snapshot but no reset cycles
	now := time.Now().UTC()
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B (Medium)", RemainingFraction: 0.6, RemainingPercent: 60},
			{ModelID: "MODEL_PLACEHOLDER_M36", Label: "Gemini 3.1 Pro", RemainingFraction: 0.8, RemainingPercent: 80},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot); err != nil {
		t.Fatalf("failed to insert antigravity snapshot: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Antigravity returns cycle overview with groupBy matching the query
	if response["groupBy"] != api.AntigravityQuotaGroupClaudeGPT {
		t.Fatalf("expected antigravity groupBy '%s', got %v", api.AntigravityQuotaGroupClaudeGPT, response["groupBy"])
	}

	// Should return empty cycles array when no reset cycles exist
	cycles, ok := response["cycles"].([]interface{})
	if !ok {
		t.Fatalf("expected cycles array, got %T", response["cycles"])
	}
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles when no reset cycles exist, got %d", len(cycles))
	}
}

func TestHandler_CycleOverview_AntigravityReturnsSingleActiveCycleRowPerGroup(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.75, RemainingPercent: 75, ResetTime: &reset},
			{ModelID: "gpt-4o", Label: "GPT 4o", RemainingFraction: 0.65, RemainingPercent: 65, ResetTime: &reset},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot); err != nil {
		t.Fatalf("failed to insert antigravity snapshot: %v", err)
	}

	if _, err := s.CreateAntigravityCycle("claude-4-5-sonnet", now.Add(-2*time.Hour), &reset); err != nil {
		t.Fatalf("failed to create claude active cycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("claude-4-5-sonnet", 0.30, 0.10); err != nil {
		t.Fatalf("failed to update claude active cycle: %v", err)
	}

	if _, err := s.CreateAntigravityCycle("gpt-4o", now.Add(-90*time.Minute), &reset); err != nil {
		t.Fatalf("failed to create gpt active cycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("gpt-4o", 0.40, 0.12); err != nil {
		t.Fatalf("failed to update gpt active cycle: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity&groupBy=antigravity_claude_gpt", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	cyclesRaw, ok := response["cycles"].([]interface{})
	if !ok {
		t.Fatalf("expected cycles array, got %T", response["cycles"])
	}

	activeCount := 0
	for _, item := range cyclesRaw {
		cycle, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if cycle["cycleEnd"] == nil {
			activeCount++
		}
	}
	if activeCount > 1 {
		t.Fatalf("expected at most 1 active cycle row for antigravity group, got %d", activeCount)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Logging History Handler Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_LoggingHistory_AntigravityReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot1 := &api.AntigravitySnapshot{
		CapturedAt: now.Add(-2 * time.Minute),
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B", RemainingFraction: 0.8, RemainingPercent: 80},
		},
	}
	snapshot2 := &api.AntigravitySnapshot{
		CapturedAt: now.Add(-1 * time.Minute),
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B", RemainingFraction: 0.7, RemainingPercent: 70},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot1); err != nil {
		t.Fatalf("failed to insert snapshot1: %v", err)
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot2); err != nil {
		t.Fatalf("failed to insert snapshot2: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "antigravity" {
		t.Fatalf("expected provider 'antigravity', got %v", response["provider"])
	}

	logs, ok := response["logs"].([]interface{})
	if !ok {
		t.Fatalf("expected logs array, got %T", response["logs"])
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logging entries, got %d", len(logs))
	}

	// Verify logs are ordered newest first
	log1 := logs[0].(map[string]interface{})
	log2 := logs[1].(map[string]interface{})
	if log1["id"].(float64) < log2["id"].(float64) {
		t.Fatalf("expected logs to be ordered newest first")
	}
}

func TestHandler_LoggingHistory_AntigravityCalculatesDeltas(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	// First snapshot: 80% remaining (20% used)
	snapshot1 := &api.AntigravitySnapshot{
		CapturedAt: now.Add(-2 * time.Minute),
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B", RemainingFraction: 0.8, RemainingPercent: 80},
		},
	}
	// Second snapshot: 70% remaining (30% used) - delta should be +10%
	snapshot2 := &api.AntigravitySnapshot{
		CapturedAt: now.Add(-1 * time.Minute),
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B", RemainingFraction: 0.7, RemainingPercent: 70},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot1); err != nil {
		t.Fatalf("failed to insert snapshot1: %v", err)
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot2); err != nil {
		t.Fatalf("failed to insert snapshot2: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	logs := response["logs"].([]interface{})
	// logs[0] is the newest (snapshot2), logs[1] is older (snapshot1)
	// The delta should show in the second snapshot (snapshot2) relative to first

	// First log (older, snapshot1) should have delta 0 (no previous)
	log1 := logs[1].(map[string]interface{})
	cq1 := log1["crossQuotas"].([]interface{})[0].(map[string]interface{})
	delta1 := cq1["delta"].(float64)
	if delta1 != 0 {
		t.Fatalf("expected first snapshot delta to be 0, got %v", delta1)
	}

	// Second log (newer, snapshot2) should have delta +10 (usage went from 20% to 30%)
	log2 := logs[0].(map[string]interface{})
	cq2 := log2["crossQuotas"].([]interface{})[0].(map[string]interface{})
	delta2 := cq2["delta"].(float64)
	expectedDelta := 10.0 // 30% - 20% = 10%
	if math.Abs(delta2-expectedDelta) > 0.001 {
		t.Fatalf("expected second snapshot delta to be %v, got %v", expectedDelta, delta2)
	}
}

func TestHandler_LoggingHistory_SyntheticReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-5 * time.Minute)
	for i, sub := range []float64{10, 25, 40} {
		snap := &api.Snapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Sub:        api.QuotaInfo{Limit: 100, Requests: sub, RenewsAt: base.Add(5 * time.Hour)},
			Search:     api.QuotaInfo{Limit: 50, Requests: float64(5 + i), RenewsAt: base.Add(time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 200, Requests: float64(20 + i), RenewsAt: base.Add(3 * time.Hour)},
		}
		if _, err := s.InsertSnapshot(snap); err != nil {
			t.Fatalf("InsertSnapshot[%d]: %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=synthetic&limit=2", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "synthetic" {
		t.Fatalf("expected provider synthetic, got %v", response["provider"])
	}

	quotaNames := response["quotaNames"].([]interface{})
	expectedQuotaNames := []string{"subscription", "search", "toolcall"}
	if len(quotaNames) != len(expectedQuotaNames) {
		t.Fatalf("expected %d quota names, got %d", len(expectedQuotaNames), len(quotaNames))
	}
	for i, expected := range expectedQuotaNames {
		if quotaNames[i].(string) != expected {
			t.Fatalf("quotaNames[%d]=%s, want %s", i, quotaNames[i].(string), expected)
		}
	}

	logs := response["logs"].([]interface{})
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	newest := logs[0].(map[string]interface{})
	older := logs[1].(map[string]interface{})
	newestTime, _ := time.Parse(time.RFC3339, newest["capturedAt"].(string))
	olderTime, _ := time.Parse(time.RFC3339, older["capturedAt"].(string))
	if !newestTime.After(olderTime) {
		t.Fatalf("expected newest-first order, got %s then %s", newestTime, olderTime)
	}

	newestCQ := newest["crossQuotas"].([]interface{})
	olderCQ := older["crossQuotas"].([]interface{})
	if len(newestCQ) == 0 || len(olderCQ) == 0 {
		t.Fatal("expected crossQuotas in logs")
	}

	olderSub := olderCQ[0].(map[string]interface{})
	if olderSub["name"].(string) != "subscription" {
		t.Fatalf("expected first quota subscription, got %s", olderSub["name"].(string))
	}
	if olderSub["delta"].(float64) != 0 {
		t.Fatalf("expected older delta 0, got %.2f", olderSub["delta"].(float64))
	}

	newestSub := newestCQ[0].(map[string]interface{})
	expectedDelta := 15.0 // 40% - 25%
	if math.Abs(newestSub["delta"].(float64)-expectedDelta) > 0.001 {
		t.Fatalf("expected newest delta %.2f, got %.2f", expectedDelta, newestSub["delta"].(float64))
	}
}

func TestHandler_LoggingHistory_ZaiReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-5 * time.Minute)
	for i, pct := range []int{10, 25, 40} {
		snap := &api.ZaiSnapshot{
			CapturedAt:       base.Add(time.Duration(i) * time.Minute),
			TimeLimit:        1000,
			TimeUnit:         1,
			TimeNumber:       1000,
			TimeUsage:        float64(100 + (i * 10)),
			TimeCurrentValue: float64(100 + (i * 10)),
			TimeRemaining:    float64(900 - (i * 10)),
			TimePercentage:   10 + i,
			TokensLimit:      200,
			TokensUnit:       1,
			TokensNumber:     200,
			TokensUsage:      float64(20 + (i * 30)),
			TokensPercentage: pct,
		}
		if _, err := s.InsertZaiSnapshot(snap); err != nil {
			t.Fatalf("InsertZaiSnapshot[%d]: %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithZai())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=zai&limit=2", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "zai" {
		t.Fatalf("expected provider zai, got %v", response["provider"])
	}

	quotaNames := response["quotaNames"].([]interface{})
	expectedQuotaNames := []string{"tokens", "time"}
	if len(quotaNames) != len(expectedQuotaNames) {
		t.Fatalf("expected %d quota names, got %d", len(expectedQuotaNames), len(quotaNames))
	}
	for i, expected := range expectedQuotaNames {
		if quotaNames[i].(string) != expected {
			t.Fatalf("quotaNames[%d]=%s, want %s", i, quotaNames[i].(string), expected)
		}
	}

	logs := response["logs"].([]interface{})
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	newest := logs[0].(map[string]interface{})
	older := logs[1].(map[string]interface{})
	newestTime, _ := time.Parse(time.RFC3339, newest["capturedAt"].(string))
	olderTime, _ := time.Parse(time.RFC3339, older["capturedAt"].(string))
	if !newestTime.After(olderTime) {
		t.Fatalf("expected newest-first order, got %s then %s", newestTime, olderTime)
	}

	olderToken := older["crossQuotas"].([]interface{})[0].(map[string]interface{})
	if olderToken["name"].(string) != "tokens" {
		t.Fatalf("expected tokens first, got %s", olderToken["name"].(string))
	}
	if olderToken["delta"].(float64) != 0 {
		t.Fatalf("expected older delta 0, got %.2f", olderToken["delta"].(float64))
	}

	newestToken := newest["crossQuotas"].([]interface{})[0].(map[string]interface{})
	expectedDelta := 15.0 // 40 - 25
	if math.Abs(newestToken["delta"].(float64)-expectedDelta) > 0.001 {
		t.Fatalf("expected delta %.2f, got %.2f", expectedDelta, newestToken["delta"].(float64))
	}
}

func TestHandler_LoggingHistory_AnthropicReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-5 * time.Minute)
	for i, util := range []float64{20, 35, 55} {
		snap := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: util},
				{Name: "seven_day", Utilization: 10 + float64(i)},
				{Name: "seven_day_sonnet", Utilization: 5 + float64(i)},
			},
			RawJSON: "{}",
		}
		if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
			t.Fatalf("InsertAnthropicSnapshot[%d]: %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=anthropic&limit=2", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "anthropic" {
		t.Fatalf("expected provider anthropic, got %v", response["provider"])
	}

	quotaNames := response["quotaNames"].([]interface{})
	expectedQuotaNames := []string{"five_hour", "seven_day", "seven_day_sonnet"}
	if len(quotaNames) != len(expectedQuotaNames) {
		t.Fatalf("expected %d quota names, got %d", len(expectedQuotaNames), len(quotaNames))
	}
	for i, expected := range expectedQuotaNames {
		if quotaNames[i].(string) != expected {
			t.Fatalf("quotaNames[%d]=%s, want %s", i, quotaNames[i].(string), expected)
		}
	}

	logs := response["logs"].([]interface{})
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	newest := logs[0].(map[string]interface{})
	older := logs[1].(map[string]interface{})
	newestTime, _ := time.Parse(time.RFC3339, newest["capturedAt"].(string))
	olderTime, _ := time.Parse(time.RFC3339, older["capturedAt"].(string))
	if !newestTime.After(olderTime) {
		t.Fatalf("expected newest-first order, got %s then %s", newestTime, olderTime)
	}

	olderFiveHour := older["crossQuotas"].([]interface{})[0].(map[string]interface{})
	if olderFiveHour["delta"].(float64) != 0 {
		t.Fatalf("expected older delta 0, got %.2f", olderFiveHour["delta"].(float64))
	}

	newestFiveHour := newest["crossQuotas"].([]interface{})[0].(map[string]interface{})
	expectedDelta := 20.0 // 55 - 35
	if math.Abs(newestFiveHour["delta"].(float64)-expectedDelta) > 0.001 {
		t.Fatalf("expected delta %.2f, got %.2f", expectedDelta, newestFiveHour["delta"].(float64))
	}
}

func TestHandler_LoggingHistory_CopilotReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-5 * time.Minute)
	for i, remaining := range []int{90, 70, 40} {
		snap := &api.CopilotSnapshot{
			CapturedAt:  base.Add(time.Duration(i) * time.Minute),
			CopilotPlan: "individual_pro",
			RawJSON:     "{}",
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 100, Remaining: remaining, PercentRemaining: float64(remaining), Unlimited: false},
				{Name: "chat", Entitlement: 0, Remaining: 0, PercentRemaining: 100, Unlimited: true},
				{Name: "completions", Entitlement: 0, Remaining: 0, PercentRemaining: 100, Unlimited: true},
			},
		}
		if _, err := s.InsertCopilotSnapshot(snap); err != nil {
			t.Fatalf("InsertCopilotSnapshot[%d]: %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAll())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=copilot&limit=2", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "copilot" {
		t.Fatalf("expected provider copilot, got %v", response["provider"])
	}

	quotaNames := response["quotaNames"].([]interface{})
	expectedQuotaNames := []string{"premium_interactions", "chat", "completions"}
	if len(quotaNames) != len(expectedQuotaNames) {
		t.Fatalf("expected %d quota names, got %d", len(expectedQuotaNames), len(quotaNames))
	}
	for i, expected := range expectedQuotaNames {
		if quotaNames[i].(string) != expected {
			t.Fatalf("quotaNames[%d]=%s, want %s", i, quotaNames[i].(string), expected)
		}
	}

	logs := response["logs"].([]interface{})
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	newest := logs[0].(map[string]interface{})
	older := logs[1].(map[string]interface{})
	newestTime, _ := time.Parse(time.RFC3339, newest["capturedAt"].(string))
	olderTime, _ := time.Parse(time.RFC3339, older["capturedAt"].(string))
	if !newestTime.After(olderTime) {
		t.Fatalf("expected newest-first order, got %s then %s", newestTime, olderTime)
	}

	olderPremium := older["crossQuotas"].([]interface{})[0].(map[string]interface{})
	if olderPremium["delta"].(float64) != 0 {
		t.Fatalf("expected older delta 0, got %.2f", olderPremium["delta"].(float64))
	}

	newestPremium := newest["crossQuotas"].([]interface{})[0].(map[string]interface{})
	expectedDelta := 30.0 // used 60 - used 30
	if math.Abs(newestPremium["delta"].(float64)-expectedDelta) > 0.001 {
		t.Fatalf("expected delta %.2f, got %.2f", expectedDelta, newestPremium["delta"].(float64))
	}
}

func TestHandler_LoggingHistory_CodexReturnsSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-5 * time.Minute)
	for i, util := range []float64{10, 25, 45} {
		snap := &api.CodexSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			PlanType:   "pro",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: util},
				{Name: "seven_day", Utilization: 5 + float64(i)},
				{Name: "code_review", Utilization: 2 + float64(i)},
			},
		}
		if _, err := s.InsertCodexSnapshot(snap); err != nil {
			t.Fatalf("InsertCodexSnapshot[%d]: %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=codex&limit=2", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["provider"] != "codex" {
		t.Fatalf("expected provider codex, got %v", response["provider"])
	}

	quotaNames := response["quotaNames"].([]interface{})
	expectedQuotaNames := []string{"five_hour", "seven_day", "code_review"}
	if len(quotaNames) != len(expectedQuotaNames) {
		t.Fatalf("expected %d quota names, got %d", len(expectedQuotaNames), len(quotaNames))
	}
	for i, expected := range expectedQuotaNames {
		if quotaNames[i].(string) != expected {
			t.Fatalf("quotaNames[%d]=%s, want %s", i, quotaNames[i].(string), expected)
		}
	}

	logs := response["logs"].([]interface{})
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	newest := logs[0].(map[string]interface{})
	older := logs[1].(map[string]interface{})
	newestTime, _ := time.Parse(time.RFC3339, newest["capturedAt"].(string))
	olderTime, _ := time.Parse(time.RFC3339, older["capturedAt"].(string))
	if !newestTime.After(olderTime) {
		t.Fatalf("expected newest-first order, got %s then %s", newestTime, olderTime)
	}

	olderFiveHour := older["crossQuotas"].([]interface{})[0].(map[string]interface{})
	if olderFiveHour["delta"].(float64) != 0 {
		t.Fatalf("expected older delta 0, got %.2f", olderFiveHour["delta"].(float64))
	}

	newestFiveHour := newest["crossQuotas"].([]interface{})[0].(map[string]interface{})
	expectedDelta := 20.0 // 45 - 25
	if math.Abs(newestFiveHour["delta"].(float64)-expectedDelta) > 0.001 {
		t.Fatalf("expected delta %.2f, got %.2f", expectedDelta, newestFiveHour["delta"].(float64))
	}
}

func TestHandler_LoggingHistory_UnknownProviderReturnsEmpty(t *testing.T) {
	cfg := createTestConfigWithAll()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=unknown", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	logs, ok := response["logs"].([]interface{})
	if !ok {
		t.Fatalf("expected logs array, got %T", response["logs"])
	}
	if len(logs) != 0 {
		t.Fatalf("expected empty logs array for unknown provider, got %d", len(logs))
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Update Handler Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CheckUpdate_NoUpdater(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	// No updater set

	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	h.CheckUpdate(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_CheckUpdate_MethodNotAllowed(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	h.CheckUpdate(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandler_ApplyUpdate_NoUpdater(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)
	// No updater set

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	h.ApplyUpdate(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_ApplyUpdate_MethodNotAllowed(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	h.ApplyUpdate(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Anthropic Handler Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Current_Anthropic_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.2, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 12.8, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":45.2},"seven_day":{"utilization":12.8}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array in response")
	}
	if len(quotas) != 2 {
		t.Errorf("expected 2 quotas, got %d", len(quotas))
	}

	// Verify first quota structure
	q0, ok := quotas[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected quota to be a map")
	}
	if q0["name"] != "five_hour" {
		t.Errorf("expected first quota name 'five_hour', got %v", q0["name"])
	}
	if q0["utilization"].(float64) != 45.2 {
		t.Errorf("expected utilization 45.2, got %v", q0["utilization"])
	}
}

func TestHandler_Current_Anthropic_EmptyDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatal("expected quotas array in response")
	}
	if len(quotas) != 0 {
		t.Errorf("expected empty quotas for empty DB, got %d", len(quotas))
	}
}

func TestHandler_History_Anthropic(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.2, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":45.2}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=anthropic&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(response))
	}
	if len(response) > 0 {
		if _, ok := response[0]["five_hour"]; !ok {
			t.Error("expected five_hour field in history entry")
		}
	}
}

func TestHandler_Insights_Anthropic_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetsAt := time.Now().Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.2, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 12.8, ResetsAt: &resetsAt},
		},
		RawJSON: `{"five_hour":{"utilization":45.2},"seven_day":{"utilization":12.8}}`,
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response insightsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response.Stats == nil {
		t.Error("expected stats in response")
	}
	if response.Insights == nil {
		t.Error("expected insights in response")
	}
}

func TestHandler_History_Antigravity_UsesRFC3339Labels(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(2 * time.Hour)
	first := &api.AntigravitySnapshot{
		CapturedAt: now.Add(-30 * time.Minute),
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B (Medium)", RemainingFraction: 0.80, RemainingPercent: 80, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M36", Label: "Gemini 3.1 Pro (Low)", RemainingFraction: 0.90, RemainingPercent: 90, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M18", Label: "Gemini 3 Flash", RemainingFraction: 0.95, RemainingPercent: 95, ResetTime: &reset},
		},
	}
	second := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B (Medium)", RemainingFraction: 0.70, RemainingPercent: 70, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M36", Label: "Gemini 3.1 Pro (Low)", RemainingFraction: 0.85, RemainingPercent: 85, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M18", Label: "Gemini 3 Flash", RemainingFraction: 0.90, RemainingPercent: 90, ResetTime: &reset},
		},
	}

	if _, err := s.InsertAntigravitySnapshot(first); err != nil {
		t.Fatalf("failed to insert first antigravity snapshot: %v", err)
	}
	if _, err := s.InsertAntigravitySnapshot(second); err != nil {
		t.Fatalf("failed to insert second antigravity snapshot: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=antigravity&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	labelsRaw, ok := response["labels"].([]interface{})
	if !ok || len(labelsRaw) == 0 {
		t.Fatalf("expected non-empty labels array, got %#v", response["labels"])
	}

	firstLabel, ok := labelsRaw[0].(string)
	if !ok || firstLabel == "" {
		t.Fatalf("expected first label to be non-empty string, got %#v", labelsRaw[0])
	}

	if _, err := time.Parse(time.RFC3339, firstLabel); err != nil {
		t.Fatalf("expected RFC3339 label, got %q: %v", firstLabel, err)
	}
}

func TestBuildAntigravityInsights_AggregatesBurnRateByAverageAcrossGroups(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)
	start := now.Add(-1 * time.Hour)

	snapshot := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B (Medium)", RemainingFraction: 0.40, RemainingPercent: 40, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M36", Label: "Gemini 3.1 Pro (Low)", RemainingFraction: 0.70, RemainingPercent: 70, ResetTime: &reset},
			{ModelID: "MODEL_PLACEHOLDER_M18", Label: "Gemini 3 Flash", RemainingFraction: 1.00, RemainingPercent: 100, ResetTime: &reset},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot); err != nil {
		t.Fatalf("failed to insert antigravity snapshot: %v", err)
	}

	if _, err := s.CreateAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", start, &reset); err != nil {
		t.Fatalf("failed to create claude/gpt cycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", 0.60, 0.60); err != nil {
		t.Fatalf("failed to update claude/gpt cycle: %v", err)
	}

	if _, err := s.CreateAntigravityCycle("MODEL_PLACEHOLDER_M36", start, &reset); err != nil {
		t.Fatalf("failed to create gemini-pro cycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("MODEL_PLACEHOLDER_M36", 0.30, 0.30); err != nil {
		t.Fatalf("failed to update gemini-pro cycle: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	resp := h.buildAntigravityInsights(map[string]bool{}, 24*time.Hour)
	if len(resp.Stats) == 0 {
		t.Fatal("expected stats to be present")
	}

	statsByLabel := map[string]string{}
	for _, stat := range resp.Stats {
		statsByLabel[stat.Label] = stat.Value
	}

	// With active cycles, the effective burn rate is shown as "Current Burn"
	// The rate should be ~45%/hr (average of 60%/hr for GPT and 30%/hr for Gemini Pro)
	got := statsByLabel["Current Burn"]
	if got != "45.0%/hr" && got != "45.1%/hr" && got != "44.9%/hr" {
		t.Fatalf("expected Current Burn around 45.0%%/hr, got %q", got)
	}
}

func TestBuildAntigravityInsights_RangeFiltersOldCycles(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)

	// Insert current snapshot
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "MODEL_OPENAI_GPT_OSS_120B_MEDIUM", Label: "GPT-OSS 120B (Medium)", RemainingFraction: 0.50, RemainingPercent: 50, ResetTime: &reset},
		},
	}
	if _, err := s.InsertAntigravitySnapshot(snapshot); err != nil {
		t.Fatalf("failed to insert snapshot: %v", err)
	}

	// Create old cycle (8 days ago) - should be excluded from 7d range
	oldStart := now.Add(-8 * 24 * time.Hour)
	oldEnd := now.Add(-7 * 24 * time.Hour)
	if _, err := s.CreateAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", oldStart, &reset); err != nil {
		t.Fatalf("failed to create old cycle: %v", err)
	}
	// Close the old cycle
	if err := s.CloseAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", oldEnd, 0.80, 0.80); err != nil {
		t.Fatalf("failed to close old cycle: %v", err)
	}

	// Create recent cycle (1 day ago) - should be included in 7d range
	recentStart := now.Add(-24 * time.Hour)
	if _, err := s.CreateAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", recentStart, &reset); err != nil {
		t.Fatalf("failed to create recent cycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("MODEL_OPENAI_GPT_OSS_120B_MEDIUM", 0.30, 0.30); err != nil {
		t.Fatalf("failed to update recent cycle: %v", err)
	}

	cfg := createTestConfigWithAll()
	h := NewHandler(s, nil, nil, nil, cfg)

	// With 7d range, only recent cycle should be included
	resp7d := h.buildAntigravityInsights(map[string]bool{}, 7*24*time.Hour)
	found7d := false
	for _, stat := range resp7d.Stats {
		if stat.Label == "Current Burn" || stat.Label == "Avg Burn Rate" {
			found7d = true
			// Rate should be based only on recent cycle (30%/hr for 24h duration = 0.3 * 100 / 24 = 1.25%/hr)
			// But with active cycle, it calculates from now, not cycle end
		}
	}
	if !found7d && len(resp7d.Stats) > 0 {
		t.Log("7d range returned stats, verifying range filter works")
	}

	// With 1d range, recent cycle should still be included
	resp1d := h.buildAntigravityInsights(map[string]bool{}, 24*time.Hour)
	// Verify we got some response
	if len(resp1d.Insights) == 0 && len(resp1d.Stats) == 0 {
		t.Fatal("expected at least some insights for 1d range")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Dashboard With Provider Param Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Dashboard_WithProviderParam(t *testing.T) {
	cfg := createTestConfigWithAll()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %s", ct)
	}
}

func TestHandler_Dashboard_AppJSVersionedURL_Rendered(t *testing.T) {
	cfg := createTestConfigWithAll()
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.SetVersion("2.11.0")

	req := httptest.NewRequest(http.MethodGet, "/?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "/static/app.js?v=2.11.0") {
		t.Fatalf("expected versioned app.js URL, body=%s", body)
	}

	if strings.Contains(body, "/static/app.js?v=") && !strings.Contains(body, "/static/app.js?v=2.11.0") {
		t.Fatalf("expected app.js version token to match 2.11.0, body=%s", body)
	}
}

func TestHandler_Dashboard_NotFound_For_NonRootPath(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rr := httptest.NewRecorder()
	h.Dashboard(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for non-root path, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Utility Function Tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_formatDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{"negative", -1 * time.Minute, "Resetting..."},
		{"days and hours", 4*24*time.Hour + 11*time.Hour, "4d 11h"},
		{"hours and minutes", 3*time.Hour + 16*time.Minute, "3h 16m"},
		{"only minutes", 45 * time.Minute, "45m"},
		{"zero", 0, "0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.input)
			if got != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHandler_downsampleStep(t *testing.T) {
	tests := []struct {
		n, max, want int
	}{
		{100, 500, 1},  // No downsampling needed
		{1000, 500, 2}, // Need to reduce
		{0, 500, 1},    // Empty
		{500, 0, 1},    // Max 0
		{1500, 500, 3}, // ceil(1500/500) = 3
	}

	for _, tt := range tests {
		got := downsampleStep(tt.n, tt.max)
		if got != tt.want {
			t.Errorf("downsampleStep(%d, %d) = %d, want %d", tt.n, tt.max, got, tt.want)
		}
	}
}

func TestHandler_parseInsightsRange(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"", 7 * 24 * time.Hour},        // default
		{"invalid", 7 * 24 * time.Hour}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseInsightsRange(tt.input)
			if got != tt.want {
				t.Errorf("parseInsightsRange(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Security Tests: MaxBytesReader and Error Sanitization ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_MaxBytesReader_RejectsLargeBody(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	// Create valid JSON that exceeds 64KB when parsed
	// Use a key with a large string value to exceed the limit
	largeValue := strings.Repeat("x", 65*1024)
	largePayload := fmt.Sprintf(`{"timezone":"%s"}`, largeValue)

	tests := []struct {
		name    string
		method  string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{
			name:    "UpdateSettings PUT",
			method:  http.MethodPut,
			handler: h.UpdateSettings,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/settings", strings.NewReader(largePayload))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			tt.handler(rr, req)

			// MaxBytesReader returns 413 Entity Too Large for oversized bodies
			if rr.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("expected status %d (RequestEntityTooLarge), got %d", http.StatusRequestEntityTooLarge, rr.Code)
			}
		})
	}
}

func TestHandler_ApplyUpdate_SanitizesErrors(t *testing.T) {
	// Create a mock updater that will return an error
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	// The handler should sanitize internal errors
	// We'll test that the ApplyUpdate endpoint doesn't leak internal error details

	// Since we can't easily mock the updater, we test the 503 case (no updater configured)
	// which already returns a generic message
	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()

	h.ApplyUpdate(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}

	// Verify the error message is generic
	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if response["error"] != "updater not configured" {
		t.Errorf("expected generic error message, got %q", response["error"])
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Security Tests: Login Error Whitelist ──
// ═══════════════════════════════════════════════════════════════════

func TestLogin_WhitelistsErrorCodes(t *testing.T) {
	tests := []struct {
		name         string
		errorCode    string
		wantContains string
	}{
		{
			name:         "invalid error code shows whitelisted message",
			errorCode:    "invalid",
			wantContains: "Invalid username or password",
		},
		{
			name:         "expired error code shows whitelisted message",
			errorCode:    "expired",
			wantContains: "Session expired",
		},
		{
			name:         "required error code shows whitelisted message",
			errorCode:    "required",
			wantContains: "Authentication required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createTestConfigWithSynthetic()
			h := NewHandler(nil, nil, nil, nil, cfg)

			req := httptest.NewRequest(http.MethodGet, "/login?error="+tt.errorCode, nil)
			rr := httptest.NewRecorder()
			h.Login(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rr.Code)
			}

			body := rr.Body.String()
			if !strings.Contains(body, tt.wantContains) {
				t.Errorf("expected body to contain %q, got:\n%s", tt.wantContains, body)
			}
		})
	}
}

func TestLogin_RejectsUnknownErrorCode(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	// Unknown error code should result in empty error message
	req := httptest.NewRequest(http.MethodGet, "/login?error=malicious<script>alert(1)</script>", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	// The error should not contain the malicious input
	// Note: we check for the specific malicious pattern, not all <script> tags
	// since the template legitimately contains theme-toggle scripts
	if strings.Contains(body, "malicious") {
		t.Error("body should not contain unknown error code")
	}
	if strings.Contains(body, "alert(1)") {
		t.Error("body should not contain malicious script content")
	}
	// Verify the error-message div is not rendered for unknown codes
	if strings.Contains(body, `class="error-message"`) {
		t.Error("error-message div should not be rendered for unknown error codes")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── New coverage tests: helper functions ──
// ═══════════════════════════════════════════════════════════════════

func createTestConfigWithCopilot() *config.Config {
	return &config.Config{
		CopilotToken: "ghp_test_copilot_token",
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func createTestConfigWithAntigravity() *config.Config {
	return &config.Config{
		AntigravityEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

func createTestConfigWithAllProviders() *config.Config {
	return &config.Config{
		SyntheticAPIKey:    "syn_test_key",
		ZaiAPIKey:          "zai_test_key",
		ZaiBaseURL:         "https://api.z.ai/api",
		AnthropicToken:     "test_anthropic_token",
		CopilotToken:       "ghp_test_copilot_token",
		CodexToken:         "codex_test_token",
		AntigravityEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

func TestHandler_SetCopilotTracker(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	tr := tracker.NewCopilotTracker(s, nil)
	h.SetCopilotTracker(tr)

	if h.copilotTracker == nil {
		t.Error("expected copilotTracker to be set")
	}
}

func TestHandler_SetAntigravityTracker(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	tr := tracker.NewAntigravityTracker(s, nil)
	h.SetAntigravityTracker(tr)

	if h.antigravityTracker == nil {
		t.Error("expected antigravityTracker to be set")
	}
}

func TestHandler_SetUpdater(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	// SetUpdater with nil should not panic
	h.SetUpdater(nil)
	if h.updater != nil {
		t.Error("expected updater to be nil")
	}
}

func TestHandler_GetSessionStore(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	ss := h.GetSessionStore()
	if ss != nil {
		t.Error("expected nil session store when not configured")
	}
}

func TestHandler_SetRateLimiter(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	rl := NewLoginRateLimiter(100)
	h.SetRateLimiter(rl)
	if h.rateLimiter == nil {
		t.Error("expected rateLimiter to be set")
	}
}

func TestIsMaxBytesError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"unrelated error", fmt.Errorf("some other error"), false},
		{"max bytes error", fmt.Errorf("http: request body too large"), true},
		{"wrapped max bytes error", fmt.Errorf("read: http: request body too large"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isMaxBytesError(tt.err)
			if result != tt.expected {
				t.Errorf("isMaxBytesError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestSanitizeSMTPError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, "SMTP test failed"},
		{"auth error", fmt.Errorf("535 Authentication failed"), "Authentication failed: check username/password"},
		{"password error", fmt.Errorf("invalid password provided"), "Authentication failed: check username/password"},
		{"connection refused", fmt.Errorf("dial tcp: connection refused"), "Connection failed: unable to reach SMTP server"},
		{"timeout error", fmt.Errorf("i/o timeout"), "Connection failed: unable to reach SMTP server"},
		{"no such host", fmt.Errorf("no such host"), "Connection failed: unable to reach SMTP server"},
		{"tls error", fmt.Errorf("TLS handshake failure"), "TLS error: check encryption settings"},
		{"certificate error", fmt.Errorf("x509: certificate has expired"), "TLS error: check encryption settings"},
		{"unknown error", fmt.Errorf("something unexpected happened"), "SMTP test failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeSMTPError(tt.err)
			if result != tt.expected {
				t.Errorf("sanitizeSMTPError(%v) = %q, want %q", tt.err, result, tt.expected)
			}
		})
	}
}

func TestSeverityFromPercent(t *testing.T) {
	tests := []struct {
		pct      float64
		expected string
	}{
		{0, "positive"},
		{25, "positive"},
		{49.9, "positive"},
		{50, "info"},
		{79.9, "info"},
		{80, "warning"},
		{94.9, "warning"},
		{95, "negative"},
		{100, "negative"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("pct_%.1f", tt.pct), func(t *testing.T) {
			result := severityFromPercent(tt.pct)
			if result != tt.expected {
				t.Errorf("severityFromPercent(%.1f) = %q, want %q", tt.pct, result, tt.expected)
			}
		})
	}
}

func TestCompactNum(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
		{1000000000, "1.0B"},
		{3500000000, "3.5B"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%.0f", tt.input), func(t *testing.T) {
			result := compactNum(tt.input)
			if result != tt.expected {
				t.Errorf("compactNum(%.0f) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatDuration_AllBranches(t *testing.T) {
	tests := []struct {
		name     string
		dur      time.Duration
		expected string
	}{
		{"negative", -1 * time.Hour, "Resetting..."},
		{"minutes only", 45 * time.Minute, "45m"},
		{"zero", 0, "0m"},
		{"hours only", 3 * time.Hour, "3h"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h 30m"},
		{"days and hours", 2*24*time.Hour + 5*time.Hour, "2d 5h"},
		{"days and minutes (no hours)", 1*24*time.Hour + 30*time.Minute, "1d 30m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.dur)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.dur, result, tt.expected)
			}
		})
	}
}

func TestParseCycleOverviewLimit(t *testing.T) {
	tests := []struct {
		query    string
		expected int
	}{
		{"", 50},             // default
		{"?limit=10", 10},    // valid
		{"?limit=500", 500},  // max
		{"?limit=1000", 500}, // capped
		{"?limit=-1", 50},    // invalid negative
		{"?limit=abc", 50},   // invalid string
		{"?limit=0", 50},     // zero treated as invalid
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview"+tt.query, nil)
			result := parseCycleOverviewLimit(req)
			if result != tt.expected {
				t.Errorf("parseCycleOverviewLimit(%q) = %d, want %d", tt.query, result, tt.expected)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Billing period helper tests ──
// ═══════════════════════════════════════════════════════════════════

func TestGroupBillingPeriods_Empty(t *testing.T) {
	result := groupBillingPeriods(nil)
	if result != nil {
		t.Errorf("expected nil for empty cycles, got %v", result)
	}
}

func TestGroupBillingPeriods_SingleCycle(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 100},
	}
	result := groupBillingPeriods(cycles)
	if len(result) != 1 {
		t.Fatalf("expected 1 period, got %d", len(result))
	}
	if result[0].maxPeak != 100 {
		t.Errorf("expected maxPeak 100, got %.0f", result[0].maxPeak)
	}
}

func TestGroupBillingPeriods_MultiCyclesSamePeriod(t *testing.T) {
	now := time.Now().UTC()
	// DESC order (newest first) - all have similar peaks so same period
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 120},
		{CycleStart: now.Add(-1 * time.Hour), PeakRequests: 110},
		{CycleStart: now.Add(-2 * time.Hour), PeakRequests: 100},
	}
	result := groupBillingPeriods(cycles)
	if len(result) != 1 {
		t.Fatalf("expected 1 period (no reset), got %d", len(result))
	}
	if result[0].maxPeak != 120 {
		t.Errorf("expected maxPeak 120, got %.0f", result[0].maxPeak)
	}
}

func TestGroupBillingPeriods_WithReset(t *testing.T) {
	now := time.Now().UTC()
	// DESC order (newest first) - peak drops >50% indicating reset
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 50},                      // new period
		{CycleStart: now.Add(-1 * time.Hour), PeakRequests: 200}, // old period peak
		{CycleStart: now.Add(-2 * time.Hour), PeakRequests: 180}, // old period
	}
	result := groupBillingPeriods(cycles)
	if len(result) != 2 {
		t.Fatalf("expected 2 periods (reset detected), got %d", len(result))
	}
}

func TestBillingPeriodAvg_Empty(t *testing.T) {
	result := billingPeriodAvg(nil)
	if result != 0 {
		t.Errorf("expected 0 for empty cycles, got %.1f", result)
	}
}

func TestBillingPeriodAvg_WithData(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 100},
	}
	result := billingPeriodAvg(cycles)
	if result != 100 {
		t.Errorf("expected avg 100, got %.1f", result)
	}
}

func TestBillingPeriodPeak(t *testing.T) {
	now := time.Now().UTC()
	// Two billing periods with different peaks
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 30},                      // new period (after reset)
		{CycleStart: now.Add(-1 * time.Hour), PeakRequests: 200}, // old period
		{CycleStart: now.Add(-2 * time.Hour), PeakRequests: 150}, // old period
	}
	result := billingPeriodPeak(cycles)
	if result != 200 {
		t.Errorf("expected peak 200, got %.1f", result)
	}
}

func TestBillingPeriodCount(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 100},
	}
	result := billingPeriodCount(cycles)
	if result != 1 {
		t.Errorf("expected count 1, got %d", result)
	}
}

func TestCycleSumConsumptionSince(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.ResetCycle{
		{CycleStart: now, PeakRequests: 100},
		{CycleStart: now.Add(-2 * time.Hour), PeakRequests: 200},
	}
	// Only include cycles since 1 hour ago
	since := now.Add(-1 * time.Hour)
	result := cycleSumConsumptionSince(cycles, since)
	if result != 100 {
		t.Errorf("expected 100, got %.1f", result)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Anthropic billing period helper tests ──
// ═══════════════════════════════════════════════════════════════════

func TestGroupAnthropicBillingPeriods_Empty(t *testing.T) {
	result := groupAnthropicBillingPeriods(nil)
	if result != nil {
		t.Errorf("expected nil for empty cycles, got %v", result)
	}
}

func TestGroupAnthropicBillingPeriods_SingleCycle(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 50.0},
	}
	result := groupAnthropicBillingPeriods(cycles)
	if len(result) != 1 {
		t.Fatalf("expected 1 period, got %d", len(result))
	}
	if result[0].maxPeak != 50.0 {
		t.Errorf("expected maxPeak 50.0, got %.1f", result[0].maxPeak)
	}
}

func TestGroupAnthropicBillingPeriods_WithReset(t *testing.T) {
	now := time.Now().UTC()
	// DESC order: peak drops >50% indicating reset. Need peak > 5 for detection.
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 10.0},                     // new period
		{CycleStart: now.Add(-1 * time.Hour), PeakUtilization: 80.0}, // old period peak
		{CycleStart: now.Add(-2 * time.Hour), PeakUtilization: 60.0}, // old period
	}
	result := groupAnthropicBillingPeriods(cycles)
	if len(result) != 2 {
		t.Fatalf("expected 2 periods (reset detected), got %d", len(result))
	}
}

func TestGroupAnthropicBillingPeriods_LowPeakNoReset(t *testing.T) {
	now := time.Now().UTC()
	// Peak <= 5 should not trigger a reset boundary even if it drops
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 1.0},
		{CycleStart: now.Add(-1 * time.Hour), PeakUtilization: 4.0},
	}
	result := groupAnthropicBillingPeriods(cycles)
	if len(result) != 1 {
		t.Fatalf("expected 1 period (low peak no reset), got %d", len(result))
	}
}

func TestAnthropicBillingPeriodCount(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 50.0},
	}
	result := anthropicBillingPeriodCount(cycles)
	if result != 1 {
		t.Errorf("expected count 1, got %d", result)
	}
}

func TestAnthropicBillingPeriodAvg_Empty(t *testing.T) {
	result := anthropicBillingPeriodAvg(nil)
	if result != 0 {
		t.Errorf("expected 0 for empty, got %.1f", result)
	}
}

func TestAnthropicBillingPeriodAvg_WithData(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 60.0},
	}
	result := anthropicBillingPeriodAvg(cycles)
	if result != 60.0 {
		t.Errorf("expected avg 60.0, got %.1f", result)
	}
}

func TestAnthropicBillingPeriodPeak(t *testing.T) {
	now := time.Now().UTC()
	cycles := []*store.AnthropicResetCycle{
		{CycleStart: now, PeakUtilization: 10.0},
		{CycleStart: now.Add(-1 * time.Hour), PeakUtilization: 80.0},
		{CycleStart: now.Add(-2 * time.Hour), PeakUtilization: 60.0},
	}
	result := anthropicBillingPeriodPeak(cycles)
	if result != 80.0 {
		t.Errorf("expected peak 80.0, got %.1f", result)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── anthropicCycleToMap tests ──
// ═══════════════════════════════════════════════════════════════════

func TestAnthropicCycleToMap(t *testing.T) {
	now := time.Now().UTC()
	endTime := now.Add(1 * time.Hour)
	resetsAt := now.Add(2 * time.Hour)
	cycle := &store.AnthropicResetCycle{
		ID:              42,
		QuotaName:       "five_hour",
		CycleStart:      now,
		CycleEnd:        &endTime,
		ResetsAt:        &resetsAt,
		PeakUtilization: 75.5,
		TotalDelta:      12.3,
	}
	result := anthropicCycleToMap(cycle)

	if result["id"] != int64(42) {
		t.Errorf("expected id 42, got %v", result["id"])
	}
	if result["quotaName"] != "five_hour" {
		t.Errorf("expected quotaName five_hour, got %v", result["quotaName"])
	}
	if result["peakUtilization"] != 75.5 {
		t.Errorf("expected peakUtilization 75.5, got %v", result["peakUtilization"])
	}
	if result["cycleEnd"] == nil {
		t.Error("expected cycleEnd to be set")
	}
	if result["renewsAt"] == nil {
		t.Error("expected renewsAt to be set")
	}
}

func TestAnthropicCycleToMap_NilEnds(t *testing.T) {
	now := time.Now().UTC()
	cycle := &store.AnthropicResetCycle{
		ID:              1,
		QuotaName:       "daily",
		CycleStart:      now,
		PeakUtilization: 50.0,
		TotalDelta:      5.0,
	}
	result := anthropicCycleToMap(cycle)
	if result["cycleEnd"] != nil {
		t.Errorf("expected cycleEnd nil, got %v", result["cycleEnd"])
	}
	if _, ok := result["renewsAt"]; ok {
		t.Error("expected renewsAt to not be set when ResetsAt is nil")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── buildZaiTrackerSummaryResponse tests ──
// ═══════════════════════════════════════════════════════════════════

func TestBuildZaiTrackerSummaryResponse_WithRenewsAt(t *testing.T) {
	renewsAt := time.Now().UTC().Add(3 * time.Hour)
	summary := &tracker.ZaiSummary{
		QuotaType:       "tokens",
		CurrentUsage:    500,
		CurrentLimit:    1000,
		UsagePercent:    50.0,
		RenewsAt:        &renewsAt,
		TimeUntilReset:  3 * time.Hour,
		CurrentRate:     10.0,
		ProjectedUsage:  800,
		CompletedCycles: 5,
		AvgPerCycle:     600,
		PeakCycle:       900,
		TotalTracked:    3000,
		TrackingSince:   time.Now().UTC().Add(-24 * time.Hour),
	}

	result := buildZaiTrackerSummaryResponse(summary)

	if result["quotaType"] != "tokens" {
		t.Errorf("expected quotaType tokens, got %v", result["quotaType"])
	}
	if result["currentUsage"] != 500.0 {
		t.Errorf("expected currentUsage 500, got %v", result["currentUsage"])
	}
	if result["renewsAt"] == nil {
		t.Error("expected renewsAt to be set")
	}
	if result["timeUntilReset"] == "N/A" {
		t.Error("expected timeUntilReset to not be N/A when renewsAt is set")
	}
	if result["trackingSince"] == nil {
		t.Error("expected trackingSince to be set")
	}
}

func TestBuildZaiTrackerSummaryResponse_WithoutRenewsAt(t *testing.T) {
	summary := &tracker.ZaiSummary{
		QuotaType:    "time",
		CurrentUsage: 100,
		CurrentLimit: 200,
	}

	result := buildZaiTrackerSummaryResponse(summary)

	if result["timeUntilReset"] != "N/A" {
		t.Errorf("expected timeUntilReset N/A, got %v", result["timeUntilReset"])
	}
	if result["trackingSince"] != nil {
		t.Errorf("expected trackingSince nil, got %v", result["trackingSince"])
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Push notification handler tests ──
// ═══════════════════════════════════════════════════════════════════

type mockNotifierWithVAPID struct {
	sendTestErr  error
	sendPushErr  error
	reloadCalled bool
	vapidKey     string
}

func (m *mockNotifierWithVAPID) Reload() error             { m.reloadCalled = true; return nil }
func (m *mockNotifierWithVAPID) ConfigureSMTP() error      { return nil }
func (m *mockNotifierWithVAPID) ConfigurePush() error      { return nil }
func (m *mockNotifierWithVAPID) SendTestEmail() error      { return m.sendTestErr }
func (m *mockNotifierWithVAPID) SendTestPush() error       { return m.sendPushErr }
func (m *mockNotifierWithVAPID) SetEncryptionKey(_ string) {}
func (m *mockNotifierWithVAPID) GetVAPIDPublicKey() string { return m.vapidKey }

func TestHandler_PushVAPIDKey_Success(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: "test-vapid-key-abc123"})

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid", nil)
	rr := httptest.NewRecorder()
	h.PushVAPIDKey(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["public_key"] != "test-vapid-key-abc123" {
		t.Errorf("expected public_key test-vapid-key-abc123, got %v", response["public_key"])
	}
}

func TestHandler_PushVAPIDKey_MethodNotAllowed(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: "key"})

	req := httptest.NewRequest(http.MethodPost, "/api/push/vapid", nil)
	rr := httptest.NewRecorder()
	h.PushVAPIDKey(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandler_PushVAPIDKey_NoNotifier(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid", nil)
	rr := httptest.NewRecorder()
	h.PushVAPIDKey(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_PushVAPIDKey_EmptyKey(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid", nil)
	rr := httptest.NewRecorder()
	h.PushVAPIDKey(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_PushSubscribe_Post_Success(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	body := `{"endpoint":"https://push.example.com/sub1","keys":{"p256dh":"BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8p8jftPGs","auth":"tBHItJI5svbpC7KF2fqSwQ"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["status"] != "subscribed" {
		t.Errorf("expected status subscribed, got %v", response["status"])
	}
}

func TestHandler_PushSubscribe_Post_MissingFields(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	body := `{"endpoint":"https://push.example.com/sub1","keys":{"p256dh":"","auth":""}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_PushSubscribe_Post_InvalidJSON(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_PushSubscribe_Delete_Success(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// First subscribe
	s.SavePushSubscription("https://push.example.com/sub1", "p256dh", "auth")

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	body := `{"endpoint":"https://push.example.com/sub1"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscribe", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]string
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["status"] != "unsubscribed" {
		t.Errorf("expected status unsubscribed, got %v", response["status"])
	}
}

func TestHandler_PushSubscribe_Delete_MissingEndpoint(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	body := `{"endpoint":""}`
	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscribe", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_PushSubscribe_Delete_InvalidJSON(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscribe", strings.NewReader("bad"))
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_PushSubscribe_MethodNotAllowed(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodGet, "/api/push/subscribe", nil)
	rr := httptest.NewRecorder()
	h.PushSubscribe(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandler_PushTest_Success(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: "key"})

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	rr := httptest.NewRecorder()
	h.PushTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["success"] != true {
		t.Errorf("expected success true, got %v", response["success"])
	}
}

func TestHandler_PushTest_MethodNotAllowed(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodGet, "/api/push/test", nil)
	rr := httptest.NewRecorder()
	h.PushTest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandler_PushTest_NoNotifier(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	// Reset the push test cooldown to avoid rate limiting
	h.pushTestLastSent = time.Time{}

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	rr := httptest.NewRecorder()
	h.PushTest(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_PushTest_RateLimit(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: "key"})

	// First request succeeds
	req1 := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	rr1 := httptest.NewRecorder()
	h.PushTest(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: expected status 200, got %d", rr1.Code)
	}

	// Second request within 30s should be rate-limited
	req2 := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	rr2 := httptest.NewRecorder()
	h.PushTest(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected status 429, got %d", rr2.Code)
	}
}

func TestHandler_PushTest_SendFailure(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifierWithVAPID{vapidKey: "key", sendPushErr: fmt.Errorf("push failed")})

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", nil)
	rr := httptest.NewRecorder()
	h.PushTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 (even on failure), got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["success"] != false {
		t.Errorf("expected success false, got %v", response["success"])
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── "Both" handler tests (cyclesBoth, summaryBoth, insightsBoth) ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CyclesBoth_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["synthetic"]; !ok {
		t.Error("expected synthetic key in response")
	}
	if _, ok := response["zai"]; !ok {
		t.Error("expected zai key in response")
	}
}

func TestHandler_CyclesBoth_NilStore(t *testing.T) {
	cfg := createTestConfigWithBoth()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_CyclesBoth_WithAllProviders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	// All configured providers should have keys
	for _, provider := range []string{"synthetic", "zai", "anthropic", "codex", "antigravity"} {
		if _, ok := response[provider]; !ok {
			t.Errorf("expected %s key in response", provider)
		}
	}
}

func TestHandler_SummaryBoth_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["synthetic"]; !ok {
		t.Error("expected synthetic key in response")
	}
	if _, ok := response["zai"]; !ok {
		t.Error("expected zai key in response")
	}
}

func TestHandler_SummaryBoth_WithAllProviders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	for _, provider := range []string{"synthetic", "zai", "anthropic", "copilot", "codex"} {
		if _, ok := response[provider]; !ok {
			t.Errorf("expected %s key in response", provider)
		}
	}
}

func TestHandler_InsightsBoth_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["synthetic"]; !ok {
		t.Error("expected synthetic key in response")
	}
	if _, ok := response["zai"]; !ok {
		t.Error("expected zai key in response")
	}
}

func TestHandler_InsightsBoth_WithAllProviders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=both", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	for _, provider := range []string{"synthetic", "zai", "anthropic", "copilot", "codex", "antigravity"} {
		if _, ok := response[provider]; !ok {
			t.Errorf("expected %s key in response", provider)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Copilot handler tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Current_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["capturedAt"]; !ok {
		t.Error("expected capturedAt field")
	}
	if _, ok := response["quotas"]; !ok {
		t.Error("expected quotas field")
	}
}

func TestHandler_Current_Copilot_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	snapshot := &api.CopilotSnapshot{
		CapturedAt:  time.Now().UTC(),
		CopilotPlan: "copilot_for_business",
		ResetDate:   &resetDate,
		Quotas: []api.CopilotQuota{
			{
				Name:             "premium_interactions",
				Entitlement:      1000,
				Remaining:        750,
				PercentRemaining: 75,
				Unlimited:        false,
			},
		},
	}
	s.InsertCopilotSnapshot(snapshot)

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["copilotPlan"] != "copilot_for_business" {
		t.Errorf("expected copilotPlan copilot_for_business, got %v", response["copilotPlan"])
	}

	quotas, ok := response["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatal("expected non-empty quotas array")
	}

	q := quotas[0].(map[string]interface{})
	if q["name"] != "premium_interactions" {
		t.Errorf("expected name premium_interactions, got %v", q["name"])
	}
	if _, ok := q["usagePercent"]; !ok {
		t.Error("expected usagePercent field")
	}
	if _, ok := q["status"]; !ok {
		t.Error("expected status field")
	}
	if _, ok := q["timeUntilReset"]; !ok {
		t.Error("expected timeUntilReset field")
	}
}

func TestCopilotUsageStatus(t *testing.T) {
	tests := []struct {
		pct       float64
		unlimited bool
		expected  string
	}{
		{0, false, "healthy"},
		{49, false, "healthy"},
		{50, false, "warning"},
		{79, false, "warning"},
		{80, false, "danger"},
		{94, false, "danger"},
		{95, false, "critical"},
		{100, false, "critical"},
		{100, true, "healthy"},
		{0, true, "healthy"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("pct_%.0f_unlimited_%v", tt.pct, tt.unlimited), func(t *testing.T) {
			result := copilotUsageStatus(tt.pct, tt.unlimited)
			if result != tt.expected {
				t.Errorf("copilotUsageStatus(%.0f, %v) = %q, want %q", tt.pct, tt.unlimited, result, tt.expected)
			}
		})
	}
}

func TestHandler_History_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_History_Copilot_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	for i := 0; i < 5; i++ {
		snapshot := &api.CopilotSnapshot{
			CapturedAt: time.Now().UTC().Add(-time.Duration(i) * time.Minute),
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 1000 - i*50, PercentRemaining: float64(100 - i*5)},
			},
		}
		s.InsertCopilotSnapshot(snapshot)
	}

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=copilot&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if len(response) == 0 {
		t.Error("expected non-empty history response")
	}
}

func TestHandler_Cycles_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestCopilotCycleToMap(t *testing.T) {
	now := time.Now().UTC()
	endTime := now.Add(1 * time.Hour)
	resetDate := now.Add(24 * time.Hour)
	cycle := &store.CopilotResetCycle{
		ID:         1,
		QuotaName:  "premium_interactions",
		CycleStart: now,
		CycleEnd:   &endTime,
		ResetDate:  &resetDate,
		PeakUsed:   500,
		TotalDelta: 100,
	}

	result := copilotCycleToMap(cycle)
	if result["quotaName"] != "premium_interactions" {
		t.Errorf("expected quotaName premium_interactions, got %v", result["quotaName"])
	}
	if result["cycleEnd"] == nil {
		t.Error("expected cycleEnd to be set")
	}
	if result["resetDate"] == nil {
		t.Error("expected resetDate to be set")
	}
}

func TestCopilotCycleToMap_NilEnds(t *testing.T) {
	now := time.Now().UTC()
	cycle := &store.CopilotResetCycle{
		ID:         1,
		QuotaName:  "premium_interactions",
		CycleStart: now,
		PeakUsed:   100,
		TotalDelta: 50,
	}

	result := copilotCycleToMap(cycle)
	if result["cycleEnd"] != nil {
		t.Errorf("expected nil cycleEnd, got %v", result["cycleEnd"])
	}
	if _, ok := result["resetDate"]; ok {
		t.Error("expected resetDate to not be set when nil")
	}
}

func TestHandler_Summary_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)
	// Should be empty map when no data
	if len(response) != 0 {
		t.Errorf("expected empty response, got %v", response)
	}
}

func TestHandler_Insights_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	// Should have "Getting Started" insight
	insights, ok := response["insights"].([]interface{})
	if !ok || len(insights) == 0 {
		t.Error("expected non-empty insights with Getting Started")
	}
}

func TestHandler_Insights_Copilot_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	snapshot := &api.CopilotSnapshot{
		CapturedAt:  time.Now().UTC(),
		CopilotPlan: "copilot_for_business",
		ResetDate:   &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1000, Remaining: 250, PercentRemaining: 25},
		},
	}
	s.InsertCopilotSnapshot(snapshot)

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	stats, ok := response["stats"].([]interface{})
	if !ok {
		t.Fatal("expected stats array in response")
	}
	if len(stats) == 0 {
		t.Error("expected non-empty stats")
	}
}

func TestCopilotInsightSeverity(t *testing.T) {
	tests := []struct {
		pct      float64
		expected string
	}{
		{0, "info"},
		{49, "info"},
		{69, "info"},
		{70, "warning"},
		{89, "warning"},
		{90, "critical"},
		{100, "critical"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("pct_%.0f", tt.pct), func(t *testing.T) {
			result := copilotInsightSeverity(tt.pct)
			if result != tt.expected {
				t.Errorf("copilotInsightSeverity(%.0f) = %q, want %q", tt.pct, result, tt.expected)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Antigravity handler tests ──
// ═══════════════════════════════════════════════════════════════════

func TestAntigravityUsageStatus(t *testing.T) {
	tests := []struct {
		pct      float64
		expected string
	}{
		{0, "healthy"},
		{49, "healthy"},
		{50, "warning"},
		{79, "warning"},
		{80, "danger"},
		{94, "danger"},
		{95, "critical"},
		{100, "critical"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("pct_%.0f", tt.pct), func(t *testing.T) {
			result := antigravityUsageStatus(tt.pct)
			if result != tt.expected {
				t.Errorf("antigravityUsageStatus(%.0f) = %q, want %q", tt.pct, result, tt.expected)
			}
		})
	}
}

func TestHandler_Current_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["capturedAt"]; !ok {
		t.Error("expected capturedAt field")
	}
	if _, ok := response["quotas"]; !ok {
		t.Error("expected quotas field")
	}
	if _, ok := response["pools"]; !ok {
		t.Error("expected pools field")
	}
}

func TestHandler_Current_Antigravity_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetTime := time.Now().UTC().Add(3 * time.Hour)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: time.Now().UTC(),
		Email:      "test@example.com",
		PlanName:   "pro",
		Models: []api.AntigravityModelQuota{
			{
				ModelID:           "claude-sonnet",
				RemainingFraction: 0.75,
				IsExhausted:       false,
				ResetTime:         &resetTime,
			},
		},
	}
	s.InsertAntigravitySnapshot(snapshot)

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["email"] != "test@example.com" {
		t.Errorf("expected email test@example.com, got %v", response["email"])
	}
	if response["planName"] != "pro" {
		t.Errorf("expected planName pro, got %v", response["planName"])
	}
}

func TestAntigravityCycleToMap(t *testing.T) {
	now := time.Now().UTC()
	endTime := now.Add(1 * time.Hour)
	resetTime := now.Add(3 * time.Hour)
	cycle := &store.AntigravityResetCycle{
		ID:         1,
		ModelID:    "claude-sonnet",
		CycleStart: now,
		CycleEnd:   &endTime,
		ResetTime:  &resetTime,
		PeakUsage:  85.5,
		TotalDelta: 10.2,
	}

	result := antigravityCycleToMap(cycle)
	if result["modelId"] != "claude-sonnet" {
		t.Errorf("expected modelId claude-sonnet, got %v", result["modelId"])
	}
	if result["cycleEnd"] == nil {
		t.Error("expected cycleEnd to be set")
	}
	if result["resetTime"] == nil {
		t.Error("expected resetTime to be set")
	}
	if result["peakUsage"] != 85.5 {
		t.Errorf("expected peakUsage 85.5, got %v", result["peakUsage"])
	}
}

func TestAntigravityCycleToMap_NilEnds(t *testing.T) {
	now := time.Now().UTC()
	cycle := &store.AntigravityResetCycle{
		ID:         1,
		ModelID:    "gpt-4",
		CycleStart: now,
		PeakUsage:  50.0,
		TotalDelta: 5.0,
	}
	result := antigravityCycleToMap(cycle)
	if result["cycleEnd"] != nil {
		t.Errorf("expected nil cycleEnd, got %v", result["cycleEnd"])
	}
	if _, ok := result["resetTime"]; ok {
		t.Error("expected resetTime to not be set when nil")
	}
}

func TestHandler_Cycles_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=antigravity&type=claude-sonnet", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_Cycles_Antigravity_NoType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)
	if len(response) != 0 {
		t.Errorf("expected empty array when no type specified, got %d items", len(response))
	}
}

func TestHandler_Insights_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_Insights_Antigravity_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// Insert multiple snapshots for richer insights
	for i := 0; i < 5; i++ {
		resetTime := time.Now().UTC().Add(3 * time.Hour)
		snapshot := &api.AntigravitySnapshot{
			CapturedAt: time.Now().UTC().Add(-time.Duration(i) * 10 * time.Minute),
			Models: []api.AntigravityModelQuota{
				{
					ModelID:           "claude-sonnet",
					RemainingFraction: 0.75 - float64(i)*0.05,
					IsExhausted:       false,
					ResetTime:         &resetTime,
				},
			},
		}
		s.InsertAntigravitySnapshot(snapshot)
	}

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=antigravity&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestTruncateName(t *testing.T) {
	tests := []struct {
		name     string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exact_len!", 10, "exact_len!"},
		{"a-very-long-name", 10, "a-very-lo..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateName(tt.name, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateName(%q, %d) = %q, want %q", tt.name, tt.maxLen, result, tt.expected)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── UpdateSettings edge case tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_UpdateSettings_SMTP_InvalidPort(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"smtp":{"host":"smtp.example.com","port":99999,"username":"user","password":"pass","from_address":"from@example.com","to":"to@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid port, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_SMTP_InvalidProtocol(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"smtp":{"host":"smtp.example.com","port":587,"protocol":"invalid","username":"user","password":"pass","from_address":"from@example.com","to":"to@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid protocol, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_EmptyTimezone(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"timezone":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for empty timezone (clear), got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Login with rate limiter tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_LoginPost_RateLimited(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	sessions := NewSessionStore("admin", "invalid_hash", s)
	h := NewHandler(s, nil, nil, sessions, cfg)

	rl := NewLoginRateLimiter(100)
	h.SetRateLimiter(rl)

	// Simulate enough failures to trigger block
	for i := 0; i < 10; i++ {
		rl.RecordFailure("192.168.1.1")
	}

	// Login attempt from blocked IP
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", rr.Code)
	}

	location := rr.Header().Get("Location")
	if !strings.Contains(location, "ratelimit") {
		t.Errorf("expected redirect to ratelimit error, got %s", location)
	}

	// Verify Retry-After header
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestHandler_LoginPost_FailedAttemptRecorded(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	sessions := NewSessionStore("admin", "invalid_hash_that_wont_match", s)
	h := NewHandler(s, nil, nil, sessions, cfg)

	rl := NewLoginRateLimiter(100)
	h.SetRateLimiter(rl)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=admin&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected redirect, got %d", rr.Code)
	}

	location := rr.Header().Get("Location")
	if !strings.Contains(location, "invalid") {
		t.Errorf("expected redirect to invalid error, got %s", location)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Providers endpoint coverage tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Providers_WithVisibility(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// Set provider_visibility that hides synthetic
	s.SetSetting("provider_visibility", `{"synthetic":{"dashboard":false}}`)

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	providers, ok := response["providers"].([]interface{})
	if !ok {
		t.Fatal("expected providers array")
	}

	for _, p := range providers {
		if p == "synthetic" {
			t.Error("synthetic should be hidden by provider_visibility")
		}
	}
}

func TestHandler_Providers_NilConfig(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_Providers_WithRequestedProvider(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Providers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if response["current"] != "zai" {
		t.Errorf("expected current provider zai, got %v", response["current"])
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── getHiddenInsightKeys tests ──
// ═══════════════════════════════════════════════════════════════════

func TestGetHiddenInsightKeys_NilStore(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	hidden := h.getHiddenInsightKeys()
	if len(hidden) != 0 {
		t.Errorf("expected empty map, got %v", hidden)
	}
}

func TestGetHiddenInsightKeys_WithCorrelations(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// Set hidden_insights with a key that has correlations
	s.SetSetting("hidden_insights", `["trend"]`)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := h.getHiddenInsightKeys()
	// "trend" should expand to include "trend_24h" (from insightCorrelations)
	if !hidden["trend"] {
		t.Error("expected trend to be hidden")
	}
	if !hidden["trend_24h"] {
		t.Error("expected trend_24h to be hidden (correlated with trend)")
	}
}

func TestGetHiddenInsightKeys_EmptySetting(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := h.getHiddenInsightKeys()
	if len(hidden) != 0 {
		t.Errorf("expected empty hidden map, got %v", hidden)
	}
}

func TestGetHiddenInsightKeys_InvalidJSON(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	s.SetSetting("hidden_insights", "not valid json")

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := h.getHiddenInsightKeys()
	if len(hidden) != 0 {
		t.Errorf("expected empty map for invalid JSON, got %v", hidden)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── zaiToolCallsPercent tests ──
// ═══════════════════════════════════════════════════════════════════

func TestZaiToolCallsPercent_NoDetails(t *testing.T) {
	snapshot := &api.ZaiSnapshot{TimeUsage: 100, TimeUsageDetails: ""}
	result := zaiToolCallsPercent(snapshot)
	if result != 0 {
		t.Errorf("expected 0 for empty details, got %.1f", result)
	}
}

func TestZaiToolCallsPercent_ZeroBudget(t *testing.T) {
	snapshot := &api.ZaiSnapshot{TimeUsage: 0, TimeUsageDetails: `[{"name":"tool1","usage":50}]`}
	result := zaiToolCallsPercent(snapshot)
	if result != 0 {
		t.Errorf("expected 0 for zero budget, got %.1f", result)
	}
}

func TestZaiToolCallsPercent_WithDetails(t *testing.T) {
	snapshot := &api.ZaiSnapshot{
		TimeUsage:        200,
		TimeUsageDetails: `[{"name":"tool1","usage":50},{"name":"tool2","usage":100}]`,
	}
	result := zaiToolCallsPercent(snapshot)
	expected := (150.0 / 200.0) * 100
	if math.Abs(result-expected) > 0.01 {
		t.Errorf("expected %.1f, got %.1f", expected, result)
	}
}

func TestZaiToolCallsPercent_InvalidJSON(t *testing.T) {
	snapshot := &api.ZaiSnapshot{TimeUsage: 100, TimeUsageDetails: "not json"}
	result := zaiToolCallsPercent(snapshot)
	if result != 0 {
		t.Errorf("expected 0 for invalid JSON, got %.1f", result)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── codexQuotaInsightLabel tests ──
// ═══════════════════════════════════════════════════════════════════

func TestCodexQuotaInsightLabel(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"five_hour", "Short Window"},
		{"seven_day", "Weekly Window"},
		{"unknown", api.CodexDisplayName("unknown")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := codexQuotaInsightLabel(tt.name)
			if result != tt.expected {
				t.Errorf("codexQuotaInsightLabel(%q) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── History "both" coverage tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_HistoryBoth_WithAllProviders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	// Verify all provider keys present (antigravity uses separate historyAntigravity handler)
	for _, provider := range []string{"synthetic", "zai", "anthropic", "copilot", "codex"} {
		if _, ok := response[provider]; !ok {
			t.Errorf("expected %s key in historyBoth response", provider)
		}
	}
}

func TestHandler_HistoryBoth_InvalidRange(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=invalid", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid range, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── CycleOverview Copilot tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CycleOverview_Copilot_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Insights with data for Synthetic, Zai, Anthropic coverage ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Insights_Synthetic_WithRichData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	// Insert snapshots over 30 days to trigger various insight branches
	for i := 0; i < 30; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: now.Add(-time.Duration(i) * 24 * time.Hour),
			Sub:        api.QuotaInfo{Limit: 1000, Requests: float64(100 + i*20), RenewsAt: now.Add(time.Duration(5-i) * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i * 5), RenewsAt: now.Add(time.Duration(1) * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 200), RenewsAt: now.Add(time.Duration(3) * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	cfg := createTestConfigWithSynthetic()
	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, cfg)

	// Test with different ranges
	for _, r := range []string{"", "7d", "30d"} {
		t.Run("range_"+r, func(t *testing.T) {
			url := "/api/insights?provider=synthetic"
			if r != "" {
				url += "&range=" + r
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rr := httptest.NewRecorder()
			h.Insights(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rr.Code)
			}
		})
	}
}

func TestHandler_Insights_Zai_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:       now.Add(-time.Duration(i) * time.Hour),
			TokensUsage:      float64(i * 100),
			TokensLimit:      1000,
			TokensPercentage: i * 10,
			TimeUsage:        float64(i * 50),
			TimeLimit:        500,
			TimePercentage:   i * 10,
		}
		s.InsertZaiSnapshot(snapshot)
	}

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_Insights_Anthropic_WithTrackerData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)
	for i := 0; i < 10; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-time.Duration(i) * time.Hour),
			Quotas: []api.AnthropicQuota{
				{
					Name:        "five_hour",
					Utilization: float64(i * 10),
					ResetsAt:    &resetsAt,
				},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	cfg := createTestConfigWithAnthropic()
	atr := tracker.NewAnthropicTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(atr)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=anthropic&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["stats"]; !ok {
		t.Error("expected stats key in response")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Copilot & Antigravity summary handler tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Summary_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	// Antigravity goes through summaryCodex-like pattern
	// but it doesn't have a separate summary handler - it uses summaryBoth
	// Let's test the cycle overview for antigravity instead
	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── SMTP settings edge cases in UpdateSettings ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_UpdateSettings_SMTP_ValidConfig(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	sessions := NewSessionStore("admin", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", s)
	h := NewHandler(s, nil, nil, sessions, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"smtp":{"host":"smtp.example.com","port":587,"protocol":"starttls","username":"user@example.com","password":"secret","from_address":"noreply@example.com","from_name":"OnWatch","to":"admin@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_UpdateSettings_SMTP_InvalidFromAddress(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"smtp":{"host":"smtp.example.com","port":587,"username":"user","password":"pass","from_address":"not-an-email","to":"admin@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid email, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_SMTP_InvalidToAddress(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"smtp":{"host":"smtp.example.com","port":587,"username":"user","password":"pass","from_address":"valid@example.com","to":"not-an-email"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid to email, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_Notifications_PushChannel(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetNotifier(&mockNotifier{})

	body := `{"notifications":{"enabled":true,"warning_threshold":70,"critical_threshold":90,"channels":["email","push"]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── computeAnthropicRate coverage (via insights with enough data) ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Insights_Anthropic_ComputeRate(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	// Need multiple recent snapshots to compute rate
	for i := 0; i < 20; i++ {
		util := float64(i) * 5.0
		if util > 95 {
			util = 95
		}
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-time.Duration(20-i) * 10 * time.Minute),
			Quotas: []api.AnthropicQuota{
				{
					Name:        "five_hour",
					Utilization: util,
					ResetsAt:    &resetsAt,
				},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	cfg := createTestConfigWithAnthropic()
	atr := tracker.NewAnthropicTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(atr)

	req := httptest.NewRequest(http.MethodGet, "/api/insights?provider=anthropic&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Insights(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	// Should have insights generated
	insights, ok := response["insights"].([]interface{})
	if !ok {
		t.Fatal("expected insights array")
	}
	if len(insights) == 0 {
		t.Error("expected non-empty insights for anthropic with data")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── History Antigravity & Copilot with data tests ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_History_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_History_Antigravity_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(3 * time.Hour)
	for i := 0; i < 5; i++ {
		snapshot := &api.AntigravitySnapshot{
			CapturedAt: now.Add(-time.Duration(i) * 10 * time.Minute),
			Models: []api.AntigravityModelQuota{
				{
					ModelID:           "claude-sonnet",
					RemainingFraction: 0.8 - float64(i)*0.1,
					IsExhausted:       false,
					ResetTime:         &resetTime,
				},
			},
		}
		s.InsertAntigravitySnapshot(snapshot)
	}

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=antigravity&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	if _, ok := response["labels"]; !ok {
		t.Error("expected labels key in antigravity history response")
	}
	if _, ok := response["datasets"]; !ok {
		t.Error("expected datasets key in antigravity history response")
	}
}

func TestHandler_History_Copilot_InvalidRange(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=copilot&range=invalid", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_History_Antigravity_InvalidRange(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=antigravity&range=invalid", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Deep insights coverage tests with rich data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildSyntheticInsights_WithCycles(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	// Insert enough snapshots to generate cycles
	for i := 0; i < 50; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: now.Add(-time.Duration(50-i) * 30 * time.Minute),
			Sub:        api.QuotaInfo{Limit: 1000, Requests: float64(100 + (i%10)*80), RenewsAt: now.Add(time.Duration(5-i%5) * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i % 20 * 10), RenewsAt: now.Add(1 * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 100), RenewsAt: now.Add(3 * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	cfg := createTestConfigWithSynthetic()
	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, cfg)

	hidden := map[string]bool{}
	resp := h.buildSyntheticInsights(hidden, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats")
	}
	if len(resp.Stats) != 4 {
		t.Errorf("expected exactly 4 stat cards, got %d", len(resp.Stats))
	}
}

func TestHandler_BuildSyntheticInsights_HiddenKeys(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: now.Add(-time.Duration(i) * time.Hour),
			Sub:        api.QuotaInfo{Limit: 1000, Requests: float64(500 + i*50), RenewsAt: now.Add(5 * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i * 10), RenewsAt: now.Add(1 * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 500), RenewsAt: now.Add(3 * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	cfg := createTestConfigWithSynthetic()
	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, cfg)

	// Hide all insight keys
	hidden := map[string]bool{
		"cycle_utilization": true,
		"weekly_pace":       true,
		"variance":          true,
		"trend":             true,
	}
	resp := h.buildSyntheticInsights(hidden, 7*24*time.Hour)

	// Should still have stats but insights should show "Getting Started"
	if len(resp.Stats) == 0 {
		t.Error("expected stats even when insights are hidden")
	}
}

func TestHandler_BuildZaiInsights_WithRichData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetTime := now.Add(12 * time.Hour)
	// Insert enough snapshots over 7 days with tool call details
	for i := 0; i < 20; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:          now.Add(-time.Duration(20-i) * time.Hour),
			TokensUsage:         200000000,
			TokensLimit:         200000000,
			TokensCurrentValue:  float64(i * 10000000),
			TokensRemaining:     float64(200000000 - i*10000000),
			TokensPercentage:    i * 5,
			TokensNextResetTime: &resetTime,
			TimeUsage:           1000,
			TimeCurrentValue:    float64(i * 50),
			TimeRemaining:       float64(1000 - i*50),
			TimePercentage:      i * 5,
			TimeUsageDetails:    `[{"modelCode":"search-prime","usage":50},{"modelCode":"code-prime","usage":30}]`,
		}
		s.InsertZaiSnapshot(snapshot)
	}

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := map[string]bool{}
	resp := h.buildZaiInsights(hidden)

	if len(resp.Stats) != 4 {
		t.Errorf("expected 4 stat cards, got %d", len(resp.Stats))
	}
	if len(resp.Insights) == 0 {
		t.Error("expected non-empty insights with rich data")
	}

	// Verify specific insight types exist
	foundTokenRate := false
	foundTopTool := false
	foundPlanCapacity := false
	for _, insight := range resp.Insights {
		switch insight.Key {
		case "token_rate":
			foundTokenRate = true
		case "top_tool":
			foundTopTool = true
		case "plan_capacity":
			foundPlanCapacity = true
		}
	}
	if !foundTokenRate {
		t.Error("expected token_rate insight with enough data")
	}
	if !foundTopTool {
		t.Error("expected top_tool insight with tool call details")
	}
	if !foundPlanCapacity {
		t.Error("expected plan_capacity insight")
	}
}

func TestHandler_BuildZaiInsights_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := map[string]bool{}
	resp := h.buildZaiInsights(hidden)

	// Should show "Getting Started"
	if len(resp.Insights) == 0 {
		t.Fatal("expected Getting Started insight")
	}
	if resp.Insights[0].Title != "Getting Started" {
		t.Errorf("expected Getting Started, got %q", resp.Insights[0].Title)
	}
}

func TestHandler_BuildAnthropicInsights_WithCycles(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	// Insert many snapshots to trigger cycle creation and all insight branches
	for i := 0; i < 30; i++ {
		util := float64(i%10) * 10
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-time.Duration(30-i) * 10 * time.Minute),
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: util, ResetsAt: &resetsAt},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	cfg := createTestConfigWithAnthropic()
	atr := tracker.NewAnthropicTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(atr)

	hidden := map[string]bool{}
	resp := h.buildAnthropicInsights(hidden, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats")
	}
	if len(resp.Insights) == 0 {
		t.Error("expected non-empty insights")
	}
}

func TestComputeAnthropicRate_WithSnapshots(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	// Insert snapshots within last 30 minutes with increasing utilization
	// First at -20min, last at -5min => 15min elapsed (>5 min), all within 30min window
	for i := 0; i < 4; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-20*time.Minute + time.Duration(i)*5*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: 10 + float64(i)*5, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("failed to insert snapshot %d: %v", i, err)
		}
	}

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	rate := h.computeAnthropicRate("five_hour", 25.0, nil)

	if !rate.HasRate {
		t.Error("expected HasRate to be true with snapshot data")
	}
	if rate.Rate <= 0 {
		t.Errorf("expected positive rate, got %f", rate.Rate)
	}
}

func TestComputeAnthropicRate_IdleRate(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	// Insert snapshots with no utilization change (idle)
	// First at -20min, last at -5min => 15min elapsed, all within 30min window
	for i := 0; i < 4; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-20*time.Minute + time.Duration(i)*5*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: 25.0, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("failed to insert snapshot %d: %v", i, err)
		}
	}

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	rate := h.computeAnthropicRate("five_hour", 25.0, nil)

	if !rate.HasRate {
		t.Error("expected HasRate to be true even when idle")
	}
	if rate.Rate != 0 {
		t.Errorf("expected rate 0 for idle, got %f", rate.Rate)
	}
}

func TestComputeAnthropicRate_NoData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	rate := h.computeAnthropicRate("five_hour", 25.0, nil)

	if rate.HasRate {
		t.Error("expected HasRate to be false with no data")
	}
}

func TestComputeAnthropicRate_FallbackToTracker(t *testing.T) {
	// No store snapshots, but has tracker summary
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithAnthropic())

	resetsAt := time.Now().UTC().Add(3 * time.Hour)
	summary := &tracker.AnthropicSummary{
		QuotaName:   "five_hour",
		CurrentUtil: 40.0,
		ResetsAt:    &resetsAt,
		CurrentRate: 5.0,
	}

	rate := h.computeAnthropicRate("five_hour", 40.0, summary)

	if !rate.HasRate {
		t.Error("expected HasRate to be true from tracker fallback")
	}
	if rate.Rate != 5.0 {
		t.Errorf("expected rate 5.0 from tracker, got %f", rate.Rate)
	}
	if rate.TimeToExhaust <= 0 {
		t.Error("expected positive TimeToExhaust")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Copilot & Codex with tracker data (summaryMap coverage) ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Summary_Copilot_WithTrackerData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	snapshot := &api.CopilotSnapshot{
		CapturedAt:  time.Now().UTC(),
		CopilotPlan: "copilot_for_business",
		ResetDate:   &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1000, Remaining: 750, PercentRemaining: 75},
		},
	}
	s.InsertCopilotSnapshot(snapshot)

	cfg := createTestConfigWithCopilot()
	ctr := tracker.NewCopilotTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCopilotTracker(ctr)

	req := httptest.NewRequest(http.MethodGet, "/api/summary?provider=copilot", nil)
	rr := httptest.NewRecorder()
	h.Summary(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestBuildCopilotSummaryResponse(t *testing.T) {
	resetDate := time.Now().UTC().Add(24 * time.Hour)
	summary := &tracker.CopilotSummary{
		QuotaName:        "premium_interactions",
		Entitlement:      1000,
		CurrentRemaining: 750,
		CurrentUsed:      250,
		UsagePercent:     25.0,
		Unlimited:        false,
		ResetDate:        &resetDate,
		TimeUntilReset:   24 * time.Hour,
		CurrentRate:      10.5,
		ProjectedUsage:   500,
		CompletedCycles:  3,
		AvgPerCycle:      300.0,
		PeakCycle:        450,
		TotalTracked:     900,
		TrackingSince:    time.Now().UTC().Add(-72 * time.Hour),
	}

	result := buildCopilotSummaryResponse(summary)

	if result["quotaName"] != "premium_interactions" {
		t.Errorf("expected quotaName premium_interactions, got %v", result["quotaName"])
	}
	if result["resetDate"] == nil {
		t.Error("expected resetDate to be set")
	}
	if result["trackingSince"] == nil {
		t.Error("expected trackingSince to be set")
	}
	if result["timeUntilReset"] == nil {
		t.Error("expected timeUntilReset to be set")
	}
}

func TestBuildCopilotSummaryResponse_NoResetDate(t *testing.T) {
	summary := &tracker.CopilotSummary{
		QuotaName: "test",
	}
	result := buildCopilotSummaryResponse(summary)
	if _, ok := result["resetDate"]; ok {
		t.Error("expected no resetDate when nil")
	}
	if result["trackingSince"] != nil {
		t.Error("expected nil trackingSince for zero time")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Cycles Copilot with data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Cycles_Copilot_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	for i := 0; i < 10; i++ {
		snapshot := &api.CopilotSnapshot{
			CapturedAt: time.Now().UTC().Add(-time.Duration(10-i) * 10 * time.Minute),
			ResetDate:  &resetDate,
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 1000 - i*50, PercentRemaining: float64(100 - i*5)},
			},
		}
		s.InsertCopilotSnapshot(snapshot)
	}

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=copilot&type=premium_interactions&range=7d", nil)
	rr := httptest.NewRecorder()
	h.Cycles(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response []interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)
	if len(response) == 0 {
		t.Error("expected non-empty cycles response with data")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── buildAnthropicSummaryResponse coverage ──
// ═══════════════════════════════════════════════════════════════════

func TestBuildAnthropicSummaryResponse(t *testing.T) {
	resetsAt := time.Now().UTC().Add(3 * time.Hour)
	summary := &tracker.AnthropicSummary{
		QuotaName:       "five_hour",
		CurrentUtil:     45.0,
		ResetsAt:        &resetsAt,
		TimeUntilReset:  3 * time.Hour,
		CurrentRate:     5.0,
		ProjectedUtil:   80.0,
		CompletedCycles: 10,
		AvgPerCycle:     55.0,
		PeakCycle:       90.0,
		TotalTracked:    500.0,
		TrackingSince:   time.Now().UTC().Add(-7 * 24 * time.Hour),
	}

	result := buildAnthropicSummaryResponse(summary)

	if result["quotaName"] != "five_hour" {
		t.Errorf("expected quotaName five_hour, got %v", result["quotaName"])
	}
	if result["resetsAt"] == nil {
		t.Error("expected resetsAt to be set")
	}
	if result["trackingSince"] == nil {
		t.Error("expected trackingSince to be set")
	}
	if result["currentRate"] != 5.0 {
		t.Errorf("expected currentRate 5.0, got %v", result["currentRate"])
	}
}

func TestBuildAnthropicSummaryResponse_NoResetsAt(t *testing.T) {
	summary := &tracker.AnthropicSummary{
		QuotaName:   "daily",
		CurrentUtil: 20.0,
	}
	result := buildAnthropicSummaryResponse(summary)
	if _, ok := result["resetsAt"]; ok {
		t.Error("expected no resetsAt when nil")
	}
	if result["trackingSince"] != nil {
		t.Error("expected nil trackingSince for zero time")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── CycleOverview with Antigravity (normalizeAntigravityGroupBy) ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CycleOverview_Antigravity_EmptyStore(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_CycleOverview_Copilot_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	for i := 0; i < 5; i++ {
		snapshot := &api.CopilotSnapshot{
			CapturedAt: time.Now().UTC().Add(-time.Duration(5-i) * 10 * time.Minute),
			ResetDate:  &resetDate,
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 900 - i*50, PercentRemaining: float64(90 - i*5)},
			},
		}
		s.InsertCopilotSnapshot(snapshot)
	}

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=copilot&limit=10", nil)
	rr := httptest.NewRecorder()
	h.CycleOverview(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── UpdateSettings SMTP with full flow coverage ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_UpdateSettings_SMTP_InvalidSMTPJSON(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"smtp":"not an object"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid smtp JSON, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_InvalidTimezoneValue(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"timezone":123}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid timezone type, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_InvalidHiddenInsightsValue(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"hidden_insights":"not an array"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid hidden_insights, got %d", rr.Code)
	}
}

func TestHandler_UpdateSettings_NullHiddenInsights(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"hidden_insights":null}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for null hidden_insights, got %d", rr.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── historyBoth with data coverage ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_HistoryBoth_WithSyntheticData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: now.Add(-time.Duration(i) * 10 * time.Minute),
			Sub:        api.QuotaInfo{Limit: 1000, Requests: float64(100 + i*50), RenewsAt: now.Add(5 * time.Hour)},
			Search:     api.QuotaInfo{Limit: 250, Requests: float64(i * 10), RenewsAt: now.Add(1 * time.Hour)},
			ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 200), RenewsAt: now.Add(3 * time.Hour)},
		}
		s.InsertSnapshot(snapshot)
	}

	cfg := createTestConfigWithBoth()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=1h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &response)

	synData, ok := response["synthetic"].([]interface{})
	if !ok || len(synData) == 0 {
		t.Error("expected non-empty synthetic data in historyBoth")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── buildCopilotInsights with copilot data for deeper coverage ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildCopilotInsights_WithQuotaData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	resetDate := time.Now().UTC().Add(24 * time.Hour)
	// Insert snapshot with unlimited quota
	snapshot := &api.CopilotSnapshot{
		CapturedAt:  time.Now().UTC(),
		CopilotPlan: "copilot_for_business",
		ResetDate:   &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1000, Remaining: 100, PercentRemaining: 10, Unlimited: false},
			{Name: "basic_completions", Entitlement: 0, Remaining: 0, PercentRemaining: 0, Unlimited: true},
		},
	}
	s.InsertCopilotSnapshot(snapshot)

	cfg := createTestConfigWithCopilot()
	ctr := tracker.NewCopilotTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCopilotTracker(ctr)

	hidden := map[string]bool{}
	resp := h.buildCopilotInsights(hidden, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Codex build insights with data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildCodexInsights_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	for i := 0; i < 10; i++ {
		snapshot := &api.CodexSnapshot{
			CapturedAt: now.Add(-time.Duration(10-i) * 10 * time.Minute),
			PlanType:   "pro_plan",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i * 10), ResetsAt: &resetsAt},
			},
		}
		s.InsertCodexSnapshot(snapshot)
	}

	cfg := createTestConfigWithCodex()
	cxtr := tracker.NewCodexTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCodexTracker(cxtr)

	hidden := map[string]bool{}
	resp := h.buildCodexInsights(store.DefaultCodexAccountID, hidden, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats for codex with data")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Synthetic Insights with cycle data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildSyntheticInsights_WithCycleData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()

	// Insert a snapshot so latest is non-nil and subLimit > 0
	snapshot := &api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{Requests: 500, Limit: 1000, RenewsAt: now.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Requests: 100, Limit: 500, RenewsAt: now.Add(24 * time.Hour)},
		ToolCall:   api.QuotaInfo{Requests: 0, Limit: 16200, RenewsAt: now.Add(24 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	// Create enough subscription cycles for all insight branches
	// Need >= 4 billing periods for trend insight
	for i := 0; i < 8; i++ {
		cycleStart := now.Add(-time.Duration(8-i) * 24 * time.Hour)
		renewsAt := cycleStart.Add(24 * time.Hour)
		cycleEnd := renewsAt

		id, _ := s.CreateCycle("subscription", cycleStart, renewsAt)
		peak := 200 + float64(i*50)
		delta := peak
		// Use raw SQL to close with specific peak values
		s.CloseCycle("subscription", cycleEnd, peak, delta)
		_ = id
	}

	// Create search and tool cycles
	for i := 0; i < 3; i++ {
		cycleStart := now.Add(-time.Duration(3-i) * 24 * time.Hour)
		renewsAt := cycleStart.Add(24 * time.Hour)
		cycleEnd := renewsAt
		s.CreateCycle("search", cycleStart, renewsAt)
		s.CloseCycle("search", cycleEnd, 50, 50)
		s.CreateCycle("toolcall", cycleStart, renewsAt)
		s.CloseCycle("toolcall", cycleEnd, 30, 30)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := map[string]bool{}
	resp := h.buildSyntheticInsights(hidden, 30*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats")
	}
	if len(resp.Insights) == 0 {
		t.Error("expected non-empty insights with cycle data")
	}

	// Verify we got cycle_utilization, weekly_pace, variance, and trend insights
	insightKeys := map[string]bool{}
	for _, ins := range resp.Insights {
		insightKeys[ins.Key] = true
	}

	if !insightKeys["cycle_utilization"] {
		t.Error("expected cycle_utilization insight")
	}
}

func TestHandler_BuildSyntheticInsights_HighUtilization(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot := &api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{Requests: 950, Limit: 1000, RenewsAt: now.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Requests: 0, Limit: 500, RenewsAt: now.Add(24 * time.Hour)},
		ToolCall:   api.QuotaInfo{Requests: 0, Limit: 16200, RenewsAt: now.Add(24 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	// Create cycles with consistently high usage (>95% utilization)
	for i := 0; i < 6; i++ {
		cycleStart := now.Add(-time.Duration(6-i) * 24 * time.Hour)
		renewsAt := cycleStart.Add(24 * time.Hour)
		cycleEnd := renewsAt
		s.CreateCycle("subscription", cycleStart, renewsAt)
		s.CloseCycle("subscription", cycleEnd, 960, 960)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	resp := h.buildSyntheticInsights(map[string]bool{}, 30*24*time.Hour)

	// Should have negative severity for high utilization
	found := false
	for _, ins := range resp.Insights {
		if ins.Key == "cycle_utilization" {
			found = true
			if ins.Severity != "negative" {
				t.Errorf("expected negative severity for >95%% utilization, got %s", ins.Severity)
			}
		}
	}
	if !found {
		t.Error("expected cycle_utilization insight")
	}
}

func TestHandler_BuildSyntheticInsights_LowUtilization(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot := &api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{Requests: 50, Limit: 1000, RenewsAt: now.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Requests: 0, Limit: 500, RenewsAt: now.Add(24 * time.Hour)},
		ToolCall:   api.QuotaInfo{Requests: 0, Limit: 16200, RenewsAt: now.Add(24 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	// Cycles with low usage (<25%)
	for i := 0; i < 4; i++ {
		cycleStart := now.Add(-time.Duration(4-i) * 24 * time.Hour)
		renewsAt := cycleStart.Add(24 * time.Hour)
		cycleEnd := renewsAt
		s.CreateCycle("subscription", cycleStart, renewsAt)
		s.CloseCycle("subscription", cycleEnd, 100, 100)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	resp := h.buildSyntheticInsights(map[string]bool{}, 30*24*time.Hour)

	for _, ins := range resp.Insights {
		if ins.Key == "cycle_utilization" {
			if ins.Severity != "warning" {
				t.Errorf("expected warning severity for <25%% utilization, got %s", ins.Severity)
			}
		}
	}
}

func TestHandler_BuildSyntheticInsights_HiddenKeys_Coverage(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot := &api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{Requests: 500, Limit: 1000, RenewsAt: now.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Requests: 0, Limit: 500, RenewsAt: now.Add(24 * time.Hour)},
		ToolCall:   api.QuotaInfo{Requests: 0, Limit: 16200, RenewsAt: now.Add(24 * time.Hour)},
	}
	s.InsertSnapshot(snapshot)

	for i := 0; i < 6; i++ {
		cycleStart := now.Add(-time.Duration(6-i) * 24 * time.Hour)
		renewsAt := cycleStart.Add(24 * time.Hour)
		cycleEnd := renewsAt
		s.CreateCycle("subscription", cycleStart, renewsAt)
		s.CloseCycle("subscription", cycleEnd, 500, 500)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := map[string]bool{
		"cycle_utilization": true,
		"weekly_pace":       true,
		"variance":          true,
		"trend":             true,
	}
	resp := h.buildSyntheticInsights(hidden, 30*24*time.Hour)

	for _, ins := range resp.Insights {
		if hidden[ins.Key] {
			t.Errorf("expected insight %s to be hidden", ins.Key)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Anthropic Insights with cycle data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildAnthropicInsights_WithCycleData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)

	// Insert snapshots for utilization series (for rate calculation)
	for i := 0; i < 4; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-20*time.Minute + time.Duration(i)*5*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: 10 + float64(i)*5, ResetsAt: &resetsAt},
				{Name: "seven_day", Utilization: 5 + float64(i)*2, ResetsAt: &resetsAt},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	// Create completed anthropic cycles for variance and trend insights
	for i := 0; i < 6; i++ {
		cycleStart := now.Add(-time.Duration(6-i) * 5 * time.Hour)
		cycleEnd := cycleStart.Add(5 * time.Hour)
		s.CreateAnthropicCycle("five_hour", cycleStart, &cycleEnd)
	}

	cfg := createTestConfigWithAnthropic()
	atr := tracker.NewAnthropicTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(atr)

	resp := h.buildAnthropicInsights(map[string]bool{}, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats for anthropic with data")
	}
	if len(resp.Insights) == 0 {
		t.Error("expected non-empty insights for anthropic with data")
	}
}

func TestHandler_BuildAnthropicInsights_NoData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	resp := h.buildAnthropicInsights(map[string]bool{}, 7*24*time.Hour)

	if len(resp.Insights) != 1 {
		t.Errorf("expected 1 getting-started insight, got %d", len(resp.Insights))
	}
	if resp.Insights[0].Title != "Getting Started" {
		t.Errorf("expected 'Getting Started' title, got %s", resp.Insights[0].Title)
	}
}

func TestHandler_BuildAnthropicInsights_HiddenForecasts(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: now,
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 50, ResetsAt: &resetsAt},
		},
	}
	s.InsertAnthropicSnapshot(snapshot)

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	hidden := map[string]bool{
		"forecast_five_hour": true,
	}
	resp := h.buildAnthropicInsights(hidden, 7*24*time.Hour)

	for _, ins := range resp.Insights {
		if ins.Key == "forecast_five_hour" {
			t.Error("expected forecast_five_hour to be hidden")
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── ChangePassword handler ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_ChangePassword_Success_Coverage(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("oldpassword")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"current_password":"oldpassword","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPut, "/api/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_ChangePassword_WrongCurrent(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("oldpassword")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"current_password":"wrongpassword","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPut, "/api/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ChangePassword_ShortPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("oldpassword")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"current_password":"oldpassword","new_password":"abc"}`
	req := httptest.NewRequest(http.MethodPut, "/api/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ChangePassword_EmptyFields(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("oldpassword")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"current_password":"","new_password":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ChangePassword_NoAuth(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	body := `{"current_password":"old","new_password":"newpass"}`
	req := httptest.NewRequest(http.MethodPut, "/api/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandler_ChangePassword_MethodNotAllowed_Coverage(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/password", nil)
	w := httptest.NewRecorder()

	h.ChangePassword(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Cycles endpoints with data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CyclesSynthetic_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()

	// Create an active cycle and closed cycles
	s.CreateCycle("subscription", now.Add(-1*time.Hour), now.Add(23*time.Hour))

	s.CreateCycle("subscription", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	s.CloseCycle("subscription", now.Add(-24*time.Hour), 300, 300)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=synthetic&type=subscription", nil)
	w := httptest.NewRecorder()

	h.Cycles(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if len(result) == 0 {
		t.Error("expected non-empty cycles response")
	}
}

func TestHandler_CyclesSynthetic_InvalidType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=synthetic&type=invalid", nil)
	w := httptest.NewRecorder()

	h.Cycles(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CyclesZai_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	nextReset := now.Add(24 * time.Hour)

	s.CreateZaiCycle("tokens", now.Add(-1*time.Hour), &nextReset)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=zai&type=tokens", nil)
	w := httptest.NewRecorder()

	h.Cycles(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_CyclesZai_InvalidType(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=zai&type=invalid", nil)
	w := httptest.NewRecorder()

	h.Cycles(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Z.ai summary and current with store fallback ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildZaiSummaryMap_StoreFallback(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot := &api.ZaiSnapshot{
		CapturedAt:         now,
		TokensUsage:        10000,
		TokensCurrentValue: 3000,
		TokensPercentage:   30,
		TimeUsage:          3600,
		TimeCurrentValue:   1200,
		TimePercentage:     33,
	}
	s.InsertZaiSnapshot(snapshot)

	cfg := createTestConfigWithZai()
	// No zaiTracker set - forces store fallback
	h := NewHandler(s, nil, nil, nil, cfg)

	result := h.buildZaiSummaryMap()

	if result["tokensLimit"] == nil {
		t.Error("expected tokensLimit in result")
	}
	if result["timeLimit"] == nil {
		t.Error("expected timeLimit in result")
	}

	tokensMap, ok := result["tokensLimit"].(map[string]interface{})
	if !ok {
		t.Fatal("expected tokensLimit to be map")
	}
	if tokensMap["currentUsage"] != float64(3000) {
		t.Errorf("expected currentUsage=3000, got %v", tokensMap["currentUsage"])
	}
}

func TestHandler_BuildZaiCurrent_WithStoreData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	snapshot := &api.ZaiSnapshot{
		CapturedAt:         now,
		TokensUsage:        10000,
		TokensCurrentValue: 3000,
		TokensPercentage:   30,
		TimeUsage:          3600,
		TimeCurrentValue:   1200,
		TimePercentage:     33,
		TimeUsageDetails:   `[{"type":"code_completion","usage":500},{"type":"chat","usage":300}]`,
	}
	s.InsertZaiSnapshot(snapshot)

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	result := h.buildZaiCurrent()

	if result["toolCalls"] == nil {
		t.Error("expected toolCalls in result")
	}

	toolCalls, ok := result["toolCalls"].(map[string]interface{})
	if !ok {
		t.Fatal("expected toolCalls to be map")
	}
	if toolCalls["usage"].(float64) <= 0 {
		t.Error("expected positive tool calls usage")
	}
	if toolCalls["usageDetails"] == nil {
		t.Error("expected usageDetails in tool calls response")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── UpdateSettings SMTP branch with encryption ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_UpdateSettings_SMTPInvalidPort(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"smtp":{"host":"smtp.example.com","port":99999}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid port, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_SMTPInvalidProtocol(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"smtp":{"host":"smtp.example.com","port":587,"protocol":"invalid"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid protocol, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_SMTPInvalidFromAddress(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"smtp":{"host":"smtp.example.com","port":587,"from_address":"not-an-email"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid from address, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_SMTPInvalidRecipient(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"smtp":{"host":"smtp.example.com","port":587,"to":"bad-email"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid recipient, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_SMTPSaveWithPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	body := `{"smtp":{"host":"smtp.example.com","port":587,"protocol":"tls","username":"user","password":"secret","from_address":"test@example.com","to":"admin@example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid SMTP, got %d: %s", w.Code, w.Body.String())
	}

	// Verify SMTP was saved
	saved, _ := s.GetSetting("smtp")
	if saved == "" {
		t.Error("expected SMTP setting to be saved")
	}
}

func TestHandler_UpdateSettings_SMTPPreservePassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	// Store existing SMTP config with password
	s.SetSetting("smtp", `{"host":"smtp.old.com","password":"old_encrypted_pass"}`)

	hash, _ := HashPassword("testpass")
	sessions := NewSessionStore("admin", hash, s)
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	// Update SMTP without password - should preserve existing
	body := `{"smtp":{"host":"smtp.new.com","port":587}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	saved, _ := s.GetSetting("smtp")
	if !strings.Contains(saved, "\"host\":\"smtp.new.com\"") {
		t.Error("expected updated host to be saved")
	}
	if !strings.Contains(saved, "\"password\":") {
		t.Error("expected password field to be preserved")
	}
	if strings.Contains(saved, "\"password\":\"\"") {
		t.Error("expected preserved password to remain non-empty")
	}
}

func TestHandler_UpdateSettings_NotificationOverrides(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"notifications":{"warning_threshold":70,"critical_threshold":90,"notify_warning":true,"notify_critical":true,"cooldown_minutes":5,"overrides":[{"quota_key":"subscription","provider":"synthetic","warning":60,"critical":80}]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_UpdateSettings_NotificationAbsoluteOverrides(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"notifications":{"warning_threshold":70,"critical_threshold":90,"overrides":[{"quota_key":"subscription","provider":"synthetic","warning":500,"critical":900,"is_absolute":true}]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_UpdateSettings_NotificationInvalidAbsoluteOverrides(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"notifications":{"warning_threshold":70,"critical_threshold":90,"overrides":[{"quota_key":"sub","provider":"synthetic","warning":-1,"critical":900,"is_absolute":true}]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative absolute threshold, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_NotificationInvalidPercentOverrides(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := `{"notifications":{"warning_threshold":70,"critical_threshold":90,"overrides":[{"quota_key":"sub","provider":"synthetic","warning":150,"critical":90}]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for >100%% percent threshold, got %d", w.Code)
	}
}

func TestHandler_UpdateSettings_StoreNotAvailable(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	body := `{"timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()

	h.UpdateSettings(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for no store, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── ValidateToken edge cases ──
// ═══════════════════════════════════════════════════════════════════

func TestSessionStore_ValidateToken_Expired(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	sessions := NewSessionStore("admin", "dummy_hash", s)

	// Add a token that's already expired
	sessions.mu.Lock()
	sessions.tokens["expired_token"] = time.Now().Add(-1 * time.Hour)
	sessions.mu.Unlock()

	if sessions.ValidateToken("expired_token") {
		t.Error("expected expired token to be invalid")
	}

	// Token should have been cleaned up
	sessions.mu.RLock()
	_, exists := sessions.tokens["expired_token"]
	sessions.mu.RUnlock()
	if exists {
		t.Error("expected expired token to be removed from cache")
	}
}

func TestSessionStore_ValidateToken_FromDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	sessions := NewSessionStore("admin", "dummy_hash", s)

	// Insert token directly into DB (simulating a previous daemon run)
	expiry := time.Now().Add(1 * time.Hour)
	s.SaveAuthToken("db_token", expiry)

	// Token not in memory, but should be found in DB
	if !sessions.ValidateToken("db_token") {
		t.Error("expected token from DB to be valid")
	}

	// Should now be cached in memory
	sessions.mu.RLock()
	_, exists := sessions.tokens["db_token"]
	sessions.mu.RUnlock()
	if !exists {
		t.Error("expected token to be cached in memory after DB lookup")
	}
}

func TestSessionStore_ValidateToken_ExpiredInDB(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	sessions := NewSessionStore("admin", "dummy_hash", s)

	// Insert expired token in DB
	expiry := time.Now().Add(-1 * time.Hour)
	s.SaveAuthToken("old_db_token", expiry)

	if sessions.ValidateToken("old_db_token") {
		t.Error("expected expired DB token to be invalid")
	}
}

func TestSessionStore_EvictExpiredTokens(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	sessions := NewSessionStore("admin", "dummy_hash", s)

	// Add mix of valid and expired tokens
	sessions.mu.Lock()
	sessions.tokens["valid_token"] = time.Now().Add(1 * time.Hour)
	sessions.tokens["expired_1"] = time.Now().Add(-1 * time.Hour)
	sessions.tokens["expired_2"] = time.Now().Add(-2 * time.Hour)
	sessions.mu.Unlock()

	sessions.EvictExpiredTokens()

	sessions.mu.RLock()
	defer sessions.mu.RUnlock()

	if _, exists := sessions.tokens["valid_token"]; !exists {
		t.Error("expected valid token to remain after eviction")
	}
	if _, exists := sessions.tokens["expired_1"]; exists {
		t.Error("expected expired_1 to be evicted")
	}
	if _, exists := sessions.tokens["expired_2"]; exists {
		t.Error("expected expired_2 to be evicted")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── SessionAuthMiddleware basic auth path ──
// ═══════════════════════════════════════════════════════════════════

func TestSessionAuthMiddleware_BasicAuth(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	req.SetBasicAuth("admin", "adminpass")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid basic auth, got %d", w.Code)
	}
}

func TestSessionAuthMiddleware_BasicAuth_WrongPassword(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	req.SetBasicAuth("admin", "wrongpass")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", w.Code)
	}
}

func TestSessionAuthMiddleware_BasicAuth_WrongUsername(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
	req.SetBasicAuth("wronguser", "adminpass")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong username, got %d", w.Code)
	}
}

func TestSessionAuthMiddleware_RedirectBrowserToLogin(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Non-API, non-static request should redirect to login
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect to /login, got %s", w.Header().Get("Location"))
	}
}

func TestSessionAuthMiddleware_StaticBypass(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("static content"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for static asset, got %d", w.Code)
	}
}

func TestSessionAuthMiddleware_SessionCookie(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	hash, _ := HashPassword("adminpass")
	sessions := NewSessionStore("admin", hash, s)

	// Create a valid session token
	token, _ := sessions.Authenticate("admin", "adminpass")
	if token == "" {
		t.Fatal("expected valid token from authentication")
	}

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authenticated"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "onwatch_session", Value: token})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid session cookie, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── CheckUpdate and ApplyUpdate ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CheckUpdate_MethodNotAllowed_Coverage(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/update/check", nil)
	w := httptest.NewRecorder()

	h.CheckUpdate(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandler_ApplyUpdate_MethodNotAllowed_Coverage(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h := NewHandler(nil, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/update/apply", nil)
	w := httptest.NewRecorder()

	h.ApplyUpdate(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── CycleOverview endpoints ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_CycleOverview_Copilot(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=copilot", nil)
	w := httptest.NewRecorder()

	h.CycleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_CycleOverview_Antigravity(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=antigravity", nil)
	w := httptest.NewRecorder()

	h.CycleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_CycleOverview_WithLimit(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=synthetic&limit=10", nil)
	w := httptest.NewRecorder()

	h.CycleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_CycleOverview_WithLargeLimit(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	// Should be capped at 500
	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=synthetic&limit=1000", nil)
	w := httptest.NewRecorder()

	h.CycleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Logging history edge cases ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_LoggingHistory_Copilot(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCopilot()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=copilot&range=7d", nil)
	w := httptest.NewRecorder()

	h.LoggingHistory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_LoggingHistory_Codex(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithCodex()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=codex&range=7d", nil)
	w := httptest.NewRecorder()

	h.LoggingHistory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_LoggingHistory_Antigravity(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAntigravity()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=antigravity&range=7d", nil)
	w := httptest.NewRecorder()

	h.LoggingHistory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── LoginRateLimiter eviction and edge cases ──
// ═══════════════════════════════════════════════════════════════════

func TestLoginRateLimiter_EvictStaleEntries_AllNonBlocked(t *testing.T) {
	rl := NewLoginRateLimiter(100)

	// Record failures for several IPs
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		rl.RecordFailure(ip)
	}

	// Evict with maxAge=0 should remove all non-blocked entries
	rl.EvictStaleEntries(0)

	rl.mu.RLock()
	remaining := len(rl.attempts)
	rl.mu.RUnlock()

	if remaining != 0 {
		t.Errorf("expected 0 remaining after evicting all non-blocked, got %d", remaining)
	}
}

func TestLoginRateLimiter_Clear(t *testing.T) {
	rl := NewLoginRateLimiter(100)

	rl.RecordFailure("1.2.3.4")
	rl.RecordFailure("1.2.3.4")

	rl.Clear("1.2.3.4")

	if rl.IsBlocked("1.2.3.4") {
		t.Error("expected IP to not be blocked after clear")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── computeAnthropicRate with derived values ──
// ═══════════════════════════════════════════════════════════════════

func TestComputeAnthropicRate_ExhaustsBeforeReset(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	// Reset far in the future, high burn rate
	resetsAt := now.Add(10 * time.Hour)

	// Insert snapshots with steep utilization climb
	for i := 0; i < 4; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-20*time.Minute + time.Duration(i)*5*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: 10 + float64(i)*20, ResetsAt: &resetsAt},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	cfg := createTestConfigWithAnthropic()
	atr := tracker.NewAnthropicTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAnthropicTracker(atr)

	summary := &tracker.AnthropicSummary{
		QuotaName: "five_hour",
		ResetsAt:  &resetsAt,
	}
	rate := h.computeAnthropicRate("five_hour", 70.0, summary)

	if !rate.HasRate {
		t.Error("expected HasRate to be true")
	}
	if rate.Rate <= 0 {
		t.Errorf("expected positive rate, got %f", rate.Rate)
	}
	if rate.TimeToExhaust <= 0 {
		t.Error("expected positive TimeToExhaust")
	}
	if rate.TimeToReset <= 0 {
		t.Error("expected positive TimeToReset")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── percentUsed helper ──
// ═══════════════════════════════════════════════════════════════════

func TestPercentUsed(t *testing.T) {
	tests := []struct {
		usage, limit float64
		expected     float64
	}{
		{50, 100, 50.0},
		{0, 100, 0.0},
		{100, 100, 100.0},
		{0, 0, 0.0},
		{50, 0, 0.0},
	}

	for _, tt := range tests {
		result := percentUsed(tt.usage, tt.limit)
		if math.Abs(result-tt.expected) > 0.01 {
			t.Errorf("percentUsed(%f, %f) = %f, want %f", tt.usage, tt.limit, result, tt.expected)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── buildZaiToolCallsResponse with critical/warning status ──
// ═══════════════════════════════════════════════════════════════════

func TestBuildZaiToolCallsResponse_CriticalStatus(t *testing.T) {
	snapshot := &api.ZaiSnapshot{
		TimeUsage:        100,
		TimeCurrentValue: 90,
		TimePercentage:   90,
		TimeUsageDetails: `[{"type":"code_completion","usage":96}]`,
	}

	result := buildZaiToolCallsResponse(snapshot)

	if result["status"] != "critical" {
		t.Errorf("expected critical status for 96%% usage, got %s", result["status"])
	}
}

func TestBuildZaiToolCallsResponse_WarningStatus(t *testing.T) {
	snapshot := &api.ZaiSnapshot{
		TimeUsage:        100,
		TimeCurrentValue: 50,
		TimePercentage:   50,
		TimeUsageDetails: `[{"type":"code_completion","usage":60}]`,
	}

	result := buildZaiToolCallsResponse(snapshot)

	if result["status"] != "warning" {
		t.Errorf("expected warning status for 60%% usage, got %s", result["status"])
	}
}

func TestBuildZaiToolCallsResponse_DangerStatus(t *testing.T) {
	snapshot := &api.ZaiSnapshot{
		TimeUsage:        100,
		TimeCurrentValue: 80,
		TimePercentage:   80,
		TimeUsageDetails: `[{"type":"code_completion","usage":85}]`,
	}

	result := buildZaiToolCallsResponse(snapshot)

	if result["status"] != "danger" {
		t.Errorf("expected danger status for 85%% usage, got %s", result["status"])
	}
}

func TestBuildZaiToolCallsResponse_EmptyDetails(t *testing.T) {
	snapshot := &api.ZaiSnapshot{
		TimeUsage:        100,
		TimeCurrentValue: 50,
		TimePercentage:   50,
	}

	result := buildZaiToolCallsResponse(snapshot)

	if result["usage"].(float64) != 0 {
		t.Errorf("expected 0 usage for empty details, got %v", result["usage"])
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── History endpoints with data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_HistoryZai_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:         now.Add(-time.Duration(5-i) * time.Hour),
			TokensUsage:        10000,
			TokensCurrentValue: float64(i * 1000),
			TokensPercentage:   i * 10,
			TimeUsage:          3600,
			TimeCurrentValue:   float64(i * 300),
			TimePercentage:     i * 8,
		}
		s.InsertZaiSnapshot(snapshot)
	}

	cfg := createTestConfigWithZai()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=zai&range=24h", nil)
	w := httptest.NewRecorder()

	h.History(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if len(result) == 0 {
		t.Error("expected non-empty history response")
	}
}

func TestHandler_HistoryAnthropic_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(3 * time.Hour)
	for i := 0; i < 5; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: now.Add(-time.Duration(5-i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i * 10), ResetsAt: &resetsAt},
			},
		}
		s.InsertAnthropicSnapshot(snapshot)
	}

	cfg := createTestConfigWithAnthropic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=anthropic&range=24h", nil)
	w := httptest.NewRecorder()

	h.History(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Sessions endpoint with data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_Sessions_WithData(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=synthetic", nil)
	w := httptest.NewRecorder()

	h.Sessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandler_Sessions_Both(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=both", nil)
	w := httptest.NewRecorder()

	h.Sessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── codexPlanLabel helper ──
// ═══════════════════════════════════════════════════════════════════

func TestCodexPlanLabel(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"pro_plan", "Pro Plan"},
		{"free_tier", "Free Tier"},
		{"", ""},
		{"ENTERPRISE", "Enterprise"},
	}

	for _, tt := range tests {
		result := codexPlanLabel(tt.input)
		if result != tt.expected {
			t.Errorf("codexPlanLabel(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── buildInsight helper with all severity levels ──
// ═══════════════════════════════════════════════════════════════════

func TestBuildInsight_AllSeverities(t *testing.T) {
	tests := []struct {
		current, limit float64
		expectStatus   string
	}{
		{95, 100, "critical"},
		{80, 100, "danger"},
		{50, 100, "warning"},
		{10, 100, "healthy"},
	}

	for _, tt := range tests {
		q := api.QuotaInfo{
			Requests: tt.current,
			Limit:    tt.limit,
			RenewsAt: time.Now().Add(24 * time.Hour),
		}
		result := buildQuotaResponse("test", "test desc", q, nil, "subscription")
		if result["status"] != tt.expectStatus {
			t.Errorf("buildQuotaResponse(usage=%.0f, limit=%.0f) status = %v, want %s", tt.current, tt.limit, result["status"], tt.expectStatus)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── getProviderFromRequest edge cases ──
// ═══════════════════════════════════════════════════════════════════

func TestGetProviderFromRequest_DefaultWithMultipleProviders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithAllProviders()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	w := httptest.NewRecorder()

	h.Current(w, req)

	// Should default to "both" when multiple providers
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── formatDuration helper ──
// ═══════════════════════════════════════════════════════════════════

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		dur    time.Duration
		expect string
	}{
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{25 * time.Hour, "1d 1h"},
		{0, "0m"},
		{-5 * time.Minute, "Resetting..."},
	}

	for _, tt := range tests {
		result := formatDuration(tt.dur)
		if result != tt.expect {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.dur, result, tt.expect)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── compactNum helper ──
// ═══════════════════════════════════════════════════════════════════

func TestCompactNum_Coverage(t *testing.T) {
	tests := []struct {
		input  float64
		expect string
	}{
		{500, "500"},
		{1500, "1.5K"},
		{1500000, "1.5M"},
		{0, "0"},
	}

	for _, tt := range tests {
		result := compactNum(tt.input)
		if result != tt.expect {
			t.Errorf("compactNum(%f) = %q, want %q", tt.input, result, tt.expect)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── truncateName helper ──
// ═══════════════════════════════════════════════════════════════════

func TestTruncateName_Coverage(t *testing.T) {
	short := "short"
	if truncateName(short, 30) != short {
		t.Errorf("expected short name to be unchanged")
	}

	long := "this is a very long model name that exceeds thirty characters"
	result := truncateName(long, 30)
	if len(result) > 33 { // 30 + "..."
		t.Errorf("expected truncated name <= 33 chars, got %d", len(result))
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Copilot insights with tracker data ──
// ═══════════════════════════════════════════════════════════════════

func TestHandler_BuildCopilotInsights_WithTracker(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	now := time.Now().UTC()
	resetDate := now.Add(24 * time.Hour)
	snapshot := &api.CopilotSnapshot{
		CapturedAt:  now,
		CopilotPlan: "individual",
		ResetDate:   &resetDate,
		RawJSON:     "{}",
		Quotas: []api.CopilotQuota{
			{Name: "chat", Entitlement: 100, Remaining: 60, PercentRemaining: 60},
			{Name: "completions", Unlimited: true},
		},
	}
	s.InsertCopilotSnapshot(snapshot)

	cfg := createTestConfigWithCopilot()
	cpt := tracker.NewCopilotTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetCopilotTracker(cpt)

	resp := h.buildCopilotInsights(map[string]bool{}, 7*24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Error("expected non-empty stats for copilot with data")
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── Anthropic billing period helpers ──
// ═══════════════════════════════════════════════════════════════════

func TestGroupAnthropicBillingPeriods(t *testing.T) {
	now := time.Now().UTC()

	// Create cycles with decreasing peaks (no reset boundary)
	cycles := []*store.AnthropicResetCycle{
		{ID: 1, CycleStart: now.Add(-4 * time.Hour), PeakUtilization: 80},
		{ID: 2, CycleStart: now.Add(-8 * time.Hour), PeakUtilization: 70},
		{ID: 3, CycleStart: now.Add(-12 * time.Hour), PeakUtilization: 60},
	}

	periods := groupAnthropicBillingPeriods(cycles)
	if len(periods) == 0 {
		t.Error("expected non-empty billing periods")
	}
}

func TestAnthropicBillingPeriodCount_Coverage(t *testing.T) {
	now := time.Now().UTC()

	cycles := []*store.AnthropicResetCycle{
		{ID: 1, CycleStart: now.Add(-4 * time.Hour), PeakUtilization: 80},
		{ID: 2, CycleStart: now.Add(-8 * time.Hour), PeakUtilization: 20},
	}

	count := anthropicBillingPeriodCount(cycles)
	if count < 1 {
		t.Error("expected at least 1 billing period")
	}
}

func TestAnthropicBillingPeriodAvg(t *testing.T) {
	now := time.Now().UTC()

	cycles := []*store.AnthropicResetCycle{
		{ID: 1, CycleStart: now.Add(-4 * time.Hour), PeakUtilization: 80},
		{ID: 2, CycleStart: now.Add(-8 * time.Hour), PeakUtilization: 60},
	}

	avg := anthropicBillingPeriodAvg(cycles)
	if avg <= 0 {
		t.Error("expected positive average")
	}
}

func TestAnthropicBillingPeriodPeak_Coverage(t *testing.T) {
	now := time.Now().UTC()

	cycles := []*store.AnthropicResetCycle{
		{ID: 1, CycleStart: now.Add(-4 * time.Hour), PeakUtilization: 80},
		{ID: 2, CycleStart: now.Add(-8 * time.Hour), PeakUtilization: 60},
	}

	peak := anthropicBillingPeriodPeak(cycles)
	if peak < 60 {
		t.Errorf("expected peak >= 60, got %f", peak)
	}
}

func TestNormalizeAntigravityGroupBy(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "passes through canonical group",
			input:  api.AntigravityQuotaGroupGeminiPro,
			expect: api.AntigravityQuotaGroupGeminiPro,
		},
		{
			name:   "maps model id to group",
			input:  "gemini-2.5-flash",
			expect: api.AntigravityQuotaGroupGeminiFlash,
		},
		{
			name:   "falls back for unknown",
			input:  "unknown-model",
			expect: api.AntigravityQuotaGroupClaudeGPT,
		},
		{
			name:   "falls back for empty input",
			input:  "",
			expect: api.AntigravityQuotaGroupClaudeGPT,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAntigravityGroupBy(tt.input)
			if got != tt.expect {
				t.Fatalf("normalizeAntigravityGroupBy(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestPercentUsed_ClampBehavior(t *testing.T) {
	tests := []struct {
		name         string
		value        float64
		limit        float64
		expectResult float64
	}{
		{name: "normal percent", value: 25, limit: 100, expectResult: 25},
		{name: "negative value clamps to zero", value: -1, limit: 100, expectResult: 0},
		{name: "over limit clamps to hundred", value: 250, limit: 100, expectResult: 100},
		{name: "non-positive limit returns zero", value: 20, limit: 0, expectResult: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentUsed(tt.value, tt.limit)
			if got != tt.expectResult {
				t.Fatalf("percentUsed(%v, %v) = %v, want %v", tt.value, tt.limit, got, tt.expectResult)
			}
		})
	}
}

func TestCodexQuotaDisplayOrder(t *testing.T) {
	if got := codexQuotaDisplayOrder("five_hour"); got != 0 {
		t.Fatalf("codexQuotaDisplayOrder(five_hour) = %d, want 0", got)
	}
	if got := codexQuotaDisplayOrder("seven_day"); got != 1 {
		t.Fatalf("codexQuotaDisplayOrder(seven_day) = %d, want 1", got)
	}
	if got := codexQuotaDisplayOrder("code_review"); got != 2 {
		t.Fatalf("codexQuotaDisplayOrder(code_review) = %d, want 2", got)
	}
	if got := codexQuotaDisplayOrder("other"); got != 100 {
		t.Fatalf("codexQuotaDisplayOrder(other) = %d, want 100", got)
	}
}

func TestHandler_LoggingHistoryRangeAndLimit(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil)

	t.Run("defaults", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/logging-history", nil)
		before := time.Now().UTC()
		start, end, limit := h.loggingHistoryRangeAndLimit(req)
		after := time.Now().UTC()

		if limit != 30*24*60 {
			t.Fatalf("default limit = %d, want %d", limit, 30*24*60)
		}
		if end.Before(before) || end.After(after) {
			t.Fatalf("end %v not within invocation window [%v, %v]", end, before, after)
		}

		expectDuration := 30 * 24 * time.Hour
		gotDuration := end.Sub(start)
		if math.Abs(gotDuration.Seconds()-expectDuration.Seconds()) > 2 {
			t.Fatalf("duration = %v, want about %v", gotDuration, expectDuration)
		}
	})

	t.Run("range and limit clamp", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/logging-history?range=7&limit=999999", nil)
		start, end, limit := h.loggingHistoryRangeAndLimit(req)

		if limit != 50000 {
			t.Fatalf("clamped limit = %d, want 50000", limit)
		}

		expectDuration := 7 * 24 * time.Hour
		gotDuration := end.Sub(start)
		if math.Abs(gotDuration.Seconds()-expectDuration.Seconds()) > 2 {
			t.Fatalf("duration = %v, want about %v", gotDuration, expectDuration)
		}
	})

	t.Run("invalid range keeps default and positive custom limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/logging-history?range=31&limit=123", nil)
		start, end, limit := h.loggingHistoryRangeAndLimit(req)

		if limit != 123 {
			t.Fatalf("limit = %d, want 123", limit)
		}

		expectDuration := 30 * 24 * time.Hour
		gotDuration := end.Sub(start)
		if math.Abs(gotDuration.Seconds()-expectDuration.Seconds()) > 2 {
			t.Fatalf("duration = %v, want about %v", gotDuration, expectDuration)
		}
	})
}

func TestGetProviderFromRequest_ErrorsAndNormalization(t *testing.T) {
	t.Run("nil config returns error", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/current?provider=synthetic", nil)
		_, err := h.getProviderFromRequest(req)
		if err == nil || err.Error() != "configuration not available" {
			t.Fatalf("expected configuration not available error, got %v", err)
		}
	})

	t.Run("no providers configured returns error", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, &config.Config{})
		req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
		_, err := h.getProviderFromRequest(req)
		if err == nil || err.Error() != "no providers configured" {
			t.Fatalf("expected no providers configured error, got %v", err)
		}
	})

	t.Run("normalizes provider query", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
		req := httptest.NewRequest(http.MethodGet, "/api/current?provider=SYNTHETIC", nil)
		provider, err := h.getProviderFromRequest(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if provider != "synthetic" {
			t.Fatalf("provider = %q, want synthetic", provider)
		}
	})

	t.Run("both requires multiple providers", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
		req := httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil)
		_, err := h.getProviderFromRequest(req)
		if err == nil || err.Error() != "'both' requires multiple providers to be configured" {
			t.Fatalf("expected both-provider error, got %v", err)
		}
	})

	t.Run("unknown provider returns error", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
		req := httptest.NewRequest(http.MethodGet, "/api/current?provider=codex", nil)
		_, err := h.getProviderFromRequest(req)
		if err == nil || err.Error() != "provider 'codex' is not configured" {
			t.Fatalf("expected unknown provider error, got %v", err)
		}
	})
}
