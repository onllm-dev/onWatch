package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// daemonSandboxRemoveTimeout bounds how long the daemon-subprocess tests keep
// retrying to empty a sandbox home before t.TempDir's cleanup runs.
const daemonSandboxRemoveTimeout = 5 * time.Second

// removeAllWithRetry removes path, retrying until the deadline. Removing a
// directory while a process is still writing into it fails with "directory is
// not empty" (ENOTEMPTY on Windows); a short retry window lets the last write
// land and the removal converge. remove is injectable for tests.
func removeAllWithRetry(path string, timeout time.Duration, remove func(string) error) error {
	deadline := time.Now().Add(timeout)
	delay := 10 * time.Millisecond
	for {
		err := remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay *= 2
		}
	}
}

// sandboxHomeUnderTemp returns the child home named by env (USERPROFILE on
// Windows, HOME elsewhere) when it lies inside the OS temp directory, so the
// caller may remove it before t.TempDir cleanup. The last duplicate key wins,
// matching how exec.Cmd resolves duplicate environment entries (callers append
// the sandbox to os.Environ). A real home is never returned.
func sandboxHomeUnderTemp(env []string) (string, bool) {
	var home string
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "USERPROFILE="); ok {
			home = value
		}
	}
	if home == "" {
		for _, kv := range env {
			if value, ok := strings.CutPrefix(kv, "HOME="); ok {
				home = value
			}
		}
	}
	if home == "" {
		return "", false
	}
	home = filepath.Clean(home)
	temp := filepath.Clean(os.TempDir())
	rel, err := filepath.Rel(temp, home)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return home, true
}

// cleanupDaemonSandbox empties the child's sandbox home after a daemon
// subprocess was stopped, so t.TempDir's cleanup does not race a last write
// from the dying child. On Windows testing's TempDir cleanup fails with
// "unlinkat ...: The directory is not empty" when an entry appears between its
// scan and rmdir; Go only retries sharing violations there, not ENOTEMPTY.
func cleanupDaemonSandbox(t *testing.T, env []string) {
	t.Helper()
	home, ok := sandboxHomeUnderTemp(env)
	if !ok {
		return
	}
	_ = removeAllWithRetry(home, daemonSandboxRemoveTimeout, os.RemoveAll)
}
