package api

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// kimiCodeScanTimeout bounds the process listing used by IsKimiCodeRunning so a
// wedged `ps` can never stall a poll cycle (same guard as the Claude Code scan
// in internal/agent/anthropic_cc_detect.go).
const kimiCodeScanTimeout = 5 * time.Second

// isKimiCodeCommandLine reports whether a full process command line belongs to
// the Kimi Code CLI.
//
// The match is deliberately narrow. A false positive makes onWatch adopt a
// possibly stale disk token and skip refresh, which stalls the quota card until
// the CLI writes again; a false negative is worse, because refreshing rotates
// the refresh token out from under a live CLI session and logs it out.
func isKimiCodeCommandLine(cmdline string) bool {
	line := strings.TrimSpace(cmdline)
	if line == "" {
		return false
	}

	// Native install: the launcher lives under the CLI's own home directory,
	// so the path alone identifies it regardless of the executable name.
	if strings.Contains(line, "/.kimi-code/bin/") {
		return true
	}

	// Electron/desktop helpers never belong to the CLI.
	if strings.Contains(line, ".app/Contents/") || strings.Contains(line, "--type=") {
		return false
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	if runtime.GOOS == "windows" {
		base = strings.TrimSuffix(base, ".exe")
	}
	// The TUI reports its process name as "kimi-code" even though the packaged
	// binary is named "kimi"; a bare "kimi" elsewhere on PATH is not enough.
	return base == "kimi-code"
}

// scanForKimiCode reports whether any line of a process listing is a Kimi Code
// CLI process.
func scanForKimiCode(psOutput []byte) bool {
	for _, line := range bytes.Split(psOutput, []byte("\n")) {
		if isKimiCodeCommandLine(string(line)) {
			return true
		}
	}
	return false
}

// IsKimiCodeRunning reports whether a Kimi Code CLI process is currently
// executing.
//
// When the CLI is alive it owns the OAuth refresh-token chain. onWatch then
// adopts the access token the CLI already wrote to ~/.kimi-code/credentials and
// must not call POST /api/oauth/token: rotation would invalidate the live
// session, the same failure mode that made onWatch skip refresh while Claude
// Code is running.
//
// Exported as a package-level variable so tests can override it.
var IsKimiCodeRunning = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), kimiCodeScanTimeout)
	defer cancel()

	if runtime.GOOS == "windows" {
		// tasklist always exits 0; findstr verifies a real match.
		cmd := exec.CommandContext(ctx, "cmd", "/C", `tasklist /FI "IMAGENAME eq kimi-code.exe" /NH 2>nul | findstr /I "kimi-code.exe"`)
		return cmd.Run() == nil
	}

	// `ps -Ao args=` is POSIX and prints the full command line of every process
	// on both macOS and Linux.
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "args=").Output()
	if err != nil {
		// An unusable process listing means "not running": refreshing is the
		// behaviour onWatch had before this guard existed.
		return false
	}
	return scanForKimiCode(out)
}
