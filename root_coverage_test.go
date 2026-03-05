package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
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
		SyntheticAPIKey:   "syn_abcdefghijkl",
		ZaiAPIKey:         "zai-abcdef",
		AnthropicToken:    "anthropic-token",
		AnthropicAutoToken: true,
		CopilotToken:      "copilot-token",
		CodexToken:        "codex-token",
		CodexAutoToken:    true,
		AntigravityEnabled: true,
		PollInterval:      60 * time.Second,
		Port:              9211,
		DBPath:            "/tmp/onwatch.db",
		AdminUser:         "admin",
		TestMode:          true,
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
		}, "\n") + "\n"
		r := bufio.NewReader(strings.NewReader(input))
		syn, zai, zaiURL, anth, codex, anti := collectMultipleProviders(r, testLogger())
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
		"5",      // antigravity only
		"",       // default admin user
		"",       // auto-generate password
		"70000",  // invalid port
		"9211",   // valid port
		"9",      // invalid interval
		"60",     // valid interval
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
