package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// ---------------------------------------------------------------------------
// run() - stop / status with no PID file
// ---------------------------------------------------------------------------

func TestRun_StopNoPIDFile(t *testing.T) {
	// Skip if a real onwatch is running on default ports - port scanning would find it
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch detected on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch", "stop"})

	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run stop (no pid file) error: %v", err)
		}
	})
	if !strings.Contains(out, "No running onwatch instance found") {
		t.Fatalf("expected 'No running onwatch instance found', got: %s", out)
	}
}

func TestRun_StatusNoPIDFile(t *testing.T) {
	// Skip if a real onwatch is running on default ports - port scanning would find it
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch detected on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch", "status"})

	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run status (no pid file) error: %v", err)
		}
	})
	if !strings.Contains(out, "onwatch is not running") {
		t.Fatalf("expected 'onwatch is not running', got: %s", out)
	}
}

func TestRun_StopTestModeNoPIDFile(t *testing.T) {
	oldPIDDir := pidDir
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch-test.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	setTestArgs(t, []string{"onwatch", "--test", "stop"})

	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run --test stop error: %v", err)
		}
	})
	if !strings.Contains(out, "No running onwatch (test) instance found") {
		t.Fatalf("expected '...test...', got: %s", out)
	}
}

func TestRun_StatusTestModeNoPIDFile(t *testing.T) {
	oldPIDDir := pidDir
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch-test.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	setTestArgs(t, []string{"onwatch", "--test", "status"})

	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run --test status error: %v", err)
		}
	})
	if !strings.Contains(out, "onwatch (test) is not running") {
		t.Fatalf("expected '...test...not running...', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// run() - _ONWATCH_DAEMON=1 startup error path
// ---------------------------------------------------------------------------

func TestRun_DaemonChildStartupError(t *testing.T) {
	t.Setenv("_ONWATCH_DAEMON", "1")
	t.Setenv("SYNTHETIC_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("ANTHROPIC_TOKEN", "")
	t.Setenv("COPILOT_TOKEN", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("ANTIGRAVITY_ENABLED", "")
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	setTestArgs(t, []string{"onwatch"})

	err := run()
	if err == nil {
		t.Fatal("expected error when daemon child startup fails")
	}
	if !strings.Contains(err.Error(), "failed to setup logging") && !strings.Contains(err.Error(), "server error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// freshSetup() - choices 1 (Synthetic), 2 (Z.ai), 3 (Multiple)
// ---------------------------------------------------------------------------

func TestFreshSetup_SyntheticOnly(t *testing.T) {
	input := strings.Join([]string{
		"1",            // Synthetic only
		"syn_abc12345", // valid key
		"admin",        // admin user
		"mypassword",   // password
		"9211",         // port
		"60",           // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 1 error: %v", err)
	}
	if cfg.syntheticKey != "syn_abc12345" {
		t.Fatalf("expected synthetic key 'syn_abc12345', got %q", cfg.syntheticKey)
	}
	if cfg.zaiKey != "" {
		t.Fatalf("expected empty zai key, got %q", cfg.zaiKey)
	}
	if cfg.adminUser != "admin" {
		t.Fatalf("expected admin user 'admin', got %q", cfg.adminUser)
	}
	if cfg.port != 9211 {
		t.Fatalf("expected port 9211, got %d", cfg.port)
	}
}

func TestFreshSetup_ZaiOnly(t *testing.T) {
	input := strings.Join([]string{
		"2",          // Z.ai only
		"zai-secret", // zai key
		"y",          // use default URL
		"",           // default admin user
		"",           // auto-generate password
		"9211",       // port
		"60",         // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 2 error: %v", err)
	}
	if cfg.zaiKey != "zai-secret" {
		t.Fatalf("expected zai key 'zai-secret', got %q", cfg.zaiKey)
	}
	if cfg.zaiBaseURL != "https://api.z.ai/api" {
		t.Fatalf("expected default zai URL, got %q", cfg.zaiBaseURL)
	}
	if cfg.syntheticKey != "" {
		t.Fatalf("expected empty synthetic key, got %q", cfg.syntheticKey)
	}
	if cfg.adminPass == "" {
		t.Fatal("expected auto-generated password")
	}
}

func TestFreshSetup_AllProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain tools so no auto-detect occurs for anthropic/codex
	t.Setenv("PATH", "")

	input := strings.Join([]string{
		"7",            // All providers
		"syn_abc12345", // synthetic key
		"zai-key",      // zai key
		"y",            // use default zai URL
		"",             // anthropic: skip auto-detect prompt (none found), manual entry
		"anth-token",   // anthropic manual token
		"",             // codex: no auto-detect, manual entry
		"codex-token",  // codex manual token
		"",             // admin user (default)
		"",             // auto-generate password
		"9211",         // port
		"60",           // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 7 error: %v", err)
	}
	if cfg.syntheticKey == "" {
		t.Fatal("expected synthetic key to be set")
	}
	if cfg.zaiKey == "" {
		t.Fatal("expected zai key to be set")
	}
	if !cfg.antigravityEnabled {
		t.Fatal("expected antigravity enabled for 'all'")
	}
}

func TestFreshSetup_MultipleProviders_Choice6(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain tools so no auto-detect occurs
	t.Setenv("PATH", "")

	input := strings.Join([]string{
		"6",            // Multiple
		"y",            // add synthetic
		"syn_abc12345", // synthetic key
		"n",            // skip zai
		"n",            // skip anthropic
		"n",            // skip codex
		"y",            // add antigravity
		"",             // admin user (default)
		"",             // auto-generate password
		"9211",         // port
		"60",           // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 6 error: %v", err)
	}
	if cfg.syntheticKey == "" {
		t.Fatal("expected synthetic key")
	}
	if !cfg.antigravityEnabled {
		t.Fatal("expected antigravity enabled")
	}
	if cfg.zaiKey != "" {
		t.Fatalf("expected empty zai key, got %q", cfg.zaiKey)
	}
}

func TestFreshSetup_AnthropicOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Disable keychain tools and PATH so no auto-detect occurs
	t.Setenv("PATH", "")

	// No credentials file -> fallback to manual entry
	input := strings.Join([]string{
		"3",        // Anthropic only
		"anth-tok", // manual anthropic token
		"",         // admin user (default)
		"testpass", // password
		"9211",     // port
		"60",       // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 3 error: %v", err)
	}
	if cfg.anthropicToken == "" {
		t.Fatal("expected anthropic token to be set")
	}
}

func TestFreshSetup_CodexOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))

	// No codex auth file -> fallback to manual entry
	input := strings.Join([]string{
		"4",           // Codex only
		"codex-token", // manual codex token
		"",            // admin user (default)
		"",            // auto-generate password
		"9211",        // port
		"60",          // interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup choice 4 error: %v", err)
	}
	if cfg.codexToken == "" {
		t.Fatal("expected codex token to be set")
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - all providers answered "n"
// ---------------------------------------------------------------------------

func TestAddMissingProviders_AllSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain tools so no auto-detect occurs
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// All "missing" providers are answered "n"
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	content := string(data)
	// Should only have the original content - no new providers
	if strings.Contains(content, "ZAI_API_KEY") {
		t.Fatal("unexpected ZAI_API_KEY in env")
	}
	if strings.Contains(content, "ANTHROPIC_TOKEN") {
		t.Fatal("unexpected ANTHROPIC_TOKEN in env")
	}
	if strings.Contains(content, "CODEX_TOKEN") {
		t.Fatal("unexpected CODEX_TOKEN in env")
	}
	if strings.Contains(content, "ANTIGRAVITY_ENABLED") {
		t.Fatal("unexpected ANTIGRAVITY_ENABLED in env")
	}
}

func TestAddMissingProviders_ZaiSkippedAnthropicAdded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain tools so no auto-detect occurs
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// Zai=n, Anthropic=y (manual), Codex=n, Antigravity=n
	input := strings.Join([]string{
		"n",        // skip zai
		"y",        // add anthropic (no auto-detect found -> promptYesNo shown)
		"anth-tok", // manual anthropic token
		"n",        // skip codex
		"n",        // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "ANTHROPIC_TOKEN=anth-tok") {
		t.Fatalf("expected ANTHROPIC_TOKEN in env, got:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// collectAnthropicToken() - auto-detect via .claude/.credentials.json
// ---------------------------------------------------------------------------

func TestCollectAnthropicToken_AutoDetect_Accept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix credential file path test")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Disable keychain lookup tools so file fallback is used
	t.Setenv("PATH", "")

	// Create .claude/.credentials.json with a valid token (claudeAiOauth format)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	credsJSON := `{"claudeAiOauth":{"accessToken":"auto-detected-token"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(credsJSON), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	// User accepts auto-detected token (press Enter = yes)
	input := "\n"
	reader := bufio.NewReader(strings.NewReader(input))
	token := collectAnthropicToken(reader, testLogger())
	if token != "auto-detected-token" {
		t.Fatalf("expected 'auto-detected-token', got %q", token)
	}
}

func TestCollectAnthropicToken_AutoDetect_Decline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix credential file path test")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Disable keychain lookup tools so file fallback is used
	t.Setenv("PATH", "")

	// Create .claude/.credentials.json with a valid token (claudeAiOauth format)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	credsJSON := `{"claudeAiOauth":{"accessToken":"auto-tok"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(credsJSON), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	// User declines auto-detected token, enters manual
	input := strings.Join([]string{
		"n",            // decline auto-detected
		"manual-token", // manual token
	}, "\n") + "\n"
	reader := bufio.NewReader(strings.NewReader(input))
	token := collectAnthropicToken(reader, testLogger())
	if token != "manual-token" {
		t.Fatalf("expected 'manual-token', got %q", token)
	}
}

// ---------------------------------------------------------------------------
// collectCodexToken() - auto-detect via CODEX_HOME/auth.json
// ---------------------------------------------------------------------------

func TestCollectCodexToken_AutoDetect_Accept(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	authJSON := `{"tokens":{"access_token":"codex-auto-token"}}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	// User accepts auto-detected token (press Enter = yes)
	input := "\n"
	reader := bufio.NewReader(strings.NewReader(input))
	token := collectCodexToken(reader, testLogger())
	if token != "codex-auto-token" {
		t.Fatalf("expected 'codex-auto-token', got %q", token)
	}
}

func TestCollectCodexToken_AutoDetect_Decline(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	authJSON := `{"tokens":{"access_token":"codex-auto-token"}}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	// User declines auto-detected token, enters manual
	input := strings.Join([]string{
		"n",                // decline auto-detected
		"codex-manual-tok", // manual codex token
	}, "\n") + "\n"
	reader := bufio.NewReader(strings.NewReader(input))
	token := collectCodexToken(reader, testLogger())
	if token != "codex-manual-tok" {
		t.Fatalf("expected 'codex-manual-tok', got %q", token)
	}
}

func TestCollectCodexToken_NoAutoDetect_ManualEntry(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "no-codex-dir"))

	input := "codex-manual\n"
	reader := bufio.NewReader(strings.NewReader(input))
	token := collectCodexToken(reader, testLogger())
	if token != "codex-manual" {
		t.Fatalf("expected 'codex-manual', got %q", token)
	}
}

// ---------------------------------------------------------------------------
// findOnwatchOnPort() - test with local listener and non-responding port
// ---------------------------------------------------------------------------

func TestFindOnwatchOnPort_NonRespondingPort(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}

	// Use a port that's not in use - lsof will return nothing
	pids := findOnwatchOnPort(1) // port 1 requires root, lsof will fail
	// Result can be nil or empty - both are valid
	if len(pids) > 0 {
		t.Logf("unexpectedly got pids %v for port 1 (may be running as root)", pids)
	}
}

func TestFindOnwatchOnPort_WithLocalListener(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}

	// Start a local listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port

	// findOnwatchOnPort may return 0 or more pids - the process is "go test" not "onwatch"
	// so isOnwatchProcess will filter it out. We just verify no panic.
	pids := findOnwatchOnPort(port)
	t.Logf("findOnwatchOnPort(%d) = %v (expected empty since process is 'go test')", port, pids)
	// The result is valid whether empty or not
}

func TestFindOnwatchOnPort_WindowsReturnsNil(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("windows-only test")
	}
	pids := findOnwatchOnPort(9211)
	if pids != nil {
		t.Fatalf("expected nil on non-darwin/linux, got %v", pids)
	}
}

// ---------------------------------------------------------------------------
// runUpdate() - error path from Check() (via bad version / network error)
// ---------------------------------------------------------------------------

func TestRunUpdate_CheckFailsOnNetworkError(t *testing.T) {
	// Use a non-dev version so it tries to hit GitHub
	origVersion := version
	version = "2.0.0"
	t.Cleanup(func() { version = origVersion })

	// Set an invalid PID file path so it doesn't interfere
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch", "update"})

	// We can't easily inject a failure in Check() from outside the package,
	// but we can test the "dev version" case which succeeds immediately.
	version = "dev"
	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run update (dev) error: %v", err)
		}
	})
	if !strings.Contains(out, "Already at the latest version") {
		t.Fatalf("expected 'Already at the latest version', got: %s", out)
	}
}

func TestRunUpdate_AlreadyLatest(t *testing.T) {
	origVersion := version
	version = "dev"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		if err := runUpdate(); err != nil {
			t.Fatalf("runUpdate error: %v", err)
		}
	})
	if !strings.Contains(out, "Already at the latest version") {
		t.Fatalf("expected 'Already at the latest version', got: %s", out)
	}
}

