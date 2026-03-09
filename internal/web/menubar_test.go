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
	if _, ok := response["variant"]; !ok {
		t.Fatal("expected variant in response")
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

	body := strings.NewReader(`{"menubar":{"enabled":false,"default_view":"detailed","refresh_seconds":120,"providers_order":["synthetic"],"warning_percent":55,"critical_percent":80}}`)
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
}

func TestMenubarTestEndpointRequiresTestMode(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/test?view=minimal", nil)
	rr := httptest.NewRecorder()

	h.MenubarTest(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestMenubarTestEndpointRendersRequestedView(t *testing.T) {
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
