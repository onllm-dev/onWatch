package api

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/procscan"
)

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

// IsKimiCodeRunning reports whether a Kimi Code CLI process is currently
// executing.
//
// When the CLI is alive it owns the OAuth refresh-token chain. onWatch then
// adopts the access token the CLI already wrote to ~/.kimi-code/credentials and
// must not call POST /api/oauth/token: rotation would invalidate the live
// session, the same failure mode that made onWatch skip refresh while Claude
// Code is running.
//
// Exported as a package-level variable so tests can override it. The context
// bounds the scan: a poll that is cancelled must not leave a wedged `ps`
// holding its goroutine open.
var IsKimiCodeRunning = func(ctx context.Context) bool {
	return procscan.RunningContext(ctx, "kimi-code.exe", isKimiCodeCommandLine)
}