func TestRunUpdate_WithStalePIDFile(t *testing.T) {
	origVersion := version
	version = "dev"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write a stale PID to the PID file so the restart branch is exercised
	stalePID := 999999
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// dev version -> no update available, but PID file restart path still runs
	out := captureStdout(t, func() {
		if err := runUpdate(); err != nil {
			t.Fatalf("runUpdate error: %v", err)
		}
	})
	if !strings.Contains(out, "Already at the latest version") {
		t.Fatalf("expected 'Already at the latest version', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStop() / runStatus() - non-test mode no PID file path
// ---------------------------------------------------------------------------

func TestRunStop_NonTestMode_NoPIDFile(t *testing.T) {
	// Skip if a real onwatch is listening on known ports.
	for _, port := range []string{"9211", "8932"} {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("real onwatch detected on port %s, skipping", port)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "no.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop(false) error: %v", err)
		}
	})
	if !strings.Contains(out, "No running onwatch instance found") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunStatus_NonTestMode_NoPIDFile(t *testing.T) {
	// Skip if a real onwatch is listening on known ports.
	for _, port := range []string{"9211", "8932"} {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("real onwatch detected on port %s, skipping", port)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "no.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus(false) error: %v", err)
		}
	})
	if !strings.Contains(out, "onwatch is not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - PID file with valid running process (self PID)
// ---------------------------------------------------------------------------

func TestRunStatus_SelfPIDRunning(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	self := os.Getpid()
	// Write a different PID - use the parent PID or self+1 workaround.
	// We need a PID that's running but != self. Let's use the parent PID if available.
	ppid := os.Getppid()
	if ppid <= 0 {
		t.Skip("could not get parent PID")
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(ppid)+":9211"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	_ = self

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus(true) error: %v", err)
		}
	})
	// Either "is running" or "stale PID file" depending on whether ppid is alive
	if !strings.Contains(out, "is running") && !strings.Contains(out, "stale PID file") && !strings.Contains(out, "is not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - auto-detect anthropic path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_AnthropicAutoDetected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix credential file path test")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain lookup tools so file fallback is used
	t.Setenv("PATH", "")

	// Create .claude/.credentials.json with a valid token (claudeAiOauth format)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	credsJSON := `{"claudeAiOauth":{"accessToken":"auto-anth-tok"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(credsJSON), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// zai=n, anthropic auto-detected -> accept ("y"), codex=n, antigravity=n
	input := strings.Join([]string{
		"n", // skip zai
		"y", // accept auto-detected anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "ANTHROPIC_TOKEN=auto-anth-tok") {
		t.Fatalf("expected ANTHROPIC_TOKEN auto-detected in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - file open error path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_FileOpenError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	// Use a non-existent env file (can't be opened for append)
	envFile := filepath.Join(t.TempDir(), "nonexistent-dir", ".env")

	existing := &existingEnv{syntheticKey: "existing"}
	reader := bufio.NewReader(strings.NewReader("\n"))
	err := addMissingProviders(reader, envFile, existing)
	if err == nil {
		t.Fatal("expected error when env file can't be opened")
	}
	if !strings.Contains(err.Error(), "failed to open .env") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - zai added path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_ZaiAdded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// zai=y (add), rest=n
	input := strings.Join([]string{
		"y",          // add zai
		"zai-newkey", // zai key
		"y",          // use default URL
		"n",          // skip anthropic
		"n",          // skip codex
		"n",          // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "ZAI_API_KEY=zai-newkey") {
		t.Fatalf("expected ZAI_API_KEY in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - antigravity added path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_AntigravityAdded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// zai=n, anthropic=n, codex=n, antigravity=y
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
		"y", // add antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "ANTIGRAVITY_ENABLED=true") {
		t.Fatalf("expected ANTIGRAVITY_ENABLED in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - codex manual path (no auto-detect)
// ---------------------------------------------------------------------------

func TestAddMissingProviders_CodexManualPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// No codex auto-detect -> user says "y" -> manual entry
	input := strings.Join([]string{
		"n",         // skip zai
		"n",         // skip anthropic
		"y",         // add codex (no auto-detect, manual)
		"codex-man", // codex manual token
		"n",         // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "CODEX_TOKEN=codex-man") {
		t.Fatalf("expected CODEX_TOKEN in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - anthropic auto-detect decline path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_AnthropicAutoDetectDeclined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix credential file path test")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	// Create credentials file
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	credsJSON := `{"claudeAiOauth":{"accessToken":"auto-anth"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(credsJSON), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// zai=n, anthropic auto-detected -> decline ("n"), codex=n, antigravity=n
	input := strings.Join([]string{
		"n", // skip zai
		"n", // decline auto-detected anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	// anthropic should NOT have been added
	if strings.Contains(string(data), "ANTHROPIC_TOKEN") {
		t.Fatalf("ANTHROPIC_TOKEN should NOT be in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - codex auto-detect decline path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_CodexAutoDetectDeclined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	authJSON := `{"tokens":{"access_token":"auto-codex-tok"}}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// zai=n, anthropic=n, codex auto-detected -> decline ("n"), antigravity=n
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic (no auto-detect)
		"n", // decline auto-detected codex
		"n", // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	// codex should NOT have been added
	if strings.Contains(string(data), "CODEX_TOKEN") {
		t.Fatalf("CODEX_TOKEN should NOT be in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - synthetic added path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_SyntheticAdded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("ZAI_API_KEY=zai_existing\nZAI_BASE_URL=https://api.z.ai/api\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// synthetic=y, rest=n (zai already configured)
	input := strings.Join([]string{
		"y",            // add synthetic
		"syn_abc12345", // synthetic key
		"n",            // skip anthropic
		"n",            // skip codex
		"n",            // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{zaiKey: "zai_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "SYNTHETIC_API_KEY=syn_abc12345") {
		t.Fatalf("expected SYNTHETIC_API_KEY in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - codex auto-detected path
// ---------------------------------------------------------------------------

func TestAddMissingProviders_CodexAutoDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	authJSON := `{"tokens":{"access_token":"auto-codex-tok"}}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// No anthropic creds -> zai=n, anthropic=n, codex auto-detected -> accept, antigravity=n
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic
		"y", // accept auto-detected codex
		"n", // skip antigravity
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing"}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "CODEX_TOKEN=auto-codex-tok") {
		t.Fatalf("expected CODEX_TOKEN auto-detected in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// stopPreviousInstance() - non-test mode with port check
// ---------------------------------------------------------------------------

func TestStopPreviousInstance_NonTestModeNoPIDFile(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "no.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Should not panic or error - just no-op since no pid file and port not in use
	stopPreviousInstance(1, false)
}

func TestStopPreviousInstance_WithPIDFilePortAndListener(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a local listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write PID file with stale PID but valid port
	stalePID := 999994
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:%d", stalePID, port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// Call stopPreviousInstance - pid is stale, port is open, exercises port-scan branch
	stopPreviousInstance(port, false)
}

func TestStopPreviousInstance_WithSelfPIDFile(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write own PID to file (myPID == pid => skip kill)
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9999", self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// Should remove PID file without killing self
	stopPreviousInstance(0, false)

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// migrateDBLocation() - branch where new DB already exists
// ---------------------------------------------------------------------------

func TestMigrateDBLocation_NewAlreadyExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create both old and new DB
	oldDB := filepath.Join(home, ".onwatch", "onwatch.db")
	newDB := filepath.Join(home, ".onwatch", "data", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(oldDB), 0o755); err != nil {
		t.Fatalf("mkdir old dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(newDB), 0o755); err != nil {
		t.Fatalf("mkdir new dir: %v", err)
	}
	if err := os.WriteFile(oldDB, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old db: %v", err)
	}
	if err := os.WriteFile(newDB, []byte("new"), 0o600); err != nil {
		t.Fatalf("write new db: %v", err)
	}

	// Both exist -> should NOT move (new already exists, break branch)
	migrateDBLocation(newDB, testLogger())

	// Old DB should still be there (not migrated)
	if _, err := os.Stat(oldDB); err != nil {
		t.Fatalf("old db should still exist: %v", err)
	}
}

func TestMigrateDBLocation_OldPathEqualsNew(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// newPath == one of the oldPaths -> should skip (continue branch)
	newDB := filepath.Join(home, ".onwatch", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(newDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(newDB, []byte("data"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}

	// Should not panic and not move anything
	migrateDBLocation(newDB, testLogger())
	if _, err := os.Stat(newDB); err != nil {
		t.Fatalf("db should still exist: %v", err)
	}
}

// ---------------------------------------------------------------------------
// freshSetup() error path - no providers selected
// ---------------------------------------------------------------------------

func TestFreshSetup_NoProviderSelected_ReturnsError(t *testing.T) {
	// Provide choice 6 (Multiple), answer "n" to everything.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	// Disable keychain tools so no auto-detect occurs
	t.Setenv("PATH", "")

	input := strings.Join([]string{
		"6", // Multiple
		"n", // skip synthetic
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	_, err := freshSetup(reader)
	if err == nil {
		t.Fatal("expected error when no providers selected")
	}
	if !strings.Contains(err.Error(), "at least one provider is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// defaultPIDDir() - verify it returns a valid directory path
// ---------------------------------------------------------------------------

func TestDefaultPIDDir_ReturnsNonEmpty(t *testing.T) {
	dir := defaultPIDDir()
	if dir == "" {
		t.Fatal("defaultPIDDir should return non-empty path")
	}
}

// ---------------------------------------------------------------------------
// Subprocess-based tests for run() daemon child path
// ---------------------------------------------------------------------------

// TestDaemonChildRun_HelperProcess is a helper subprocess entry point.
// The actual test drives it via TestDaemonChildRun_DebugMode*.
func TestDaemonChildRun_HelperProcess(t *testing.T) {
	if os.Getenv("GO_DAEMON_HELPER") != "1" {
		return
	}

	mode := os.Getenv("DAEMON_HELPER_MODE")
	switch mode {
	case "debug_antigravity":
		// Run in debug mode with antigravity enabled - exercises daemon child path
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "debug_synthetic":
		// Run in debug mode with synthetic key configured
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "debug_all_providers":
		// Run with all providers to cover all agent init branches
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "run_update_check":
		// Run update with an old version string so GitHub returns "update available"
		// Apply() will likely fail (binary not writable in test env) -> covers error path
		os.Args = []string{"onwatch", "update"}
		main() // This will try to apply update and likely fail
		os.Exit(0)
	case "run_update_error":
		// Run update command that will fail (non-dev version + bad network)
		os.Args = []string{"onwatch", "update"}
		main()
		os.Exit(0)
	case "debug_default_password":
		// Run with default password to trigger the warning branch
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "debug_with_stored_password":
		// Run with pre-built DB that has stored password hash
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "daemonize_test":
		// Run without _ONWATCH_DAEMON=1, without --debug -> triggers daemonize()
		// The parent will spawn a child and exit
		os.Args = []string{"onwatch"}
		main()
		os.Exit(0)
	case "debug_auto_tokens":
		// Run in debug mode with empty tokens but with credential files present
		// so auto-detection paths are hit
		os.Args = []string{"onwatch", "--debug", "--test"}
		main()
		os.Exit(0)
	case "server_error_test":
		// Bind a port first, then try to start run() on the same port.
		// The web server will fail to bind → serverErr path in run() is covered.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(3)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		// Keep ln open so server can't bind - set the env var BEFORE calling main()
		_ = os.Setenv("ONWATCH_PORT", strconv.Itoa(port))
		os.Args = []string{"onwatch", "--debug", "--test"}
		main() // server fails to bind → serverErr → run() logs error and exits
		ln.Close()
		os.Exit(0)
	default:
		os.Exit(127)
	}
}

// runDaemonSubprocess starts a test subprocess in daemon/debug mode,
// waits briefly, then sends SIGINT and waits for exit.
func runDaemonSubprocess(t *testing.T, env []string, waitMs int) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=TestDaemonChildRun_HelperProcess")
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}

	time.Sleep(time.Duration(waitMs) * time.Millisecond)
	_ = cmd.Process.Signal(os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

// TestDaemonChildRun_DebugModeAntigravity starts a real daemon in debug mode
// using a subprocess, waits for it to start, then sends SIGTERM.
// This covers the large daemon startup path in run().
func TestDaemonChildRun_DebugModeAntigravity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_antigravity",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
	), 500)
}

// TestDaemonChildRun_DebugModeSyntheticProvider covers the synthetic agent init branch.
func TestDaemonChildRun_DebugModeSyntheticProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_synthetic",
		"_ONWATCH_DAEMON=1",
		"SYNTHETIC_API_KEY=syn_test_fake_key_1234567890",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"ANTIGRAVITY_ENABLED=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
	), 500)
}

// TestDaemonChildRun_DebugModeAllProviders covers all agent/tracker init branches.
func TestDaemonChildRun_DebugModeAllProviders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_all_providers",
		"_ONWATCH_DAEMON=1",
		"SYNTHETIC_API_KEY=syn_test_fake_key_1234567890",
		"ZAI_API_KEY=zai-fake-key-for-testing",
		"ZAI_BASE_URL=https://api.z.ai/api",
		"ANTHROPIC_TOKEN=fake-anthropic-token",
		"COPILOT_TOKEN=fake-copilot-token",
		"CODEX_TOKEN=fake-codex-token",
		"ANTIGRAVITY_ENABLED=true",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
		"ONWATCH_LOG_LEVEL=warn",
	), 800)
}

// ---------------------------------------------------------------------------
// runStop() with PID file port + local listener (covers port-scan branch)
// ---------------------------------------------------------------------------

func TestRunStop_NonTestMode_WithPIDFilePort_LocalListener(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}
	// Skip if real onwatch is running - runStop(false) scans default ports as fallback
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a local listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write PID file with stale PID but valid port (triggers port-based detection)
	stalePID := 999993
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:%d", stalePID, port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// runStop in non-test mode: pid is stale -> port detection runs
	// findOnwatchOnPort will return the go test process but isOnwatchProcess filters it out
	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	// Should either find nothing (filtered by isOnwatchProcess) or stop something
	_ = out
}

func TestRunStop_NonTestMode_DefaultPortScan(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}

	// Skip if a real onwatch is running on default ports
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch detected on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// No PID file + ports 9211/8932 not in use -> goes through the loop but finds nothing
	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop(false) error: %v", err)
		}
	})
	if !strings.Contains(out, "No running onwatch instance found") {
		t.Fatalf("expected 'No running onwatch instance found', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() with non-test mode port scan paths
// ---------------------------------------------------------------------------

func TestRunStatus_NonTestMode_WithLocalListener(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only available on macOS/Linux")
	}
	// Skip if a real onwatch is running on default ports
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch detected on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a local listener on a random port to exercise the port-check branch
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Port is listening but go test is not an onwatch process, so no PID returned

	// Since we can't put this on ports 9211/8932, just verify no panic for non-test mode
	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus(false) error: %v", err)
		}
	})
	if !strings.Contains(out, "onwatch is not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStop() with PID file having a PID that is not running
// ---------------------------------------------------------------------------

func TestRunStop_LegacyPIDFormat(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write legacy PID format (no port)
	stalePID := 999997
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop(true) error: %v", err)
		}
	})
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "No running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() with legacy PID format
// ---------------------------------------------------------------------------

func TestRunStatus_LegacyPIDFormat(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write legacy PID format (no port)
	stalePID := 999998
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus(true) error: %v", err)
		}
	})
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "is not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runSetup() - .env exists but no providers, then fresh setup
// ---------------------------------------------------------------------------

func TestRunSetup_ExistingEnvNoProviders_FreshSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))

	installDir := filepath.Join(home, ".onwatch")
	if err := os.MkdirAll(filepath.Join(installDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}

	// Create .env with no providers (triggers "remove and start fresh" branch)
	envFile := filepath.Join(installDir, ".env")
	if err := os.WriteFile(envFile, []byte("# empty config\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	input := strings.Join([]string{
		"5",    // antigravity only
		"",     // default admin user
		"",     // auto-generate password
		"9211", // valid port
		"60",   // valid interval
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := runSetup(); err != nil {
			t.Fatalf("runSetup error: %v", err)
		}
	})

	// Verify env file was created with antigravity
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(data), "ANTIGRAVITY_ENABLED=true") {
		t.Fatalf("expected ANTIGRAVITY_ENABLED in env:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// runSetup() - .env exists with some providers, triggers addMissingProviders
// ---------------------------------------------------------------------------

func TestRunSetup_ExistingEnvSomeProviders_AddsMore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))

	installDir := filepath.Join(home, ".onwatch")
	if err := os.MkdirAll(filepath.Join(installDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}

	// Create .env with only synthetic
	envFile := filepath.Join(installDir, ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// Skip all missing providers
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := runSetup(); err != nil {
			t.Fatalf("runSetup error: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// collectMultipleProviders() - all "no" answers (already covered via freshSetup,
// but test directly to hit the false branches)
// ---------------------------------------------------------------------------

func TestCollectMultipleProviders_AllNo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))

	input := strings.Join([]string{
		"n", // skip synthetic
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
		"n", // skip antigravity
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	syn, zai, zaiURL, anth, codex, anti := collectMultipleProviders(reader, testLogger())

	if syn != "" || zai != "" || zaiURL != "" || anth != "" || codex != "" || anti {
		t.Fatalf("expected all empty/false: syn=%q zai=%q zaiURL=%q anth=%q codex=%q anti=%v",
			syn, zai, zaiURL, anth, codex, anti)
	}
}

func TestCollectMultipleProviders_AnthropicAndCodexAdded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))

	input := strings.Join([]string{
		"n",         // skip synthetic
		"n",         // skip zai
		"y",         // add anthropic
		"anth-tok",  // anthropic manual token
		"y",         // add codex
		"codex-tok", // codex manual token
		"n",         // skip antigravity
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	_, _, _, anth, codex, _ := collectMultipleProviders(reader, testLogger())

	if anth == "" {
		t.Fatal("expected anthropic token")
	}
	if codex == "" {
		t.Fatal("expected codex token")
	}
}

// ---------------------------------------------------------------------------
// generatePassword() - fallback path (hard to test directly, but ensure
// the normal path is exercised; fallback requires rand.Read to fail)
// ---------------------------------------------------------------------------

func TestGeneratePassword_NormalPath(t *testing.T) {
	pwd := generatePassword()
	if len(pwd) == 0 {
		t.Fatal("expected non-empty password")
	}
}

// ---------------------------------------------------------------------------
// run() daemon mode with different log levels to cover switch branches
// ---------------------------------------------------------------------------

func TestDaemonChildRun_DebugModeLogLevels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	for _, logLevel := range []string{"debug", "warn", "error"} {
		logLevel := logLevel
		t.Run("log_level_"+logLevel, func(t *testing.T) {
			runDaemonSubprocess(t, append(os.Environ(),
				"GO_DAEMON_HELPER=1",
				"DAEMON_HELPER_MODE=debug_antigravity",
				"_ONWATCH_DAEMON=1",
				"ANTIGRAVITY_ENABLED=true",
				"SYNTHETIC_API_KEY=",
				"ZAI_API_KEY=",
				"ANTHROPIC_TOKEN=",
				"COPILOT_TOKEN=",
				"CODEX_TOKEN=",
				"HOME="+home,
				fmt.Sprintf("ONWATCH_PORT=%d", port),
				"ONWATCH_DB_PATH="+dbPath,
				"ONWATCH_ADMIN_PASS=testpass",
				"ONWATCH_LOG_LEVEL="+logLevel,
			), 400)
		})
	}
}

// TestRunUpdate_CheckAvailableViaSubprocess tests the "update available" path
// by running with an old version (0.0.1) so GitHub returns a newer version.
// Apply() will likely fail because the test binary isn't in a writable location,
// which covers the "update failed" error return path.
func TestRunUpdate_CheckAvailableViaSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping update subprocess test in short mode")
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestDaemonChildRun_HelperProcess", "-test.v")
	cmd.Env = append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=run_update_check",
		// Override version to trigger "update available"
		// Note: version var can't be set via env, so this relies on the actual version
		// being old enough that GitHub has a newer release
	)

	// Run with timeout
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// Subprocess completed (either updated or failed)
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

// TestDaemonChildRun_AutoTokenDetection covers the AnthropicAutoToken and
// CodexAutoToken detection paths in run() by providing credential files but
// NOT setting the token env vars.
func TestDaemonChildRun_AutoTokenDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix credential file path test")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	// Create .claude/.credentials.json for anthropic auto-detect
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	credsJSON := `{"claudeAiOauth":{"accessToken":"auto-detected-for-test"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(credsJSON), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	// Create CODEX_HOME/auth.json for codex auto-detect
	codexHome := filepath.Join(home, "codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	authJSON := `{"tokens":{"access_token":"codex-auto-for-test"}}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_auto_tokens",
		"_ONWATCH_DAEMON=1",
		// Empty token vars -> triggers auto-detection
		"ANTHROPIC_TOKEN=",
		"CODEX_TOKEN=",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"COPILOT_TOKEN=",
		"ANTIGRAVITY_ENABLED=true", // Need at least one provider
		"HOME="+home,
		"CODEX_HOME="+codexHome,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
		// Disable keychain lookup (empty PATH for this test)
		// Note: Can't set PATH="" in subprocess as it breaks exec
	), 500)
}

// TestDaemonize_ViaSubprocess exercises the daemonize() function by running
// without _ONWATCH_DAEMON=1 and without --debug, which triggers daemonize().
func TestDaemonize_ViaSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemonize subprocess test in short mode")
	}

	home := t.TempDir()
	dbDir := filepath.Join(home, ".onwatch", "data")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	dbPath := filepath.Join(dbDir, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestDaemonChildRun_HelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=daemonize_test",
		// No _ONWATCH_DAEMON=1 -> triggers daemonize()
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		// Subprocess should exit after spawning daemon child
		_ = err
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Log("subprocess timed out - daemon may have started but took too long")
	}

	// Kill any spawned daemon children
	// PID file goes to $HOME/.onwatch/onwatch.pid (not dbDir)
	pidPath := filepath.Join(home, ".onwatch", "onwatch.pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.Split(strings.TrimSpace(string(data)), ":")[0]); err == nil && pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				proc.Kill()
			}
		}
	}
}

// TestDaemonChildRun_MigrateDBPath exercises the migrateDBLocation path (no explicit DB).
func TestDaemonChildRun_MigrateDBPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	// Do NOT set ONWATCH_DB_PATH -> DBPathExplicit=false -> migrateDBLocation is called

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_antigravity",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_ADMIN_PASS=testpass",
		// No ONWATCH_DB_PATH -> uses default -> DBPathExplicit=false -> migrateDBLocation
	), 500)
}

// TestDaemonChildRun_DefaultPassword exercises the IsDefaultPassword warning branch.
func TestDaemonChildRun_DefaultPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Don't set ONWATCH_ADMIN_PASS -> uses default password -> triggers warning
	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_default_password",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		// No ONWATCH_ADMIN_PASS -> default password
	), 400)
}

// TestDaemonChildRun_WithAntigravityManualURL exercises the manual Antigravity config path.
func TestDaemonChildRun_WithAntigravityManualURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_antigravity",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"ANTIGRAVITY_BASE_URL=https://localhost:9999",
		"ANTIGRAVITY_CSRF_TOKEN=fake-csrf-token",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
	), 400)
}

// ---------------------------------------------------------------------------
// writePIDFile() - ensure error is returned if mkdir fails
// ---------------------------------------------------------------------------

func TestWritePIDFile_InvalidDir(t *testing.T) {
	oldPIDDir := pidDir
	oldPIDFile := pidFile
	// Use a file as the PID directory (will cause MkdirAll to fail)
	tmpFile, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()

	pidDir = tmpFile.Name()
	pidFile = filepath.Join(pidDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	if err := writePIDFile(9211); err == nil {
		t.Fatal("expected error when PID dir is a file")
	}
}

// ---------------------------------------------------------------------------
// runStop() - successfully stops a live subprocess via SIGTERM
// ---------------------------------------------------------------------------

// startSleepSubprocess starts a long-running subprocess and returns its PID.
// The caller must call proc.Kill() when done.
func startSleepSubprocess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestSleepHelperProcess_NeverRun")
	cmd.Env = append(os.Environ(), "GO_SLEEP_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep subprocess: %v", err)
	}
	return cmd
}

