package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/onllm-dev/onwatch/v2/internal/update"
	"github.com/onllm-dev/onwatch/v2/internal/web"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer r.Close()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.txt")
	if err != nil {
		t.Fatalf("create temp stdin file: %v", err)
	}
	if _, err := f.WriteString(input); err != nil {
		t.Fatalf("write temp stdin file: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek temp stdin file: %v", err)
	}

	oldStdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = oldStdin }()
	defer f.Close()

	fn()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestPIDFileLifecycle(t *testing.T) {
	oldPIDDir := pidDir
	oldPIDFile := pidFile
	t.Cleanup(func() {
		pidDir = oldPIDDir
		pidFile = oldPIDFile
	})

	pidDir = filepath.Join(t.TempDir(), "pid")
	pidFile = filepath.Join(pidDir, "onwatch.pid")

	if err := ensurePIDDir(); err != nil {
		t.Fatalf("ensurePIDDir error: %v", err)
	}
	if _, err := os.Stat(pidDir); err != nil {
		t.Fatalf("pid dir should exist: %v", err)
	}

	if err := writePIDFile(9211); err != nil {
		t.Fatalf("writePIDFile error: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if !strings.Contains(string(data), ":9211") {
		t.Fatalf("unexpected pid file content: %q", string(data))
	}

	removePIDFile()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed, err=%v", err)
	}
}

func TestMigrateDBLocation_MovesDBAndSidecars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldDB := filepath.Join(home, ".onwatch", "onwatch.db")
	newDB := filepath.Join(home, ".onwatch", "data", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(oldDB), 0o755); err != nil {
		t.Fatalf("mkdir old db dir: %v", err)
	}
	if err := os.WriteFile(oldDB, []byte("db"), 0o600); err != nil {
		t.Fatalf("write old db: %v", err)
	}
	if err := os.WriteFile(oldDB+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatalf("write old wal: %v", err)
	}
	if err := os.WriteFile(oldDB+"-shm", []byte("shm"), 0o600); err != nil {
		t.Fatalf("write old shm: %v", err)
	}

	migrateDBLocation(newDB, testLogger())

	if _, err := os.Stat(newDB); err != nil {
		t.Fatalf("new db should exist: %v", err)
	}
	if _, err := os.Stat(newDB + "-wal"); err != nil {
		t.Fatalf("new wal should exist: %v", err)
	}
	if _, err := os.Stat(newDB + "-shm"); err != nil {
		t.Fatalf("new shm should exist: %v", err)
	}
	if _, err := os.Stat(oldDB); !os.IsNotExist(err) {
		t.Fatalf("old db should be moved, err=%v", err)
	}
}

func TestFixExplicitDBPath_RedirectsToCanonicalWhenBetter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	canonical := filepath.Join(home, ".onwatch", "data", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("mkdir canonical dir: %v", err)
	}
	if err := os.WriteFile(canonical, bytes.Repeat([]byte("a"), 64), 0o600); err != nil {
		t.Fatalf("write canonical: %v", err)
	}

	t.Run("missing explicit path", func(t *testing.T) {
		cfg := &config.Config{DBPath: filepath.Join(t.TempDir(), "missing.db")}
		fixExplicitDBPath(cfg, testLogger())
		if cfg.DBPath != canonical {
			t.Fatalf("expected redirect to canonical, got %q", cfg.DBPath)
		}
	})

	t.Run("smaller explicit file", func(t *testing.T) {
		explicit := filepath.Join(t.TempDir(), "explicit.db")
		if err := os.WriteFile(explicit, []byte("small"), 0o600); err != nil {
			t.Fatalf("write explicit: %v", err)
		}
		cfg := &config.Config{DBPath: explicit}
		fixExplicitDBPath(cfg, testLogger())
		if cfg.DBPath != canonical {
			t.Fatalf("expected redirect to canonical, got %q", cfg.DBPath)
		}
	})
}

