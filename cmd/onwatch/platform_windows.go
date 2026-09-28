//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const createNoWindow = 0x08000000

// waitTimeout is WAIT_TIMEOUT: the process object is not signalled, so the
// process is still running.
const waitTimeout = uint32(0x00000102)

// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION, the
// least access right that allows reading a process's image path.
const processQueryLimitedInformation = 0x1000

var procQueryFullProcessImageNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

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

// processCommandName returns the image file name of pid, e.g. onwatch.exe
// ("" when unknown). Windows has no ps, so ask the process object itself. Only
// the base name counts: a full path such as C:\Users\x\.onwatch\bin\... would
// let any binary under an "onwatch" directory pass for onWatch.
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