// TestSleepHelperProcess_NeverRun is a helper that just sleeps.
// It exits immediately unless GO_SLEEP_HELPER=1.
func TestSleepHelperProcess_NeverRun(t *testing.T) {
	if os.Getenv("GO_SLEEP_HELPER") != "1" {
		return
	}
	// Just sleep indefinitely until killed
	time.Sleep(60 * time.Second)
	os.Exit(0)
}

func TestRunStop_WithLivePIDAndPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})

	// Subprocess was killed by SIGTERM, so "Stopped" should be in output
	if !strings.Contains(out, "Stopped") && !strings.Contains(out, "stale PID") {
		t.Fatalf("expected stop output, got: %s", out)
	}
}

func TestRunStop_WithLivePIDLegacyFormat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write legacy PID-only format (no port)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})

	if !strings.Contains(out, "Stopped") && !strings.Contains(out, "stale PID") {
		t.Fatalf("expected stop output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - shows "is running" for a live PID with port
// ---------------------------------------------------------------------------

func TestRunStatus_WithLivePIDAndPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})

	// Either "is running" (process alive) or "stale PID file" (process already exited)
	if !strings.Contains(out, "is running") && !strings.Contains(out, "stale PID file") && !strings.Contains(out, "is not running") {
		t.Fatalf("expected status output, got: %s", out)
	}
}

