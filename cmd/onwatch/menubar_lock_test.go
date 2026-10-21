package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

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

// The locked region must never cover the pid text at offset 0: Windows locks
// are mandatory, so the file would be unreadable while held.
func TestMenubarLockRegionLeavesPidReadable(t *testing.T) {
	if menubarLockOffset == 0 {
		t.Fatal("the locked range must start past the pid text")
	}
	if menubarLockOffset <= 4096 {
		t.Fatalf("lock offset %d is too close to the pid text", menubarLockOffset)
	}
	if menubarLockLength <= 0 {
		t.Fatal("the lock must cover at least one byte")
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

// Review item 4: when the companion cannot open menubar.log it must fall back
// to STDERR (the daemon captures stderr for crash diagnostics), not stdout,
// which the daemon now discards.
func TestCompanionLogSinkFallbackGoesToStderr(t *testing.T) {
	// A directory path cannot be opened as a file, forcing the failure path on
	// every platform.
	badPath := t.TempDir()
	sink, closer, err := companionLogSink(badPath, false)
	if err == nil {
		t.Fatal("expected an error opening a directory as the log file")
	}
	if sink != os.Stderr {
		t.Fatal("the fallback sink must be stderr so crash traces reach the daemon log")
	}
	// os.Stderr is an *os.File and so satisfies io.Closer; handing it back
	// would let the caller close the process's stderr and lose crash traces.
	if closer != nil {
		t.Fatal("the stderr fallback must not be closable")
	}
}

// --- review item 3: restart ordering -------------------------------------

// stopMenubarProcess must wait for the companion to exit so a replacement can
// take the single-instance lock. A slow-exiting old companion used to make the
// new one fail the lock and exit, leaving no tray at all.
func TestStopMenubarProcessWaitsForExit(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	cmd := startSleepSubprocess(t)
	pid := cmd.Process.Pid
	// The helper ignores SIGTERM's default? It does not install a handler, so
	// SIGTERM ends it; the wait path is what is under test here.
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	if err := os.WriteFile(menubarPIDPath(false), []byte(fmt.Sprintf("%d", pid)), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- stopMenubarProcess(false) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopMenubarProcess: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("stopMenubarProcess did not return")
	}
	if processAlive(pid) {
		t.Fatalf("process %d still alive after stopMenubarProcess returned", pid)
	}
	if readRuntimePID(menubarPIDPath(false)) != 0 {
		t.Fatal("pid file must be removed")
	}
	_, _ = cmd.Process.Wait()
}

// The companion's lock acquisition retries the non-blocking lock for a bounded
// window, so a replacement started while the old one is still exiting does not
// immediately give up and leave no tray.
func TestAcquireMenubarLockRetries(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	holder, err := acquireMenubarLock(false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(750 * time.Millisecond)
		_ = holder.Close()
		close(released)
	}()
	lock, err := acquireMenubarLockWait(false, 5*time.Second, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("waiting acquire: %v", err)
	}
	<-released
	_ = lock.Close()

	// The bounded window still ends when nobody releases.
	holder, err = acquireMenubarLock(false)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer holder.Close()
	if _, err := acquireMenubarLockWait(false, 300*time.Millisecond, 100*time.Millisecond); !errors.Is(err, errMenubarCompanionRunning) {
		t.Fatalf("bounded acquire err = %v, want errMenubarCompanionRunning", err)
	}
}

type countingWriter struct{ n int }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}

var _ io.Writer = (*countingWriter)(nil)
var _ = slog.Default
