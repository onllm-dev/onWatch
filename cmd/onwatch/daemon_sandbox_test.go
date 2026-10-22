package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// daemon subprocess tests sandbox the child's home inside t.TempDir. On
// Windows the child is terminated abruptly (os.Interrupt cannot be delivered),
// and testing's own TempDir cleanup reports "directory is not empty" when a
// file lands between its directory scan and rmdir. The helper removes that
// home itself, with retries, before the TempDir cleanup runs.

func TestRemoveAllWithRetryRetriesTransientFailure(t *testing.T) {
	calls := 0
	errNotSettled := errors.New("unlinkat: The directory is not empty")
	err := removeAllWithRetry(t.TempDir(), 2*time.Second, func(string) error {
		calls++
		if calls < 3 {
			return errNotSettled
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry did not converge: %v", err)
	}
	if calls != 3 {
		t.Fatalf("remove called %d times, want 3", calls)
	}
}

func TestRemoveAllWithRetryStopsAtDeadline(t *testing.T) {
	calls := 0
	err := removeAllWithRetry(t.TempDir(), 60*time.Millisecond, func(string) error {
		calls++
		return errors.New("unlinkat: The directory is not empty")
	})
	if err == nil {
		t.Fatal("expected the last removal error")
	}
	if calls < 2 {
		t.Fatalf("remove called %d times, want at least 2 before giving up", calls)
	}
}

func TestSandboxHomeUnderTempOnlyTrustsTempPaths(t *testing.T) {
	tmp := t.TempDir()

	home, ok := sandboxHomeUnderTemp([]string{"HOME=/home/someone", "USERPROFILE=" + tmp})
	if !ok || home != filepath.Clean(tmp) {
		t.Fatalf("sandboxHomeUnderTemp = %q, %v; want %q, true", home, ok, filepath.Clean(tmp))
	}

	// The last duplicate key wins: callers append the sandbox after os.Environ.
	home, ok = sandboxHomeUnderTemp([]string{"USERPROFILE=" + filepath.Join(tmp, "a"), "USERPROFILE=" + filepath.Join(tmp, "b")})
	if !ok || home != filepath.Join(filepath.Clean(tmp), "b") {
		t.Fatalf("duplicate key: sandboxHomeUnderTemp = %q, %v", home, ok)
	}

	// A real home must never be offered for removal.
	if _, ok := sandboxHomeUnderTemp([]string{"HOME=/Users/someone", "USERPROFILE=/Users/someone"}); ok {
		t.Fatal("a home outside the temp directory must not be removed")
	}
	if _, ok := sandboxHomeUnderTemp(nil); ok {
		t.Fatal("no home must yield no cleanup path")
	}
}

func TestCleanupDaemonSandboxRemovesTempHome(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, "onwatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "onwatch.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupDaemonSandbox(t, []string{"HOME=/not/temp", "USERPROFILE=" + home})
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("sandbox home still exists: %v", err)
	}
	// t.TempDir's own cleanup now finds an empty parent and succeeds.
}

func TestCleanupDaemonSandboxLeavesRealHome(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "not-removed")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// A path outside os.TempDir must be ignored.
	cleanupDaemonSandbox(t, []string{"HOME=" + filepath.Join(string(filepath.Separator), "home", "someone")})
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("unrelated dir gone: %v", err)
	}
}
