//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenRotatingLogFile_IsNotWorldReadable asserts the debug log is owner-only.
//
// The log records provider account names and other identifiers (see
// internal/agent/minimax_agent_manager.go), so on a shared or multi-user host a
// world-readable log discloses personal data to every local account. 0600 is
// the same posture already used for the .env file.
func TestOpenRotatingLogFile_IsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", ".onwatch.log")

	file, err := OpenRotatingLogFile(path)
	if err != nil {
		t.Fatalf("OpenRotatingLogFile: %v", err)
	}
	defer file.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file permissions = %o, want 600 (owner-only)", perm)
	}
}

// TestOpenRotatingLogFile_TightensExistingPermissions asserts an upgrade fixes a
// log file left world-readable by an earlier version.
func TestOpenRotatingLogFile_TightensExistingPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".onwatch.log")

	if err := os.WriteFile(path, []byte("existing log line\n"), 0o644); err != nil {
		t.Fatalf("seed log file: %v", err)
	}

	file, err := OpenRotatingLogFile(path)
	if err != nil {
		t.Fatalf("OpenRotatingLogFile: %v", err)
	}
	defer file.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("existing log file permissions = %o, want 600 after reopen", perm)
	}

	// Tightening permissions must not discard already-written history.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("reopening the log truncated existing content")
	}
}