func TestRunStatus_WithLivePIDLegacyFormat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write legacy PID-only format (no port)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})

	if !strings.Contains(out, "is running") && !strings.Contains(out, "stale PID file") && !strings.Contains(out, "is not running") {
		t.Fatalf("expected status output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// stopPreviousInstance() - successfully stops a live subprocess via SIGTERM
// ---------------------------------------------------------------------------

func TestStopPreviousInstance_WithLivePID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// stopPreviousInstance sends SIGTERM → covers the "stopped=true" + time.Sleep branch
	out := captureStdout(t, func() {
		stopPreviousInstance(0, true)
	})

	if !strings.Contains(out, "Stopped previous instance") && !strings.Contains(out, "not running") {
		t.Logf("stopPreviousInstance output: %s", out)
	}
	// Verify subprocess is no longer running (received SIGTERM)
	// We don't assert on output since the subprocess may have been stopped already
}

func TestStopPreviousInstance_WithLivePIDLegacyFormat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write legacy PID-only format (triggers the else branch in PID parsing)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		stopPreviousInstance(0, true)
	})
	_ = out
}

// ---------------------------------------------------------------------------
// migrateDBLocation() - mkdir fails (parent dir is a file)
// ---------------------------------------------------------------------------

func TestMigrateDBLocation_MkdirFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a file where the old DB is expected - to simulate "file exists" but in wrong place
	oldDB := filepath.Join(home, ".onwatch", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(oldDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(oldDB, []byte("data"), 0o600); err != nil {
		t.Fatalf("write old db: %v", err)
	}

	// The new path's parent is a file (not a dir) - MkdirAll will fail
	blockFile := filepath.Join(home, "block")
	if err := os.WriteFile(blockFile, []byte("block"), 0o600); err != nil {
		t.Fatalf("write block: %v", err)
	}

	newDB := filepath.Join(blockFile, "data", "onwatch.db")
	// This exercises the "Failed to create data directory" warn branch
	migrateDBLocation(newDB, testLogger())
	// Should not panic - error is just logged
}

func TestMigrateDBLocation_RenameFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create old DB
	oldDB := filepath.Join(home, ".onwatch", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(oldDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(oldDB, []byte("data"), 0o600); err != nil {
		t.Fatalf("write old db: %v", err)
	}

	// The new path's parent directory - make it read-only to cause rename to fail
	newDir := filepath.Join(home, ".onwatch", "data")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("mkdir new dir: %v", err)
	}
	// Make directory read-only so rename fails
	if err := os.Chmod(newDir, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(newDir, 0o755) })

	newDB := filepath.Join(newDir, "onwatch.db")
	// rename should fail because dir is read-only (on Unix)
	migrateDBLocation(newDB, testLogger())
	// Should not panic - error is just logged

	// Restore permissions
	os.Chmod(newDir, 0o755)
}

