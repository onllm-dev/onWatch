package api

import (
	"os/exec"
	"runtime"
)

// IsKimiCodeRunning reports whether a Kimi Code CLI process is currently executing.
//
// When the CLI is alive it owns the OAuth refresh-token chain. onWatch then
// adopts the access token the CLI already wrote to ~/.kimi-code/credentials
// and must not call POST /api/oauth/token: rotation would invalidate the live
// session (the same reason Anthropic skips refresh while Claude Code is running).
//
// Exported as a package-level variable so tests can override it.
var IsKimiCodeRunning = func() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		// TUI process comm is "kimi-code" even though the binary is named "kimi".
		if exec.Command("pgrep", "-x", "kimi-code").Run() == nil {
			return true
		}
		// Fallback: native install path appears on the command line.
		if exec.Command("pgrep", "-f", "/.kimi-code/bin/kimi").Run() == nil {
			return true
		}
		return false
	case "windows":
		// tasklist always exits 0; findstr verifies a real match.
		if exec.Command("cmd", "/C", `tasklist /FI "IMAGENAME eq kimi-code.exe" /NH 2>nul | findstr /I "kimi-code.exe"`).Run() == nil {
			return true
		}
		return false
	default:
		return false
	}
}
