package update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type updateExitPanic struct {
	code int
}

func fakeExecCommandSuccess(t *testing.T) func(name string, arg ...string) *exec.Cmd {
	t.Helper()
	return func(name string, arg ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 0")
	}
}

func newUpdateVersionServer(t *testing.T, latest string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v" + latest})
	}))
}

func TestApply_DownloadFailures(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		apiSrv := newUpdateVersionServer(t, "99.0.0")
		defer apiSrv.Close()
		dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer dlSrv.Close()

		u := NewUpdater("1.0.0", slog.Default())
		u.apiURL = apiSrv.URL
		u.downloadURL = dlSrv.URL

		err := u.Apply()
		if err == nil || !strings.Contains(err.Error(), "download returned HTTP 502") {
			t.Fatalf("Apply(http error) = %v", err)
		}
	})

	t.Run("empty download", func(t *testing.T) {
		apiSrv := newUpdateVersionServer(t, "99.0.1")
		defer apiSrv.Close()
		dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer dlSrv.Close()

		u := NewUpdater("1.0.0", slog.Default())
		u.apiURL = apiSrv.URL
		u.downloadURL = dlSrv.URL

		err := u.Apply()
		if err == nil || !strings.Contains(err.Error(), "downloaded file is empty") {
			t.Fatalf("Apply(empty download) = %v", err)
		}
	})

	t.Run("invalid binary", func(t *testing.T) {
		apiSrv := newUpdateVersionServer(t, "99.0.2")
		defer apiSrv.Close()
		dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not-a-binary"))
		}))
		defer dlSrv.Close()

		u := NewUpdater("1.0.0", slog.Default())
		u.apiURL = apiSrv.URL
		u.downloadURL = dlSrv.URL

		err := u.Apply()
		if err == nil || !strings.Contains(err.Error(), "not a valid executable") {
			t.Fatalf("Apply(invalid binary) = %v", err)
		}
	})
}

func TestRestart_SpawnsAppliedBinary(t *testing.T) {
	exePath := filepath.Join(t.TempDir(), "onwatch")
	markerPath := filepath.Join(t.TempDir(), "spawned.txt")
	oldArgs := os.Args
	oldExecCommand := execCommand
	t.Cleanup(func() {
		os.Args = oldArgs
		execCommand = oldExecCommand
	})
	os.Args = []string{oldArgs[0], "--debug", "update", "--update", "--port", "9211"}
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name != exePath {
			t.Fatalf("spawn name = %q, want %q", name, exePath)
		}
		cmd := exec.Command("sh", "-c", "printf spawned > \"$ONWATCH_SPAWN_MARKER\"")
		cmd.Env = append(os.Environ(), "ONWATCH_SPAWN_MARKER="+markerPath)
		return cmd
	}

	u := NewUpdater("1.0.0", slog.Default())
	u.lastAppliedPath = exePath

	if err := u.Restart(); err != nil {
		t.Fatalf("Restart() = %v", err)
	}

	for i := 0; i < 40; i++ {
		if data, err := os.ReadFile(markerPath); err == nil {
			if string(data) != "spawned" {
				t.Fatalf("spawn marker = %q, want spawned", string(data))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("spawned marker was not written by restarted process")
}

func TestRestart_SystemdBranchUsesSystemctl(t *testing.T) {
	oldInvocationID := os.Getenv("INVOCATION_ID")
	oldExecCommand := execCommand
	oldSleepFn := sleepFn
	oldExitFn := exitFn
	oldReadCgroup := readCgroupFile
	t.Cleanup(func() {
		_ = os.Setenv("INVOCATION_ID", oldInvocationID)
		execCommand = oldExecCommand
		sleepFn = oldSleepFn
		exitFn = oldExitFn
		readCgroupFile = oldReadCgroup
	})

	if err := os.Setenv("INVOCATION_ID", "systemd-test"); err != nil {
		t.Fatalf("set INVOCATION_ID: %v", err)
	}
	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/onwatch.service"), nil
	}
	execCommand = fakeExecCommandSuccess(t)
	sleepFn = func(time.Duration) {}
	exitFn = func(code int) { panic(updateExitPanic{code: code}) }

	u := NewUpdater("1.0.0", slog.Default())
	defer func() {
		r := recover()
		exit, ok := r.(updateExitPanic)
		if !ok {
			t.Fatalf("expected updateExitPanic, got %v", r)
		}
		if exit.code != 0 {
			t.Fatalf("exit code = %d, want 0", exit.code)
		}
	}()

	_ = u.Restart()
}

func TestFallbackSystemctlRestart_Success(t *testing.T) {
	oldExecCommand := execCommand
	oldSleepFn := sleepFn
	oldExitFn := exitFn
	oldReadCgroup := readCgroupFile
	t.Cleanup(func() {
		execCommand = oldExecCommand
		sleepFn = oldSleepFn
		exitFn = oldExitFn
		readCgroupFile = oldReadCgroup
	})

	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/onwatch.service"), nil
	}
	execCommand = fakeExecCommandSuccess(t)
	sleepFn = func(time.Duration) {}
	exitFn = func(code int) { panic(updateExitPanic{code: code}) }

	u := NewUpdater("1.0.0", slog.Default())
	defer func() {
		r := recover()
		exit, ok := r.(updateExitPanic)
		if !ok {
			t.Fatalf("expected updateExitPanic, got %v", r)
		}
		if exit.code != 0 {
			t.Fatalf("exit code = %d, want 0", exit.code)
		}
	}()

	_ = u.fallbackSystemctlRestart()
}

