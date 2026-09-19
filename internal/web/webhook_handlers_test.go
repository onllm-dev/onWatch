package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// errWebhookTest stands in for a delivery failure from the notifier.
var errWebhookTest = errors.New("notify.Webhook: request failed: endpoint unreachable")

// putSettings issues a settings update and returns the recorder.
func putSettings(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	return rr
}

// storedWebhook reads back the persisted webhook settings blob.
func storedWebhook(t *testing.T, s *store.Store) map[string]interface{} {
	t.Helper()
	raw, err := s.GetSetting("webhook")
	if err != nil {
		t.Fatalf("GetSetting(webhook) error = %v", err)
	}
	if raw == "" {
		t.Fatal("webhook settings were not persisted")
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("stored webhook is not valid JSON: %v", err)
	}
	return out
}

func TestUpdateSettingsPersistsWebhookConfig(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, NewSessionStore("admin", "hash-for-test", nil), createTestConfigWithSynthetic())

	rr := putSettings(t, h, `{"webhook":{
		"url":"http://localhost:8080/onwatch",
		"headers":{"X-Title":"onWatch"},
		"bearer_token":"tk_secret",
		"timeout_seconds":7,
		"retries":1,
		"events":{"warning":true,"critical":true,"reset":false,"auth_error":true,"starter_success":false,"starter_failure":true}
	}}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	saved := storedWebhook(t, s)
	if saved["url"] != "http://localhost:8080/onwatch" {
		t.Errorf("url = %v, want the submitted URL", saved["url"])
	}
	if saved["timeout_seconds"] != float64(7) {
		t.Errorf("timeout_seconds = %v, want 7", saved["timeout_seconds"])
	}
	events, ok := saved["events"].(map[string]interface{})
	if !ok {
		t.Fatalf("events = %v, want an object", saved["events"])
	}
	if events["critical"] != true || events["starter_failure"] != true {
		t.Errorf("events = %v, want critical and starter_failure enabled", events)
	}
	if events["reset"] != false || events["starter_success"] != false {
		t.Errorf("events = %v, want reset and starter_success disabled", events)
	}
	headers, ok := saved["headers"].(map[string]interface{})
	if !ok || headers["X-Title"] != "onWatch" {
		t.Errorf("headers = %v, want X-Title preserved", saved["headers"])
	}
}

// The bearer token is a credential and must not be written to the DB in the clear.
func TestUpdateSettingsEncryptsWebhookBearerToken(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, NewSessionStore("admin", "hash-for-test", nil), createTestConfigWithSynthetic())

	rr := putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic","bearer_token":"tk_secret"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	saved := storedWebhook(t, s)
	token, _ := saved["bearer_token"].(string)
	if token == "tk_secret" {
		t.Error("bearer token was stored in plaintext, want it encrypted")
	}
	if !notify.IsEncryptedValue(token) {
		t.Errorf("bearer_token = %q, want an encrypted value", token)
	}
}

func TestUpdateSettingsPreservesWebhookTokenWhenOmitted(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, NewSessionStore("admin", "hash-for-test", nil), createTestConfigWithSynthetic())

	if rr := putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic","bearer_token":"tk_secret"}}`); rr.Code != http.StatusOK {
		t.Fatalf("first save status = %d, want 200", rr.Code)
	}
	first, _ := storedWebhook(t, s)["bearer_token"].(string)

	// The UI never re-sends the token, so a blank value must not wipe it.
	if rr := putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/other","bearer_token":""}}`); rr.Code != http.StatusOK {
		t.Fatalf("second save status = %d, want 200", rr.Code)
	}

	saved := storedWebhook(t, s)
	if saved["bearer_token"] != first {
		t.Errorf("bearer_token = %v, want the previous token preserved", saved["bearer_token"])
	}
	if saved["url"] != "https://ntfy.sh/other" {
		t.Errorf("url = %v, want the updated URL", saved["url"])
	}
}

func TestUpdateSettingsRejectsInvalidWebhookURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"missing scheme", "ntfy.sh/topic"},
		{"missing host", "http://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := store.New(":memory:")
			defer s.Close()
			h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

			body, _ := json.Marshal(map[string]interface{}{"webhook": map[string]interface{}{"url": tt.url}})
			rr := putSettings(t, h, string(body))

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for URL %q", rr.Code, tt.url)
			}
			if raw, _ := s.GetSetting("webhook"); raw != "" {
				t.Errorf("webhook settings were persisted despite validation failure: %s", raw)
			}
		})
	}
}

