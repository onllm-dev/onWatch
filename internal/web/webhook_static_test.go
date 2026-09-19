package web

import (
	"strings"
	"testing"
)

func readSettingsTemplate(t *testing.T) string {
	t.Helper()
	data, err := templatesFS.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read templates/settings.html: %v", err)
	}
	return string(data)
}

func TestSettingsTemplate_HasWebhookChannelAndConfig(t *testing.T) {
	t.Parallel()

	html := readSettingsTemplate(t)

	for _, id := range []string{
		`id="channel-webhook"`,
		`id="webhook-url"`,
		`id="webhook-token"`,
		`id="webhook-headers"`,
		`id="webhook-timeout"`,
		`id="webhook-retries"`,
		`id="webhook-test-btn"`,
		`id="webhook-test-result"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("settings page is missing %s", id)
		}
	}

	for _, id := range []string{
		`id="webhook-event-warning"`,
		`id="webhook-event-critical"`,
		`id="webhook-event-reset"`,
		`id="webhook-event-auth-error"`,
		`id="webhook-event-starter-success"`,
		`id="webhook-event-starter-failure"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("settings page is missing the per-event toggle %s", id)
		}
	}
}

func TestAppJS_WebhookSettingsRoundTrip(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	for _, fn := range []string{
		"function loadWebhookSettings(webhook)",
		"function collectWebhookSettings()",
		"function parseWebhookHeaders(text)",
		"function setupWebhookTest()",
	} {
		if !strings.Contains(appJS, fn) {
			t.Errorf("app.js is missing %s", fn)
		}
	}

	if !strings.Contains(appJS, "setupWebhookTest();") {
		t.Error("setupWebhookTest is never wired up")
	}
	if !strings.Contains(appJS, "'/api/settings/webhook/test'") {
		t.Error("app.js does not call the webhook test endpoint")
	}
	if !strings.Contains(appJS, "webhook: document.getElementById('channel-webhook')?.checked ?? false") {
		t.Error("the webhook channel toggle is not included in the saved channels")
	}
}

// The token is write-only: the server masks it, so the client must never try to
// repopulate the field from the settings response.
func TestAppJS_WebhookTokenFieldIsNotRepopulated(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)

	start := strings.Index(appJS, "function loadWebhookSettings(webhook)")
	if start < 0 {
		t.Fatal("loadWebhookSettings not found")
	}
	end := strings.Index(appJS[start:], "\nfunction ")
	if end < 0 {
		t.Fatal("could not bound loadWebhookSettings")
	}
	body := appJS[start : start+end]

	if !strings.Contains(body, "tokenInput.value = ''") {
		t.Error("loadWebhookSettings must clear the token field rather than repopulate it")
	}
	if strings.Contains(body, "w.bearer_token;") || strings.Contains(body, "w.bearer_token ||") {
		t.Error("loadWebhookSettings must not read the masked bearer_token value")
	}
	if !strings.Contains(body, "bearer_token_set") {
		t.Error("loadWebhookSettings should surface whether a token is saved")
	}
}
