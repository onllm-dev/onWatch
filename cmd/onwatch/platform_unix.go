//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func defaultPIDDir() string {
	return filepath.Join(os.Getenv("HOME"), ".onwatch")
}

// companionSysProcAttr configures the spawned tray companion. Unix needs
// nothing special: it inherits the daemon's session and environment.
func companionSysProcAttr() *syscall.SysProcAttr { return nil }

// terminateProcess asks a child to exit gracefully.
func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

// processAlive reports whether pid is a live, non-zombie process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if processZombie(pid) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// processCommandName returns the executable base name of pid ("" when
// unknown). macOS ps prints the full path for comm, and only the base name may
// count: otherwise any binary under a directory named onwatch would pass
// isOnwatchProcess. This matches the Windows variant.
func processCommandName(pid int) string {
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

func processZombie(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(string(out)), "Z")
}
