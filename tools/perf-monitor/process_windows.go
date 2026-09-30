//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// onwatchBinaryName is the file name of the onWatch executable.
const onwatchBinaryName = "onwatch.exe"

// waitTimeout is WAIT_TIMEOUT: the process object is not signalled, so the
// process is still running.
const waitTimeout = uint32(0x00000102)

// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION, the
// least access right that allows reading a process's image path.
const processQueryLimitedInformation = 0x1000

var procQueryFullProcessImageNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

// pidFilePath mirrors onWatch's own PID file location on Windows.
func pidFilePath() string {
	dir := os.Getenv("LOCALAPPDATA")
	if dir != "" {
		return filepath.Join(dir, "onwatch", "onwatch.pid")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), ".onwatch", "onwatch.pid")
}

// isProcessRunning reports whether pid names a running process. Windows has
// no signal-0 probe, and an exited process keeps an openable handle while
// anything holds one, so ask whether the process object has been signalled
// (which happens exactly when the process ends).
func isProcessRunning(pid int) bool {
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

// stopProcess terminates the process. Windows cannot deliver os.Interrupt to
// another process (os.Process.Signal only supports Kill there).
func stopProcess(proc *os.Process) error {
	return proc.Kill()
}

// processCommandName returns the image file base name of pid, e.g.
// onwatch.exe ("" when unknown). Windows has no ps, so ask the process object.
func processCommandName(pid int) string {
	if pid <= 0 {
		return ""
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(handle)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size]))
}
