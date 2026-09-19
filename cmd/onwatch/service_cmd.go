package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/service"
	"github.com/onllm-dev/onwatch/v2/internal/update"
)

// Auto-start (launchd) plumbing, indirected so tests never shell out to
// launchctl and never touch the user's real LaunchAgents directory.
var (
	autostartInstall   = service.Install
	autostartUninstall = service.Uninstall
	autostartRestart   = service.Restart
	autostartInstalled = service.IsInstalled
	autostartLoaded    = service.Loaded
	autostartOptions   = service.DefaultOptions
	autostartSupported = service.Supported
)

// autostartDeclinedFile marks that the user said no to auto-start, so setup and
// update stop asking. Deleting it (or running `onwatch service install`)
// re-enables the offer.
func autostartDeclinedFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".onwatch", ".autostart-declined")
}

func autostartDeclined() bool {
	path := autostartDeclinedFile()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func recordAutostartDecline() {
	path := autostartDeclinedFile()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("onwatch service install\n"), 0o644)
}

func clearAutostartDecline() {
	if path := autostartDeclinedFile(); path != "" {
		_ = os.Remove(path)
	}
}

// stdinIsTerminal reports whether prompts can be answered. Piped installs and
// cron runs must never block - or, worse, take a default - on input nobody is
// there to give. A ModeCharDevice check is not enough: /dev/null is a character
// device too, so `onwatch update < /dev/null` would look interactive.
var stdinIsTerminal = realStdinIsTerminal

func realStdinIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// offerAutostart asks - once - whether onWatch should start itself at login.
// It is a no-op unless the platform supports it, the agent is missing, the user
// has not already declined, and there is a terminal to answer the question.
func offerAutostart(reader *bufio.Reader) {
	if !autostartSupported() || autostartInstalled() || autostartDeclined() {
		return
	}
	if !stdinIsTerminal() {
		fmt.Println()
		fmt.Println("  onWatch will not start automatically after a reboot.")
		fmt.Println("  Enable it with: onwatch service install")
		return
	}

	fmt.Println()
	fmt.Println("  onWatch does not start automatically after a reboot or logout.")
	fmt.Print("  Start onWatch automatically at login? (Y/n): ")
	answer, err := reader.ReadString('\n')
	if err != nil && answer == "" {
		// Stdin closed before answering - treat silence as "not now" rather
		// than installing a login item nobody asked for.
		fmt.Println()
		fmt.Println("  No answer - skipped. Enable it later with: onwatch service install")
		return
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		// Accept - a bare Enter takes the (Y/n) default.
	case "n", "no":
		recordAutostartDecline()
		fmt.Println("  Skipped. Enable it later with: onwatch service install")
		return
	default:
		// Unrecognised input is not consent for a login item.
		fmt.Println("  Not recognised - skipped. Enable it later with: onwatch service install")
		return
	}
	if err := installAutostart(); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not enable auto-start: %v\n", err)
	}
}

// installAutostart writes and loads the launchd agent, printing what it did.
func installAutostart() error {
	opts, err := autostartOptions()
	if err != nil {
		return err
	}
	path, err := autostartInstall(opts)
	if err != nil {
		return err
	}
	clearAutostartDecline()
	fmt.Printf("  Auto-start enabled: %s\n", path)
	fmt.Printf("  Runs: %s\n", opts.BinPath)
	fmt.Println("  Disable with: onwatch service uninstall")
	return nil
}

func runService() error {
	action := serviceAction()

	if !autostartSupported() {
		switch action {
		case "status":
			fmt.Println("Auto-start management is macOS-only.")
			fmt.Println("On Linux, install.sh creates a systemd unit: systemctl --user status onwatch")
			return nil
		default:
			return fmt.Errorf("onwatch service: auto-start management is macOS-only (Linux uses systemd)")
		}
	}

	switch action {
	case "install", "enable":
		return installAutostart()
	case "uninstall", "disable", "remove":
		// launchctl bootout stops the job as well as unloading it, so a
		// launchd-owned daemon goes down with the agent. Say so, and offer the
		// one command that brings it back.
		wasRunning := autostartLoaded()
		if err := autostartUninstall(); err != nil {
			return err
		}
		recordAutostartDecline()
		fmt.Println("Auto-start disabled. onWatch will no longer start at login.")
		if wasRunning {
			fmt.Println("The launchd-managed instance was stopped with it - restart onWatch with: onwatch")
		}
		return nil
	case "status":
		printServiceStatus()
		return nil
	default:
		printServiceHelp()
		return nil
	}
}

// serviceAction returns the word following the `service` subcommand.
func serviceAction() string {
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "service" || arg == "--service" || arg == "autostart" {
			if i+1 < len(args) {
				return strings.ToLower(strings.TrimPrefix(args[i+1], "--"))
			}
			return ""
		}
	}
	return ""
}

func printServiceStatus() {
	if !autostartInstalled() {
		fmt.Println("Auto-start: not installed")
		fmt.Println("  onWatch will NOT come back after a reboot or logout.")
		fmt.Println("  Enable with: onwatch service install")
		return
	}
	path, _ := service.PlistPath()
	fmt.Println("Auto-start: installed")
	fmt.Printf("  Agent:  %s\n", service.Label)
	fmt.Printf("  Plist:  %s\n", path)
	if autostartLoaded() {
		fmt.Println("  Loaded: yes (launchd will restart onWatch at login and after a crash)")
	} else {
		fmt.Println("  Loaded: no (run `onwatch service install` to reload it)")
	}
}