func TestPrintBannerAndHelp(t *testing.T) {
	cfg := &config.Config{
		SyntheticAPIKey:    "syn_abcdefghijkl",
		ZaiAPIKey:          "zai-abcdef",
		AnthropicToken:     "anthropic-token",
		AnthropicAutoToken: true,
		CopilotToken:       "copilot-token",
		CodexToken:         "codex-token",
		CodexAutoToken:     true,
		AntigravityEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9211,
		DBPath:             "/tmp/onwatch.db",
		AdminUser:          "admin",
		TestMode:           true,
	}

	banner := captureStdout(t, func() {
		printBanner(cfg, "1.2.3")
	})
	for _, want := range []string{"onWatch v1.2.3", "Providers:", "Synthetic API Key:", "Codex (auto):", "Mode:      TEST"} {
		if !strings.Contains(banner, want) {
			t.Fatalf("banner should contain %q, got:\n%s", want, banner)
		}
	}

	help := captureStdout(t, func() {
		printHelp()
	})
	for _, want := range []string{"onWatch - Multi-Provider API Usage Tracker", "Commands:", "Test Mode (--test):"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help should contain %q, got:\n%s", want, help)
		}
	}
}

func TestInitEncryptionSalt_LoadsAndGenerates(t *testing.T) {
	t.Run("loads existing valid salt", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()

		existing := make([]byte, 16)
		for i := range existing {
			existing[i] = byte(i + 1)
		}
		if err := s.SetSetting("encryption_salt", hex.EncodeToString(existing)); err != nil {
			t.Fatalf("set setting: %v", err)
		}

		if err := initEncryptionSalt(s, testLogger()); err != nil {
			t.Fatalf("initEncryptionSalt error: %v", err)
		}
		if got := web.GetEncryptionSalt(); !bytes.Equal(got, existing) {
			t.Fatalf("loaded salt mismatch: got %x want %x", got, existing)
		}
	})

	t.Run("generates and stores when missing", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()

		if err := initEncryptionSalt(s, testLogger()); err != nil {
			t.Fatalf("initEncryptionSalt error: %v", err)
		}

		saltHex, err := s.GetSetting("encryption_salt")
		if err != nil {
			t.Fatalf("get setting: %v", err)
		}
		salt, err := hex.DecodeString(saltHex)
		if err != nil {
			t.Fatalf("decode stored salt: %v", err)
		}
		if len(salt) != 16 {
			t.Fatalf("expected 16-byte salt, got %d", len(salt))
		}
	})
}

func TestInputHelpers(t *testing.T) {
	t.Run("readLine trims newline", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader(" value \n"))
		if got := readLine(r); got != "value" {
			t.Fatalf("readLine got %q", got)
		}
	})

	t.Run("promptWithDefault uses default", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("\n"))
		if got := promptWithDefault(r, "Name", "admin"); got != "admin" {
			t.Fatalf("promptWithDefault got %q", got)
		}
	})

	t.Run("promptYesNo handles default and explicit yes", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("\n"))
		if !promptYesNo(r, "Continue?", true) {
			t.Fatal("expected default yes")
		}
		r2 := bufio.NewReader(strings.NewReader("yes\n"))
		if !promptYesNo(r2, "Continue?", false) {
			t.Fatal("expected explicit yes")
		}
	})

	t.Run("promptSecret retries empty", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("\nsecret-token\n"))
		if got := promptSecret(r, "Token"); got != "secret-token" {
			t.Fatalf("promptSecret got %q", got)
		}
	})

	t.Run("promptChoice retries invalid", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("0\n2\n"))
		if got := promptChoice(r, "Pick one", []string{"A", "B"}); got != 2 {
			t.Fatalf("promptChoice got %d", got)
		}
	})
}

