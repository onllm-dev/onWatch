package main

import (
	"bufio"
	"os"
	"os/exec"
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

func TestMain_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Getenv("HELPER_MAIN_MODE")
	switch mode {
	case "help":
		os.Args = []string{"onwatch", "--help"}
		main()
		os.Exit(0)
	case "error":
		for _, key := range []string{"SYNTHETIC_API_KEY", "ZAI_API_KEY", "ANTHROPIC_TOKEN", "COPILOT_TOKEN", "CODEX_TOKEN", "ANTIGRAVITY_ENABLED", "ANTIGRAVITY_BASE_URL", "ANTIGRAVITY_CSRF_TOKEN"} {
			_ = os.Unsetenv(key)
		}
		os.Args = []string{"onwatch"}
		main() // expected to os.Exit(1)
		os.Exit(2)
	default:
		os.Exit(3)
	}
}

func TestMain_SubprocessScenarios(t *testing.T) {
	t.Run("main help exits successfully", func(t *testing.T) {
		// Retry once — under heavy parallel test load (race detector + 12 packages),
		// the subprocess may transiently fail to start.
		var out []byte
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			cmd := exec.Command(os.Args[0], "-test.run=TestMain_HelperProcess")
			cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MAIN_MODE=help")
			out, err = cmd.CombinedOutput()
			if err == nil && strings.Contains(string(out), "Usage: onwatch") {
				return
			}
			if attempt == 0 {
				t.Logf("attempt %d failed (retrying): %v", attempt+1, err)
			}
		}
		if err != nil {
			t.Fatalf("helper process failed: %v\noutput:\n%s", err, string(out))
		}
		if !strings.Contains(string(out), "Usage: onwatch") {
			t.Fatalf("expected help output, got:\n%s", string(out))
		}
	})

	t.Run("main error path exits with status 1", func(t *testing.T) {
		home := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=TestMain_HelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_MAIN_MODE=error",
			"HOME="+home,
			"ONWATCH_PORT=1",
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit, output:\n%s", string(out))
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("expected ExitError, got %T: %v", err, err)
		}
		if code := exitErr.ExitCode(); code != 1 {
			t.Fatalf("expected exit code 1, got %d\noutput:\n%s", code, string(out))
		}
		if !strings.Contains(string(out), "failed to load config") {
			t.Fatalf("expected config error in output, got:\n%s", string(out))
		}
	})
}

