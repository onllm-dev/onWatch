package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestMain pins the platform dispatch to macOS so the launchd tests exercise
// launchd on every host; the Task Scheduler tests switch to Windows explicitly.
func TestMain(m *testing.M) {
	goos = "darwin"
	os.Exit(m.Run())
}

func onWindows(t *testing.T) {
	t.Helper()
	prevGOOS, prevUser, prevHeadless, prevTemp := goos, currentUserName, headlessConsole, tempDir
	goos = "windows"
	currentUserName = func() (string, error) { return `DESKTOP-1\tester`, nil }
	headlessConsole = func() (string, bool) { return `C:\Windows\System32\conhost.exe`, true }
	dir := t.TempDir()
	tempDir = func() string { return dir }
	t.Cleanup(func() {
		goos, currentUserName, headlessConsole, tempDir = prevGOOS, prevUser, prevHeadless, prevTemp
	})
}

func winOptions() Options {
	return Options{
		BinPath: `C:\Users\tester\.onwatch\bin\onwatch.exe`,
		WorkDir: `C:\Users\tester\.onwatch`,
		LogPath: `C:\Users\tester\.onwatch\data\.onwatch.log`,
		Home:    `C:\Users\tester`,
	}
}

func TestSupportedOnMacOSAndWindows(t *testing.T) {
	for os, want := range map[string]bool{"darwin": true, "windows": true, "linux": false, "freebsd": false} {
		prev := goos
		goos = os
		if got := Supported(); got != want {
			t.Errorf("Supported() on %s = %v, want %v", os, got, want)
		}
		goos = prev
	}
}

