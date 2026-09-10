package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/service"
)

// TestMain runs before all tests in the main package. It unsets
// OPENCODE_HOME/XDG_DATA_HOME so the interactive setup flow's codex credential
// auto-detection never resolves to the host's real
// ~/.local/share/opencode/auth.json. Setup tests set a temp HOME and drive the
// prompts with fixed input; reading a real OpenCode ChatGPT login would shift
// those input sequences and make the tests environment-dependent (flaky).
func TestMain(m *testing.M) {
	os.Unsetenv("OPENCODE_HOME")
	os.Unsetenv("XDG_DATA_HOME")

	// GitHub Actions runs jobs under systemd, so INVOCATION_ID is set on the
	// runner and update.IsSystemd() reports true there but not on a developer
	// machine. Clear it so the restart-path tests see the same world in both
	// places; the systemd test opts back in with t.Setenv.
	os.Unsetenv("INVOCATION_ID")

	// Auto-start and daemon-spawn safety net: no test may shell out to
	// launchctl, touch the developer's real LaunchAgents directory, or start a
	// real onWatch daemon (which would poll live provider APIs). Tests that
	// exercise these paths override the stubs themselves.
	autostartSupported = func() bool { return false }
	autostartInstalled = func() bool { return false }
	autostartLoaded = func() bool { return false }
	autostartInstall = func(service.Options) (string, error) { return "", nil }
	autostartUninstall = func() error { return nil }
	autostartRestart = func() error { return nil }
	startDaemonProcess = func(string) (int, error) { return 0, nil }
	stdinIsTerminal = func() bool { return false }
	systemctlRestart = func() error { return nil }
	inContainer = func() bool { return false }
	// Setup wizard key verification must never hit ollama.com from tests.
	verifyOllamaKey = func(string) (string, error) { return "free", nil }
	// Muse key verification must never hit api.meta.ai from tests.
	verifyMuseKey = func(string, string) (string, error) { return "5h 1.0% used / weekly 2.0% used", nil }

	os.Exit(m.Run())
}

// This runs during package initialisation rather than from TestMain. A
// full-suite run leaves behind a spawned test binary that reaches run() without
// TestMain ever executing in it (proven by instrumenting TestMain: the stray's
// PID never appears while every other spawned binary does). Package init runs
// during Go runtime startup, before the test harness, so it still covers that
// process.
func init() {
	isolateSpawnedDaemonChild()
}

// isolateSpawnedDaemonChild protects the developer's running onWatch from the
// test suite.
//
// Several tests exercise daemonize() by re-executing this test binary; the
// child inherits _ONWATCH_DAEMON=1, which tells run() it is the daemon child
// and must serve. A full-suite run has been observed leaving such a child alive
// as the REAL daemon - production ~/.onwatch/.env, port 9211, the production
// SQLite database, and its own menubar companion - after SIGTERMing the user's
// actual onWatch instance.
//
// Rather than depend on every spawn site remembering to sandbox its child, pin
// any inherited-daemon-child test binary to a throwaway HOME, database, and
// port. A child that reaches run() then serves a scratch instance instead of
// hijacking the user's.
func isolateSpawnedDaemonChild() {
	if os.Getenv("_ONWATCH_DAEMON") != "1" {
		return
	}
	// A spawn site that already sandboxed its child is left alone.
	if os.Getenv("ONWATCH_DB_PATH") != "" {
		return
	}

	dir, err := os.MkdirTemp("", "onwatch-daemon-child")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Join(dir, ".onwatch", "data"), 0o755)
	os.Setenv("HOME", dir)
	os.Setenv("ONWATCH_DB_PATH", filepath.Join(dir, "onwatch.db"))
	if port, err := freePort(); err == nil {
		os.Setenv("ONWATCH_PORT", fmt.Sprintf("%d", port))
	}
	pidDir = dir
	pidFile = filepath.Join(dir, "onwatch.pid")
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
