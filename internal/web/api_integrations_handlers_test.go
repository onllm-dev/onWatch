package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func insertAPIIntegrationEventForTest(t *testing.T, s *store.Store, line, sourcePath string) {
	t.Helper()
	event, err := apiintegrations.ParseUsageEventLine([]byte(line), sourcePath)
	if err != nil {
		t.Fatalf("ParseUsageEventLine: %v", err)
	}
	if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
		t.Fatalf("InsertAPIIntegrationUsageEvent: %v", err)
	}
}

func TestHandler_APIIntegrationsCurrent_Empty(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/current", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsCurrent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(response) != 0 {
		t.Fatalf("response=%v want empty object", response)
	}
}

func TestHandler_APIIntegrationsCurrent_GroupedTotals(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","account":"personal","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5,"cost_usd":0.1}`, "/tmp/api-integrations/notes.jsonl")
	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:02:00Z","integration":"notes","provider":"anthropic","account":"personal","model":"claude-3-7-haiku","prompt_tokens":4,"completion_tokens":1,"cost_usd":0.05}`, "/tmp/api-integrations/notes.jsonl")
	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:04:00Z","integration":"notes","provider":"mistral","account":"team","model":"mistral-small-latest","prompt_tokens":6,"completion_tokens":2}`, "/tmp/api-integrations/notes.jsonl")
	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:05:00Z","integration":"report","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}`, "/tmp/api-integrations/report.jsonl")

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/current", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsCurrent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}

	var response map[string]struct {
		Integration      string  `json:"integration"`
		RequestCount     int     `json:"requestCount"`
		PromptTokens     int     `json:"promptTokens"`
		CompletionTokens int     `json:"completionTokens"`
		TotalTokens      int     `json:"totalTokens"`
		TotalCostUSD     float64 `json:"totalCostUsd"`
		LastCapturedAt   string  `json:"lastCapturedAt"`
		Providers        []struct {
			Provider         string  `json:"provider"`
			RequestCount     int     `json:"requestCount"`
			PromptTokens     int     `json:"promptTokens"`
			CompletionTokens int     `json:"completionTokens"`
			TotalTokens      int     `json:"totalTokens"`
			TotalCostUSD     float64 `json:"totalCostUsd"`
			LastCapturedAt   string  `json:"lastCapturedAt"`
			Accounts         []struct {
				Account          string `json:"account"`
				RequestCount     int    `json:"requestCount"`
				PromptTokens     int    `json:"promptTokens"`
				CompletionTokens int    `json:"completionTokens"`
				TotalTokens      int    `json:"totalTokens"`
				Models           []struct {
					Model            string `json:"model"`
					RequestCount     int    `json:"requestCount"`
					PromptTokens     int    `json:"promptTokens"`
					CompletionTokens int    `json:"completionTokens"`
					TotalTokens      int    `json:"totalTokens"`
				} `json:"models"`
			} `json:"accounts"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	notes := response["notes"]
	if notes.Integration != "notes" || notes.RequestCount != 3 || notes.TotalTokens != 28 {
		t.Fatalf("notes=%+v", notes)
	}
	if notes.LastCapturedAt != "2026-04-03T12:04:00Z" {
		t.Fatalf("notes lastCapturedAt=%q", notes.LastCapturedAt)
	}
	if len(notes.Providers) != 2 || notes.Providers[0].Provider != "anthropic" || notes.Providers[1].Provider != "mistral" {
		t.Fatalf("notes providers=%+v", notes.Providers)
	}
	if notes.Providers[0].Accounts[0].Account != "personal" || len(notes.Providers[0].Accounts[0].Models) != 2 {
		t.Fatalf("notes anthropic account breakdown=%+v", notes.Providers[0].Accounts)
	}
	if notes.Providers[0].Accounts[0].Models[0].Model != "claude-3-7-haiku" || notes.Providers[0].Accounts[0].Models[1].Model != "claude-3-7-sonnet" {
		t.Fatalf("notes anthropic models=%+v", notes.Providers[0].Accounts[0].Models)
	}

	report := response["report"]
	if report.Integration != "report" || report.RequestCount != 1 || report.TotalTokens != 5 {
		t.Fatalf("report=%+v", report)
	}
	if len(report.Providers) != 1 || report.Providers[0].Accounts[0].Account != "default" {
		t.Fatalf("report providers=%+v", report.Providers)
	}
}