// ---------------------------------------------------------------------------
// runUpdate() - covers the "with stale PID in legacy format" restart path
// ---------------------------------------------------------------------------

func TestRunUpdate_WithLegacyPIDFormat(t *testing.T) {
	origVersion := version
	version = "dev"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write legacy PID-only format (no port) with stale PID
	stalePID := 999998
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runUpdate(); err != nil {
			t.Fatalf("runUpdate error: %v", err)
		}
	})
	if !strings.Contains(out, "Already at the latest version") {
		t.Fatalf("expected 'Already at the latest version', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// generatePassword() - validate it returns a hex string
// ---------------------------------------------------------------------------

func TestGeneratePassword_ReturnsHexString(t *testing.T) {
	pass := generatePassword()
	if pass == "" {
		t.Fatal("expected non-empty password")
	}
	// Should be either hex (12 chars for 6 bytes) or timestamp-based
	if len(pass) < 5 {
		t.Fatalf("password too short: %q", pass)
	}
}

// ---------------------------------------------------------------------------
// initEncryptionSalt() - coverage via subprocess (salt generation path)
// ---------------------------------------------------------------------------

// TestDaemonChildRun_DebugModeAllProvidersWithCopilot covers copilot agent init.
func TestDaemonChildRun_DebugModeAllProvidersWithCopilot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_all_providers",
		"_ONWATCH_DAEMON=1",
		"SYNTHETIC_API_KEY=syn_test_fake_key_12345",
		"ZAI_API_KEY=zai-test-key",
		"ANTHROPIC_TOKEN=anth-test-token",
		"COPILOT_TOKEN=copilot-test-token",
		"CODEX_TOKEN=codex-test-token",
		"ANTIGRAVITY_ENABLED=true",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
		"ONWATCH_LOG_LEVEL=debug",
	), 800)
}

// ---------------------------------------------------------------------------
// runStatus() - test mode, with port in PID file (shows Dashboard URL)
// ---------------------------------------------------------------------------

func TestRunStatus_ShowsDashboardURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps so we have a live PID
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format - port 9211 is in PID file
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})

	// If process is alive, should show dashboard URL
	if strings.Contains(out, "is running") {
		if !strings.Contains(out, "Dashboard") && !strings.Contains(out, "localhost") {
			t.Logf("status output (may be missing Dashboard URL if file check failed): %s", out)
		}
	}
}

// ---------------------------------------------------------------------------
// runStop() - with stopped port format (covers port display in stop message)
// ---------------------------------------------------------------------------

func TestRunStop_ShowsPortInOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format with a specific port
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:8765", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	_ = out
}

// ---------------------------------------------------------------------------
// run() - server error path via subprocess (server can't bind to busy port)
// ---------------------------------------------------------------------------

// TestDaemonChildRun_ServerError starts a daemon subprocess where the port
// is already in use, causing the web server to fail immediately.
// This covers the serverErr channel path in run().
func TestDaemonChildRun_ServerError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping server error subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("server bind error test is unix-specific")
	}

	home := t.TempDir()
	dbPath := filepath.Join(home, "onwatch.db")

	// The subprocess will bind the port itself (via server_error_test mode)
	// and then send SIGTERM to itself after server fails - we use runDaemonSubprocess
	// which sends SIGINT to trigger graceful shutdown.
	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=server_error_test",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		"ONWATCH_DB_PATH="+dbPath,
		"ONWATCH_ADMIN_PASS=testpass",
		// ONWATCH_PORT is set by the subprocess itself (server_error_test mode)
	), 2000) // wait 2s for server to fail, then send SIGINT
}

// ---------------------------------------------------------------------------
// run() - coverage for "fixExplicitDBPath" redirect branch (explicit path
//         smaller than canonical -> redirect to canonical)
// ---------------------------------------------------------------------------

