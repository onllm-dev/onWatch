//go:build menubar && windows

package menubar

import (
	"os"
	"time"
)

// Windows has no SIGUSR1, so the daemon touches a marker file next to the
// PID file and the companion watches its modification time.

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = proc.Release()
	return true
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
