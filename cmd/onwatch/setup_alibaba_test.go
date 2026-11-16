package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// writeAlibabaCookiesFile writes a minimal Netscape export with an Alibaba row.
func writeAlibabaCookiesFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "alibaba-cookies.txt")
	raw := "# Netscape HTTP Cookie File\n" +
		".alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write cookies file: %v", err)
	}
	return path
}

func TestCollectAlibabaCookiesFileAndRegion(t *testing.T) {
	path := writeAlibabaCookiesFile(t)
	r := bufio.NewReader(strings.NewReader(path + "\nus-east-1\n"))
	enabled, cookiesFile, region := collectAlibaba(r)
	if cookiesFile != path {
		t.Fatalf("cookiesFile = %q, want %q", cookiesFile, path)
	}
	if !enabled {
		t.Fatal("a configured Alibaba provider must be enabled")
	}
	if region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", region)
	}
}

func TestCollectAlibabaDefaultsRegionAndDashboardOption(t *testing.T) {
	// An empty path means "configure later from the dashboard" and enables the
	// provider so accounts can be added; the region keeps its default.
	r := bufio.NewReader(strings.NewReader("\n\n"))
	enabled, cookiesFile, region := collectAlibaba(r)
	if cookiesFile != "" {
		t.Fatalf("cookiesFile = %q, want empty", cookiesFile)
	}
	if !enabled {
		t.Fatal("choosing Alibaba without a file must still enable it for dashboard setup")
	}
	if region != "ap-southeast-1" {
		t.Fatalf("region = %q, want the default", region)
	}
}

func TestCollectAlibabaRejectsMissingFileThenContinues(t *testing.T) {
	path := writeAlibabaCookiesFile(t)
	// A missing file is reported and re-prompted; the user then gives a valid
	// path, then a bad region which is re-prompted.
	missing := filepath.Join(t.TempDir(), "nope.txt")
	r := bufio.NewReader(strings.NewReader(strings.Join([]string{
		missing,     // rejected: does not exist
		path,        // accepted
		"../../etc", // rejected region
		"cn-hangzhou",
	}, "\n") + "\n"))
	enabled, cookiesFile, region := collectAlibaba(r)
	if !enabled || cookiesFile != path {
		t.Fatalf("cookiesFile = %q enabled=%v", cookiesFile, enabled)
	}
	if region != "cn-hangzhou" {
		t.Fatalf("region = %q, want cn-hangzhou", region)
	}
}

