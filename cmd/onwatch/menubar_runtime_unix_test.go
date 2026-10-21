//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
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
