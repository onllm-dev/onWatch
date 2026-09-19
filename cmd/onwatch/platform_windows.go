//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

const createNoWindow = 0x08000000

// waitTimeout is WAIT_TIMEOUT: the process object is not signalled, so the
// process is still running.
const waitTimeout = uint32(0x00000102)

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

// processAlive reports whether pid names a running process. Opening a handle
// is not enough on Windows: a process that has exited still has an openable
// handle while anything holds one, so ask whether the process object has been
// signalled (which happens exactly when the process ends).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// A process we may not synchronize on still exists.
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(handle)
	state, err := syscall.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}
	return state == waitTimeout
}