func TestRenderTaskXMLStartsAtLogonAndNeverTimesOut(t *testing.T) {
	out := RenderTaskXML(winOptions(), `DESKTOP-1\tester`, `C:\Windows\System32\conhost.exe`)
	for _, want := range []string{
		`<LogonTrigger>`,
		`<UserId>DESKTOP-1\tester</UserId>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<RunLevel>LeastPrivilege</RunLevel>`,
		// The supervisor runs for the whole session; the 72h default would kill it.
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		// Laptops: the defaults refuse to start, or stop the task, on battery.
		`<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<Command>C:\Windows\System32\conhost.exe</Command>`,
		`<Arguments>--headless "C:\Users\tester\.onwatch\bin\onwatch.exe" service run</Arguments>`,
		`<WorkingDirectory>C:\Users\tester\.onwatch</WorkingDirectory>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task XML missing %q\n%s", want, out)
		}
	}
}

func TestRenderTaskXMLWithoutHeadlessConsoleRunsBinaryDirectly(t *testing.T) {
	out := RenderTaskXML(winOptions(), `DESKTOP-1\tester`, "")
	if !strings.Contains(out, `<Command>C:\Users\tester\.onwatch\bin\onwatch.exe</Command>`) ||
		!strings.Contains(out, `<Arguments>service run</Arguments>`) {
		t.Errorf("expected the binary as the command\n%s", out)
	}
}

func TestRenderTaskXMLEscapes(t *testing.T) {
	o := winOptions()
	o.BinPath = `C:\Users\R&D <lab>\onwatch.exe`
	out := RenderTaskXML(o, `CORP\r&d`, "")
	if strings.Contains(out, `R&D <lab>`) || !strings.Contains(out, `R&amp;D &lt;lab&gt;`) {
		t.Errorf("binary path not escaped\n%s", out)
	}
	if !strings.Contains(out, `<UserId>CORP\r&amp;d</UserId>`) {
		t.Errorf("user not escaped\n%s", out)
	}
}

func TestInstallOnWindowsRegistersAndStartsTask(t *testing.T) {
	onWindows(t)
	f := installFake(t)
	root := t.TempDir()
	o := winOptions()
	o.WorkDir = filepath.Join(root, ".onwatch")
	o.LogPath = filepath.Join(root, ".onwatch", "data", ".onwatch.log")

	got, err := Install(o)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got != `\onWatch` {
		t.Errorf("Install returned %q, want the task path", got)
	}
	calls := f.joined()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "schtasks /Create /TN onWatch /XML ") || !strings.HasSuffix(calls[0], " /F") {
		t.Fatalf("unexpected calls: %v", calls)
	}
	if calls[1] != "schtasks /Run /TN onWatch" {
		t.Errorf("task not started after install: %v", calls)
	}
	// The task starts in directories that must already exist.
	if _, err := os.Stat(filepath.Dir(o.LogPath)); err != nil {
		t.Errorf("log directory not created: %v", err)
	}
	// The definition is handed to schtasks as UTF-16 and removed afterwards.
	xmlPath := f.calls[0][5]
	if _, err := os.Stat(xmlPath); !os.IsNotExist(err) {
		t.Errorf("temporary task XML %s was left behind", xmlPath)
	}
}

func TestWriteTaskXMLIsUTF16WithBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.xml")
	if err := writeUTF16File(path, "<Task>é</Task>"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xfe {
		t.Fatalf("missing UTF-16LE BOM: % x", data[:2])
	}
	units := make([]uint16, 0, (len(data)-2)/2)
	for i := 2; i+1 < len(data); i += 2 {
		units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
	}
	if got := string(utf16.Decode(units)); got != "<Task>é</Task>" {
		t.Errorf("round trip = %q", got)
	}
}

func TestInstallOnWindowsFailsWhenCreateFails(t *testing.T) {
	onWindows(t)
	f := installFake(t)
	f.err = map[string]error{}
	runCommand = func(name string, args ...string) error {
		f.calls = append(f.calls, append([]string{name}, args...))
		if args[0] == "/Create" {
			return os.ErrPermission
		}
		return nil
	}
	o := winOptions()
	o.WorkDir, o.LogPath = "", ""
	if _, err := Install(o); err == nil {
		t.Fatal("expected Install to fail when schtasks /Create fails")
	}
	for _, c := range f.joined() {
		if strings.Contains(c, "/Run") {
			t.Errorf("task started although it was not registered: %v", f.joined())
		}
	}
}

func TestUninstallOnWindowsEndsAndDeletesTask(t *testing.T) {
	onWindows(t)
	f := installFake(t)
	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	want := []string{"schtasks /End /TN onWatch", "schtasks /Delete /TN onWatch /F"}
	if got := f.joined(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestUninstallOnWindowsIsIdempotent(t *testing.T) {
	onWindows(t)
	installFake(t)
	runCommand = func(name string, args ...string) error {
		if args[0] == "/Query" || args[0] == "/Delete" || args[0] == "/End" {
			return os.ErrNotExist
		}
		return nil
	}
	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall without a task should succeed, got %v", err)
	}
}

func TestRestartOnWindowsEndsThenRunsTask(t *testing.T) {
	onWindows(t)
	f := installFake(t)
	if err := Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := []string{"schtasks /End /TN onWatch", "schtasks /Run /TN onWatch"}
	if got := f.joined(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestIsInstalledAndLoadedOnWindowsQueryTask(t *testing.T) {
	onWindows(t)
	f := installFake(t)
	if !IsInstalled() || !Loaded() {
		t.Fatal("expected an existing task to report installed and loaded")
	}
	if c := f.joined(); len(c) == 0 || c[0] != "schtasks /Query /TN onWatch" {
		t.Errorf("calls = %v", c)
	}
	runCommand = func(string, ...string) error { return os.ErrNotExist }
	if IsInstalled() || Loaded() {
		t.Fatal("expected a missing task to report not installed")
	}
}

func TestStopSupervisorEndsTaskOnlyOnWindows(t *testing.T) {
	f := installFake(t)
	_ = StopSupervisor()
	if len(f.calls) != 0 {
		t.Fatalf("macOS must not touch Task Scheduler: %v", f.joined())
	}
	onWindows(t)
	_ = StopSupervisor()
	if got := f.joined(); len(got) != 2 || got[0] != "schtasks /Query /TN onWatch" || got[1] != "schtasks /End /TN onWatch" {
		t.Errorf("calls = %v", got)
	}
}

func TestLocationNamesPlistOrTask(t *testing.T) {
	withHome(t, "/Users/tester")
	if got, _ := Location(); !strings.HasSuffix(got, "dev.onllm.onwatch.plist") {
		t.Errorf("macOS Location = %q", got)
	}
	onWindows(t)
	if got, _ := Location(); got != `\onWatch` {
		t.Errorf("Windows Location = %q", got)
	}
}

func TestUnderSupervisorReadsEitherMarker(t *testing.T) {
	t.Setenv(envLaunchd, "")
	t.Setenv(EnvSupervised, "")
	if UnderSupervisor() {
		t.Fatal("no marker set")
	}
	t.Setenv(EnvSupervised, "1")
	if !UnderSupervisor() {
		t.Fatal("Task Scheduler supervisor marker not honoured")
	}
	t.Setenv(EnvSupervised, "")
	t.Setenv(envLaunchd, "1")
	if !UnderSupervisor() {
		t.Fatal("launchd marker not honoured")
	}
}