func TestProviderCollectionHelpers(t *testing.T) {
	t.Run("collectSyntheticKey retries until syn prefix", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("bad\nsyn_abcdef12\n"))
		if got := collectSyntheticKey(r); got != "syn_abcdef12" {
			t.Fatalf("collectSyntheticKey got %q", got)
		}
	})

	t.Run("collectZaiConfig custom url path", func(t *testing.T) {
		input := strings.Join([]string{
			"",                           // empty key -> retry
			"zai-secret",                 // valid key
			"n",                          // don't use default URL
			"http://invalid.example.com", // invalid URL
			"https://open.bigmodel.cn/api",
		}, "\n") + "\n"
		r := bufio.NewReader(strings.NewReader(input))
		key, url := collectZaiConfig(r)
		if key != "zai-secret" {
			t.Fatalf("unexpected zai key: %q", key)
		}
		if url != "https://open.bigmodel.cn/api" {
			t.Fatalf("unexpected zai url: %q", url)
		}
	})

	t.Run("collectMultipleProviders safe branches", func(t *testing.T) {
		input := strings.Join([]string{
			"y",            // synthetic yes
			"syn_abc12345", // synthetic key
			"y",            // zai yes
			"zai-key",      // zai key
			"y",            // use default zai url
			"n",            // anthropic no
			"n",            // codex no
			"y",            // antigravity yes
			"n",            // gemini no
		}, "\n") + "\n"
		r := bufio.NewReader(strings.NewReader(input))
		syn, zai, zaiURL, anth, codex, anti, _ := collectMultipleProviders(r, testLogger())
		if syn == "" || zai == "" || zaiURL == "" {
			t.Fatalf("expected synthetic and zai collected, got syn=%q zai=%q zaiURL=%q", syn, zai, zaiURL)
		}
		if anth != "" || codex != "" {
			t.Fatalf("anthropic/codex should be empty in safe branch, got anth=%q codex=%q", anth, codex)
		}
		if !anti {
			t.Fatal("expected antigravity enabled")
		}
	})
}

func TestFreshSetup_AntigravityOnlySafeBranch(t *testing.T) {
	input := strings.Join([]string{
		"5",     // antigravity only
		"",      // default admin user
		"",      // auto-generate password
		"70000", // invalid port
		"9211",  // valid port
		"9",     // invalid interval
		"60",    // valid interval
	}, "\n") + "\n"

	reader := bufio.NewReader(strings.NewReader(input))
	cfg, err := freshSetup(reader)
	if err != nil {
		t.Fatalf("freshSetup returned error: %v", err)
	}
	if !cfg.antigravityEnabled {
		t.Fatal("expected antigravity enabled")
	}
	if cfg.adminUser != "admin" {
		t.Fatalf("expected default admin user, got %q", cfg.adminUser)
	}
	if cfg.adminPass == "" {
		t.Fatal("expected generated password")
	}
	if cfg.port != 9211 || cfg.pollInterval != 60 {
		t.Fatalf("unexpected optional settings: port=%d interval=%d", cfg.port, cfg.pollInterval)
	}
}

