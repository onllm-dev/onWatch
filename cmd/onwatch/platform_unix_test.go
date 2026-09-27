//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonSysProcAttr_UnixSetsid(t *testing.T) {
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Fatal("expected non-nil SysProcAttr")
	}
	if !attr.Setsid {
		t.Fatal("expected Setsid=true")
	}
}

// macOS ps prints the full executable path for comm, so a binary that merely
// lives under a directory called onwatch must not pass for onWatch. Only the
// base name counts, as on Windows.
func TestProcessCommandName_BaseNameOnly(t *testing.T) {
	// A copy of this test binary, renamed and placed under an onwatch
	// directory, runs the idle sleep helper.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "onwatch", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "idler")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-test.run=^TestSleepHelperProcess_NeverRun$")
	cmd.Env = append(os.Environ(), "GO_SLEEP_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	if got := processCommandName(cmd.Process.Pid); got != "idler" {
		t.Errorf("processCommandName() = %q, want %q", got, "idler")
	}
	if isOnwatchProcess(cmd.Process.Pid) {
		t.Error("a binary under an onwatch directory must not be treated as onWatch")
	}
}