func TestMigrateSystemdUnit_ReadAndWriteFailuresAndNoop(t *testing.T) {
	oldInvocationID := os.Getenv("INVOCATION_ID")
	oldExecCommand := execCommand
	t.Cleanup(func() {
		_ = os.Setenv("INVOCATION_ID", oldInvocationID)
		execCommand = oldExecCommand
	})
	if err := os.Setenv("INVOCATION_ID", "systemd-test"); err != nil {
		t.Fatalf("set INVOCATION_ID: %v", err)
	}
	execCommand = fakeExecCommandSuccess(t)

	t.Run("missing unit file is noop", func(t *testing.T) {
		tmpHome := t.TempDir()
		oldHome := os.Getenv("HOME")
		t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
		if err := os.Setenv("HOME", tmpHome); err != nil {
			t.Fatalf("set HOME: %v", err)
		}
		readCgroupFile = func() ([]byte, error) {
			return []byte("0::/user.slice/user-501.slice/user@501.service/app.slice/missing.service"), nil
		}
		MigrateSystemdUnit(slog.Default())
	})

	t.Run("read failure is noop", func(t *testing.T) {
		tmpHome := t.TempDir()
		oldHome := os.Getenv("HOME")
		t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
		if err := os.Setenv("HOME", tmpHome); err != nil {
			t.Fatalf("set HOME: %v", err)
		}
		userDir := filepath.Join(tmpHome, ".config", "systemd", "user")
		if err := os.MkdirAll(userDir, 0o755); err != nil {
			t.Fatalf("mkdir user dir: %v", err)
		}
		serviceName := "read-dir.service"
		if err := os.Mkdir(filepath.Join(userDir, serviceName), 0o755); err != nil {
			t.Fatalf("mkdir fake unit dir: %v", err)
		}
		readCgroupFile = func() ([]byte, error) {
			return []byte("0::/system.slice/" + serviceName), nil
		}
		MigrateSystemdUnit(slog.Default())
	})

	t.Run("already up to date is noop", func(t *testing.T) {
		tmpHome := t.TempDir()
		oldHome := os.Getenv("HOME")
		t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
		if err := os.Setenv("HOME", tmpHome); err != nil {
			t.Fatalf("set HOME: %v", err)
		}
		serviceName := "noop.service"
		userDir := filepath.Join(tmpHome, ".config", "systemd", "user")
		if err := os.MkdirAll(userDir, 0o755); err != nil {
			t.Fatalf("mkdir user dir: %v", err)
		}
		unitPath := filepath.Join(userDir, serviceName)
		content := "[Service]\nRestart=always\nRestartSec=5\n"
		if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write unit file: %v", err)
		}
		readCgroupFile = func() ([]byte, error) {
			return []byte("0::/system.slice/" + serviceName), nil
		}
		MigrateSystemdUnit(slog.Default())
		got, err := os.ReadFile(unitPath)
		if err != nil {
			t.Fatalf("read unit file: %v", err)
		}
		if string(got) != content {
			t.Fatalf("unit file changed unexpectedly:\n%s", string(got))
		}
	})
}

func TestBinaryDownloadURL_CurrentPlatformSuffix(t *testing.T) {
	u := NewUpdater("1.0.0", slog.Default())
	got := u.binaryDownloadURL("9.9.9")
	wantSuffix := fmt.Sprintf("onwatch-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		wantSuffix += ".exe"
	}
	if !strings.Contains(got, wantSuffix) {
		t.Fatalf("binaryDownloadURL() = %q, want suffix %q", got, wantSuffix)
	}
}

