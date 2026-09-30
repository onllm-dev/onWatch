package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/service"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
	"github.com/onllm-dev/onwatch/v2/internal/update"
)

// TestMain runs before all tests in the main package. It enables api test
// mode and sandboxes the home directory, clearing provider location overrides
// such as OPENCODE_HOME/XDG_DATA_HOME, so the interactive setup flow's
// credential auto-detection never resolves to the host's real files (e.g.
// ~/.local/share/opencode/auth.json). Setup tests set a temp HOME and drive
// the prompts with fixed input; reading a real login would shift those input
// sequences and make the tests environment-dependent (flaky).
func TestMain(m *testing.M) {
	// SetTestMode must run before sandboxTestHome redirects HOME/USERPROFILE:
	// its first enable records the real home, which the credential-file guard
	// then refuses, and it keeps every keychain/keyring operation off.
	api.SetTestMode(true)

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
	// Muse credential detection must never read the developer's keychain or
	// login file: on a machine with real credentials the setup wizard takes
	// its auto-detected branch, asks one question instead of two, and the
	// scripted prompt input in the wizard tests desyncs from there on.
	detectMuseCredentialsFunc = func(*slog.Logger) *api.MuseCredentials { return nil }

	// `onwatch update` tests must never reach GitHub: with a real updater a
	// test that sets an old version downloads the latest release and replaces
	// the running test binary, after which every os.Args[0] helper spawn
	// launches a real onWatch. Tests that need other answers stub their own.
	newCLIUpdater = func(v string, _ *slog.Logger) cliUpdater { return offlineCLIUpdater{version: v} }

	// Setup tests must never star the repo through a developer's logged-in
	// gh CLI; the star tests opt back in with t.Setenv.
	os.Setenv("ONWATCH_STAR", "no")

	cleanupHome := sandboxTestHome()

	code := m.Run()
	cleanupHome()
	os.Exit(code)
}

// offlineCLIUpdater is the network-free default updater for tests. A dev
// build is always current, matching the real updater; any other version
// reports a failed check, which the update tests accept as the offline result.
type offlineCLIUpdater struct{ version string }

func (o offlineCLIUpdater) Check() (update.UpdateInfo, error) {
	if o.version == "dev" || o.version == "" {
		return update.UpdateInfo{CurrentVersion: o.version, LatestVersion: o.version}, nil
	}
	return update.UpdateInfo{}, errors.New("network access is disabled in tests")
}

func (o offlineCLIUpdater) Apply() error {
	return errors.New("network access is disabled in tests")
}

// testScratchHomeEnv marks a process tree whose home is already sandboxed.
const testScratchHomeEnv = "_ONWATCH_TEST_SCRATCH_HOME"

// sandboxTestHome is the home-directory safety net: it points the whole test
// process at a scratch home (testhome.SandboxHome: HOME, USERPROFILE and
// LOCALAPPDATA, with provider location overrides cleared) so a test that
// forgets to set its own never reads or writes the developer's (or CI
// runner's) real ~/.onwatch, ~/.codex and so on.
//
// Helper subprocesses re-run TestMain. They inherit the marker and keep the
// home and environment their parent test gave them (itself a sandbox), so a
// test that seeds a fixture home for a child still has it seen. Returns the
// cleanup for the directory this process created (a no-op when it inherited
// one).
func sandboxTestHome() func() {
	if os.Getenv(testScratchHomeEnv) != "" {
		return func() {}
	}
	dir, cleanup, err := testhome.SandboxHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create scratch home: %v\n", err)
		os.Exit(1)
	}
	os.Setenv(testScratchHomeEnv, dir)
	// pidDir was resolved at package init from the real home. Re-resolve it
	// so runStop/runStatus never read (or stop) a real daemon or menubar
	// companion. initialPIDFilePath keeps the real path for the off-limits
	// guard.
	pidDir = defaultPIDDir()
	pidFile = filepath.Join(pidDir, "onwatch.pid")
	return cleanup
}

// assertPerm checks a file's Unix permission bits. Windows has no such bits:
// os.Chmod only toggles the read-only attribute and Stat reports a writable
// file as 0666, so access there is governed by the profile directory's ACL
// and the check is Unix-only.
func assertPerm(t testing.TB, info os.FileInfo, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions = %o, want %o", info.Name(), got, want)
	}
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
	os.Setenv("USERPROFILE", dir)
	os.Setenv("LOCALAPPDATA", dir)
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

// TestMain must enable api test mode so no cmd test can reach the keychain,
// keyring or the real Claude credentials file.
func TestTestMainEnablesAPITestMode(t *testing.T) {
	if !api.IsTestMode() {
		t.Fatal("TestMain must call api.SetTestMode(true)")
	}
}
