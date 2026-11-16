package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// Zenmux is a single-line Management API Key provider. There is no local
// auto-detect and no network probe in setup, so the plain secret-prompt pattern
// applies and every test below stays offline.

func TestCollectZenmuxKeyRejectsEmptyThenAccepts(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\nzenmux_management_key\n"))
	if key := collectZenmuxKey(r); key != "zenmux_management_key" {
		t.Fatalf("collectZenmuxKey = %q, want the key after the empty retry", key)
	}
}

func TestWriteEnvFileZenmux(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := &setupConfig{
		zenmuxKey:    "zenmux_management_key",
		adminUser:    "admin",
		adminPass:    "secret",
		port:         9211,
		pollInterval: 120,
	}
	if err := writeEnvFile(path, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ZENMUX_MANAGEMENT_API_KEY=zenmux_management_key") {
		t.Fatalf(".env missing the Zenmux key:\n%s", data)
	}
}

func TestFreshSetupZenmuxOnly(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")

	input := strings.Join([]string{
		"12",                    // Zenmux only
		"zenmux_management_key", // Management API Key
		"",                      // admin user (default)
		"",                      // auto-generate password
		"9211",                  // port
		"60",                    // interval
	}, "\n") + "\n"

	cfg, err := freshSetup(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("freshSetup: %v", err)
	}
	if cfg.zenmuxKey != "zenmux_management_key" {
		t.Fatalf("zenmux key = %q", cfg.zenmuxKey)
	}
	if cfg.syntheticKey != "" || cfg.commandCodeKey != "" {
		t.Fatalf("unexpected extra providers: %+v", cfg)
	}
}

// The menu index must line up with the Zenmux entry. (A blank key is rejected,
// so a reader that never supplies one would loop; this supplies one at once.)
func TestFreshSetupZenmuxMenuIndex(t *testing.T) {
	cfg, err := freshSetup(bufio.NewReader(strings.NewReader("12\nsome_zenmux_key\n\n\n9211\n60\n")))
	if err != nil {
		t.Fatalf("freshSetup: %v", err)
	}
	if cfg.zenmuxKey != "some_zenmux_key" {
		t.Fatalf("choice 12 did not select Zenmux: %+v", cfg)
	}
}

func TestAddMissingProvidersZenmux(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_x\n"), 0600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	existing := loadExistingEnv(envFile)

	// Prompt order after the already-configured Synthetic: zai, anthropic,
	// codex, opencode, antigravity, gemini, grok, ollama, commandcode, zenmux,
	// opencode go.
	answers := []string{
		"n", "n", "n", "n", "n", "n", "n", "n", "n",
		"y",                     // add Zenmux
		"zenmux_management_key", // Management API Key
		"n",                     // skip OpenCode Go
	}
	r := bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n"))
	if err := addMissingProviders(r, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders: %v", err)
	}
	data, _ := os.ReadFile(envFile)
	if !strings.Contains(string(data), "ZENMUX_MANAGEMENT_API_KEY=zenmux_management_key") {
		t.Fatalf("Zenmux not appended:\n%s", data)
	}
}

func TestLoadExistingEnvReadsZenmux(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("ZENMUX_MANAGEMENT_API_KEY=zenmux_saved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := loadExistingEnv(path)
	if env.zenmuxKey != "zenmux_saved" {
		t.Fatalf("zenmux key = %q, want the saved value", env.zenmuxKey)
	}
	if !anyProviderConfigured(env) {
		t.Fatal("a saved Zenmux key must count as a configured provider")
	}
}

func TestPrintSummaryIncludesZenmux(t *testing.T) {
	out := captureStdout(t, func() {
		printSummary(&setupConfig{zenmuxKey: "k", adminUser: "admin", adminPass: "x", port: 9211, pollInterval: 60})
	})
	if !strings.Contains(out, "Zenmux") {
		t.Fatalf("summary omitted Zenmux:\n%s", out)
	}
}