// Clearing the URL disables the channel and must be allowed.
func TestUpdateSettingsAllowsClearingWebhookURL(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	if rr := putSettings(t, h, `{"webhook":{"url":""}}`); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateSettingsRejectsTooManyWebhookHeaders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	headers := map[string]string{}
	for i := 0; i < maxWebhookHeaders+1; i++ {
		headers[strings.Repeat("X", i+1)] = "v"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"webhook": map[string]interface{}{"url": "https://ntfy.sh/topic", "headers": headers},
	})

	if rr := putSettings(t, h, string(body)); rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for too many headers", rr.Code)
	}
}

// Regression: the channel toggles round-trip. They were previously dropped
// because the handler's notification struct had no Channels field, so the
// re-marshalled blob silently overwrote whatever the user had selected.
func TestUpdateSettingsPersistsNotificationChannels(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	rr := putSettings(t, h, `{"notifications":{
		"warning_threshold":80,
		"critical_threshold":95,
		"notify_warning":true,
		"notify_critical":true,
		"cooldown_minutes":30,
		"channels":{"email":false,"push":true,"webhook":true}
	}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	raw, _ := s.GetSetting("notifications")
	var saved struct {
		Channels notify.NotificationChannels `json:"channels"`
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatalf("stored notifications is not valid JSON: %v", err)
	}

	if saved.Channels.Email {
		t.Error("channels.email = true, want the submitted false to persist")
	}
	if !saved.Channels.Push {
		t.Error("channels.push = false, want the submitted true to persist")
	}
	if !saved.Channels.Webhook {
		t.Error("channels.webhook = false, want the submitted true to persist")
	}
}

// Omitting channels entirely keeps the previous selection rather than resetting it.
func TestUpdateSettingsPreservesChannelsWhenOmitted(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	putSettings(t, h, `{"notifications":{"warning_threshold":80,"critical_threshold":95,"channels":{"email":false,"push":false,"webhook":true}}}`)
	putSettings(t, h, `{"notifications":{"warning_threshold":70,"critical_threshold":90}}`)

	raw, _ := s.GetSetting("notifications")
	var saved struct {
		Channels notify.NotificationChannels `json:"channels"`
	}
	json.Unmarshal([]byte(raw), &saved)

	if !saved.Channels.Webhook || saved.Channels.Email || saved.Channels.Push {
		t.Errorf("channels = %+v, want the previous selection preserved", saved.Channels)
	}
}

func TestGetSettingsMasksWebhookBearerToken(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, NewSessionStore("admin", "hash-for-test", nil), createTestConfigWithSynthetic())

	putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic","bearer_token":"tk_secret"}}`)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	h.GetSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "tk_secret") {
		t.Error("response contains the plaintext bearer token")
	}

	var resp struct {
		Webhook map[string]interface{} `json:"webhook"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp.Webhook == nil {
		t.Fatal("response has no webhook settings")
	}
	if resp.Webhook["bearer_token"] != "" {
		t.Errorf("bearer_token = %v, want an empty string", resp.Webhook["bearer_token"])
	}
	if resp.Webhook["bearer_token_set"] != true {
		t.Errorf("bearer_token_set = %v, want true", resp.Webhook["bearer_token_set"])
	}
	if resp.Webhook["url"] != "https://ntfy.sh/topic" {
		t.Errorf("url = %v, want it returned for display", resp.Webhook["url"])
	}
}

func TestWebhookTestEndpointRejectsNonPost(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifier{})

	req := httptest.NewRequest(http.MethodGet, "/api/settings/webhook/test", nil)
	rr := httptest.NewRecorder()
	h.WebhookTest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestWebhookTestEndpointSuccess(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifier{})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/webhook/test", nil)
	rr := httptest.NewRecorder()
	h.WebhookTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}
}

func TestWebhookTestEndpointReportsFailure(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	h.SetNotifier(&mockNotifier{webhookTestErr: errWebhookTest})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/webhook/test", nil)
	rr := httptest.NewRecorder()
	h.WebhookTest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a failure body", rr.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["success"] != false {
		t.Errorf("success = %v, want false", resp["success"])
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "unreachable") {
		t.Errorf("message = %q, want the delivery error surfaced", msg)
	}
}

func TestWebhookTestEndpointWithoutNotifier(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())

	req := httptest.NewRequest(http.MethodPost, "/api/settings/webhook/test", nil)
	rr := httptest.NewRecorder()
	h.WebhookTest(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// Saving webhook settings must reconfigure the live sender, the same way an
// SMTP save reconfigures the mailer.
func TestUpdateSettingsReconfiguresWebhookSender(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())
	mock := &mockNotifier{}
	h.SetNotifier(mock)

	putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic"}}`)

	if !mock.configureWebhookCalled {
		t.Error("ConfigureWebhook was not called after saving webhook settings")
	}
}

