package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
)

func TestMenubarLockIsExclusive(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	first, err := acquireMenubarLock(false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.Close()

	// A second acquisition must fail: the OS lock is per open file, not per
	// pid, so this mirrors a second companion process.
	if second, err := acquireMenubarLock(false); !errors.Is(err, errMenubarCompanionRunning) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second acquire err = %v, want errMenubarCompanionRunning", err)
	}

	// Releasing lets the next companion start.
	if err := first.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}
	third, err := acquireMenubarLock(false)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = third.Close()
}

func TestMenubarLockHoldsPid(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	lock, err := acquireMenubarLock(false)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Close()
	data, err := os.ReadFile(menubarLockPath(false))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("lock file must record the owning pid for diagnostics")
	}
}

// The companion opens its own log; the daemon must not also write it. The
// relay is disabled when the companion owns the file, which is what stops a
// hung daemon's 16KB pipe from blocking the companion's refresh worker.
func TestCompanionLogWriterOwnership(t *testing.T) {
	sink := &countingWriter{}
	if relay := companionRelayWriter(sink, &config.Config{}); relay != nil {
		t.Fatal("the companion owns menubar.log; the daemon must drain its pipes, not relay them")
	}
	if sink.n != 0 {
		t.Fatalf("daemon wrote %d bytes to the companion log", sink.n)
	}
	if relay := companionRelayWriter(sink, &config.Config{DebugMode: true}); relay == nil {
		t.Fatal("debug mode keeps the companion on stdout, so the daemon relays it")
	}
}

// companionLogFile opens the same path the daemon passes, in append mode, and
// writes there instead of stdout.
func TestCompanionLogFileWritesToDaemonPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "menubar.log")
	writer, err := openCompanionLog(path)
	if err != nil {
		t.Fatalf("openCompanionLog: %v", err)
	}
	if _, err := writer.Write([]byte("line one\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := os.WriteFile(path, []byte("line zero\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Reopening appends rather than truncating.
	writer, err = openCompanionLog(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_, _ = writer.Write([]byte("line two\n"))
	_ = writer.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "line zero\nline two\n" {
		t.Fatalf("log content = %q, want seeded line preserved and appended", data)
	}
}

type countingWriter struct{ n int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}

var _ io.Writer = (*countingWriter)(nil)
var _ = slog.Default
