package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type mockProviderAgentController struct {
	started  []string
	stopped  []string
	running  map[string]bool
	startErr error
}

func (m *mockProviderAgentController) Start(key string) error {
	m.started = append(m.started, key)
	if m.running == nil {
		m.running = map[string]bool{}
	}
	if m.startErr == nil {
		m.running[key] = true
	}
	return m.startErr
}

func (m *mockProviderAgentController) Stop(key string) {
	m.stopped = append(m.stopped, key)
	if m.running != nil {
		delete(m.running, key)
	}
}

func (m *mockProviderAgentController) IsRunning(key string) bool {
	return m.running != nil && m.running[key]
}

func insertCodexWebSnapshot(t *testing.T, s *store.Store, accountID int64, plan string) {
	t.Helper()
	reset := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	balance := 12.5
	_, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:     time.Now().UTC().Truncate(time.Second),
		AccountID:      accountID,
		PlanType:       plan,
		CreditsBalance: &balance,
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 35, ResetsAt: &reset},
			{Name: "code_review", Utilization: 10, ResetsAt: &reset},
		},
	})
	if err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}
}

func TestProviderUtilityFunctions(t *testing.T) {
	if providerKeyBase("codex:5") != "codex" || providerKeyBase("synthetic") != "synthetic" {
		t.Fatal("unexpected providerKeyBase mapping")
	}

	src := &config.Config{
		SyntheticAPIKey:      "syn",
		ZaiAPIKey:            "zai",
		ZaiBaseURL:           "https://z.ai",
		AnthropicToken:       "anth",
		AnthropicAutoToken:   true,
		CopilotToken:         "copilot",
		CodexToken:           "codex",
		CodexAutoToken:       true,
		AntigravityBaseURL:   "http://127.0.0.1:4242",
		AntigravityCSRFToken: "csrf",
		AntigravityEnabled:   true,
		MiniMaxAPIKey:        "minimax",
	}
	dst := &config.Config{}
	applyProviderConfig(dst, src)
	if dst.CodexToken != "codex" || dst.AntigravityBaseURL != "http://127.0.0.1:4242" || dst.MiniMaxAPIKey != "minimax" {
		t.Fatalf("applyProviderConfig failed: %+v", dst)
	}

	catalog := providerCatalog()
	if len(catalog) < 7 {
		t.Fatalf("providerCatalog length = %d, want at least 7", len(catalog))
	}
	if catalog[0].Key != "anthropic" {
		t.Fatalf("providerCatalog first key = %q, want anthropic", catalog[0].Key)
	}
}

func TestHandler_ProviderVisibilityHelpers(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	if got := h.providerVisibilityMap(); len(got) != 0 {
		t.Fatalf("providerVisibilityMap() on empty store = %+v", got)
	}

	polling := false
	dashboard := false
	if err := h.setProviderVisibility("synthetic", &polling, &dashboard); err != nil {
		t.Fatalf("setProviderVisibility: %v", err)
	}

	vis := h.providerVisibilityMap()
	if h.providerPollingEnabled("synthetic", vis) {
		t.Fatal("expected synthetic polling to be disabled")
	}
	if h.providerDashboardVisible("synthetic", vis) {
		t.Fatal("expected synthetic dashboard to be hidden")
	}
	if !h.providerPollingEnabled("missing", vis) || !h.providerDashboardVisible("missing", vis) {
		t.Fatal("expected missing providers to default to true")
	}

	nilStoreHandler := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	if err := nilStoreHandler.saveProviderVisibility(map[string]map[string]bool{}); err == nil {
		t.Fatal("expected saveProviderVisibility to fail without a store")
	}
}