// net/http rejects non-token header names at request time, so they must be
// rejected at save time instead of failing every delivery later.
func TestUpdateSettingsRejectsMalformedWebhookHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"space in name", map[string]string{"X Title": "onWatch"}},
		{"quote in name", map[string]string{`X-Tags"`: "warning"}},
		{"non-ascii name", map[string]string{"X-Titelé": "x"}},
		{"newline in value", map[string]string{"X-Title": "a\nb"}},
		{"control char in value", map[string]string{"X-Title": "a\x01b"}},
		{"empty name", map[string]string{"": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := store.New(":memory:")
			defer s.Close()
			h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

			body, _ := json.Marshal(map[string]interface{}{
				"webhook": map[string]interface{}{"url": "https://ntfy.sh/topic", "headers": tc.headers},
			})
			if rr := putSettings(t, h, string(body)); rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestUpdateSettingsAcceptsValidWebhookHeaders(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	body, _ := json.Marshal(map[string]interface{}{
		"webhook": map[string]interface{}{
			"url":     "https://ntfy.sh/topic",
			"headers": map[string]string{"X-Title": "onWatch alert", "X-Priority": "high", "X-Tags": "warning,onwatch", "Cache-Control": "no-cache"},
		},
	})
	if rr := putSettings(t, h, string(body)); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

// Zero is accepted as "use the default"; the validation message must say so.
func TestUpdateSettingsWebhookTimeoutBoundsMessage(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	if rr := putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic","timeout_seconds":0}}`); rr.Code != http.StatusOK {
		t.Errorf("timeout 0: status = %d, want 200 (default applies)", rr.Code)
	}
	rr := putSettings(t, h, `{"webhook":{"url":"https://ntfy.sh/topic","timeout_seconds":-1}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("timeout -1: status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "0") || !strings.Contains(rr.Body.String(), "15") {
		t.Errorf("message = %s, want it to state the real 0-15 range", rr.Body.String())
	}
}

// The repeat flag is what makes the cooldown setting meaningful, so it must
// survive a save like the other notification toggles.
func TestUpdateSettingsPersistsNotifyRepeat(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())

	rr := putSettings(t, h, `{"notifications":{
		"warning_threshold":80,
		"critical_threshold":95,
		"notify_critical":true,
		"notify_repeat":true,
		"cooldown_minutes":45
	}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	raw, _ := s.GetSetting("notifications")
	var saved struct {
		NotifyRepeat    bool `json:"notify_repeat"`
		CooldownMinutes int  `json:"cooldown_minutes"`
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatalf("stored notifications is not valid JSON: %v", err)
	}
	if !saved.NotifyRepeat {
		t.Error("notify_repeat = false, want the submitted true to persist")
	}
	if saved.CooldownMinutes != 45 {
		t.Errorf("cooldown_minutes = %d, want 45", saved.CooldownMinutes)
	}
}

func TestSettingsTemplateHasRepeatToggle(t *testing.T) {
	html := readSettingsTemplate(t)
	if !strings.Contains(html, `id="notify-repeat"`) {
		t.Error("settings page is missing the repeat-alerts toggle")
	}
}

func TestAppJSRoundTripsNotifyRepeat(t *testing.T) {
	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "notify_repeat: document.getElementById('notify-repeat')?.checked ?? false") {
		t.Error("app.js does not send notify_repeat when saving")
	}
	if !strings.Contains(appJS, "repeatCheck.checked = !!n.notify_repeat") {
		t.Error("app.js does not load notify_repeat back into the form")
	}
}
