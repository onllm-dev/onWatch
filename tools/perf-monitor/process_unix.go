//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// onwatchBinaryName is the file name of the onWatch executable.
const onwatchBinaryName = "onwatch"

// pidFilePath mirrors onWatch's own PID file location on Unix.
func pidFilePath() string {
	return filepath.Join(os.Getenv("HOME"), ".onwatch", "onwatch.pid")
}

// isProcessRunning reports whether pid names a live process. Signal 0 checks
// existence without delivering anything. A nil os.Signal is not a signal-0
// probe - os.Process.Signal rejects it - so it must be syscall.Signal(0).
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// stopProcess asks the process to shut down gracefully.
func stopProcess(proc *os.Process) error {
	return proc.Signal(os.Interrupt)
}

// processCommandName returns the executable base name of pid ("" when
// unknown). macOS ps prints the full path for comm; only the base name may
// count, or any binary under a directory named onwatch would match.
func processCommandName(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return ""
	}
	return filepath.Base(name)
}