func TestHandler_IsProviderConfiguredAndTryAutoDetect(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)

	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"tokens":{"access_token":"codex-auto"}}`), 0o600); err != nil {
		t.Fatalf("write codex auth: %v", err)
	}

	cfg := &config.Config{
		SyntheticAPIKey:    "syn",
		ZaiAPIKey:          "zai",
		CopilotToken:       "copilot",
		AntigravityEnabled: true,
		MiniMaxAPIKey:      "minimax",
	}
	h := NewHandler(nil, nil, nil, nil, cfg)

	for _, provider := range []string{"synthetic", "zai", "copilot", "codex", "antigravity", "minimax"} {
		if !h.isProviderConfigured(provider) {
			t.Fatalf("expected %s to be configured", provider)
		}
	}
	if h.isProviderConfigured("unknown") {
		t.Fatal("unexpected unknown provider configured")
	}

	cfg.CodexToken = ""
	if !h.tryAutoDetect("codex") {
		t.Fatal("expected codex auto-detect to succeed")
	}
	if cfg.CodexToken != "codex-auto" || !cfg.CodexAutoToken {
		t.Fatalf("unexpected codex auto-detect config: %+v", cfg)
	}
	if h.tryAutoDetect("unknown") {
		t.Fatal("unexpected auto-detect success for unknown provider")
	}
}

func TestHandler_SetAgentManagerAndProviderStatuses(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if err := s.SetSetting("provider_visibility", `{"synthetic":{"polling":false,"dashboard":true},"codex":{"polling":true,"dashboard":false}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	cfg.CodexToken = "codex"
	controller := &mockProviderAgentController{running: map[string]bool{"codex": true}}
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(controller)

	statuses := h.providerStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected non-empty providerStatuses")
	}

	var synthetic, codex *ProviderStatus
	for i := range statuses {
		switch statuses[i].Key {
		case "synthetic":
			synthetic = &statuses[i]
		case "codex":
			codex = &statuses[i]
		}
	}
	if synthetic == nil || synthetic.Configured != true || synthetic.PollingEnabled != false || synthetic.DashboardVisible != true {
		t.Fatalf("unexpected synthetic status: %+v", synthetic)
	}
	if codex == nil || codex.IsPolling != true || codex.DashboardVisible != false {
		t.Fatalf("unexpected codex status: %+v", codex)
	}
}

func TestHandler_ProvidersStatus(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	req := httptest.NewRequest(http.MethodGet, "/api/providers/status", nil)
	rr := httptest.NewRecorder()

	h.ProvidersStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ProvidersStatus status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"providers"`) {
		t.Fatalf("unexpected ProvidersStatus body: %s", rr.Body.String())
	}
}

func TestHandler_ToggleProvider(t *testing.T) {
	t.Run("rejects invalid requests", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())

		rr := httptest.NewRecorder()
		h.ToggleProvider(rr, httptest.NewRequest(http.MethodGet, "/api/providers/toggle", nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET status = %d, want 405", rr.Code)
		}

		rr = httptest.NewRecorder()
		h.ToggleProvider(rr, httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader("{")))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("bad JSON status = %d, want 400", rr.Code)
		}

		rr = httptest.NewRecorder()
		h.ToggleProvider(rr, httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader(`{"provider":"unknown"}`)))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("unknown provider status = %d, want 400", rr.Code)
		}
	})

	t.Run("returns credentials required when enabling unconfigured provider", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		h := NewHandler(s, nil, nil, nil, &config.Config{})

		req := httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader(`{"provider":"synthetic","polling":true}`))
		rr := httptest.NewRecorder()
		h.ToggleProvider(rr, req)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"credentials_required"`) {
			t.Fatalf("unexpected credentials-required response: status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("starts and stops agent", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		cfg := createTestConfigWithSynthetic()
		controller := &mockProviderAgentController{}
		h := NewHandler(s, nil, nil, nil, cfg)
		h.SetAgentManager(controller)

		req := httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader(`{"provider":"synthetic","polling":true,"dashboard":false}`))
		rr := httptest.NewRecorder()
		h.ToggleProvider(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("enable status = %d body=%s", rr.Code, rr.Body.String())
		}
		if len(controller.started) != 1 || controller.started[0] != "synthetic" {
			t.Fatalf("unexpected started providers: %v", controller.started)
		}

		var vis map[string]map[string]bool
		raw, _ := s.GetSetting("provider_visibility")
		if err := json.Unmarshal([]byte(raw), &vis); err != nil {
			t.Fatalf("unmarshal visibility: %v", err)
		}
		if vis["synthetic"]["dashboard"] != false {
			t.Fatalf("expected dashboard visibility false, got %+v", vis)
		}

		req = httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader(`{"provider":"synthetic","polling":false}`))
		rr = httptest.NewRecorder()
		h.ToggleProvider(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("disable status = %d body=%s", rr.Code, rr.Body.String())
		}
		if len(controller.stopped) != 1 || controller.stopped[0] != "synthetic" {
			t.Fatalf("unexpected stopped providers: %v", controller.stopped)
		}
	})
}

