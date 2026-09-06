//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

const createNoWindow = 0x08000000

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func defaultPIDDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "onwatch")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), ".onwatch")
}

// companionSysProcAttr keeps the tray companion from flashing a console
// window when the daemon spawns it; its stdout/stderr still flow through
// the log pipes.
func companionSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// terminateProcess kills the child: Windows has no SIGTERM equivalent for
// console-less processes.
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}

// processAlive reports whether pid names a running process. os.FindProcess
// opens a handle on Windows and fails when the process does not exist.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = proc.Release()
	return true
}