func TestCollectAlibabaRejectsFileWithoutAlibabaCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unrelated.txt")
	if err := os.WriteFile(path, []byte(".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := writeAlibabaCookiesFile(t)
	r := bufio.NewReader(strings.NewReader(path + "\n" + good + "\n\n"))
	_, cookiesFile, _ := collectAlibaba(r)
	if cookiesFile != good {
		t.Fatalf("cookiesFile = %q, want the export with Alibaba cookies", cookiesFile)
	}
}

// The Alibaba menu entry is a single-provider choice that prompts for the
// cookies file path and region instead of a one-line secret.
func TestFreshSetup_AlibabaOnly(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	path := writeAlibabaCookiesFile(t)

	input := strings.Join([]string{
		"13",   // Alibaba Token Plan only
		path,   // cookies export path
		"",     // default region
		"",     // admin user (default)
		"",     // auto-generate password
		"9211", // port
		"60",   // interval
	}, "\n") + "\n"

	cfg, err := freshSetup(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("freshSetup alibaba only: %v", err)
	}
	if cfg.alibabaCookiesFile != path || !cfg.alibabaEnabled {
		t.Fatalf("alibaba config = %+v", cfg)
	}
	if cfg.alibabaRegion != "ap-southeast-1" {
		t.Fatalf("region = %q", cfg.alibabaRegion)
	}

	// The env file records the path and region, never the cookie contents.
	envPath := filepath.Join(home, ".env")
	if err := writeEnvFile(envPath, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	data, _ := os.ReadFile(envPath)
	env := string(data)
	want, ok := alibabaEnvValue(path)
	if !ok {
		t.Fatalf("alibabaEnvValue(%q) rejected a representable path", path)
	}
	if !strings.Contains(env, "ALIBABA_COOKIES_FILE="+want) {
		t.Fatalf("env is missing the quoted ALIBABA_COOKIES_FILE:\n%s", env)
	}
	if !strings.Contains(env, "ALIBABA_REGION=ap-southeast-1") {
		t.Fatalf("env is missing ALIBABA_REGION:\n%s", env)
	}
	if strings.Contains(env, "cna=abc") {
		t.Fatalf("env must never contain cookie contents:\n%s", env)
	}
}

func TestWriteEnvFile_AlibabaDashboardOnly(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	cfg := &setupConfig{
		alibabaEnabled: true,
		alibabaRegion:  "ap-southeast-1",
		adminUser:      "admin",
		adminPass:      "pw",
		port:           9211,
		pollInterval:   120,
	}
	if err := writeEnvFile(envPath, cfg); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "ALIBABA_ENABLED=true") {
		t.Fatalf("env is missing ALIBABA_ENABLED:\n%s", data)
	}
}

func TestLoadExistingEnvAlibaba(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "ALIBABA_COOKIES_FILE=/tmp/alibaba.txt\nALIBABA_REGION=cn-hangzhou\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env := loadExistingEnv(path)
	if !env.alibabaConfigured {
		t.Fatal("an ALIBABA_COOKIES_FILE entry must count as configured")
	}
	if !anyProviderConfigured(env) {
		t.Fatal("Alibaba alone must count as at least one configured provider")
	}
}

func TestAddMissingProvidersAddsAlibaba(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	path := writeAlibabaCookiesFile(t)

	envFile := filepath.Join(home, ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := loadExistingEnv(envFile)

	answers := []string{
		"n", // zai
		"n", // anthropic
		"n", // codex
		"n", // opencode (codex)
		"n", // antigravity
		"n", // gemini
		"n", // grok
		"n", // ollama
		"n", // command code
		"n", // opencode go
		"y", // add Alibaba
		path,
		"", // default region
	}
	r := bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n"))
	if err := addMissingProviders(r, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders: %v", err)
	}
	data, _ := os.ReadFile(envFile)
	out := string(data)
	want, ok := alibabaEnvValue(path)
	if !ok {
		t.Fatalf("alibabaEnvValue(%q) rejected a representable path", path)
	}
	if !strings.Contains(out, "ALIBABA_COOKIES_FILE="+want) {
		t.Fatalf("Alibaba not appended:\n%s", out)
	}
	if !strings.Contains(out, "ALIBABA_REGION=ap-southeast-1") {
		t.Fatalf("Alibaba region not appended:\n%s", out)
	}
}

// The Alibaba cookie file path is written into .env, which is loaded with
// godotenv. An unquoted value would be cut at " #", have "$" expanded, or break
// the whole file on a stray quote, so the path must round-trip byte for byte.
func TestAlibabaEnvValueRoundTripsThroughGodotenv(t *testing.T) {
	paths := []string{
		"/home/you/My Cookies/alibaba cookies.txt",
		"/home/you/alibaba#1/cookies.txt",
		"/home/you/$HOME/cookies.txt",
		"/home/you/cookies\"double.txt",
		// Backslashes that are not the final character are literal inside a
		// single-quoted value, so Windows-style paths must keep working.
		`C:\Users\me\cookies.txt`,
		`C:\Users\me\My Cookies\cookies #1.txt`,
	}
	for _, path := range paths {
		value, ok := alibabaEnvValue(path)
		if !ok {
			t.Fatalf("alibabaEnvValue(%q) rejected a representable path", path)
		}
		envPath := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(envPath, []byte("ALIBABA_COOKIES_FILE="+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := godotenv.Read(envPath)
		if err != nil {
			t.Fatalf("godotenv.Read for %q: %v", path, err)
		}
		if got["ALIBABA_COOKIES_FILE"] != path {
			t.Errorf("godotenv read back %q, want %q", got["ALIBABA_COOKIES_FILE"], path)
		}
		env := loadExistingEnv(envPath)
		if !env.alibabaConfigured {
			t.Errorf("loadExistingEnv did not detect %q as configured", path)
		}
		if env.alibabaCookiesFile != path {
			t.Errorf("loadExistingEnv read back %q, want %q", env.alibabaCookiesFile, path)
		}
	}
}

// A path containing the quote character (or a line break) cannot be represented
// in a single-quoted godotenv value; it must be rejected rather than written. A
// path ending in a backslash is also rejected: godotenv reads the backslash as
// escaping the closing quote and fails the whole .env as unterminated.
func TestAlibabaEnvValueRejectsUnrepresentablePaths(t *testing.T) {
	for _, path := range []string{
		"/home/you/it's here/cookies.txt",
		"/home/you/a\nb/cookies.txt",
		`/home/you/cookies\`,
		`C:\Users\me\cookies\`,
	} {
		if _, ok := alibabaEnvValue(path); ok {
			t.Errorf("alibabaEnvValue(%q) accepted an unrepresentable path", path)
		}
	}
}

// A path ending in a backslash is valid on disk on POSIX, so the wizard must
// reject it and re-prompt instead of writing a value that breaks the whole .env.
func TestCollectAlibabaRejectsTrailingBackslashPathThenContinues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a file name cannot end in a backslash on Windows")
	}
	good := writeAlibabaCookiesFile(t)
	bad := filepath.Join(t.TempDir(), `cookies\`)
	raw := "# Netscape HTTP Cookie File\n.alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n"
	if err := os.WriteFile(bad, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := alibabaEnvValue(bad); ok {
		t.Fatalf("test setup: %q should be unrepresentable", bad)
	}
	r := bufio.NewReader(strings.NewReader(strings.Join([]string{bad, good, ""}, "\n") + "\n"))
	_, cookiesFile, _ := collectAlibaba(r)
	if cookiesFile != good {
		t.Fatalf("cookiesFile = %q, want the representable path %q", cookiesFile, good)
	}
}

// The wizard re-prompts on an unrepresentable path instead of writing it.
func TestCollectAlibabaRejectsUnrepresentablePathThenContinues(t *testing.T) {
	good := writeAlibabaCookiesFile(t)
	// A directory whose name contains a quote is valid on disk but not in .env.
	badDir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(badDir, "cookies.txt")
	raw := "# Netscape HTTP Cookie File\n.alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n"
	if err := os.WriteFile(bad, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(strings.NewReader(strings.Join([]string{bad, good, ""}, "\n") + "\n"))
	_, cookiesFile, _ := collectAlibaba(r)
	if cookiesFile != good {
		t.Fatalf("cookiesFile = %q, want the representable path %q", cookiesFile, good)
	}
}
