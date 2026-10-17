package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Windows auto-start is a per-user Task Scheduler task, the counterpart of the
// macOS launchd agent: it starts at logon and needs no administrator rights.
//
// Task Scheduler's own restart-on-failure only covers a task that fails to
// launch, not a process that dies later, so the task runs `onwatch service
// run`: a small supervisor that keeps the daemon running and restarts it after
// a crash. A daemon exiting with code 0 was stopped on purpose (`onwatch stop`
// or a manual takeover) and stays stopped - the same contract as launchd's
// KeepAlive {SuccessfulExit: false}.

// TaskName is the Task Scheduler task registered for onWatch.
const TaskName = "onWatch"

// EnvSupervised marks a daemon started by the Windows supervisor. Like
// _ONWATCH_LAUNCHD it means "run in the foreground: something else owns the
// lifecycle".
const EnvSupervised = "_ONWATCH_SUPERVISED"

// Indirection points for tests: which platform's service manager to drive,
// who the task runs as, where the task definition is staged, and whether a
// headless console host is available.
var (
	goos            = runtimeGOOS
	currentUserName = func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		return u.Username, nil
	}
	tempDir         = os.TempDir
	headlessConsole = headlessConsoleHost
)

// UnderSupervisor reports whether a service manager - launchd, or the Windows
// supervisor - owns this process, so it must neither fork nor background.
func UnderSupervisor() bool {
	return UnderLaunchd() || os.Getenv(EnvSupervised) == "1"
}

// Location names the installed auto-start entry for status output: the plist
// path on macOS, the task path on Windows.
func Location() (string, error) {
	if goos == "windows" {
		return `\` + TaskName, nil
	}
	return PlistPath()
}

// StopSupervisor ends the Windows supervisor so `onwatch stop` is not undone
// by a restart. launchd needs nothing: it leaves a cleanly exited job alone.
func StopSupervisor() error {
	if goos != "windows" || !taskInstalled() {
		return nil
	}
	return runCommand("schtasks", "/End", "/TN", TaskName)
}

// RenderTaskXML builds the Task Scheduler definition. consoleHost, when set,
// is conhost.exe: run with --headless it gives the supervisor a console
// without ever showing a window at logon.
func RenderTaskXML(o Options, userID, consoleHost string) string {
	command := o.BinPath
	args := "service run"
	if consoleHost != "" {
		command = consoleHost
		args = fmt.Sprintf(`--headless "%s" service run`, o.BinPath)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n")
	b.WriteString("    <Description>Starts onWatch at logon and restarts it if it crashes.</Description>\n")
	b.WriteString("  </RegistrationInfo>\n")
	b.WriteString("  <Triggers>\n    <LogonTrigger>\n      <Enabled>true</Enabled>\n")
	b.WriteString("      <UserId>" + escapeXML(userID) + "</UserId>\n")
	b.WriteString("    </LogonTrigger>\n  </Triggers>\n")
	b.WriteString("  <Principals>\n    <Principal id=\"Author\">\n")
	b.WriteString("      <UserId>" + escapeXML(userID) + "</UserId>\n")
	b.WriteString("      <LogonType>InteractiveToken</LogonType>\n")
	b.WriteString("      <RunLevel>LeastPrivilege</RunLevel>\n")
	b.WriteString("    </Principal>\n  </Principals>\n")
	b.WriteString("  <Settings>\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <AllowHardTerminate>true</AllowHardTerminate>\n")
	b.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	b.WriteString("    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>\n")
	b.WriteString("    <IdleSettings>\n      <StopOnIdleEnd>false</StopOnIdleEnd>\n      <RestartOnIdle>false</RestartOnIdle>\n    </IdleSettings>\n")
	b.WriteString("    <AllowStartOnDemand>true</AllowStartOnDemand>\n")
	b.WriteString("    <Enabled>true</Enabled>\n")
	b.WriteString("    <Hidden>false</Hidden>\n")
	b.WriteString("    <RunOnlyIfIdle>false</RunOnlyIfIdle>\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	b.WriteString("    <Priority>7</Priority>\n")
	b.WriteString("  </Settings>\n")
	b.WriteString("  <Actions Context=\"Author\">\n    <Exec>\n")
	b.WriteString("      <Command>" + escapeXML(command) + "</Command>\n")
	b.WriteString("      <Arguments>" + escapeXML(args) + "</Arguments>\n")
	if o.WorkDir != "" {
		b.WriteString("      <WorkingDirectory>" + escapeXML(o.WorkDir) + "</WorkingDirectory>\n")
	}
	b.WriteString("    </Exec>\n  </Actions>\n</Task>\n")
	return b.String()
}

// installTask registers (or replaces) the task and starts the supervisor now,
// so auto-start takes effect without waiting for the next logon.
func installTask(o Options) (string, error) {
	userID, err := currentUserName()
	if err != nil {
		return "", fmt.Errorf("service: cannot determine the current user: %w", err)
	}
	for _, dir := range []string{o.WorkDir, filepath.Dir(o.LogPath)} {
		if dir == "" || dir == "." {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("service: create %s: %w", dir, err)
		}
	}
	host, _ := headlessConsole()
	xmlPath := filepath.Join(tempDir(), fmt.Sprintf("onwatch-task-%d.xml", os.Getpid()))
	if err := writeUTF16File(xmlPath, RenderTaskXML(o, userID, host)); err != nil {
		return "", fmt.Errorf("service: write task definition: %w", err)
	}
	defer os.Remove(xmlPath)
	if err := runCommand("schtasks", "/Create", "/TN", TaskName, "/XML", xmlPath, "/F"); err != nil {
		return "", fmt.Errorf("service: schtasks /Create: %w", err)
	}
	if err := runCommand("schtasks", "/Run", "/TN", TaskName); err != nil {
		return "", fmt.Errorf("service: schtasks /Run: %w", err)
	}
	return `\` + TaskName, nil
}

// uninstallTask ends the supervisor and removes the task. The daemon keeps
// running, exactly like a manually started one.
func uninstallTask() error {
	_ = runCommand("schtasks", "/End", "/TN", TaskName)
	if err := runCommand("schtasks", "/Delete", "/TN", TaskName, "/F"); err != nil && taskInstalled() {
		return fmt.Errorf("service: schtasks /Delete: %w", err)
	}
	return nil
}

// restartTask replaces the running supervisor with a fresh one, which starts
// the (possibly just updated) binary and takes over from the old daemon.
func restartTask() error {
	_ = runCommand("schtasks", "/End", "/TN", TaskName)
	if err := runCommand("schtasks", "/Run", "/TN", TaskName); err != nil {
		return fmt.Errorf("service: schtasks /Run: %w", err)
	}
	return nil
}

func taskInstalled() bool {
	return runCommand("schtasks", "/Query", "/TN", TaskName) == nil
}

// writeUTF16File writes s as UTF-16LE with a byte order mark, the encoding
// schtasks /XML reliably accepts.
func writeUTF16File(path, s string) error {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 2, 2+2*len(units))
	buf[0], buf[1] = 0xff, 0xfe
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return os.WriteFile(path, buf, 0o600)
}
