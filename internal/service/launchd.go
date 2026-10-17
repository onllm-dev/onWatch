// Package service manages the OS-level auto-start integration for onWatch.
//
// On macOS this is a per-user launchd agent (~/Library/LaunchAgents), which is
// what makes onWatch come back after a reboot or logout. On Windows it is a
// per-user Task Scheduler task running a small supervisor (taskscheduler.go).
// Linux uses systemd units created by install.sh (see internal/update for unit
// migration), so Supported() reports false there.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const runtimeGOOS = runtime.GOOS

// Label is the launchd job label for the onWatch agent.
const Label = "dev.onllm.onwatch"

// envLaunchd is exported into the agent's environment by the plist so the
// running process can tell it was started by launchd rather than by hand.
const envLaunchd = "_ONWATCH_LAUNCHD"

// Indirection points, swapped in tests so no real launchctl call is made.
var (
	runCommand  = execRun
	userHomeDir = os.UserHomeDir
)

func execRun(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// Options describes the launchd job to be written.
type Options struct {
	BinPath string // absolute path to the onwatch binary launchd should run
	WorkDir string // working directory for the job (usually ~/.onwatch)
	LogPath string // stdout/stderr destination
	Home    string // HOME for the job's environment
}

// Supported reports whether auto-start is managed here on this platform.
func Supported() bool { return goos == "darwin" || goos == "windows" }

// UnderLaunchd reports whether this process was started by the launchd agent.
// The agent runs onWatch in the foreground - launchd owns the lifecycle, so the
// process must not fork itself into the background.
func UnderLaunchd() bool { return os.Getenv(envLaunchd) == "1" }

// PlistPath returns the location of the user's LaunchAgent plist.
func PlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("service: cannot determine home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// IsInstalled reports whether the LaunchAgent plist exists on disk, or on
// Windows whether the task is registered.
func IsInstalled() bool {
	if goos == "windows" {
		return taskInstalled()
	}
	path, err := PlistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// DefaultOptions derives the job description from the running binary and the
// standard install layout.
func DefaultOptions() (Options, error) {
	home, err := userHomeDir()
	if err != nil {
		return Options{}, fmt.Errorf("service: cannot determine home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return Options{}, fmt.Errorf("service: cannot determine executable path: %w", err)
	}
	installDir := filepath.Join(home, ".onwatch")
	return Options{
		BinPath: resolveExe(exe),
		WorkDir: installDir,
		LogPath: filepath.Join(installDir, "data", ".onwatch.log"),
		Home:    home,
	}, nil
}

// resolveExe picks the path the plist should record.
//
// It deliberately does NOT follow symlinks when the invoked path exists: a
// Homebrew install is reached through the stable /opt/homebrew/bin/onwatch
// symlink, and resolving it would bake a versioned Cellar path into the plist
// that `brew upgrade` deletes - leaving launchd retrying a missing binary
// forever. Symlinks are only resolved as a fallback, e.g. for the "(deleted)"
// path Linux reports after the self-updater replaces the binary.
func resolveExe(exe string) string {
	exe = strings.TrimSuffix(exe, " (deleted)")
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

func currentUID() string { return strconv.Itoa(os.Getuid()) }

func domain() string  { return "gui/" + currentUID() }
func service() string { return domain() + "/" + Label }

// RenderPlist builds the LaunchAgent property list.
//
// KeepAlive is deliberately {SuccessfulExit: false} rather than true: onWatch
// exits 0 on SIGTERM, so `onwatch stop` (and the takeover a manual `onwatch`
// run performs) stays stopped, while a crash still gets relaunched. RunAtLoad
// covers boot and login.
func RenderPlist(o Options) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	writeKeyString(&b, "Label", Label)

	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	b.WriteString("\t\t<string>" + escapeXML(o.BinPath) + "</string>\n")
	b.WriteString("\t</array>\n")

	writeKeyString(&b, "WorkingDirectory", o.WorkDir)

	b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	b.WriteString("\t\t<key>" + envLaunchd + "</key>\n\t\t<string>1</string>\n")
	b.WriteString("\t\t<key>HOME</key>\n\t\t<string>" + escapeXML(o.Home) + "</string>\n")
	b.WriteString("\t\t<key>PATH</key>\n\t\t<string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n")
	b.WriteString("\t</dict>\n")

	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	b.WriteString("\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	writeKeyString(&b, "StandardOutPath", o.LogPath)
	writeKeyString(&b, "StandardErrorPath", o.LogPath)
	writeKeyString(&b, "ProcessType", "Background")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func writeKeyString(b *strings.Builder, key, value string) {
	b.WriteString("\t<key>" + key + "</key>\n\t<string>" + escapeXML(value) + "</string>\n")
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// Install writes the LaunchAgent plist and loads it into the user's launchd
// domain. It returns the plist path. Safe to call when already installed - the
// job is booted out and re-bootstrapped so the new plist takes effect.
func Install(o Options) (string, error) {
	if goos == "windows" {
		return installTask(o)
	}
	path, err := PlistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("service: create LaunchAgents dir: %w", err)
	}

	// launchd does not create missing directories: a WorkingDirectory or a
	// StandardOutPath parent that does not exist makes the job fail to spawn
	// with no obvious error anywhere the user would look.
	if o.WorkDir != "" {
		if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
			return "", fmt.Errorf("service: create working directory %s: %w", o.WorkDir, err)
		}
	}
	if o.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(o.LogPath), 0o700); err != nil {
			return "", fmt.Errorf("service: create log directory %s: %w", filepath.Dir(o.LogPath), err)
		}
	}

	if err := os.WriteFile(path, []byte(RenderPlist(o)), 0o644); err != nil {
		return "", fmt.Errorf("service: write %s: %w", path, err)
	}

	// Not-loaded is the common case; its failure is expected and ignored.
	_ = runCommand("launchctl", "bootout", service())

	// enable must precede bootstrap: bootstrapping a label that a previous
	// `launchctl disable` left disabled fails outright.
	_ = runCommand("launchctl", "enable", service())

	if err := runCommand("launchctl", "bootstrap", domain(), path); err != nil {
		return "", fmt.Errorf("service: launchctl bootstrap %s: %w", path, err)
	}
	// RunAtLoad already started it; kickstart makes that deterministic.
	_ = runCommand("launchctl", "kickstart", service())

	return path, nil
}

// Uninstall unloads the agent and removes the plist. It is a no-op when no
// agent is installed.
func Uninstall() error {
	if goos == "windows" {
		return uninstallTask()
	}
	path, err := PlistPath()
	if err != nil {
		return err
	}
	_ = runCommand("launchctl", "bootout", service())
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: remove %s: %w", path, err)
	}
	return nil
}

// Restart asks launchd to stop and respawn the agent, so launchd stays the
// owner of the process across an update.
func Restart() error {
	if goos == "windows" {
		return restartTask()
	}
	if err := runCommand("launchctl", "kickstart", "-k", service()); err != nil {
		return fmt.Errorf("service: launchctl kickstart: %w", err)
	}
	return nil
}

// Loaded reports whether launchd currently knows about the agent. A registered
// Windows task is always loaded: Task Scheduler runs it at every logon.
func Loaded() bool {
	if goos == "windows" {
		return taskInstalled()
	}
	return runCommand("launchctl", "print", service()) == nil
}
