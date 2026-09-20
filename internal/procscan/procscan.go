// Package procscan reports whether a local CLI process is currently running.
//
// Several providers must not refresh or probe while the vendor's own CLI is
// live: onWatch would rotate a refresh token out from under the session, or
// contend for the same rate-limited endpoint. Every one of those checks needs
// the same two things - a process listing that works on macOS, Linux and
// Windows, and a hard deadline so a wedged `ps` can never stall a poll cycle.
package procscan

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"time"
)

// ScanTimeout bounds the process listing. A poll cycle must never block on it.
const ScanTimeout = 5 * time.Second

// Running reports whether any running process matches.
//
// On unix the full command line of every process is passed to match, which lets
// callers tell a CLI apart from a desktop app or an unrelated process that
// merely mentions the vendor's name. On Windows, tasklist cannot report command
// lines without a much heavier WMI/PowerShell query, so windowsImage is matched
// against the executable name instead; callers that share an image name with a
// desktop app inherit that limitation.
//
// An unusable process listing reports false: callers fall back to the behaviour
// they had before the guard existed, which their own backoff still bounds.
func Running(windowsImage string, match func(cmdline string) bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), ScanTimeout)
	defer cancel()
	return RunningContext(ctx, windowsImage, match)
}

// RunningContext is Running with a caller-supplied context.
func RunningContext(ctx context.Context, windowsImage string, match func(cmdline string) bool) bool {
	if runtime.GOOS == "windows" {
		if windowsImage == "" {
			return false
		}
		// tasklist always exits 0; findstr verifies a real match.
		query := `tasklist /FI "IMAGENAME eq ` + windowsImage + `" /NH 2>nul | findstr /I "` + windowsImage + `"`
		return exec.CommandContext(ctx, "cmd", "/C", query).Run() == nil
	}
	if match == nil {
		return false
	}
	// `ps -Ao args=` is POSIX and prints the full command line of every process
	// on both macOS and Linux.
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "args=").Output()
	if err != nil {
		return false
	}
	return Scan(out, match)
}

// Scan reports whether any line of a process listing satisfies match.
func Scan(psOutput []byte, match func(cmdline string) bool) bool {
	if match == nil {
		return false
	}
	for _, line := range bytes.Split(psOutput, []byte("\n")) {
		if match(string(line)) {
			return true
		}
	}
	return false
}