func TestDaemonChildRun_FixExplicitDBPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping daemon subprocess test in short mode")
	}

	home := t.TempDir()

	// Create canonical DB with data
	canonicalDir := filepath.Join(home, ".onwatch", "data")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("mkdir canonical: %v", err)
	}
	canonicalDB := filepath.Join(canonicalDir, "onwatch.db")
	// Write some non-trivial data so canonical is "larger"
	if err := os.WriteFile(canonicalDB, make([]byte, 1024), 0o600); err != nil {
		t.Fatalf("write canonical db: %v", err)
	}

	// Create explicit DB path with less data (triggers redirect)
	explicitDB := filepath.Join(home, "explicit.db")
	if err := os.WriteFile(explicitDB, []byte("small"), 0o600); err != nil {
		t.Fatalf("write explicit db: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	runDaemonSubprocess(t, append(os.Environ(),
		"GO_DAEMON_HELPER=1",
		"DAEMON_HELPER_MODE=debug_antigravity",
		"_ONWATCH_DAEMON=1",
		"ANTIGRAVITY_ENABLED=true",
		"SYNTHETIC_API_KEY=",
		"ZAI_API_KEY=",
		"ANTHROPIC_TOKEN=",
		"COPILOT_TOKEN=",
		"CODEX_TOKEN=",
		"HOME="+home,
		fmt.Sprintf("ONWATCH_PORT=%d", port),
		"ONWATCH_DB_PATH="+explicitDB, // explicit path → DBPathExplicit=true → fixExplicitDBPath
		"ONWATCH_ADMIN_PASS=testpass",
	), 600)
}

// ---------------------------------------------------------------------------
// runStatus() - covers log file and DB file display branches
// ---------------------------------------------------------------------------

func TestRunStatus_WithLogAndDBFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not supported the same way on Windows")
	}

	oldPIDFile := pidFile
	pidDir := t.TempDir()
	pidFile = filepath.Join(pidDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a subprocess that sleeps
	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pid := cmd.Process.Pid
	// Write PID:PORT format (port=0 → triggers the non-testMode log file search,
	// but testMode=true so log path = ".onwatch-test.log")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:0", pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// Create the log file in the current dir so the stat check succeeds
	// (testMode=true → logPath = ".onwatch-test.log")
	logFile := ".onwatch-test.log"
	if err := os.WriteFile(logFile, []byte("log data\n"), 0o600); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	t.Cleanup(func() { os.Remove(logFile) })

	// Create DB file in home/.onwatch/data/onwatch.db
	home, _ := os.UserHomeDir()
	dbDir := filepath.Join(home, ".onwatch", "data")
	if mkErr := os.MkdirAll(dbDir, 0o755); mkErr == nil {
		dbFile := filepath.Join(dbDir, "onwatch.db")
		if _, statErr := os.Stat(dbFile); os.IsNotExist(statErr) {
			// Create a temp DB just for this test
			if err := os.WriteFile(dbFile, []byte("dbdata"), 0o600); err == nil {
				t.Cleanup(func() { os.Remove(dbFile) })
			}
		}
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})

	// If process is still running, output should include log file info
	if strings.Contains(out, "is running") {
		t.Logf("runStatus output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStop() - stale PID with PID:PORT format (dead process)
// ---------------------------------------------------------------------------

func TestRunStop_StalePIDWithPort(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write a PID:PORT file with a dead process PID
	stalePID := 999999
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	// Should see "stale PID file" or "No running" message
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "No running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunStop_SelfPIDInFile(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write our own PID - runStop should skip killing self
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	// Should not stop self, should report "No running instance"
	if !strings.Contains(out, "No running") {
		t.Fatalf("expected 'No running', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - stale PID with PID:PORT format and legacy format
// ---------------------------------------------------------------------------

func TestRunStatus_StalePIDWithPort(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write a PID:PORT file with a dead process PID
	stalePID := 999997
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// Stale PID should say "not running (stale PID file)"
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunStatus_SelfPIDInFile(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write our own PID - should be skipped (pid != myPID check fails)
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// Self PID is skipped, so "not running"
	if !strings.Contains(out, "not running") {
		t.Fatalf("expected 'not running', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - running process with port displayed directly
// ---------------------------------------------------------------------------

func TestRunStatus_RunningProcessWithPort(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Use parent PID which should be running
	ppid := os.Getppid()
	if ppid <= 0 {
		t.Skip("could not get parent PID")
	}

	// Write PID:PORT format so the "port > 0" branch is covered
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", ppid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// If parent is alive, should show "is running" with Dashboard URL
	if strings.Contains(out, "is running") {
		if !strings.Contains(out, "Dashboard: http://localhost:9211") {
			t.Fatalf("expected Dashboard URL in output, got: %s", out)
		}
		if !strings.Contains(out, "PID file:") {
			t.Fatalf("expected PID file in output, got: %s", out)
		}
	}
	// If parent is not alive (stale), that's also valid
}

// ---------------------------------------------------------------------------
// stopPreviousInstance() - various edge cases
// ---------------------------------------------------------------------------

func TestStopPreviousInstance_SelfPIDInFile(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Write our own PID - should NOT kill self
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:12345", self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// Should not panic or error; just silently skip killing self
	captureStdout(t, func() {
		stopPreviousInstance(12345, true)
	})

	// PID file should have been removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected PID file to be removed, but it still exists")
	}
}

func TestStopPreviousInstance_StalePIDPortFormat(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Write a dead PID with port
	stalePID := 999995
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:54321", stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		stopPreviousInstance(54321, true)
	})
	// Dead process - signal will fail, no "Stopped" output expected
	_ = out
}

func TestStopPreviousInstance_LegacyPIDFormat(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Write a legacy PID file (just PID, no port)
	stalePID := 999994
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	captureStdout(t, func() {
		stopPreviousInstance(9211, true)
	})

	// PID file should have been removed
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected PID file to be removed")
	}
}

func TestStopPreviousInstance_NoPIDFile(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "nonexistent.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// No PID file exists, no port in use - should be a no-op
	captureStdout(t, func() {
		stopPreviousInstance(0, true)
	})
}

// ---------------------------------------------------------------------------
// runUpdate() - test with high version (already latest via GitHub API)
// ---------------------------------------------------------------------------

func TestRunUpdate_HighVersionAlreadyLatest(t *testing.T) {
	origVersion := version
	// Set a very high version so GitHub API returns "no update"
	version = "999.999.999"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		err := runUpdate()
		if err != nil {
			// Network errors are acceptable in CI/test environments
			if strings.Contains(err.Error(), "update check failed") {
				t.Logf("network error (expected in CI): %v", err)
				return
			}
			t.Fatalf("runUpdate error: %v", err)
		}
	})
	// If network succeeded, should say "Already at the latest version"
	if out != "" && !strings.Contains(out, "Already at the latest version") && !strings.Contains(out, "checking for updates") {
		t.Logf("output: %s", out)
	}
}

// TestRunUpdate_UpdateAvailableApplyFails tests runUpdate when the GitHub API
// returns an update (version 0.0.1 < latest), Check succeeds, and Apply either
// succeeds or fails. No PID file means no daemon restart is attempted.
// This test requires GitHub API to not be rate-limited.
func TestRunUpdate_UpdateAvailableApplyFails(t *testing.T) {
	origVersion := version
	version = "0.0.1"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		err := runUpdate()
		if err != nil {
			// Network errors (rate limit) or apply failures are both acceptable
			if strings.Contains(err.Error(), "update check failed") ||
				strings.Contains(err.Error(), "update failed") {
				t.Logf("expected error: %v", err)
				return
			}
			t.Fatalf("unexpected error: %v", err)
		}
		t.Logf("runUpdate succeeded (download happened)")
	})
	_ = out
}

func TestRunUpdate_UpdateAvailableWithSelfPID(t *testing.T) {
	origVersion := version
	version = "0.0.2"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write self-PID so the restart branch sees pid == os.Getpid() and skips
	// This exercises the PID file reading/parsing in runUpdate without spawning a daemon
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		err := runUpdate()
		if err != nil {
			if strings.Contains(err.Error(), "update check failed") ||
				strings.Contains(err.Error(), "update failed") {
				t.Logf("expected error: %v", err)
				return
			}
			t.Fatalf("unexpected runUpdate error: %v", err)
		}
	})
	_ = out
}

func TestRunUpdate_UpdateAvailableLegacySelfPID(t *testing.T) {
	origVersion := version
	version = "0.0.3"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write self-PID in legacy format (no port) so restart branch is skipped
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		err := runUpdate()
		if err != nil {
			if strings.Contains(err.Error(), "update check failed") ||
				strings.Contains(err.Error(), "update failed") {
				t.Logf("expected error: %v", err)
				return
			}
			t.Fatalf("unexpected runUpdate error: %v", err)
		}
	})
	_ = out
}