func TestHandler_CodexProfilesAndUsage(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	workAcc, err := s.GetOrCreateProviderAccount("codex", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(work): %v", err)
	}
	personalAcc, err := s.GetOrCreateProviderAccount("codex", "personal")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(personal): %v", err)
	}
	insertCodexWebSnapshot(t, s, workAcc.ID, "pro")
	insertCodexWebSnapshot(t, s, personalAcc.ID, "free")

	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	rr := httptest.NewRecorder()
	h.CodexProfiles(rr, httptest.NewRequest(http.MethodGet, "/api/codex/profiles", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"work"`) || !strings.Contains(rr.Body.String(), `"personal"`) {
		t.Fatalf("unexpected CodexProfiles response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.CodexUsage(rr, httptest.NewRequest(http.MethodGet, "/api/codex/usage?account="+strconv.FormatInt(workAcc.ID, 10), nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"accountId":`+strconv.FormatInt(workAcc.ID, 10)) {
		t.Fatalf("unexpected CodexUsage single response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.CodexUsage(rr, httptest.NewRequest(http.MethodGet, "/api/codex/usage?all=true", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"accounts"`) {
		t.Fatalf("unexpected CodexUsage all response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.CodexAccountsUsage(rr, httptest.NewRequest(http.MethodGet, "/api/codex/accounts", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"accounts"`) {
		t.Fatalf("unexpected CodexAccountsUsage response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCodexParsingAndUsageHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?account=3", nil)
	if parseCodexAccountID(req) != 3 {
		t.Fatalf("parseCodexAccountID(valid) = %d", parseCodexAccountID(req))
	}
	req = httptest.NewRequest(http.MethodGet, "/?account=bad", nil)
	if parseCodexAccountID(req) != DefaultCodexAccountID {
		t.Fatalf("parseCodexAccountID(invalid) = %d", parseCodexAccountID(req))
	}

	if codexUsageAccountID(nil) != DefaultCodexAccountID || codexUsageAccountName(nil) != "" {
		t.Fatal("unexpected default codex usage account helpers")
	}
	if codexUsageAccountID(map[string]interface{}{"accountId": float64(9)}) != 9 {
		t.Fatal("unexpected codexUsageAccountID conversion")
	}
	if codexUsageAccountName(map[string]interface{}{"accountName": "work"}) != "work" {
		t.Fatal("unexpected codexUsageAccountName conversion")
	}

	if !codexIsFreePlan("free") || codexIsFreePlan("pro") {
		t.Fatal("unexpected codexIsFreePlan result")
	}
	if codexNormalizedQuotaName("free", "five_hour") != "seven_day" || codexNormalizedQuotaName("pro", "five_hour") != "five_hour" {
		t.Fatal("unexpected codexNormalizedQuotaName result")
	}

	normalized := codexNormalizeQuotasForPlan("free", []api.CodexQuota{
		{Name: "five_hour", Utilization: 10},
		{Name: "seven_day", Utilization: 15},
	})
	if len(normalized) != 1 || normalized[0].Name != "seven_day" || normalized[0].Utilization != 15 {
		t.Fatalf("unexpected codexNormalizeQuotasForPlan result: %+v", normalized)
	}
}
