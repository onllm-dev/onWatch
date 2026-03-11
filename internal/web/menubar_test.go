package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func newMenubarTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New returned error: %v", err)
	}

	snapshot := &api.Snapshot{
		CapturedAt: time.Now().UTC(),
		Sub:        api.QuotaInfo{Limit: 100, Requests: 30, RenewsAt: time.Now().Add(2 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 50, Requests: 10, RenewsAt: time.Now().Add(90 * time.Minute)},
		ToolCall:   api.QuotaInfo{Limit: 200, Requests: 20, RenewsAt: time.Now().Add(3 * time.Hour)},
	}
	if _, err := s.InsertSnapshot(snapshot); err != nil {
		t.Fatalf("InsertSnapshot returned error: %v", err)
	}

	tr := tracker.New(s, nil)
	h := NewHandler(s, tr, nil, nil, createTestConfigWithSynthetic())
	h.SetVersion("test-version")
	return h, s
}

func TestCapabilitiesIncludesMenubarFields(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rr := httptest.NewRecorder()

	h.Capabilities(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if response["version"] != "test-version" {
		t.Fatalf("expected test version, got %#v", response["version"])
	}
	if _, ok := response["menubar_supported"]; !ok {
		t.Fatal("expected menubar_supported in response")
	}
	if _, ok := response["menubar_running"]; !ok {
		t.Fatal("expected menubar_running in response")
	}
}

func TestGetSettingsIncludesMenubarDefaults(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()

	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var response struct {
		Menubar menubar.Settings `json:"menubar"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if response.Menubar.DefaultView != menubar.ViewStandard {
		t.Fatalf("expected standard view, got %s", response.Menubar.DefaultView)
	}
}

func TestUpdateSettingsPersistsMenubarSection(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	body := strings.NewReader(`{"menubar":{"enabled":false,"default_view":"detailed","refresh_seconds":120,"providers_order":["synthetic","anthropic"],"visible_providers":["synthetic"],"warning_percent":55,"critical_percent":80}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	got, err := s.GetMenubarSettings()
	if err != nil {
		t.Fatalf("GetMenubarSettings returned error: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected menubar to be disabled after update")
	}
	if got.DefaultView != menubar.ViewDetailed {
		t.Fatalf("expected detailed view, got %s", got.DefaultView)
	}
	if got.WarningPercent != 55 || got.CriticalPercent != 80 {
		t.Fatalf("unexpected thresholds: %d/%d", got.WarningPercent, got.CriticalPercent)
	}
	if len(got.ProvidersOrder) != 2 || got.ProvidersOrder[0] != "synthetic" || got.ProvidersOrder[1] != "anthropic" {
		t.Fatalf("unexpected providers order: %#v", got.ProvidersOrder)
	}
	if len(got.VisibleProviders) != 1 || got.VisibleProviders[0] != "synthetic" {
		t.Fatalf("unexpected visible providers: %#v", got.VisibleProviders)
	}
}

func TestMenubarTestEndpointRequiresTestMode(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/test?view=standard", nil)
	rr := httptest.NewRecorder()

	h.MenubarTest(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestMenubarTestEndpointPreservesMinimalView(t *testing.T) {
	t.Setenv("ONWATCH_TEST_MODE", "1")

	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/test?view=minimal", nil)
	rr := httptest.NewRecorder()

	h.MenubarTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"default_view":"minimal"`) {
		t.Fatalf("expected minimal view bootstrap, got body: %s", rr.Body.String())
	}
}

func TestMenubarSummaryUsesConfiguredThresholds(t *testing.T) {
	t.Setenv("ONWATCH_TEST_MODE", "1")

	h, s := newMenubarTestHandler(t)
	defer s.Close()

	if err := s.SetMenubarSettings(&menubar.Settings{
		Enabled:         true,
		DefaultView:     menubar.ViewStandard,
		RefreshSeconds:  60,
		WarningPercent:  10,
		CriticalPercent: 20,
	}); err != nil {
		t.Fatalf("SetMenubarSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
	rr := httptest.NewRecorder()

	h.MenubarSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var snapshot menubar.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if snapshot.Aggregate.ProviderCount == 0 {
		t.Fatal("expected at least one provider in menubar snapshot")
	}
	if snapshot.Aggregate.Status != "critical" {
		t.Fatalf("expected critical aggregate status, got %s", snapshot.Aggregate.Status)
	}
	if len(snapshot.Providers) == 0 {
		t.Fatal("expected provider cards in snapshot")
	}
}

func TestMenubarPreferencesRoundTrip(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	getReq := httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil)
	getRR := httptest.NewRecorder()

	h.MenubarPreferences(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getRR.Code, getRR.Body.String())
	}

	var initial struct {
		DefaultView      menubar.ViewType      `json:"default_view"`
		RefreshSeconds   int                   `json:"refresh_seconds"`
		VisibleProviders []string              `json:"visible_providers"`
		StatusDisplay    menubar.StatusDisplay `json:"status_display"`
		Theme            menubar.ThemeMode     `json:"theme"`
		Providers        []map[string]any      `json:"providers"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &initial); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if initial.DefaultView != menubar.ViewStandard {
		t.Fatalf("expected standard view, got %s", initial.DefaultView)
	}
	if initial.StatusDisplay.Mode != menubar.StatusDisplayMultiProvider {
		t.Fatalf("expected multi_provider status display, got %s", initial.StatusDisplay.Mode)
	}
	if len(initial.StatusDisplay.SelectedQuotas) == 0 {
		t.Fatal("expected multi-provider status display to resolve at least one quota")
	}
	if len(initial.Providers) == 0 {
		t.Fatal("expected provider options in preferences response")
	}
	if initial.Theme != menubar.ThemeSystem {
		t.Fatalf("expected system theme by default, got %s", initial.Theme)
	}

	body := strings.NewReader(`{"default_view":"minimal","refresh_seconds":120,"visible_providers":["synthetic"],"status_display":{"mode":"multi_provider","selected_quotas":[{"provider_id":"synthetic","quota_key":"search"}]},"theme":"dark"}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/menubar/preferences", body)
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	putReq.RemoteAddr = "127.0.0.1:12345"
	putRR := httptest.NewRecorder()

	h.MenubarPreferences(putRR, putReq)

	if putRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", putRR.Code, putRR.Body.String())
	}

	got, err := s.GetMenubarSettings()
	if err != nil {
		t.Fatalf("GetMenubarSettings returned error: %v", err)
	}
	if got.DefaultView != menubar.ViewMinimal {
		t.Fatalf("expected minimal view, got %s", got.DefaultView)
	}
	if got.RefreshSeconds != 120 {
		t.Fatalf("expected refresh 120, got %d", got.RefreshSeconds)
	}
	if len(got.VisibleProviders) != 1 || got.VisibleProviders[0] != "synthetic" {
		t.Fatalf("unexpected visible providers: %#v", got.VisibleProviders)
	}
	if got.StatusDisplay.Mode != menubar.StatusDisplayMultiProvider {
		t.Fatalf("expected multi_provider status display, got %s", got.StatusDisplay.Mode)
	}
	if len(got.StatusDisplay.SelectedQuotas) != 1 || got.StatusDisplay.SelectedQuotas[0].ProviderID != "synthetic" || got.StatusDisplay.SelectedQuotas[0].QuotaKey != "search" {
		t.Fatalf("unexpected status display selections: %#v", got.StatusDisplay.SelectedQuotas)
	}
	if got.Theme != menubar.ThemeDark {
		t.Fatalf("expected dark theme, got %s", got.Theme)
	}
}

func TestMenubarRefreshRequiresPost(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/refresh", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	h.MenubarRefresh(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestMenubarRefreshRequiresLoopback(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/menubar/refresh", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rr := httptest.NewRecorder()

	h.MenubarRefresh(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestMenubarRefreshLoopbackReturnsOK(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/menubar/refresh", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	h.MenubarRefresh(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if response["status"] != "ok" {
		t.Fatalf("expected status ok, got %#v", response)
	}
}

func TestMenubarPageRequiresLoopback(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/menubar", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rr := httptest.NewRecorder()

	h.MenubarPage(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestMenubarPageRendersLoopbackBootstrap(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/menubar?view=detailed", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	h.MenubarPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"default_view":"detailed"`) {
		t.Fatalf("expected detailed bootstrap, got body: %s", body)
	}
	if !strings.Contains(body, `id="settings-panel"`) {
		t.Fatalf("expected compact menubar shell, got body: %s", body)
	}
	if !strings.Contains(body, `function sendNativeAction(action)`) {
		t.Fatalf("expected native action bridge helper, got body: %s", body)
	}
	if !strings.Contains(body, `if (sendNativeAction("close"))`) {
		t.Fatalf("expected native close action bridge usage, got body: %s", body)
	}
	if !strings.Contains(body, `if (sendNativeAction("open_dashboard"))`) {
		t.Fatalf("expected native dashboard action bridge usage, got body: %s", body)
	}
}

func TestMenubarPageUsesPersistedDefaultViewWithoutQuery(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	if err := s.SetMenubarSettings(&menubar.Settings{
		Enabled:         true,
		DefaultView:     menubar.ViewDetailed,
		RefreshSeconds:  60,
		WarningPercent:  70,
		CriticalPercent: 90,
	}); err != nil {
		t.Fatalf("SetMenubarSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/menubar", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	h.MenubarPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"default_view":"detailed"`) {
		t.Fatalf("expected persisted detailed default view in bootstrap, got body: %s", rr.Body.String())
	}
}

func TestMenubarPagePreservesMinimalQueryView(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/menubar?view=minimal", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	h.MenubarPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"default_view":"minimal"`) {
		t.Fatalf("expected minimal bootstrap when minimal is requested, got body: %s", rr.Body.String())
	}
}

func TestSessionAuthMiddleware_AllowsLoopbackMenubarPaths(t *testing.T) {
	sessions := NewSessionStore("admin", legacyHashPassword("secret"), nil)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/menubar", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
	apiReq.RemoteAddr = "[::1]:12345"
	apiRR := httptest.NewRecorder()
	handler.ServeHTTP(apiRR, apiReq)

	if apiRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback api path, got %d", apiRR.Code)
	}

	prefsReq := httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil)
	prefsReq.RemoteAddr = "127.0.0.1:12345"
	prefsRR := httptest.NewRecorder()
	handler.ServeHTTP(prefsRR, prefsReq)

	if prefsRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback preferences path, got %d", prefsRR.Code)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/menubar/refresh", nil)
	refreshReq.RemoteAddr = "127.0.0.1:12345"
	refreshReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	refreshRR := httptest.NewRecorder()
	handler.ServeHTTP(refreshRR, refreshReq)

	if refreshRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for loopback refresh path, got %d", refreshRR.Code)
	}
}

func TestSessionAuthMiddleware_DoesNotBypassRemoteMenubarPaths(t *testing.T) {
	sessions := NewSessionStore("admin", legacyHashPassword("secret"), nil)

	handler := SessionAuthMiddleware(sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/menubar", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected redirect to login for remote request, got %d", rr.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
	apiReq.RemoteAddr = "192.168.1.50:12345"
	apiRR := httptest.NewRecorder()
	handler.ServeHTTP(apiRR, apiReq)

	if apiRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for remote api request, got %d", apiRR.Code)
	}

	prefsReq := httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil)
	prefsReq.RemoteAddr = "192.168.1.50:12345"
	prefsRR := httptest.NewRecorder()
	handler.ServeHTTP(prefsRR, prefsReq)

	if prefsRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for remote preferences api request, got %d", prefsRR.Code)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/menubar/refresh", nil)
	refreshReq.RemoteAddr = "192.168.1.50:12345"
	refreshReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	refreshRR := httptest.NewRecorder()
	handler.ServeHTTP(refreshRR, refreshReq)

	if refreshRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for remote refresh api request, got %d", refreshRR.Code)
	}
}

