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
	return RunningContext(context.Background(), windowsImage, match)
}

// RunningContext is Running with a caller-supplied context. The scan is always
// bounded by ScanTimeout on top of whatever the caller supplies: agent contexts
// are cancel-only with no deadline, so passing one straight to exec would let a
// wedged `ps` hold a poll goroutine until daemon shutdown.
func RunningContext(ctx context.Context, windowsImage string, match func(cmdline string) bool) bool {
	ctx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	if runtime.GOOS == "windows" {
		// windowsImage is interpolated into a cmd /C string, so anything that
		// could break out of the quoted argument is refused outright rather
		// than escaped. Callers pass a plain executable name.
		if !validWindowsImage(windowsImage) {
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
	out, err := execCommandContext(ctx, "ps", "-Ao", "args=")
	if err != nil {
		return false
	}
	return Scan(out, match)
}

// validWindowsImage reports whether an image name is safe to interpolate into
// a cmd /C command string: a bare executable name, no path, quoting or shell
// metacharacters.
func validWindowsImage(name string) bool {
	if name == "" || len(name) > 260 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// execCommandContext runs the process listing. A variable so tests can assert
// the deadline the scan actually receives.
var execCommandContext = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
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
