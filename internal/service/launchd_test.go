package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records launchctl invocations instead of executing them.
type fakeRunner struct {
	calls [][]string
	err   map[string]error
}

func (f *fakeRunner) run(name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.err != nil {
		if err, ok := f.err[strings.Join(args, " ")]; ok {
			return err
		}
	}
	return nil
}

func (f *fakeRunner) joined() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func installFake(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{}
	prev := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = prev })
	return f
}

func withHome(t *testing.T, home string) {
	t.Helper()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
}

func TestPlistPathUsesLaunchAgents(t *testing.T) {
	withHome(t, "/Users/tester")

	got, err := PlistPath()
	if err != nil {
		t.Fatalf("PlistPath: %v", err)
	}
	// PlistPath joins with the host separator; the path is only used on
	// macOS, but the helper must stay correct when the suite runs elsewhere.
	want := filepath.Join("/Users/tester", "Library", "LaunchAgents", "dev.onllm.onwatch.plist")
	if got != want {
		t.Errorf("PlistPath = %q, want %q", got, want)
	}
}

func TestRenderPlistContainsRequiredKeys(t *testing.T) {
	out := RenderPlist(Options{
		BinPath: "/Users/tester/.onwatch/bin/onwatch",
		WorkDir: "/Users/tester/.onwatch",
		LogPath: "/Users/tester/.onwatch/data/.onwatch.log",
		Home:    "/Users/tester",
	})

	required := []string{
		"<key>Label</key>",
		"<string>" + Label + "</string>",
		"<string>/Users/tester/.onwatch/bin/onwatch</string>",
		"<key>WorkingDirectory</key>",
		"<string>/Users/tester/.onwatch</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<false/>",
		"<key>StandardOutPath</key>",
		"<string>/Users/tester/.onwatch/data/.onwatch.log</string>",
		"<key>" + envLaunchd + "</key>",
		"<key>HOME</key>",
		"<key>ProcessType</key>",
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
}

// KeepAlive must be SuccessfulExit=false so a clean `onwatch stop` is not
// undone by launchd immediately relaunching the agent.
func TestRenderPlistKeepAliveDoesNotFightStop(t *testing.T) {
	out := RenderPlist(Options{BinPath: "/bin/onwatch", WorkDir: "/tmp", LogPath: "/tmp/x.log", Home: "/tmp"})
	if strings.Contains(out, "<key>KeepAlive</key>\n\t<true/>") {
		t.Error("KeepAlive must not be unconditional true - it would restart after `onwatch stop`")
	}
}

func TestRenderPlistEscapesXML(t *testing.T) {
	out := RenderPlist(Options{
		BinPath: "/Users/a&b/bin/onwatch",
		WorkDir: "/Users/a&b",
		LogPath: "/Users/a&b/log",
		Home:    "/Users/a&b",
	})
	if strings.Contains(out, "a&b<") || strings.Contains(out, ">/Users/a&b") {
		t.Errorf("raw & not escaped in plist:\n%s", out)
	}
	if !strings.Contains(out, "a&amp;b") {
		t.Errorf("expected escaped &amp; in plist:\n%s", out)
	}
}

func TestInstallWritesPlistAndBootstraps(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	f := installFake(t)

	path, err := Install(Options{
		BinPath: filepath.Join(home, ".onwatch", "bin", "onwatch"),
		WorkDir: filepath.Join(home, ".onwatch"),
		LogPath: filepath.Join(home, ".onwatch", "data", ".onwatch.log"),
		Home:    home,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !strings.Contains(string(data), Label) {
		t.Errorf("plist missing label:\n%s", data)
	}

	calls := f.joined()
	if len(calls) == 0 {
		t.Fatal("expected launchctl calls")
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"bootout", "bootstrap", "kickstart"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a launchctl %s call, got:\n%s", want, joined)
		}
	}
	// bootout of a job that is not loaded is expected and must not fail Install.
	if idx := strings.Index(joined, "bootstrap"); idx >= 0 && strings.Index(joined, "bootout") > idx {
		t.Error("bootout must run before bootstrap")
	}
	// enable must precede bootstrap - a previously disabled label cannot be
	// bootstrapped.
	if strings.Index(joined, "enable") > strings.Index(joined, "bootstrap") {
		t.Errorf("enable must run before bootstrap, got:\n%s", joined)
	}
}

func TestInstallSurvivesBootoutFailure(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	f := installFake(t)
	f.err = map[string]error{
		"bootout gui/" + currentUID() + "/" + Label: errors.New("no such process"),
	}

	if _, err := Install(Options{BinPath: "/bin/onwatch", WorkDir: home, LogPath: home + "/l.log", Home: home}); err != nil {
		t.Fatalf("Install must tolerate bootout failure: %v", err)
	}
}

func TestInstallFailsWhenBootstrapFails(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	f := installFake(t)
	f.err = map[string]error{
		"bootstrap gui/" + currentUID() + " " + filepath.Join(home, "Library", "LaunchAgents", Label+".plist"): errors.New("boom"),
	}

	if _, err := Install(Options{BinPath: "/bin/onwatch", WorkDir: home, LogPath: home + "/l.log", Home: home}); err == nil {
		t.Fatal("expected error when bootstrap fails")
	}
}

func TestIsInstalledReflectsPlistPresence(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	if IsInstalled() {
		t.Error("IsInstalled = true before any plist exists")
	}

	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Label+".plist"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsInstalled() {
		t.Error("IsInstalled = false after plist written")
	}
}

func TestUninstallRemovesPlistAndBootsOut(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	f := installFake(t)

	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(dir, Label+".plist")
	if err := os.WriteFile(plist, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Error("plist still present after Uninstall")
	}
	if !strings.Contains(strings.Join(f.joined(), "\n"), "bootout") {
		t.Errorf("expected bootout, got %v", f.joined())
	}
}

func TestUninstallIsIdempotent(t *testing.T) {
	withHome(t, t.TempDir())
	installFake(t)

	if err := Uninstall(); err != nil {
		t.Errorf("Uninstall on a clean system should be a no-op, got %v", err)
	}
}

func TestRestartUsesKickstart(t *testing.T) {
	withHome(t, t.TempDir())
	f := installFake(t)

	if err := Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := "launchctl kickstart -k gui/" + currentUID() + "/" + Label
	if got := strings.Join(f.joined(), "\n"); got != want {
		t.Errorf("Restart ran %q, want %q", got, want)
	}
}

func TestUnderLaunchdReadsEnv(t *testing.T) {
	t.Setenv(envLaunchd, "")
	if UnderLaunchd() {
		t.Error("UnderLaunchd = true with empty env")
	}
	t.Setenv(envLaunchd, "1")
	if !UnderLaunchd() {
		t.Error("UnderLaunchd = false with env set to 1")
	}
}

func TestDefaultOptionsPointsAtRealBinary(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	opts, err := DefaultOptions()
	if err != nil {
		t.Fatalf("DefaultOptions: %v", err)
	}
	exe, _ := os.Executable()
	if opts.BinPath == "" || opts.BinPath != resolveExe(exe) {
		t.Errorf("BinPath = %q, want executable path %q", opts.BinPath, resolveExe(exe))
	}
	if opts.WorkDir != filepath.Join(home, ".onwatch") {
		t.Errorf("WorkDir = %q", opts.WorkDir)
	}
	if opts.LogPath != filepath.Join(home, ".onwatch", "data", ".onwatch.log") {
		t.Errorf("LogPath = %q", opts.LogPath)
	}
	if opts.Home != home {
		t.Errorf("Home = %q, want %q", opts.Home, home)
	}
}

// The default runner must actually shell out to launchctl.
func TestDefaultRunCommandIsExec(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no /usr/bin/true")
	}
	if err := execRun("true"); err != nil {
		t.Errorf("execRun(true) = %v, want nil", err)
	}
	if err := execRun("false"); err == nil {
		t.Error("execRun(false) = nil, want error")
	}
}

// launchd will not create missing directories - a WorkingDirectory or log
// directory that does not exist makes the job fail to spawn, silently as far
// as the user is concerned.
func TestInstallCreatesDirectoriesLaunchdNeeds(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	installFake(t)

	workDir := filepath.Join(home, ".onwatch")
	logPath := filepath.Join(workDir, "data", ".onwatch.log")

	if _, err := Install(Options{
		BinPath: filepath.Join(workDir, "bin", "onwatch"),
		WorkDir: workDir,
		LogPath: logPath,
		Home:    home,
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, dir := range []string{workDir, filepath.Dir(logPath)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s was not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// A Homebrew install is invoked through /opt/homebrew/bin/onwatch, a symlink
// into a versioned Cellar directory. Recording the resolved Cellar path would
// leave launchd retrying a binary that `brew upgrade` deletes, so the stable
// symlink must be preserved.
func TestResolveExeKeepsStableSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "Cellar", "onwatch", "1.2.3", "bin", "onwatch")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "onwatch")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if got := resolveExe(link); got != link {
		t.Errorf("resolveExe(%q) = %q, want the symlink preserved", link, got)
	}
}

// The "(deleted)" suffix Linux reports after the self-updater swaps the binary
// must be stripped, and a path that no longer exists falls back to resolution.
func TestResolveExeStripsDeletedSuffix(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "onwatch")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := resolveExe(bin + " (deleted)"); got != bin {
		t.Errorf("resolveExe = %q, want %q", got, bin)
	}
}