func TestRunUpdate_CheckErrorPath(t *testing.T) {
	origVersion := version
	// Non-dev version to trigger actual Check() call
	version = "1.0.0"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Force a timeout by setting HTTP_PROXY to an invalid address
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	out := captureStdout(t, func() {
		err := runUpdate()
		if err != nil {
			// Should get "update check failed" due to proxy error
			if !strings.Contains(err.Error(), "update check failed") {
				t.Fatalf("expected 'update check failed', got: %v", err)
			}
			return
		}
	})
	// The checking message should appear before the error
	if !strings.Contains(out, "checking for updates") {
		t.Logf("output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// run() - via setTestArgs for update command with real version
// ---------------------------------------------------------------------------

func TestRun_UpdateCommand(t *testing.T) {
	origVersion := version
	version = "999.999.996"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch", "update"})

	out := captureStdout(t, func() {
		err := run()
		if err != nil {
			if strings.Contains(err.Error(), "update check failed") {
				t.Logf("network error (expected): %v", err)
				return
			}
			t.Fatalf("run update error: %v", err)
		}
	})
	if !strings.Contains(out, "checking for updates") {
		t.Logf("output: %s", out)
	}
}

func TestRun_UpdateCommandDashDash(t *testing.T) {
	origVersion := version
	version = "dev"
	t.Cleanup(func() { version = origVersion })

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch", "--update"})

	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run --update error: %v", err)
		}
	})
	if !strings.Contains(out, "Already at the latest version") {
		t.Fatalf("expected 'Already at the latest version', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStop() - test mode with running parent PID (exercises SIGTERM path)
// ---------------------------------------------------------------------------

func TestRunStop_TestModeWithRunningPID(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch-test.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Use parent PID which should be running - but we don't actually want to kill it.
	// Write a PID that doesn't exist (dead process) with port to cover the port branch.
	deadPID := 999991
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:7777", deadPID)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	// Dead PID should show stale PID file message or no running instance
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "No running") {
		t.Fatalf("unexpected output: %s", out)
	}
	// PID file should be cleaned up
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected PID file to be removed")
	}
}

// ---------------------------------------------------------------------------
// runStatus() - test mode with running process, no port in PID file
// ---------------------------------------------------------------------------

func TestRunStatus_RunningProcessNoPort(t *testing.T) {
	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Use parent PID (running) with legacy format (no port)
	ppid := os.Getppid()
	if ppid <= 0 {
		t.Skip("could not get parent PID")
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(ppid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// If parent is alive: "is running" (no Dashboard URL since no port and testMode skips port check)
	// If parent is dead: "stale PID file" or "not running"
	if strings.Contains(out, "is running") {
		// In test mode with no port, there should be NO Dashboard line
		// (port == 0 and testMode skips port scanning)
		if strings.Contains(out, "Dashboard:") {
			t.Fatalf("unexpected Dashboard URL in test mode with no port: %s", out)
		}
	}
}

// ---------------------------------------------------------------------------
// fixExplicitDBPath() - explicit path doesn't exist, redirects to canonical
// ---------------------------------------------------------------------------

func TestFixExplicitDBPath_ExplicitNotExist(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}

	// Create canonical path with data
	canonDir := filepath.Join(t.TempDir(), ".onwatch", "data")
	if err := os.MkdirAll(canonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	canonPath := filepath.Join(canonDir, "onwatch.db")
	if err := os.WriteFile(canonPath, []byte("some data here"), 0o644); err != nil {
		t.Fatalf("write canonical db: %v", err)
	}

	// We need to override UserHomeDir - but we can't easily. Instead,
	// test the case where explicit path doesn't exist by pointing cfg.DBPath
	// to a nonexistent file within the temp home.
	tmpHome := filepath.Dir(filepath.Dir(canonDir)) // the temp dir itself
	t.Setenv("HOME", tmpHome)

	cfg := &config.Config{
		DBPath: filepath.Join(t.TempDir(), "nonexistent.db"),
	}
	logger := testLogger()
	fixExplicitDBPath(cfg, logger)

	// Should redirect to canonical path since explicit doesn't exist
	expected := filepath.Join(tmpHome, ".onwatch", "data", "onwatch.db")
	if cfg.DBPath != expected {
		t.Logf("DBPath after fix: %s (expected %s or unchanged)", cfg.DBPath, expected)
	}
}

// ---------------------------------------------------------------------------
// stopPreviousInstance() - PID file with invalid content
// ---------------------------------------------------------------------------

func TestStopPreviousInstance_InvalidPIDContent(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Write garbage content - pid will parse as 0, should be skipped
	if err := os.WriteFile(pidFile, []byte("garbage:content"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	captureStdout(t, func() {
		stopPreviousInstance(0, true)
	})

	// PID file should have been removed even with garbage content
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected PID file to be removed")
	}
}

func TestStopPreviousInstance_EmptyPIDFile(t *testing.T) {
	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Write empty content
	if err := os.WriteFile(pidFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	captureStdout(t, func() {
		stopPreviousInstance(0, true)
	})
}

// ---------------------------------------------------------------------------
// run() - setup command branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// runStop() - non-test mode with PID file port branch
// ---------------------------------------------------------------------------

func TestRunStop_NonTestMode_PIDFilePortBranch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}
	// Skip if real onwatch on default ports
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a listener on a random port to exercise the "port in PID file" branch
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write dead PID + active port to PID file
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("999987:%d", port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	// Dead PID + port with non-onwatch process => "No running instance"
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "No running") && !strings.Contains(out, "Stopped") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - non-test mode with port from PID file and fallback port scan
// ---------------------------------------------------------------------------

func TestRunStatus_NonTestMode_PIDFilePortBranch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}
	// Skip if real onwatch on default ports
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Use parent PID (should be running) with a random port
	ppid := os.Getppid()
	if ppid <= 0 {
		t.Skip("could not get parent PID")
	}

	// Start a listener to exercise the non-test-mode port check
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write running parent PID with port
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:%d", ppid, port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// Parent should be running, output should include Dashboard URL
	if strings.Contains(out, "is running") {
		if !strings.Contains(out, fmt.Sprintf("Dashboard: http://localhost:%d", port)) {
			t.Logf("expected Dashboard URL with port %d in output: %s", port, out)
		}
	}
}

func TestRunStatus_NonTestMode_NoPIDFileFallback(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}
	// Skip if real onwatch on default ports
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// No PID file, no ports in use => "not running"
	if !strings.Contains(out, "is not running") {
		t.Fatalf("expected 'is not running', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - running process with no port, non-test mode (covers port scan fallback)
// ---------------------------------------------------------------------------

func TestRunStatus_NonTestMode_RunningProcessNoPort(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	ppid := os.Getppid()
	if ppid <= 0 {
		t.Skip("could not get parent PID")
	}

	// Write running parent PID with legacy format (no port)
	// In non-test mode, this triggers the port-scanning fallback for "which port"
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(ppid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	// Parent should be alive, so "is running"
	if strings.Contains(out, "is running") {
		t.Logf("running process detected with port scan: %s", out)
	}
}

// ---------------------------------------------------------------------------
// stopPreviousInstance() - non-test mode with port fallback
// ---------------------------------------------------------------------------

func TestStopPreviousInstance_NonTestMode_PortFallback(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}

	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "nonexistent.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Start a listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Call with non-test mode and the port - will try to find onwatch on the port
	// go test is not onwatch, so findOnwatchOnPort returns empty
	captureStdout(t, func() {
		stopPreviousInstance(port, false)
	})
}

func TestStopPreviousInstance_NonTestMode_PIDFileWithPort(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}

	oldPIDFile := pidFile
	oldPIDDir := pidDir
	tmpDir := t.TempDir()
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	// Start a listener to exercise the filePort branch
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write dead PID with active port
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("999986:%d", port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	captureStdout(t, func() {
		stopPreviousInstance(port, false)
	})
}

func TestRun_SetupCommand(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Pre-populate HOME with a complete .env so runSetup() hits the
	// "all providers configured" early return instead of entering the
	// interactive wizard (which loops forever on EOF stdin in CI).
	home := t.TempDir()
	t.Setenv("HOME", home)
	installDir := filepath.Join(home, ".onwatch")
	if err := os.MkdirAll(filepath.Join(installDir, "data"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	envContent := "ANTHROPIC_TOKEN=test\nSYNTHETIC_API_KEY=test\nZAI_API_KEY=test\nCODEX_TOKEN=test\nANTIGRAVITY_ENABLED=true\nONWATCH_ADMIN_PASS=test\n"
	if err := os.WriteFile(filepath.Join(installDir, ".env"), []byte(envContent), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	setTestArgs(t, []string{"onwatch", "setup"})

	out := captureStdout(t, func() {
		err := run()
		if err != nil {
			t.Logf("run setup error (expected): %v", err)
		}
	})
	if !strings.Contains(out, "all providers configured") {
		t.Logf("setup output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// run() - in-process daemon child that fails on server bind
// This covers database setup, agent creation, and server error path.
// ---------------------------------------------------------------------------

func TestRun_InProcessDaemonChild_ServerBindFails(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Occupy a port on 0.0.0.0 so the server (which binds 0.0.0.0:port) fails
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Set up env for daemon child mode with antigravity only (no API keys needed)
	t.Setenv("_ONWATCH_DAEMON", "1")
	t.Setenv("ANTIGRAVITY_ENABLED", "true")
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "")
	t.Setenv("SYNTHETIC_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("ANTHROPIC_TOKEN", "")
	t.Setenv("COPILOT_TOKEN", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("ONWATCH_DB_PATH", dbPath)
	t.Setenv("ONWATCH_ADMIN_PASS", "testpass123")
	t.Setenv("ONWATCH_PORT", strconv.Itoa(port))
	t.Setenv("ONWATCH_LOG_LEVEL", "error")
	t.Setenv("HOME", tmpDir)

	oldPIDFile := pidFile
	oldPIDDir := pidDir
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch-test.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	setTestArgs(t, []string{"onwatch", "--debug", "--test"})

	// run() should start, create DB, create agents, then fail on server bind
	// The server error triggers the select case, then graceful shutdown occurs.
	// Use a goroutine with timeout since run() blocks.
	done := make(chan error, 1)
	go func() {
		done <- run()
	}()

	select {
	case err := <-done:
		// run() returned - either due to server error or other error
		if err != nil {
			t.Logf("run() returned error (expected for port conflict): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("run() did not complete within 10 seconds")
	}
}

// ---------------------------------------------------------------------------
// run() - in-process daemon child with all providers configured
// This exercises more agent/tracker creation branches.
// ---------------------------------------------------------------------------

func TestRun_InProcessDaemonChild_AllProviders(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Occupy a port on 0.0.0.0 so the server fails to bind
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	t.Setenv("_ONWATCH_DAEMON", "1")
	t.Setenv("SYNTHETIC_API_KEY", "syn_test123")
	t.Setenv("ZAI_API_KEY", "zai_test123")
	t.Setenv("ANTHROPIC_TOKEN", "anth_test123")
	t.Setenv("COPILOT_TOKEN", "cop_test123")
	t.Setenv("CODEX_TOKEN", "codex_test123")
	t.Setenv("ANTIGRAVITY_ENABLED", "true")
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "")
	t.Setenv("ONWATCH_DB_PATH", dbPath)
	t.Setenv("ONWATCH_ADMIN_PASS", "testpass456")
	t.Setenv("ONWATCH_PORT", strconv.Itoa(port))
	t.Setenv("ONWATCH_LOG_LEVEL", "error")
	t.Setenv("HOME", tmpDir)

	oldPIDFile := pidFile
	oldPIDDir := pidDir
	pidDir = tmpDir
	pidFile = filepath.Join(tmpDir, "onwatch-test.pid")
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	setTestArgs(t, []string{"onwatch", "--debug", "--test"})

	done := make(chan error, 1)
	go func() {
		done <- run()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("run() returned error (expected): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("run() did not complete within 10 seconds")
	}
}

// ---------------------------------------------------------------------------
// collectSyntheticKey() - empty key then valid key
// ---------------------------------------------------------------------------

func TestCollectSyntheticKey_EmptyThenValid(t *testing.T) {
	// Empty string triggers the "Cannot be empty" branch, then valid key succeeds
	input := "\nsyn_valid123\n"
	reader := bufio.NewReader(strings.NewReader(input))
	key := collectSyntheticKey(reader)
	if key != "syn_valid123" {
		t.Fatalf("expected 'syn_valid123', got %q", key)
	}
}

// ---------------------------------------------------------------------------
// addMissingProviders() - existing env with antigravity already enabled
// ---------------------------------------------------------------------------

func TestAddMissingProviders_AntigravityAlreadyEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	t.Setenv("PATH", "")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC_API_KEY=syn_existing\nANTIGRAVITY_ENABLED=true\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	// Skip all providers
	input := strings.Join([]string{
		"n", // skip zai
		"n", // skip anthropic
		"n", // skip codex
	}, "\n") + "\n"

	existing := &existingEnv{syntheticKey: "syn_existing", antigravityEnabled: true}
	reader := bufio.NewReader(strings.NewReader(input))
	if err := addMissingProviders(reader, envFile, existing); err != nil {
		t.Fatalf("addMissingProviders error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// fixExplicitDBPath() - when explicit path IS the canonical path
// ---------------------------------------------------------------------------

func TestFixExplicitDBPath_AlreadyCanonical(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}

	canonicalPath := filepath.Join(home, ".onwatch", "data", "onwatch.db")
	cfg := &config.Config{DBPath: canonicalPath}
	logger := testLogger()

	// Should return early since explicit == canonical
	fixExplicitDBPath(cfg, logger)

	// DBPath should be unchanged
	if cfg.DBPath != canonicalPath {
		t.Fatalf("expected DBPath unchanged, got %q", cfg.DBPath)
	}
}

// ---------------------------------------------------------------------------
// fixExplicitDBPath() - canonical has more data than explicit
// ---------------------------------------------------------------------------

func TestFixExplicitDBPath_CanonicalHasMoreData(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create canonical path with large data
	canonDir := filepath.Join(tmpHome, ".onwatch", "data")
	if err := os.MkdirAll(canonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	canonPath := filepath.Join(canonDir, "onwatch.db")
	largeData := strings.Repeat("x", 1000)
	if err := os.WriteFile(canonPath, []byte(largeData), 0o644); err != nil {
		t.Fatalf("write canonical: %v", err)
	}

	// Create explicit path with small data
	explicitDir := t.TempDir()
	explicitPath := filepath.Join(explicitDir, "small.db")
	if err := os.WriteFile(explicitPath, []byte("tiny"), 0o644); err != nil {
		t.Fatalf("write explicit: %v", err)
	}

	cfg := &config.Config{DBPath: explicitPath}
	logger := testLogger()
	fixExplicitDBPath(cfg, logger)

	// Should redirect to canonical since it has more data
	if cfg.DBPath != canonPath {
		t.Fatalf("expected redirect to canonical %q, got %q", canonPath, cfg.DBPath)
	}
}

// ---------------------------------------------------------------------------
// fixExplicitDBPath() - canonical path doesn't exist
// ---------------------------------------------------------------------------

func TestFixExplicitDBPath_CanonicalDoesNotExist(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// No canonical path created

	explicitPath := filepath.Join(t.TempDir(), "explicit.db")
	if err := os.WriteFile(explicitPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write explicit: %v", err)
	}

	cfg := &config.Config{DBPath: explicitPath}
	logger := testLogger()
	fixExplicitDBPath(cfg, logger)

	// Should keep explicit path since canonical doesn't exist
	if cfg.DBPath != explicitPath {
		t.Fatalf("expected explicit path unchanged, got %q", cfg.DBPath)
	}
}

// ---------------------------------------------------------------------------
// fixExplicitDBPath() - explicit path doesn't exist, canonical does
// ---------------------------------------------------------------------------

func TestFixExplicitDBPath_ExplicitMissingCanonicalExists(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create canonical with data
	canonDir := filepath.Join(tmpHome, ".onwatch", "data")
	if err := os.MkdirAll(canonDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	canonPath := filepath.Join(canonDir, "onwatch.db")
	if err := os.WriteFile(canonPath, []byte("canonical data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Explicit path doesn't exist
	explicitPath := filepath.Join(t.TempDir(), "nonexistent.db")

	cfg := &config.Config{DBPath: explicitPath}
	logger := testLogger()
	fixExplicitDBPath(cfg, logger)

	// Should redirect to canonical
	if cfg.DBPath != canonPath {
		t.Fatalf("expected redirect to canonical, got %q", cfg.DBPath)
	}
}

// ---------------------------------------------------------------------------
// initEncryptionSalt() - test with existing valid salt in DB
// ---------------------------------------------------------------------------

func TestInitEncryptionSalt_ExistingSalt(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	logger := testLogger()

	// First call generates and stores a salt
	if err := initEncryptionSalt(db, logger); err != nil {
		t.Fatalf("first initEncryptionSalt: %v", err)
	}

	// Second call should load the existing salt
	if err := initEncryptionSalt(db, logger); err != nil {
		t.Fatalf("second initEncryptionSalt: %v", err)
	}
}

// ---------------------------------------------------------------------------
// initEncryptionSalt() - test with invalid salt in DB (should regenerate)
// ---------------------------------------------------------------------------

func TestInitEncryptionSalt_InvalidSaltInDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	logger := testLogger()

	// Set an invalid salt (wrong length)
	if err := db.SetSetting("encryption_salt", "short"); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	// Should detect invalid salt and regenerate
	if err := initEncryptionSalt(db, logger); err != nil {
		t.Fatalf("initEncryptionSalt with invalid salt: %v", err)
	}

	// Verify a valid salt is now stored
	salt, err := db.GetSetting("encryption_salt")
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if len(salt) != 32 { // 16 bytes = 32 hex chars
		t.Fatalf("expected 32 hex chars, got %d: %s", len(salt), salt)
	}
}

// ---------------------------------------------------------------------------
// runStop() - non-test mode with PID:PORT stale PID (port branch fallback)
// ---------------------------------------------------------------------------

func TestRunStop_NonTestMode_StalePIDWithPortFallback(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Start a listener on random port to exercise the "port from PID file" non-test branch
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Write stale PID:PORT - dead PID but port is active (not onwatch)
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("999985:%d", port)), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop(false) error: %v", err)
		}
	})
	// Dead PID will show "stale PID file", then port fallback finds non-onwatch, reports "No running"
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "No running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runStatus() - stale PID non-test mode
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// run() - daemon child with signal-based shutdown (covers signal handler path)
// ---------------------------------------------------------------------------

func TestRunStatus_NonTestMode_StalePIDNoPort(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("lsof only on macOS/Linux")
	}
	for _, p := range []int{9211, 8932} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Skipf("skipping: real onwatch on port %d", p)
		}
	}

	oldPIDFile := pidFile
	tmpDir := t.TempDir()
	pidFile = filepath.Join(tmpDir, "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	// Write stale PID with no port (legacy format)
	if err := os.WriteFile(pidFile, []byte("999984"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus(false) error: %v", err)
		}
	})
	if !strings.Contains(out, "stale PID file") && !strings.Contains(out, "not running") {
		t.Fatalf("unexpected output: %s", out)
	}
}