func TestPrintSummaryAndNextSteps(t *testing.T) {
	cfg := &setupConfig{
		syntheticKey:       "syn_abc",
		zaiKey:             "zai_abc",
		anthropicToken:     "anth",
		codexToken:         "codex",
		antigravityEnabled: true,
		adminUser:          "admin",
		adminPass:          "secret",
		port:               9211,
		pollInterval:       60,
	}

	out := captureStdout(t, func() {
		printSummary(cfg)
		printNextSteps()
	})

	for _, want := range []string{"Configuration Summary", "Provider:", "onwatch stop", "onwatch --debug"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary/next steps should contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunSetupEarlyPathsAndSafeRunCommands(t *testing.T) {
	t.Run("runSetup returns early when all providers already configured", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		installDir := filepath.Join(home, ".onwatch")
		envFile := filepath.Join(installDir, ".env")
		if err := os.MkdirAll(filepath.Join(installDir, "data"), 0o755); err != nil {
			t.Fatalf("mkdir install dir: %v", err)
		}
		content := strings.Join([]string{
			"SYNTHETIC_API_KEY=syn_abc",
			"ZAI_API_KEY=zai_abc",
			"ANTHROPIC_TOKEN=anth",
			"CODEX_TOKEN=codex",
			"ANTIGRAVITY_ENABLED=true",
			"GEMINI_ENABLED=true",
		}, "\n")
		if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
			t.Fatalf("write env: %v", err)
		}

		if err := runSetup(); err != nil {
			t.Fatalf("runSetup error: %v", err)
		}
	})

	t.Run("runSetup fresh safe path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		input := strings.Join([]string{
			"5",    // antigravity only
			"",     // default admin user
			"",     // auto-generated password
			"9211", // valid port
			"60",   // valid interval
		}, "\n") + "\n"

		withStdin(t, input, func() {
			if err := runSetup(); err != nil {
				t.Fatalf("runSetup error: %v", err)
			}
		})
	})

	t.Run("runStop and runStatus test-mode safe branches", func(t *testing.T) {
		oldPIDFile := pidFile
		pidFile = filepath.Join(t.TempDir(), "onwatch-test.pid")
		t.Cleanup(func() { pidFile = oldPIDFile })

		outStop := captureStdout(t, func() {
			if err := runStop(true); err != nil {
				t.Fatalf("runStop error: %v", err)
			}
		})
		if !strings.Contains(outStop, "No running onwatch (test) instance found") {
			t.Fatalf("unexpected runStop output: %s", outStop)
		}

		outStatus := captureStdout(t, func() {
			if err := runStatus(true); err != nil {
				t.Fatalf("runStatus error: %v", err)
			}
		})
		if !strings.Contains(outStatus, "onwatch (test) is not running") {
			t.Fatalf("unexpected runStatus output: %s", outStatus)
		}
	})

	t.Run("findOnwatchOnPort and isOnwatchProcess safe checks", func(t *testing.T) {
		_ = findOnwatchOnPort(1)
		if isOnwatchProcess(-1) {
			t.Fatal("invalid PID should not be identified as onwatch")
		}
	})
}

func TestRunStopAndStatus_WithPIDFileProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep process helper not used on windows")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch-test.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d:9211", cmd.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	statusOut := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	if !strings.Contains(statusOut, "onwatch (test) is running") {
		t.Fatalf("unexpected runStatus output: %s", statusOut)
	}
	if !strings.Contains(statusOut, "Dashboard: http://localhost:9211") {
		t.Fatalf("expected dashboard URL in status output: %s", statusOut)
	}

	stopOut := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	if !strings.Contains(stopOut, "Stopped onwatch (test)") {
		t.Fatalf("unexpected runStop output: %s", stopOut)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("process did not stop after runStop")
	}
}

func TestRunStatus_StalePIDFile(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch-test.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	if err := os.WriteFile(pidFile, []byte("999999:9211"), 0o600); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	if !strings.Contains(out, "stale PID file") {
		t.Fatalf("expected stale pid message, got: %s", out)
	}
}

func startOnwatchNCListener(t *testing.T, port int) *exec.Cmd {
	t.Helper()

	ncPath, err := exec.LookPath("nc")
	if err != nil {
		t.Skip("nc not available")
	}

	onwatchNC := filepath.Join(t.TempDir(), "onwatch")
	if err := os.Symlink(ncPath, onwatchNC); err != nil {
		t.Skipf("cannot create onwatch symlink for nc: %v", err)
	}

	cmd := exec.Command(onwatchNC, "-lk", "127.0.0.1", strconv.Itoa(port))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start nc listener: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	// Wait until the port is connectable.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err != nil {
		t.Skip("nc listener did not open port in time")
		return nil
	}

	// Also wait until lsof can see the process - lsof can lag a few ms behind a
	// freshly opened socket, causing the port-detection path in runStatus to miss it.
	lsofDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(lsofDeadline) {
		out, _ := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port)).Output()
		if len(strings.TrimSpace(string(out))) > 0 {
			return cmd
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Skip("lsof did not detect nc listener in time")
	return nil
}

func pickAvailableDefaultPort(t *testing.T) int {
	t.Helper()
	for _, port := range []int{9211, 8932} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port
		}
	}
	t.Skip("default ports 9211/8932 unavailable")
	return 0
}

