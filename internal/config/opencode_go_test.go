package config

import (
	"slices"
	"strings"
	"testing"
)

// A usage-API key alone enables OpenCode Go tracking; no workspace/cookie needed.
func TestLoadOpenCodeGoAPIKeyEnablesProvider(t *testing.T) {
	t.Setenv("OPENCODE_GO_WORKSPACE_ID", "")
	t.Setenv("OPENCODE_GO_AUTH_COOKIE", "")
	t.Setenv("OPENCODE_GO_API_KEY", " oc_sk_secretvalue123 ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenCodeGoAPIKey != "oc_sk_secretvalue123" {
		t.Fatalf("APIKey=%q", cfg.OpenCodeGoAPIKey)
	}
	if !cfg.OpenCodeGoConfigured() || !cfg.HasProvider("opencode") || !slices.Contains(cfg.AvailableProviders(), "opencode") {
		t.Fatal("OpenCode Go not reported as configured with only a usage API key")
	}
	if strings.Contains(cfg.String(), "oc_sk_secretvalue123") {
		t.Fatal("String() leaks the OpenCode Go API key")
	}
}

func TestOpenCodeGoConfiguredLegacyPairStillWorks(t *testing.T) {
	both := &Config{OpenCodeGoWorkspaceID: "wrk_1", OpenCodeGoAuthCookie: "cookie"}
	onlyID := &Config{OpenCodeGoWorkspaceID: "wrk_1"}
	if !both.OpenCodeGoConfigured() || onlyID.OpenCodeGoConfigured() || (&Config{}).OpenCodeGoConfigured() {
		t.Fatal("legacy workspace+cookie detection changed")
	}
}

// Base-URL overrides point the balance and OpenCode clients at a proxy or a
// mock server (the e2e suite uses them).
func TestLoadProviderBaseURLOverrides(t *testing.T) {
	t.Setenv("OPENCODE_GO_BASE_URL", " http://127.0.0.1:19212 ")
	t.Setenv("DEEPSEEK_BASE_URL", "http://127.0.0.1:19213")
	t.Setenv("MOONSHOT_BASE_URL", "http://127.0.0.1:19214")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenCodeGoBaseURL != "http://127.0.0.1:19212" || cfg.DeepSeekBaseURL != "http://127.0.0.1:19213" || cfg.MoonshotBaseURL != "http://127.0.0.1:19214" {
		t.Fatalf("base URLs = %q %q %q", cfg.OpenCodeGoBaseURL, cfg.DeepSeekBaseURL, cfg.MoonshotBaseURL)
	}
}
