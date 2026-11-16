package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

func TestCollectOpenCodeGo_UsageAPIKeyVerified(t *testing.T) {
	origVerify := verifyOpenCodeGoKey
	verifyOpenCodeGoKey = func(key string) (string, error) {
		return "5h 5% / weekly 2% / monthly 1% used", nil
	}
	t.Cleanup(func() { verifyOpenCodeGoKey = origVerify })

	r := bufio.NewReader(strings.NewReader("oc_sk_abcdefghijklmnop\n"))
	cfg := collectOpenCodeGo(r, testLogger())
	if cfg.key != "oc_sk_abcdefghijklmnop" {
		t.Fatalf("key = %q", cfg.key)
	}
	if cfg.workspaceID != "" || cfg.cookie != "" {
		t.Fatalf("key mode must not set workspace/cookie, got %+v", cfg)
	}
}

func TestCollectOpenCodeGo_InvalidPrefixRetriesThenCookie(t *testing.T) {
	origVerify := verifyOpenCodeGoKey
	verifyOpenCodeGoKey = func(key string) (string, error) { return "ok", nil }
	t.Cleanup(func() { verifyOpenCodeGoKey = origVerify })

	// A value that is neither a key nor a workspace ID is rejected, then the
	// user switches to cookie mode: workspace id + session cookie.
	input := strings.Join([]string{
		"not-a-key",      // rejected: not oc_sk_ and not wrk_
		"wrk_abcdefghij", // workspace id -> cookie mode
		"__Host-console_session=abc",
	}, "\n") + "\n"
	r := bufio.NewReader(strings.NewReader(input))
	cfg := collectOpenCodeGo(r, testLogger())
	if cfg.key != "" {
		t.Fatalf("expected cookie mode, got key %q", cfg.key)
	}
	if cfg.workspaceID != "wrk_abcdefghij" || cfg.cookie == "" {
		t.Fatalf("cookie mode values = %+v", cfg)
	}
}

func TestCollectOpenCodeGo_RejectedKeyThenEmptyDisables(t *testing.T) {
	origVerify := verifyOpenCodeGoKey
	verifyOpenCodeGoKey = func(key string) (string, error) { return "", api.ErrOpenCodeUnauthorized }
	t.Cleanup(func() { verifyOpenCodeGoKey = origVerify })

	// A rejected key retries; Enter then leaves it unconfigured (no key, no
	// workspace/cookie).
	input := strings.Join([]string{"oc_sk_rejectedkey0000", ""}, "\n") + "\n"
	r := bufio.NewReader(strings.NewReader(input))
	cfg := collectOpenCodeGo(r, testLogger())
	if cfg.key != "" || cfg.workspaceID != "" || cfg.cookie != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestOpenCodeGo_WriteEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	cfg := &setupConfig{
		openCodeGoKey:        "oc_sk_envkey",
		adminUser:            "admin",
		adminPass:            "pw",
		port:                 9211,
		pollInterval:         120,
		commandCodeEnabled:   true,
		commandCodeKey:       "user_cmd",
		openCodeGoConfigured: true,
	}
	if err := writeEnvFile(path, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	if !strings.Contains(out, "OPENCODE_GO_API_KEY=oc_sk_envkey") {
		t.Fatalf("env missing OPENCODE_GO_API_KEY:\n%s", out)
	}
	if !strings.Contains(out, "COMMAND_CODE_API_KEY=user_cmd") {
		t.Fatalf("env missing COMMAND_CODE_API_KEY:\n%s", out)
	}
}

func TestOpenCodeGo_CookieModeWriteEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	cfg := &setupConfig{
		openCodeGoWorkspaceID: "wrk_abc",
		openCodeGoAuthCookie:  "sess",
		openCodeGoConfigured:  true,
		adminUser:             "admin",
		adminPass:             "pw",
		port:                  9211,
		pollInterval:          120,
	}
	if err := writeEnvFile(path, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	if !strings.Contains(out, "OPENCODE_GO_WORKSPACE_ID=wrk_abc") || !strings.Contains(out, "OPENCODE_GO_AUTH_COOKIE=sess") {
		t.Fatalf("env missing cookie-mode keys:\n%s", out)
	}
}

// The existing-.env path must be able to add OpenCode Go and Command Code when
// they are absent.
func TestAddMissingProviders_OpenCodeGoAndCommandCode(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")

	origVerify := verifyOpenCodeGoKey
	verifyOpenCodeGoKey = func(key string) (string, error) { return "ok", nil }
	t.Cleanup(func() { verifyOpenCodeGoKey = origVerify })
	origCC := verifyCommandCodeKey
	verifyCommandCodeKey = func(key, baseURL string) (string, error) { return "ok", nil }
	t.Cleanup(func() { verifyCommandCodeKey = origCC })
	stubCommandCodeDetect(t, nil)

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_x\n"), 0600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	existing := loadExistingEnv(envFile)

	// Decline synthetic/zai/anthropic/codex/opencode/antigravity/gemini/grok/
	// ollama, accept Command Code, skip Zenmux and Alibaba, then add OpenCode Go
	// (prompt order).
	answers := []string{
		"n", // zai
		"n", // anthropic
		"n", // codex
		"n", // opencode (codex)
		"n", // antigravity
		"n", // gemini
		"n", // grok
		"n", // ollama
		"y", // add Command Code
		"user_added_cmd",
		"n", // skip Zenmux
		"n", // skip Alibaba
		"y", // add OpenCode Go
		"oc_sk_added_from_env",
	}
	r := bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n"))
	if err := addMissingProviders(r, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders: %v", err)
	}
	data, _ := os.ReadFile(envFile)
	out := string(data)
	if !strings.Contains(out, "OPENCODE_GO_API_KEY=oc_sk_added_from_env") {
		t.Fatalf("OpenCode Go not appended:\n%s", out)
	}
	if !strings.Contains(out, "COMMAND_CODE_API_KEY=user_added_cmd") {
		t.Fatalf("Command Code not appended:\n%s", out)
	}
}