func TestRestart_StandaloneSpawnFailureFallsBackToSystemctlSuccess(t *testing.T) {
	oldExecCommand := execCommand
	oldSleepFn := sleepFn
	oldExitFn := exitFn
	oldReadCgroup := readCgroupFile
	t.Cleanup(func() {
		execCommand = oldExecCommand
		sleepFn = oldSleepFn
		exitFn = oldExitFn
		readCgroupFile = oldReadCgroup
	})

	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/onwatch.service"), nil
	}
	calls := 0
	execCommand = func(name string, arg ...string) *exec.Cmd {
		calls++
		if calls == 1 {
			return exec.Command("/definitely/missing/binary")
		}
		return fakeExecCommandSuccess(t)(name, arg...)
	}
	sleepFn = func(time.Duration) {}
	exitFn = func(code int) { panic(updateExitPanic{code: code}) }

	u := NewUpdater("1.0.0", slog.Default())
	u.lastAppliedPath = "/definitely/missing/binary"

	defer func() {
		r := recover()
		exit, ok := r.(updateExitPanic)
		if !ok {
			t.Fatalf("expected updateExitPanic, got %v", r)
		}
		if exit.code != 0 {
			t.Fatalf("exit code = %d, want 0", exit.code)
		}
	}()

	_ = u.Restart()
}

func TestFakeExecCommandHelperSanity(t *testing.T) {
	cmd := fakeExecCommandSuccess(t)("systemctl", "restart", "onwatch.service")
	if cmd == nil {
		t.Fatal("expected helper command")
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper command run failed: %v", err)
	}
}

func TestReplaceBinary_RemoveThenRenameFailure(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	tmpPath := filepath.Join(dir, "missing.tmp")

	err := replaceBinary(exePath, tmpPath, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "replace failed after remove") {
		t.Fatalf("replaceBinary() error = %v", err)
	}
	if _, statErr := os.Stat(exePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected exePath to be removed, stat err = %v", statErr)
	}
}

func TestRestart_UsesExecutableWhenLastAppliedPathEmpty(t *testing.T) {
	oldExecCommand := execCommand
	oldArgs := os.Args
	t.Cleanup(func() {
		execCommand = oldExecCommand
		os.Args = oldArgs
	})

	os.Args = []string{oldArgs[0], "--debug", "update", "--update", "--port", "9811"}
	var gotName string
	var gotArgs []string
	execCommand = func(name string, arg ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), arg...)
		return exec.Command("sh", "-c", "exit 0")
	}

	u := NewUpdater("1.0.0", slog.Default())
	if err := u.Restart(); err != nil {
		t.Fatalf("Restart() = %v", err)
	}
	if gotName == "" {
		t.Fatal("expected exec command name to be populated from os.Executable")
	}
	if slices.Contains(gotArgs, "update") || slices.Contains(gotArgs, "--update") {
		t.Fatalf("expected update flags to be filtered, got args %v", gotArgs)
	}
}

func TestRestart_SystemdStartFailureCallsExit(t *testing.T) {
	oldInvocationID := os.Getenv("INVOCATION_ID")
	oldExecCommand := execCommand
	oldExitFn := exitFn
	oldReadCgroup := readCgroupFile
	t.Cleanup(func() {
		_ = os.Setenv("INVOCATION_ID", oldInvocationID)
		execCommand = oldExecCommand
		exitFn = oldExitFn
		readCgroupFile = oldReadCgroup
	})

	if err := os.Setenv("INVOCATION_ID", "systemd-test"); err != nil {
		t.Fatalf("set INVOCATION_ID: %v", err)
	}
	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/onwatch.service"), nil
	}
	execCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("/definitely/missing/systemctl")
	}
	exitFn = func(code int) { panic(updateExitPanic{code: code}) }

	u := NewUpdater("1.0.0", slog.Default())
	defer func() {
		r := recover()
		exit, ok := r.(updateExitPanic)
		if !ok {
			t.Fatalf("expected updateExitPanic, got %v", r)
		}
		if exit.code != 0 {
			t.Fatalf("exit code = %d, want 0", exit.code)
		}
	}()

	_ = u.Restart()
}

func TestFallbackSystemctlRestart_UserLevelSuccess(t *testing.T) {
	oldExecCommand := execCommand
	oldSleepFn := sleepFn
	oldExitFn := exitFn
	oldReadCgroup := readCgroupFile
	t.Cleanup(func() {
		execCommand = oldExecCommand
		sleepFn = oldSleepFn
		exitFn = oldExitFn
		readCgroupFile = oldReadCgroup
	})

	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/onwatch.service"), nil
	}
	calls := 0
	execCommand = func(name string, arg ...string) *exec.Cmd {
		calls++
		if calls%2 == 1 {
			return exec.Command("/definitely/missing/systemctl")
		}
		return exec.Command("sh", "-c", "exit 0")
	}
	sleepFn = func(time.Duration) {}
	exitFn = func(code int) { panic(updateExitPanic{code: code}) }

	u := NewUpdater("1.0.0", slog.Default())
	defer func() {
		r := recover()
		exit, ok := r.(updateExitPanic)
		if !ok {
			t.Fatalf("expected updateExitPanic, got %v", r)
		}
		if exit.code != 0 {
			t.Fatalf("exit code = %d, want 0", exit.code)
		}
	}()

	_ = u.fallbackSystemctlRestart()
}
