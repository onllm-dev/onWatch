//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Review item 1: a recycled pid (or a corrupted file) must not let the stop
// path kill an unrelated process. The command name is the best-effort proof
// that the pid still belongs to an onWatch companion before any signal.
func TestStopMenubarProcessRefusesNonOnwatchPID(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	path := menubarPIDPath(false)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	if err := stopMenubarProcess(false); err != nil {
		t.Fatalf("stopMenubarProcess: %v", err)
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("a process that is not onWatch must not be signalled")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the stale pid file must be removed")
	}
}

// Finding 3: a stale pid file naming a live unrelated process (a reused pid)
// must not make stop skip the companion. The lock names the real owner; stop
// must signal that process rather than deleting the pid file and giving up.
func TestStopMenubarUpgradesReusedPIDToLockOwner(t *testing.T) {
	prevPIDDir := pidDir
	pidDir = t.TempDir()
	defer func() { pidDir = prevPIDDir }()

	holder := startLockHolderSubprocess(t, false)
	t.Cleanup(func() {
		if holder.Process != nil {
			_ = holder.Process.Kill()
			_, _ = holder.Process.Wait()
		}
	})

	unrelated := exec.Command("sleep", "30")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _, _ = unrelated.Process.Wait() })

	if err := os.WriteFile(menubarPIDPath(false), []byte(fmt.Sprintf("%d", unrelated.Process.Pid)), 0o644); err != nil {
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
	_, _ = holder.Process.Wait()

	if processAlive(unrelated.Process.Pid) == false {
		t.Fatal("the unrelated process named by the stale pid file must not be signalled")
	}
	if processAlive(holder.Process.Pid) {
		t.Fatal("the lock-owning companion must be stopped even when the pid file is stale")
	}
}
