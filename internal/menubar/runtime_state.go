//go:build menubar && (darwin || linux || windows)

package menubar

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func companionProcessRunning() bool {
	for _, path := range []string{companionPIDPath(false), companionPIDPath(true)} {
		pid := readPID(path)
		if pid <= 0 {
			continue
		}
		if processAlive(pid) {
			return true
		}
		_ = os.Remove(path)
	}
	return false
}

func companionPIDPath(testMode bool) string {
	name := "onwatch-menubar.pid"
	if testMode {
		name = "onwatch-menubar-test.pid"
	}
	return filepath.Join(defaultCompanionPIDDir(), name)
}

func companionRefreshPath(testMode bool) string {
	name := "onwatch-menubar.refresh"
	if testMode {
		name = "onwatch-menubar-test.refresh"
	}
	return filepath.Join(defaultCompanionPIDDir(), name)
}

func defaultCompanionPIDDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "onwatch")
		}
		return filepath.Join(os.Getenv("USERPROFILE"), ".onwatch")
	}
	return filepath.Join(os.Getenv("HOME"), ".onwatch")
}

func readPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func companionPIDEnvValue(testMode bool) string {
	return fmt.Sprintf("%t:%s", testMode, companionPIDPath(testMode))
}

// TriggerRefresh asks a running companion to re-fetch its snapshot right
// away, e.g. after the user changes tray preferences in the popover.
func TriggerRefresh(testMode bool) error {
	pidPath := companionPIDPath(testMode)
	pid := readPID(pidPath)
	if pid <= 0 {
		return nil
	}
	if !processAlive(pid) {
		_ = os.Remove(pidPath)
		return nil
	}
	return requestCompanionRefresh(pid, testMode)
}
