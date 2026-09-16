package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestHandler_GetSettings_ExposesUpdateCheck asserts the dashboard can read the
// automatic-update-check state, including whether the deployment has pinned it.
func TestHandler_GetSettings_ExposesUpdateCheck(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if enabled, ok := resp["update_check"].(bool); !ok || !enabled {
		t.Errorf("update_check = %v, want true by default", resp["update_check"])
	}
	if locked, ok := resp["update_check_locked"].(bool); !ok || locked {
		t.Errorf("update_check_locked = %v, want false when the env var is unset", resp["update_check_locked"])
	}
}

// TestHandler_UpdateSettings_PersistsUpdateCheck asserts the toggle round-trips.
func TestHandler_UpdateSettings_PersistsUpdateCheck(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"update_check":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if s.UpdateCheckEnabled() {
		t.Error("store still reports the automatic update check as enabled")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr = httptest.NewRecorder()
	h.GetSettings(rr, req)
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if enabled, _ := resp["update_check"].(bool); enabled {
		t.Error("GetSettings should report update_check false after it was turned off")
	}
}

// TestHandler_UpdateSettings_RejectsInvalidUpdateCheck guards the parse path.
func TestHandler_UpdateSettings_RejectsInvalidUpdateCheck(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	body := strings.NewReader(`{"update_check":"nope"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-boolean update_check", rr.Code)
	}
}

// TestHandler_GetSettings_ReportsEnvLock asserts a deployment that pins the
// check off is reported as locked, so the UI can disable the toggle and explain
// why rather than letting the operator flip a switch that does nothing.
func TestHandler_GetSettings_ReportsEnvLock(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	cfg.UpdateCheckForcedOff = true
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if enabled, _ := resp["update_check"].(bool); enabled {
		t.Error("update_check must report false when the deployment pins it off")
	}
	if locked, _ := resp["update_check_locked"].(bool); !locked {
		t.Error("update_check_locked must be true when ONWATCH_UPDATE_CHECK pins it off")
	}
}

// TestHandler_CheckUpdate_RefusesWhenEnvPinnedOff asserts the endpoint makes no
// outbound call when the deployment pins the check off. The endpoint is also
// used as a liveness probe after applying an update, so it answers 200 with a
// disabled marker rather than an error.
func TestHandler_CheckUpdate_RefusesWhenEnvPinnedOff(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	cfg.UpdateCheckForcedOff = true
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetUpdater(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	h.CheckUpdate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if disabled, _ := resp["disabled"].(bool); !disabled {
		t.Errorf("response should carry disabled=true, got %v", resp)
	}
	if available, _ := resp["available"].(bool); available {
		t.Error("a disabled check must never report an available update")
	}
}

// TestHandler_CheckUpdate_RefusesWhenToggledOff asserts the dashboard toggle
// alone stops the outbound call, not just the deployment-level env pin.
func TestHandler_CheckUpdate_RefusesWhenToggledOff(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	if err := s.SetSetting(store.SettingUpdateCheck, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	rr := httptest.NewRecorder()
	h.CheckUpdate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if disabled, _ := resp["disabled"].(bool); !disabled {
		t.Errorf("response should carry disabled=true, got %v", resp)
	}
}

// TestHandler_ApplyUpdate_RefusesWhenCheckDisabled asserts a disabled check also
// blocks downloading a release binary, so "off" means onWatch does not talk to
// GitHub at all.
func TestHandler_ApplyUpdate_RefusesWhenCheckDisabled(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	if err := s.SetSetting(store.SettingUpdateCheck, "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	rr := httptest.NewRecorder()
	h.ApplyUpdate(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when version checks are off", rr.Code)
	}
}

// TestAppJS_SkipsAutomaticUpdateCheckWhenDisabled asserts the browser does not
// make the hourly call when the toggle is off. The server-side setting alone is
// not enough: the poll originates in the page.
func TestAppJS_SkipsAutomaticUpdateCheckWhenDisabled(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	idx := strings.Index(appJS, "async function checkForUpdate()")
	if idx < 0 {
		t.Fatal("checkForUpdate function not found")
	}
	body := appJS[idx : idx+1200]
	if !strings.Contains(body, "updateCheckEnabled") {
		t.Error("checkForUpdate must consult the cached update-check setting before calling the API")
	}

	// The hourly interval must be guarded too, not just the first call.
	if !strings.Contains(appJS, "if (!updateCheckEnabled) return;") &&
		!strings.Contains(appJS, "if (updateCheckEnabled)") {
		t.Error("the automatic update-check schedule must be guarded by the setting")
	}
}

// TestSettingsHTML_HasUpdateCheckToggle asserts the env flag is matched by a
// real control in the dashboard, not left as an environment variable only.
func TestSettingsHTML_HasUpdateCheckToggle(t *testing.T) {
	t.Parallel()

	data, err := templatesFS.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings.html: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, `id="settings-update-check"`) {
		t.Error("settings.html must expose an automatic update check toggle")
	}
	if !strings.Contains(html, `id="settings-update-check-lock-hint"`) {
		t.Error("settings.html must have a hint element explaining an env-pinned lock")
	}
	if !strings.Contains(html, "api.github.com") {
		t.Error("the toggle copy must name the host contacted, so the disclosure is on the control itself")
	}
}

// TestAppJS_WiresUpdateCheckToggle asserts the toggle is both populated on load
// and collected on save.
func TestAppJS_WiresUpdateCheckToggle(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	if !strings.Contains(appJS, "settings-update-check") {
		t.Fatal("app.js must reference the update check toggle element")
	}
	if !strings.Contains(appJS, "data.update_check") {
		t.Error("app.js must populate the toggle from the settings payload")
	}
	if !strings.Contains(appJS, "settings.update_check") {
		t.Error("app.js must include update_check when saving settings")
	}
	if !strings.Contains(appJS, "update_check_locked") {
		t.Error("app.js must disable the toggle when the deployment pins the setting")
	}
}