func TestRunStatus_PortFallbackDetectsOnwatchProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("port process detection uses lsof on unix")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	port := pickAvailableDefaultPort(t)
	_ = startOnwatchNCListener(t, port)

	out := captureStdout(t, func() {
		if err := runStatus(false); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	if !strings.Contains(out, "onwatch is running (PID") {
		t.Fatalf("expected running output, got: %s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("on port %d", port)) {
		t.Fatalf("expected detected port in output, got: %s", out)
	}
}

func TestRunStop_PortFromPIDFileFallbackStopsOnwatchProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("port process detection uses lsof on unix")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen random port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cmd := startOnwatchNCListener(t, port)
	if cmd == nil || cmd.Process == nil {
		t.Skip("listener process unavailable")
	}

	// Stale PID with a valid port forces runStop(false) into port-based fallback.
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("999999:%d", port)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStop(false); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	if !strings.Contains(out, "Stopped onwatch (PID") || !strings.Contains(out, fmt.Sprintf("on port %d", port)) {
		t.Fatalf("expected port-fallback stop output, got: %s", out)
	}
}

func TestDaemonize_SuccessAndLogOpenError(t *testing.T) {
	oldPIDFile := pidFile
	oldArgs := os.Args
	pidFile = filepath.Join(t.TempDir(), "onwatch-daemon.pid")
	t.Cleanup(func() {
		pidFile = oldPIDFile
		os.Args = oldArgs
	})

	t.Run("success", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "data", "onwatch.db")
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			t.Fatalf("mkdir db dir: %v", err)
		}

		t.Setenv("GO_WANT_DAEMON_HELPER", "1")
		os.Args = []string{oldArgs[0], "-test.run=TestDaemonizeHelperProcess"}

		cfg := &config.Config{
			DBPath:    dbPath,
			Port:      9211,
			TestMode:  true,
			AdminUser: "admin",
			AdminPass: "test",
		}

		out := captureStdout(t, func() {
			if err := daemonize(cfg); err != nil {
				t.Fatalf("daemonize error: %v", err)
			}
		})
		if !strings.Contains(out, "Daemon started (PID") {
			t.Fatalf("unexpected daemonize output: %s", out)
		}

		if _, err := os.Stat(pidFile); err != nil {
			t.Fatalf("pid file should exist: %v", err)
		}
		logPath := filepath.Join(filepath.Dir(dbPath), ".onwatch-test.log")
		if _, err := os.Stat(logPath); err != nil {
			t.Fatalf("daemon log file should exist: %v", err)
		}
	})

	t.Run("open log file error", func(t *testing.T) {
		t.Setenv("GO_WANT_DAEMON_HELPER", "1")
		os.Args = []string{oldArgs[0], "-test.run=TestDaemonizeHelperProcess"}

		cfg := &config.Config{
			DBPath:   filepath.Join(t.TempDir(), "missing", "nested", "onwatch.db"),
			Port:     9211,
			TestMode: true,
		}

		err := daemonize(cfg)
		if err == nil || !strings.Contains(err.Error(), "failed to open log file for daemon") {
			t.Fatalf("expected log open error, got %v", err)
		}
	})
}

func TestRunUpdate_DevVersionNoNetwork(t *testing.T) {
	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })

	out := captureStdout(t, func() {
		if err := runUpdate(); err != nil {
			t.Fatalf("runUpdate error: %v", err)
		}
	})

	if !strings.Contains(out, "checking for updates") {
		t.Fatalf("expected update check output, got: %s", out)
	}
	if !strings.Contains(out, "Already at the latest version (vdev)") {
		t.Fatalf("expected latest-version message, got: %s", out)
	}
}

type stubCLIUpdater struct {
	checkInfo  update.UpdateInfo
	checkErr   error
	applyErr   error
	checkCalls int
	applyCalls int
}

