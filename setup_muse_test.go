package main

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func stubMuseDetect(t *testing.T, creds *api.MuseCredentials) {
	t.Helper()
	orig := detectMuseCredentialsFunc
	detectMuseCredentialsFunc = func(*slog.Logger) *api.MuseCredentials { return creds }
	t.Cleanup(func() { detectMuseCredentialsFunc = orig })
}

func TestCollectMuse_DetectedAndEnabled(t *testing.T) {
	stubMuseDetect(t, &api.MuseCredentials{APIKey: "login-key", Model: "muse-spark-1.3", Source: "keychain"})
	r := bufio.NewReader(strings.NewReader("y\n"))
	enabled, key := collectMuse(r, testLogger())
	if !enabled {
		t.Fatal("expected muse enabled")
	}
	if key != "" {
		t.Fatalf("auto-detect must not store a key, got %q", key)
	}
}

func TestCollectMuse_DetectedButDeclined(t *testing.T) {
	stubMuseDetect(t, &api.MuseCredentials{APIKey: "login-key", Model: "m", Source: "env"})
	r := bufio.NewReader(strings.NewReader("n\n"))
	enabled, key := collectMuse(r, testLogger())
	if enabled || key != "" {
		t.Fatalf("expected disabled, got enabled=%v key=%q", enabled, key)
	}
}

func TestCollectMuse_ManualKeyVerified(t *testing.T) {
	stubMuseDetect(t, nil)
	// verifyMuseKey is stubbed in TestMain: never touches the network.
	r := bufio.NewReader(strings.NewReader("manual-muse-key\n"))
	enabled, key := collectMuse(r, testLogger())
	if !enabled {
		t.Fatal("expected muse enabled")
	}
	if key != "manual-muse-key" {
		t.Fatalf("key = %q", key)
	}
}

func TestCollectMuse_EmptyAndDeclined(t *testing.T) {
	stubMuseDetect(t, nil)
	r := bufio.NewReader(strings.NewReader("\nn\n"))
	enabled, key := collectMuse(r, testLogger())
	if enabled || key != "" {
		t.Fatalf("expected disabled, got enabled=%v key=%q", enabled, key)
	}
}

func TestWriteEnvFile_Muse(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"

	cfg := &setupConfig{
		museEnabled:  true,
		adminUser:    "admin",
		adminPass:    "secret",
		port:         9211,
		pollInterval: 120,
	}
	if err := writeEnvFile(path, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	content := readSetupTestFile(t, path)
	if !strings.Contains(content, "MUSE_ENABLED=true") {
		t.Fatalf(".env missing MUSE_ENABLED:\n%s", content)
	}

	cfg2 := &setupConfig{
		museEnabled:  true,
		museKey:      "explicit-key",
		adminUser:    "admin",
		adminPass:    "secret",
		port:         9211,
		pollInterval: 120,
	}
	if err := writeEnvFile(path, cfg2); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	content = readSetupTestFile(t, path)
	if !strings.Contains(content, "META_API_KEY=explicit-key") {
		t.Fatalf(".env missing META_API_KEY:\n%s", content)
	}
}

func readSetupTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func TestFreshSetup_MuseOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	stubMuseDetect(t, &api.MuseCredentials{APIKey: "login-key", Model: "m", Source: "keychain"})

	input := strings.Join([]string{
		"10",   // Muse only
		"y",    // enable tracking
		"",     // admin user (default)
		"",     // auto-generate password
		"9211", // port
		"60",   // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup muse only: %v", err)
	}
	if !cfg.museEnabled {
		t.Fatal("expected muse enabled")
	}
	if cfg.museKey != "" {
		t.Fatalf("expected no stored key, got %q", cfg.museKey)
	}
}
