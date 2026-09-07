//go:build menubar && windows

package menubar

import (
	"os"
	"syscall"
	"time"
)

// Windows has no SIGUSR1, so the daemon touches a marker file next to the
// PID file and the companion watches its modification time.

// waitTimeout is WAIT_TIMEOUT: the process object is not signalled, so the
// process is still running.
const waitTimeout = uint32(0x00000102)

// processAlive reports whether pid names a running process. A handle to an
// exited process stays openable while anything holds one, so check whether the
// process object has been signalled instead of trusting the open alone.
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

func requestCompanionRefresh(_ int, testMode bool) error {
	path := companionRefreshPath(testMode)
	if err := os.MkdirAll(defaultCompanionPIDDir(), 0o755); err != nil {
		return err
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(now.Format(time.RFC3339Nano)), 0o644)
}

// watchRefreshRequests polls the marker file once a second. A stat per
// second is negligible and avoids named pipes or a listening socket.
func watchRefreshRequests(stop <-chan struct{}, testMode bool, fn func()) {
	path := companionRefreshPath(testMode)
	var last time.Time
	if info, err := os.Stat(path); err == nil {
		last = info.ModTime()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(last) {
				last = info.ModTime()
				fn()
			}
		case <-stop:
			return
		}
	}
}