func (s *stubCLIUpdater) Check() (update.UpdateInfo, error) {
	s.checkCalls++
	return s.checkInfo, s.checkErr
}

func (s *stubCLIUpdater) Apply() error {
	s.applyCalls++
	return s.applyErr
}

func TestRunUpdate_WithMockedUpdater(t *testing.T) {
	oldVersion := version
	oldUpdaterFactory := newCLIUpdater
	oldPIDFile := pidFile
	t.Cleanup(func() {
		version = oldVersion
		newCLIUpdater = oldUpdaterFactory
		pidFile = oldPIDFile
	})

	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	version = "1.2.3"

	t.Run("check error", func(t *testing.T) {
		stub := &stubCLIUpdater{checkErr: fmt.Errorf("boom")}
		newCLIUpdater = func(v string, logger *slog.Logger) cliUpdater { return stub }

		err := runUpdate()
		if err == nil || !strings.Contains(err.Error(), "update check failed") {
			t.Fatalf("expected check failure, got %v", err)
		}
		if stub.checkCalls != 1 || stub.applyCalls != 0 {
			t.Fatalf("unexpected calls check=%d apply=%d", stub.checkCalls, stub.applyCalls)
		}
	})

	t.Run("no update available", func(t *testing.T) {
		stub := &stubCLIUpdater{
			checkInfo: update.UpdateInfo{
				Available:      false,
				CurrentVersion: "1.2.3",
				LatestVersion:  "1.2.3",
			},
		}
		newCLIUpdater = func(v string, logger *slog.Logger) cliUpdater { return stub }

		out := captureStdout(t, func() {
			if err := runUpdate(); err != nil {
				t.Fatalf("runUpdate error: %v", err)
			}
		})
		if !strings.Contains(out, "Already at the latest version (v1.2.3)") {
			t.Fatalf("unexpected output: %s", out)
		}
		if stub.checkCalls != 1 || stub.applyCalls != 0 {
			t.Fatalf("unexpected calls check=%d apply=%d", stub.checkCalls, stub.applyCalls)
		}
	})

	t.Run("apply error", func(t *testing.T) {
		stub := &stubCLIUpdater{
			checkInfo: update.UpdateInfo{
				Available:      true,
				CurrentVersion: "1.2.3",
				LatestVersion:  "1.2.4",
				DownloadURL:    "https://example.com/onwatch",
			},
			applyErr: fmt.Errorf("cannot replace binary"),
		}
		newCLIUpdater = func(v string, logger *slog.Logger) cliUpdater { return stub }

		err := runUpdate()
		if err == nil || !strings.Contains(err.Error(), "update failed") {
			t.Fatalf("expected apply failure, got %v", err)
		}
		if stub.checkCalls != 1 || stub.applyCalls != 1 {
			t.Fatalf("unexpected calls check=%d apply=%d", stub.checkCalls, stub.applyCalls)
		}
	})

	t.Run("apply success with self pid file", func(t *testing.T) {
		selfPID := os.Getpid()
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", selfPID)), 0o644); err != nil {
			t.Fatalf("write pid file: %v", err)
		}

		stub := &stubCLIUpdater{
			checkInfo: update.UpdateInfo{
				Available:      true,
				CurrentVersion: "1.2.3",
				LatestVersion:  "1.2.4",
				DownloadURL:    "https://example.com/onwatch",
			},
		}
		newCLIUpdater = func(v string, logger *slog.Logger) cliUpdater { return stub }

		out := captureStdout(t, func() {
			if err := runUpdate(); err != nil {
				t.Fatalf("runUpdate error: %v", err)
			}
		})
		if !strings.Contains(out, "Updated successfully to v1.2.4") {
			t.Fatalf("unexpected output: %s", out)
		}
		if stub.checkCalls != 1 || stub.applyCalls != 1 {
			t.Fatalf("unexpected calls check=%d apply=%d", stub.checkCalls, stub.applyCalls)
		}
	})
}

func TestDaemonizeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DAEMON_HELPER") != "1" {
		return
	}
	os.Exit(0)
}
