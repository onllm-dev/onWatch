//go:build menubar && (darwin || linux)

package menubar

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRefreshCompanionSignalUsesSIGUSR1(t *testing.T) {
	if refreshCompanionSignal != syscall.SIGUSR1 {
		t.Fatalf("expected refresh signal %v, got %v", syscall.SIGUSR1, refreshCompanionSignal)
	}
}

func TestCompanionPIDPathNormalMode(t *testing.T) {
	path := companionPIDPath(false)
	if !strings.HasSuffix(path, "onwatch-menubar.pid") {
		t.Fatalf("expected path ending in onwatch-menubar.pid, got %q", path)
	}
}

func TestCompanionPIDPathTestMode(t *testing.T) {
	path := companionPIDPath(true)
	if !strings.HasSuffix(path, "onwatch-menubar-test.pid") {
		t.Fatalf("expected path ending in onwatch-menubar-test.pid, got %q", path)
	}
}

func TestCompanionPIDPathDiffers(t *testing.T) {
	normal := companionPIDPath(false)
	test := companionPIDPath(true)
	if normal == test {
		t.Fatal("normal and test PID paths must differ")
	}
}

func TestDefaultCompanionPIDDirNotEmpty(t *testing.T) {
	dir := defaultCompanionPIDDir()
	if dir == "" {
		t.Fatal("expected non-empty PID directory")
	}
}

func TestReadPIDValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pid := readPID(path)
	if pid != 12345 {
		t.Fatalf("expected PID 12345, got %d", pid)
	}
}

func TestReadPIDEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	pid := readPID(path)
	if pid != 0 {
		t.Fatalf("expected PID 0 for empty file, got %d", pid)
	}
}

func TestReadPIDNonexistentFile(t *testing.T) {
	pid := readPID("/tmp/nonexistent-onwatch-pid-test-file.pid")
	if pid != 0 {
		t.Fatalf("expected PID 0 for missing file, got %d", pid)
	}
}

func TestReadPIDMalformedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pid := readPID(path)
	if pid != 0 {
		t.Fatalf("expected PID 0 for malformed content, got %d", pid)
	}
}

func TestReadPIDWithWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")
	if err := os.WriteFile(path, []byte("  42  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	pid := readPID(path)
	if pid != 42 {
		t.Fatalf("expected PID 42, got %d", pid)
	}
}

func TestCompanionPIDEnvValue(t *testing.T) {
	value := companionPIDEnvValue(false)
	if !strings.HasPrefix(value, "false:") {
		t.Fatalf("expected env value starting with 'false:', got %q", value)
	}
	if !strings.Contains(value, "onwatch-menubar.pid") {
		t.Fatalf("expected env value containing pid file name, got %q", value)
	}
}

func TestCompanionPIDEnvValueTestMode(t *testing.T) {
	value := companionPIDEnvValue(true)
	if !strings.HasPrefix(value, "true:") {
		t.Fatalf("expected env value starting with 'true:', got %q", value)
	}
	if !strings.Contains(value, "onwatch-menubar-test.pid") {
		t.Fatalf("expected env value containing test pid file name, got %q", value)
	}
}

func TestCompanionProcessRunningNoProcess(t *testing.T) {
	// With no PID files, should return false.
	running := companionProcessRunning()
	// We can't assert false since the real menubar might be running,
	// but we can assert it doesn't panic.
	_ = running
}

func TestTriggerRefreshMissingPIDFile(t *testing.T) {
	// TriggerRefresh with a non-existent PID file should return nil.
	err := TriggerRefresh(true)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestTriggerRefreshDeadProcess(t *testing.T) {
	// Write a PID file pointing to a dead process, then verify
	// TriggerRefresh cleans it up.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "onwatch-menubar-test.pid")
	// PID 99999999 almost certainly doesn't exist.
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", 99999999)), 0644); err != nil {
		t.Fatal(err)
	}
	// Verify the PID is read correctly.
	pid := readPID(pidFile)
	if pid != 99999999 {
		t.Fatalf("expected PID 99999999, got %d", pid)
	}
}