func TestBuildMenubarSnapshotPreservesAnthropicQuotaOrder(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New returned error: %v", err)
	}
	defer s.Close()

	capturedAt := time.Now().UTC().Truncate(time.Second)
	reset := capturedAt.Add(2 * time.Hour)
	if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		CapturedAt: capturedAt,
		Quotas: []api.AnthropicQuota{
			{Name: "seven_day", Utilization: 98, ResetsAt: &reset},
			{Name: "seven_day_sonnet", Utilization: 65, ResetsAt: &reset},
			{Name: "five_hour", Utilization: 28, ResetsAt: &reset},
		},
	}); err != nil {
		t.Fatalf("InsertAnthropicSnapshot returned error: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot returned error: %v", err)
	}

	provider := findMenubarProviderCard(t, snapshot, "anthropic")
	assertQuotaLabels(t, provider.Quotas, []string{
		api.AnthropicDisplayName("five_hour"),
		api.AnthropicDisplayName("seven_day"),
		api.AnthropicDisplayName("seven_day_sonnet"),
	})
}

func TestBuildMenubarSnapshotPreservesCodexFreeQuotaOrder(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New returned error: %v", err)
	}
	defer s.Close()

	capturedAt := time.Now().UTC().Truncate(time.Second)
	reset := capturedAt.Add(2 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: capturedAt,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "free",
		Quotas: []api.CodexQuota{
			{Name: "code_review", Utilization: 10, ResetsAt: &reset},
			{Name: "five_hour", Utilization: 28, ResetsAt: &reset},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot returned error: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot returned error: %v", err)
	}

	provider := findMenubarProviderCard(t, snapshot, "codex:1")
	assertQuotaLabels(t, provider.Quotas, []string{
		api.CodexDisplayName("seven_day"),
		api.CodexDisplayName("code_review"),
	})
}

func findMenubarProviderCard(t *testing.T, snapshot *menubar.Snapshot, providerID string) menubar.ProviderCard {
	t.Helper()

	for i := range snapshot.Providers {
		if snapshot.Providers[i].ID == providerID {
			return snapshot.Providers[i]
		}
	}

	t.Fatalf("expected provider %q in snapshot, got %+v", providerID, snapshot.Providers)
	return menubar.ProviderCard{}
}

func assertQuotaLabels(t *testing.T, quotas []menubar.QuotaMeter, want []string) {
	t.Helper()

	if len(quotas) != len(want) {
		t.Fatalf("quota count = %d, want %d (%v)", len(quotas), len(want), quotaLabels(quotas))
	}

	for i := range want {
		if quotas[i].Label != want[i] {
			t.Fatalf("quota order = %v, want %v", quotaLabels(quotas), want)
		}
	}
}

func quotaLabels(quotas []menubar.QuotaMeter) []string {
	labels := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		labels = append(labels, quota.Label)
	}
	return labels
}