func printServiceHelp() {
	fmt.Println("Usage: onwatch service <install|uninstall|status>")
	fmt.Println()
	fmt.Println("  install     Start onWatch automatically at login (launchd agent)")
	fmt.Println("  uninstall   Remove the launchd agent")
	fmt.Println("  status      Show whether the launchd agent is installed and loaded")
}

// --- post-update restart -------------------------------------------------

// restartArgs carries the user's configuration flags into the restarted daemon
// so `onwatch update --port 8080` does not come back on the default port. The
// update verb itself is dropped, and so are the foreground flags: the restart
// must end in a background daemon, not a process tied to this terminal.
func restartArgs(args []string) []string {
	var out []string
	for _, a := range args {
		switch a {
		case "update", "--update", "--debug", "--debugstdout":
			continue
		}
		out = append(out, a)
	}
	return out
}

// daemonEnv strips the markers that tell a process it is already the daemon.
// Inheriting either one would stop the new process from backgrounding itself.
func daemonEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "_ONWATCH_DAEMON=") || strings.HasPrefix(kv, "_ONWATCH_LAUNCHD=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// startDaemonProcess launches the (already updated) binary as a fresh daemon.
// Overridden in tests - `go test` must never spawn a real onWatch.
var startDaemonProcess = func(exePath string) (int, error) {
	cmd := exec.Command(exePath, restartArgs(os.Args[1:])...)
	cmd.Env = daemonEnv(os.Environ())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	// The launcher forks the daemon and exits; wait for it so its output does
	// not race the shell prompt, but never block forever on it.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return 0, err
		}
	case <-time.After(30 * time.Second):
	}

	return daemonPIDFromFile(), nil
}

// parsePIDContent handles both the "PID:PORT" and legacy "PID" file formats.
func parsePIDContent(content string) int {
	content = strings.TrimSpace(content)
	if idx := strings.Index(content, ":"); idx >= 0 {
		content = content[:idx]
	}
	pid, _ := strconv.Atoi(content)
	return pid
}

func daemonPIDFromFile() int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	return parsePIDContent(string(data))
}

// runningDaemonPID returns the PID of a live daemon recorded in the PID file.
// A stale PID file (process gone after a reboot or crash) reports not running.
func runningDaemonPID() (int, bool) {
	pid := daemonPIDFromFile()
	if pid <= 0 || pid == os.Getpid() {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	// The PID file can name a PID the OS has since recycled onto an unrelated
	// process; the update path would otherwise SIGTERM it and then wait on it.
	if !isOnwatchProcess(pid) {
		return 0, false
	}
	return pid, true
}

// waitForExit blocks until the process is gone or the deadline passes.
func waitForExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// systemctlRestart asks systemd to restart the detected onWatch unit.
var systemctlRestart = func() error {
	return exec.Command("systemctl", "restart", update.DetectServiceName()).Run()
}

// inContainer reports whether this process runs inside Docker or Kubernetes.
var inContainer = func() bool {
	return (&config.Config{}).IsDockerEnvironment()
}

// restartAfterUpdate leaves onWatch running the new binary, whether or not it
// was running before. An update that silently leaves the daemon stopped is the
// bug this exists to prevent.
func restartAfterUpdate() {
	// When launchd owns the process, let it do the restart so the job stays
	// supervised and keeps its auto-start behaviour.
	if autostartSupported() && autostartInstalled() && autostartLoaded() {
		fmt.Println("Restarting via launchd...")
		if err := autostartRestart(); err == nil {
			fmt.Printf("onWatch restarted (launchd agent %s)\n", service.Label)
			return
		}
		fmt.Fprintln(os.Stderr, "Warning: launchctl kickstart failed, falling back to a direct restart")
	}

	// systemd owns the lifecycle on Linux: spawning here would create an
	// unsupervised daemon beside the unit, and would restart a service the
	// operator had deliberately stopped.
	if update.IsSystemd() {
		fmt.Println("Running under systemd - restarting the service...")
		if err := systemctlRestart(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: systemctl restart failed: %v\n", err)
			fmt.Println("Restart manually with: systemctl restart onwatch")
		}
		return
	}

	// In a container onWatch is PID 1 and runs in the foreground; there is
	// nothing to background and no PID file to read. Restarting the container
	// is the operator's call.
	if inContainer() {
		fmt.Println("Running in a container - restart the container to pick up the new binary.")
		return
	}

	if pid, running := runningDaemonPID(); running {
		fmt.Println("Restarting daemon...")
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
			waitForExit(pid, 5*time.Second)
		}
	} else {
		fmt.Println("onWatch was not running - starting the updated daemon...")
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not locate the updated binary: %v\n", err)
		fmt.Println("Please start onwatch manually.")
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	newPID, err := startDaemonProcess(exePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: restart failed: %v\n", err)
		fmt.Println("Please start onwatch manually.")
		return
	}
	if newPID > 0 {
		fmt.Printf("onWatch is running (PID %d)\n", newPID)
	}
}
