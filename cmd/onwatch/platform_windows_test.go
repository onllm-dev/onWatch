//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestTerminateProcessHelper is the process TestTerminateProcessExitsWithZero
// stops; run directly it returns at once.
func TestTerminateProcessHelper(t *testing.T) {
	if os.Getenv("ONWATCH_TERMINATE_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

// The auto-start supervisor restarts a daemon that exits non-zero, so the way
// onWatch stops itself on Windows must read as a clean exit.
func TestTerminateProcessExitsWithZero(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestTerminateProcessHelper$")
	cmd.Env = append(os.Environ(), "ONWATCH_TERMINATE_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := terminateProcess(cmd.Process); err != nil {
		t.Fatalf("terminateProcess: %v", err)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("exit code = %d, want 0", exitErr.ExitCode())
	}
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
}