func TestHandler_APIIntegrationsHistory_RangeAndDownsample(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 520; i++ {
		line := `{"ts":"` + base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) + `","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":1,"completion_tokens":1}`
		insertAPIIntegrationEventForTest(t, s, line, "/tmp/api-integrations/notes.jsonl")
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/history?range=30d", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}

	var response map[string][]struct {
		CapturedAt       string  `json:"capturedAt"`
		RequestCount     int     `json:"requestCount"`
		PromptTokens     int     `json:"promptTokens"`
		CompletionTokens int     `json:"completionTokens"`
		TotalTokens      int     `json:"totalTokens"`
		TotalCostUSD     float64 `json:"totalCostUsd"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	buckets := response["notes"]
	if len(buckets) == 0 {
		t.Fatal("expected history buckets for notes")
	}
	if len(buckets) > maxChartPoints {
		t.Fatalf("len(buckets)=%d exceeds maxChartPoints=%d", len(buckets), maxChartPoints)
	}
	if buckets[0].RequestCount < 1 || buckets[0].TotalTokens < 2 {
		t.Fatalf("unexpected first bucket: %+v", buckets[0])
	}
}

func TestHandler_APIIntegrationsHistory_InvalidRange(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/history?range=2h", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsHistory(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rr.Code)
	}
}

func TestHandler_APIIntegrationsHealth_StatusFilesAndAlerts(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if err := s.UpsertAPIIntegrationIngestState(&apiintegrations.IngestState{
		SourcePath:  "/tmp/api-integrations/notes.jsonl",
		Offset:      200,
		FileSize:    256,
		FileModTime: time.Date(2026, 4, 3, 12, 10, 0, 0, time.UTC),
		PartialLine: `{"ts":"2026`,
	}); err != nil {
		t.Fatalf("UpsertAPIIntegrationIngestState: %v", err)
	}
	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:09:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5}`, "/tmp/api-integrations/notes.jsonl")
	if _, err := s.CreateSystemAlert("api_integrations", "ingest_warning", "Malformed line", "Skipped one malformed event", "warning", `{"sourcePath":"/tmp/api-integrations/notes.jsonl"}`); err != nil {
		t.Fatalf("CreateSystemAlert: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	h.agentManager = &mockProviderAgentController{running: map[string]bool{"api_integrations": true}}
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/health", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}

	var response struct {
		Enabled bool   `json:"enabled"`
		Dir     string `json:"dir"`
		Running bool   `json:"running"`
		Files   []struct {
			SourcePath     string `json:"sourcePath"`
			OffsetBytes    int64  `json:"offsetBytes"`
			FileSize       int64  `json:"fileSize"`
			PartialLine    string `json:"partialLine"`
			FileModTime    string `json:"fileModTime"`
			UpdatedAt      string `json:"updatedAt"`
			LastCapturedAt string `json:"lastCapturedAt"`
		} `json:"files"`
		Alerts []struct {
			ID        int64  `json:"id"`
			Type      string `json:"type"`
			Title     string `json:"title"`
			Message   string `json:"message"`
			Severity  string `json:"severity"`
			CreatedAt string `json:"createdAt"`
			Metadata  string `json:"metadata"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if !response.Enabled || !response.Running || response.Dir != "/tmp/api-integrations" {
		t.Fatalf("unexpected health response: %+v", response)
	}
	if len(response.Files) != 1 || response.Files[0].SourcePath != "/tmp/api-integrations/notes.jsonl" || response.Files[0].LastCapturedAt != "2026-04-03T12:09:00Z" {
		t.Fatalf("unexpected files payload: %+v", response.Files)
	}
	if len(response.Alerts) != 1 || response.Alerts[0].Type != "ingest_warning" {
		t.Fatalf("unexpected alerts payload: %+v", response.Alerts)
	}
}

func TestHandler_APIIntegrationsHealth_Disabled(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: false, APIIntegrationsDir: ""})
	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/health", nil)
	rr := httptest.NewRecorder()

	h.APIIntegrationsHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	var response struct {
		Enabled bool          `json:"enabled"`
		Dir     string        `json:"dir"`
		Running bool          `json:"running"`
		Files   []interface{} `json:"files"`
		Alerts  []interface{} `json:"alerts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if response.Enabled || response.Running || len(response.Files) != 0 || len(response.Alerts) != 0 {
		t.Fatalf("unexpected disabled response: %+v", response)
	}
}

func TestHandler_Current_DoesNotIncludeAPIIntegrationsTelemetry(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5}`, "/tmp/api-integrations/notes.jsonl")

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	rr := httptest.NewRecorder()

	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := response["notes"]; ok {
		t.Fatalf("unexpected API integrations telemetry in /api/current: %v", response)
	}
}

func TestServer_APIIntegrationsRoute_UsesAuthMiddleware(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertAPIIntegrationEventForTest(t, s, `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5}`, "/tmp/api-integrations/notes.jsonl")

	h := NewHandler(s, nil, nil, nil, &config.Config{APIIntegrationsEnabled: true, APIIntegrationsDir: "/tmp/api-integrations"})
	passHash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	server := NewServer(0, h, nil, "admin", passHash, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/api-integrations/current", nil)
	req.SetBasicAuth("admin", "secret123")
	rr := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := response["notes"]; !ok {
		t.Fatalf("expected notes payload, got %v", response)
	}
}
