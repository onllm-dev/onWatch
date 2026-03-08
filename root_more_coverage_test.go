package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func setTestArgs(t *testing.T, args []string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
}

func TestRun_CommandDispatchDeterministic(t *testing.T) {
	t.Run("help command", func(t *testing.T) {
		setTestArgs(t, []string{"onwatch", "--help"})
		out := captureStdout(t, func() {
			if err := run(); err != nil {
				t.Fatalf("run help error: %v", err)
			}
		})
		if !strings.Contains(out, "Usage: onwatch") {
			t.Fatalf("expected help output, got: %s", out)
		}
	})

	t.Run("version command", func(t *testing.T) {
		setTestArgs(t, []string{"onwatch", "version"})
		out := captureStdout(t, func() {
			if err := run(); err != nil {
				t.Fatalf("run version error: %v", err)
			}
		})
		if !strings.Contains(out, "onWatch v") {
			t.Fatalf("expected version output, got: %s", out)
		}
	})

	t.Run("update command dev mode", func(t *testing.T) {
		origVersion := version
		version = "dev"
		t.Cleanup(func() { version = origVersion })

		setTestArgs(t, []string{"onwatch", "update"})
		out := captureStdout(t, func() {
			if err := run(); err != nil {
				t.Fatalf("run update error: %v", err)
			}
		})
		if !strings.Contains(out, "Already at the latest version") {
			t.Fatalf("expected no-update output, got: %s", out)
		}
	})
}

func TestStopPreviousInstance_SelfPIDFileIsSafeAndRemoved(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+":9211"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stopPreviousInstance(0, true)

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, err=%v", err)
	}
}

func TestRunStopAndStatus_StalePIDBranches(t *testing.T) {
	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	stalePID := 999999
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)+":9211"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stopOut := captureStdout(t, func() {
		if err := runStop(true); err != nil {
			t.Fatalf("runStop error: %v", err)
		}
	})
	if !strings.Contains(stopOut, "stale PID file") {
		t.Fatalf("expected stale pid output from stop, got: %s", stopOut)
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)+":9211"), 0o644); err != nil {
		t.Fatalf("rewrite pid file: %v", err)
	}
	statusOut := captureStdout(t, func() {
		if err := runStatus(true); err != nil {
			t.Fatalf("runStatus error: %v", err)
		}
	})
	if !strings.Contains(statusOut, "stale PID file") {
		t.Fatalf("expected stale pid output from status, got: %s", statusOut)
	}
}

func TestSetupHelpers_AddMissingProvidersAndTokenCollectors(t *testing.T) {
	t.Run("addMissingProviders appends selected providers", func(t *testing.T) {
		envFile := filepath.Join(t.TempDir(), ".env")
		initial := strings.Join([]string{
			"ANTHROPIC_TOKEN=already_set",
			"CODEX_TOKEN=already_set",
		}, "\n")
		if err := os.WriteFile(envFile, []byte(initial), 0o600); err != nil {
			t.Fatalf("write initial env: %v", err)
		}

		input := strings.Join([]string{
			"y",            // add synthetic
			"syn_abc12345", // synthetic key
			"y",            // add zai
			"zai-token",    // zai key
			"y",            // use default zai URL
			"y",            // add antigravity
		}, "\n") + "\n"

		existing := &existingEnv{anthropicToken: "already_set", codexToken: "already_set"}
		if err := addMissingProviders(bufio.NewReader(strings.NewReader(input)), envFile, existing); err != nil {
			t.Fatalf("addMissingProviders error: %v", err)
		}

		data, err := os.ReadFile(envFile)
		if err != nil {
			t.Fatalf("read env file: %v", err)
		}
		content := string(data)
		for _, want := range []string{"SYNTHETIC_API_KEY=syn_abc12345", "ZAI_API_KEY=zai-token", "ZAI_BASE_URL=https://api.z.ai/api", "ANTIGRAVITY_ENABLED=true"} {
			if !strings.Contains(content, want) {
				t.Fatalf("expected env content %q, got:\n%s", want, content)
			}
		}
	})

	t.Run("collectAnthropicToken and collectCodexToken stay deterministic", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CODEX_HOME", filepath.Join(home, "missing-codex"))

		anthReader := bufio.NewReader(strings.NewReader("\nmanual-anth-token\n"))
		anth := collectAnthropicToken(anthReader, testLogger())
		if anth == "" {
			t.Fatal("expected non-empty anthropic token")
		}

		codexReader := bufio.NewReader(strings.NewReader("\nmanual-codex-token\n"))
		codex := collectCodexToken(codexReader, testLogger())
		if codex == "" {
			t.Fatal("expected non-empty codex token")
		}
	})
}

func TestDaemonSysProcAttr_UnixSetsid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Fatal("expected non-nil SysProcAttr")
	}
	if !attr.Setsid {
		t.Fatal("expected Setsid=true")
	}
}

func TestRun_HelpCommand(t *testing.T) {
	setTestArgs(t, []string{"onwatch", "--help"})
	out := captureStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run --help error: %v", err)
		}
	})
	if !strings.Contains(out, "Usage: onwatch") {
		t.Fatalf("expected help output, got:\n%s", out)
	}
}

func TestMain_ErrorPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ONWATCH_PORT", "1")
	// Clear all API keys
	for _, key := range []string{
		"SYNTHETIC_API_KEY", "SYNTHETIC_COOKIE",
		"ZAI_API_KEY", "ANTHROPIC_TOKEN",
		"COPILOT_TOKEN", "CODEX_TOKEN",
		"ANTIGRAVITY_ENABLED", "ANTIGRAVITY_BASE_URL", "ANTIGRAVITY_CSRF_TOKEN",
	} {
		t.Setenv(key, "")
	}

	oldPIDFile := pidFile
	pidFile = filepath.Join(t.TempDir(), "onwatch.pid")
	t.Cleanup(func() { pidFile = oldPIDFile })

	setTestArgs(t, []string{"onwatch"})

	err := run()
	if err == nil {
		t.Fatal("expected error from run() with no config")
	}
	if !strings.Contains(err.Error(), "failed to load config") &&
		!strings.Contains(err.Error(), "failed to setup logging") &&
		!strings.Contains(err.Error(), "server error") {
		t.Fatalf("expected startup error, got: %v", err)
	}
}
