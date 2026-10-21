package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Review item 1: the companion pid file must name the SPAWNED CHILD, never the
// daemon. The daemon used to write its own pid there and the companion only
// overwrote it after its lock wait of up to 10s. A shutdown inside that window
// read the daemon's pid back, SIGTERMed it, and (10s later, still alive) would
// have SIGKILLed it - skipping the database close.
func TestWriteRuntimePIDValueRecordsChildPID(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	cmd := startSleepSubprocess(t)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	path := menubarPIDPath(false)
	if err := writeRuntimePIDValue(path, cmd.Process.Pid); err != nil {
		t.Fatalf("writeRuntimePIDValue: %v", err)
	}
	if got := readRuntimePID(path); got != cmd.Process.Pid {
		t.Fatalf("pid file = %d, want the child pid %d", got, cmd.Process.Pid)
	}
	if got := readRuntimePID(path); got == os.Getpid() {
		t.Fatal("pid file must never contain the daemon's own pid")
	}
}

// Review item 1: stopMenubarProcess must refuse its own pid and its parent.
// A companion pid file briefly holding the daemon's pid would otherwise make
// an orderly shutdown signal the daemon itself and skip the DB close.
func TestStopMenubarProcessNeverSignalsSelfOrParent(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		path := menubarPIDPath(false)
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0o644); err != nil {
			t.Fatalf("write pid %d: %v", pid, err)
		}

		done := make(chan error, 1)
		go func() { done <- stopMenubarProcess(false) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("stopMenubarProcess(%d): %v", pid, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("stopMenubarProcess(%d) did not return", pid)
		}

		if !processAlive(os.Getpid()) {
			t.Fatal("the test process must survive a stop that named its own pid")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("a corrupt pid file naming %d must be removed", pid)
		}
	}
}
