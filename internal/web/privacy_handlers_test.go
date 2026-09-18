package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestHandler_ExportData_StreamsRedactedJSON covers GDPR Art. 15/20 and DPDP
// s.11: the operator must be able to get everything out in a usable format.
func TestHandler_ExportData_StreamsRedactedJSON(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	if err := s.SetSetting("timezone", "Asia/Kolkata"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting("gemini_tokens", `{"refresh_token":"1//LEAKME"}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/privacy/export", nil)
	rr := httptest.NewRecorder()
	h.ExportData(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q, want an attachment with a .json filename", cd)
	}

	body := rr.Body.String()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	if !strings.Contains(body, "Asia/Kolkata") {
		t.Error("export should contain the operator's settings")
	}
	if strings.Contains(body, "1//LEAKME") {
		t.Error("export leaked an OAuth refresh token")
	}
}

// TestHandler_ExportData_RejectsNonGET keeps the method surface tight.
func TestHandler_ExportData_RejectsNonGET(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/export", nil)
	rr := httptest.NewRecorder()
	h.ExportData(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// TestHandler_EraseData_RequiresExplicitConfirmation asserts the destructive
// endpoint cannot be triggered by a stray request. Erasure is irreversible, so
// it takes a typed confirmation and not merely a POST.
func TestHandler_EraseData_RequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	seedErasureFixture(t, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	for _, body := range []string{
		`{}`,
		`{"scope":"all"}`,
		`{"scope":"all","confirm":"yes"}`,
		`{"scope":"all","confirm":"erase"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/privacy/erase", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.EraseData(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rr.Code)
		}
	}

	// Nothing may have been removed by the rejected attempts.
	if !strings.Contains(exportedBody(t, h), "dev@example.com") {
		t.Error("a rejected erase request deleted data")
	}
}

// TestHandler_EraseData_All erases everything on a properly confirmed request.
func TestHandler_EraseData_All(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	seedErasureFixture(t, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/erase",
		strings.NewReader(`{"scope":"all","confirm":"ERASE"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.EraseData(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["scope"] != "all" {
		t.Errorf("response scope = %v, want all", resp["scope"])
	}
	if _, ok := resp["removed"]; !ok {
		t.Error("response must report what was removed, so the operator has a record of the action")
	}
	after := exportedBody(t, h)
	for _, gone := range []string{"dev@example.com", "ollama-user@example.com", "team-42"} {
		if strings.Contains(after, gone) {
			t.Errorf("%q survived a full erasure", gone)
		}
	}
}

// TestHandler_EraseData_SingleProvider covers the proportionate case: stop
// processing one provider without discarding everything else.
func TestHandler_EraseData_SingleProvider(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	seedErasureFixture(t, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/erase",
		strings.NewReader(`{"scope":"provider","provider":"grok","confirm":"ERASE"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.EraseData(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	after := exportedBody(t, h)
	if strings.Contains(after, "team-42") {
		t.Error("grok data survived a grok-scoped erasure")
	}
	if !strings.Contains(after, "ollama-user@example.com") {
		t.Error("a provider-scoped erasure removed another provider's data")
	}
}

// TestHandler_EraseData_RejectsUnknownProvider asserts a typo does not become a
// silent success or a full wipe.
func TestHandler_EraseData_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	seedErasureFixture(t, s)

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	for _, body := range []string{
		`{"scope":"provider","provider":"","confirm":"ERASE"}`,
		`{"scope":"provider","provider":"nope","confirm":"ERASE"}`,
		`{"scope":"nonsense","confirm":"ERASE"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/privacy/erase", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.EraseData(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
	if !strings.Contains(exportedBody(t, h), "dev@example.com") {
		t.Error("a rejected erase request deleted data")
	}
}

// TestHandler_PrivacyPage_RendersWithoutAuth asserts the notice is reachable.
// GDPR Art. 12 requires it to be easily accessible, and a notice you can only
// read after logging in is not much use to someone asking what is held on them.
func TestHandler_PrivacyPage_RendersWithoutAuth(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rr := httptest.NewRecorder()
	h.PrivacyPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(strings.ToLower(body), "privacy") {
		t.Error("privacy page does not mention privacy")
	}
	// The notice has to name what is actually collected to be a notice at all.
	for _, want := range []string{"SQLite", "api.github.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("privacy page should mention %q", want)
		}
	}
}

// TestPrivacyPath_IsPublic asserts the auth middleware lets the notice through.
func TestPrivacyPath_IsPublic(t *testing.T) {
	t.Parallel()

	if !isPublicNoticePath("/privacy", "") {
		t.Error("/privacy must be publicly reachable")
	}
	if !isPublicNoticePath("/onwatch/privacy", "/onwatch") {
		t.Error("/privacy must be reachable under a base path")
	}
	if isPublicNoticePath("/api/current", "") {
		t.Error("only the notice is public, not the API")
	}
	if isPublicNoticePath("/privacy/../api/current", "") {
		t.Error("the public check must not be fooled by a traversal-shaped path")
	}
}

// TestHandler_GetSettings_ExposesRetention asserts the retention policy is
// visible and defaults to keeping everything.
func TestHandler_GetSettings_ExposesRetention(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"retention_scrub_days", "retention_delete_days"} {
		v, ok := resp[key]
		if !ok {
			t.Errorf("settings payload is missing %s", key)
			continue
		}
		if n, ok := v.(float64); !ok || n != 0 {
			t.Errorf("%s = %v, want 0 (keep everything) by default so an upgrade deletes nothing", key, v)
		}
	}
}

// TestHandler_UpdateSettings_PersistsRetention round-trips the policy.
func TestHandler_UpdateSettings_PersistsRetention(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	req := httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"retention_scrub_days":90,"retention_delete_days":400}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	policy := s.RetentionPolicyFromSettings()
	if policy.ScrubAfter.Hours() != 90*24 {
		t.Errorf("ScrubAfter = %v, want 90 days", policy.ScrubAfter)
	}
	if policy.DeleteAfter.Hours() != 400*24 {
		t.Errorf("DeleteAfter = %v, want 400 days", policy.DeleteAfter)
	}
}

// TestHandler_UpdateSettings_RejectsIncoherentRetention asserts a policy that
// would delete rows before they are ever scrubbed is refused, since that
// silently makes the scrub setting a lie.
func TestHandler_UpdateSettings_RejectsIncoherentRetention(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)

	for _, body := range []string{
		`{"retention_scrub_days":-1}`,
		`{"retention_delete_days":-5}`,
		`{"retention_scrub_days":400,"retention_delete_days":90}`,
		`{"retention_scrub_days":"ninety"}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.UpdateSettings(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
}

// TestSettingsHTML_HasPrivacyControls asserts every new knob has a real control
// in the dashboard, not just an environment variable.
func TestSettingsHTML_HasPrivacyControls(t *testing.T) {
	t.Parallel()

	data, err := templatesFS.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings.html: %v", err)
	}
	html := string(data)

	for _, id := range []string{
		`id="settings-retention-scrub-days"`,
		`id="settings-retention-delete-days"`,
		`id="settings-export-data"`,
		`id="settings-erase-data"`,
		`id="settings-erase-provider"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("settings.html must contain %s", id)
		}
	}
	if !strings.Contains(html, "/privacy") {
		t.Error("settings must link to the privacy notice")
	}
}

// TestAppJS_PrivacyPageDoesNotRequireAuth asserts the browser does not bounce a
// logged-out visitor off the notice.
//
// Serving /privacy without auth server-side is not enough: app.js runs on every
// page, and its bootstrap calls authFetch("/api/settings"), which redirects to
// /login on a 401. Without an early branch the public notice is public in name
// only - the reader lands on a login form.
func TestAppJS_PrivacyPageDoesNotRequireAuth(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	idx := strings.Index(appJS, "document.addEventListener('DOMContentLoaded'")
	if idx < 0 {
		t.Fatal("DOMContentLoaded handler not found")
	}
	bootstrap := appJS[idx:]

	privacyIdx := strings.Index(bootstrap, `data-page="privacy"`)
	if privacyIdx < 0 {
		t.Fatal(`bootstrap must branch on the privacy page ([data-page="privacy"]) before fetching anything`)
	}

	// The branch has to come before the first authenticated call, or the
	// redirect happens anyway.
	authIdx := strings.Index(bootstrap, "authFetch(")
	if authIdx >= 0 && privacyIdx > authIdx {
		t.Error("the privacy-page branch must come before the first authFetch in the bootstrap")
	}

	// The template carries the hook the branch relies on.
	data, err := templatesFS.ReadFile("templates/privacy.html")
	if err != nil {
		t.Fatalf("read privacy.html: %v", err)
	}
	if !strings.Contains(string(data), `data-page="privacy"`) {
		t.Error(`privacy.html must carry the data-page="privacy" hook app.js branches on`)
	}
}

// TestAppJS_WiresPrivacyControls asserts the controls are actually connected.
func TestAppJS_WiresPrivacyControls(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	for _, ref := range []string{
		"settings-retention-scrub-days",
		"settings-retention-delete-days",
		"settings-export-data",
		"settings-erase-data",
		"/api/privacy/export",
		"/api/privacy/erase",
	} {
		if !strings.Contains(appJS, ref) {
			t.Errorf("app.js must reference %s", ref)
		}
	}
	if !strings.Contains(appJS, "retention_scrub_days") || !strings.Contains(appJS, "retention_delete_days") {
		t.Error("app.js must load and save the retention settings")
	}
	// Erasure is irreversible: the UI must ask before calling it.
	if !strings.Contains(appJS, "ERASE") {
		t.Error("app.js must require the typed confirmation the API expects")
	}
}

// ── helpers ──

// seedErasureFixture inserts snapshots through the store's real API so the test
// exercises the same write path the daemon uses.
func seedErasureFixture(t *testing.T, s *store.Store) {
	t.Helper()

	if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
		CapturedAt:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		AccountID:   1,
		Email:       "dev@example.com",
		TeamID:      "team-42",
		LoginMethod: "sso",
		RawJSON:     `{"email":"dev@example.com"}`,
		Quotas:      []api.GrokQuota{{Name: "credits", Utilization: 42}},
	}); err != nil {
		t.Fatalf("InsertGrokSnapshot: %v", err)
	}

	if _, err := s.InsertOllamaSnapshot(&api.OllamaSnapshot{
		CapturedAt:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		RawJSON:         `{"Email":"ollama-user@example.com"}`,
		Plan:            "pro",
		AccountName:     "Dev Person",
		AccountEmail:    "ollama-user@example.com",
		MonthlyUsedUSD:  12.5,
		MonthlyLimitUSD: 20,
	}); err != nil {
		t.Fatalf("InsertOllamaSnapshot: %v", err)
	}
}

// exportedBody runs an export and returns it, which is how these tests observe
// what is left in the database without reaching for the raw connection.
func exportedBody(t *testing.T, h *Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/privacy/export", nil)
	rr := httptest.NewRecorder()
	h.ExportData(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("export status = %d; body: %s", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}
