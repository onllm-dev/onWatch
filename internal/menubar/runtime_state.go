//go:build menubar && (darwin || linux)

package menubar

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func companionProcessRunning() bool {
	for _, path := range []string{companionPIDPath(false), companionPIDPath(true)} {
		pid := readPID(path)
		if pid <= 0 {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err == nil && proc.Signal(syscall.Signal(0)) == nil {
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

func defaultCompanionPIDDir() string {
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

const refreshCompanionSignal = syscall.SIGUSR1

func TriggerRefresh(testMode bool) error {
	pidPath := companionPIDPath(testMode)
	pid := readPID(pidPath)
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(refreshCompanionSignal) != nil {
		_ = os.Remove(pidPath)
		return nil
	}
	return nil
}
