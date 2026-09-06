package menubar

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SessionAvailable reports whether the current process runs where a system
// tray can be reached. The daemon uses it to decide whether spawning the
// companion makes sense: a headless systemd unit, a container, or a Windows
// service session has no tray to attach to.
func SessionAvailable() bool {
	return sessionAvailableFor(runtime.GOOS, os.Getenv, fileExists)
}

func sessionAvailableFor(goos string, getenv func(string) string, exists func(string) bool) bool {
	if trayDisabled(getenv) {
		return false
	}
	switch goos {
	case "darwin":
		return true
	case "linux":
		return linuxSessionAvailable(getenv, exists)
	case "windows":
		return windowsSessionAvailable(getenv)
	default:
		return false
	}
}

// trayDisabled honours ONWATCH_DISABLE_TRAY for users who run the daemon in
// a desktop session but do not want an icon.
func trayDisabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("ONWATCH_DISABLE_TRAY"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// linuxSessionAvailable checks for a reachable D-Bus session bus, which is
// what a StatusNotifierItem tray needs. DISPLAY alone is not enough: the
// tray protocol is D-Bus, not X11.
func linuxSessionAvailable(getenv func(string) string, exists func(string) bool) bool {
	if trayDisabled(getenv) {
		return false
	}
	if exists("/.dockerenv") || exists("/run/.containerenv") {
		return false
	}
	if strings.TrimSpace(getenv("DBUS_SESSION_BUS_ADDRESS")) != "" {
		return true
	}
	if dir := strings.TrimSpace(getenv("XDG_RUNTIME_DIR")); dir != "" {
		return exists(filepath.Join(dir, "bus"))
	}
	return false
}

// windowsSessionAvailable relies on SESSIONNAME, which Windows sets for
// interactive logon sessions (Console, RDP-Tcp#n) and leaves unset for
// services running in session 0.
func windowsSessionAvailable(getenv func(string) string) bool {
	if trayDisabled(getenv) {
		return false
	}
	return strings.TrimSpace(getenv("SESSIONNAME")) != ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
