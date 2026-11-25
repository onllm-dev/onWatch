package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// Issue #162: the Anthropic auto-start toggle is an "off"/"on" enum, and any
// other value falls back to "off" so the starter can never be switched on by a
// malformed save.
func TestSanitizeProviderSettings_AnthropicAutoStart(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"on":   "on",
		"off":  "off",
		"yes":  "off",
		"true": "off",
	} {
		settings := map[string]interface{}{
			"anthropic": map[string]interface{}{"auto_start_5h": value},
		}
		sanitizeProviderSettings(settings)
		got := settings["anthropic"].(map[string]interface{})["auto_start_5h"]
		if got != want {
			t.Errorf("auto_start_5h %q -> %q, want %q", value, got, want)
		}
	}
}

// The settings payload reports the .env state of the auto-start toggle, so a
// dialog with nothing saved can show it rather than "Off", and saving another
// Anthropic field does not switch off a starter enabled in .env.
func TestGetSettings_ReportsAnthropicAutoStartEnvDefault(t *testing.T) {
	t.Parallel()
	for _, envOn := range []bool{true, false} {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		cfg := &config.Config{PollInterval: time.Minute, AnthropicAutoStart5h: envOn, CodexAutoStart5h: envOn, CodexAutoStart7d: !envOn}
		h := NewHandler(s, nil, nil, nil, cfg)
		rr := httptest.NewRecorder()
		h.GetSettings(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		s.Close()

		var body struct {
			ProviderEnvDefaults map[string]map[string]string `json:"provider_env_defaults"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		want := "off"
		if envOn {
			want = "on"
		}
		if got := body.ProviderEnvDefaults["anthropic"]["auto_start_5h"]; got != want {
			t.Fatalf("env on=%v: provider_env_defaults.anthropic.auto_start_5h = %q, want %q", envOn, got, want)
		}
		// Codex windows follow .env the same way when nothing is saved.
		if got := body.ProviderEnvDefaults["codex"]["auto_start_5h"]; got != want {
			t.Fatalf("env on=%v: codex auto_start_5h = %q, want %q", envOn, got, want)
		}
		want7d := map[bool]string{true: "off", false: "on"}[envOn]
		if got := body.ProviderEnvDefaults["codex"]["auto_start_7d"]; got != want7d {
			t.Fatalf("env on=%v: codex auto_start_7d = %q, want %q", envOn, got, want7d)
		}
	}
}

// The settings dialog prefers the .env default over the field's own default
// when nothing is saved.
func TestAppJS_ProviderDialogUsesEnvDefaults(t *testing.T) {
	t.Parallel()
	data, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read static/app.js: %v", err)
	}
	js := string(data)
	for _, want := range []string{
		"State.providerEnvDefaults = data.provider_env_defaults || {};",
		"State.providerEnvDefaults = d.provider_env_defaults || {};",
		// Reload Providers re-reads the .env defaults.
		"if (fresh) State.providerEnvDefaults = fresh.provider_env_defaults || {};",
		"const value = key && ps[key] ? ps[key] : (key ? env[key] : '');",
		// An unsaved toggle still showing its .env value is not saved, so
		// it keeps following .env.
		"if (unsaved && provData[id] === envDefaults[id]) delete provData[id];",
		"if (saved[id] === undefined || saved[id] === '') effectiveDefaults[id] = envDefaults[id];",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js is missing %q", want)
		}
	}
}

// Reload Providers re-reads .env into the live config, including the
// auto-start toggles that follow .env when nothing is saved.
func TestApplyProviderConfig_CopiesAutoStartToggles(t *testing.T) {
	t.Parallel()
	dst := &config.Config{}
	applyProviderConfig(dst, &config.Config{AnthropicAutoStart5h: true, CodexAutoStart5h: true, CodexAutoStart7d: true})
	if !dst.AnthropicAutoStart5h || !dst.CodexAutoStart5h || !dst.CodexAutoStart7d {
		t.Fatalf("auto-start toggles not copied: %+v", dst)
	}
	applyProviderConfig(dst, &config.Config{})
	if dst.AnthropicAutoStart5h || dst.CodexAutoStart5h || dst.CodexAutoStart7d {
		t.Fatal("auto-start toggles not cleared by a reload that turns them off")
	}
}
