package menubar

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Quick view window resolution for Linux and Windows. The macOS companion
// shows /menubar inside a native WKWebView popover. Without cgo the closest
// equivalent is a Chromium-family browser in --app mode: a frameless window
// with no address bar, sized like the popover and anchored near the tray.
// Edge ships with every supported Windows release, so Windows users get this
// out of the box; on Linux any Chromium-family browser qualifies and the
// default browser is the fallback.

var errNotFound = errors.New("not found")

type cursorPoint struct{ X, Y int }

type screenRect struct{ Left, Top, Right, Bottom int }

type windowPosition struct{ X, Y int }

const quickViewMargin = 8

// findAppModeBrowser returns the first browser executable that supports
// Chromium's --app flag. ONWATCH_QUICKVIEW_BROWSER overrides detection.
func findAppModeBrowser(goos string, lookPath func(string) (string, error), exists func(string) bool, getenv func(string) string) (string, bool) {
	if override := strings.TrimSpace(getenv("ONWATCH_QUICKVIEW_BROWSER")); override != "" {
		if exists(override) {
			return override, true
		}
		if resolved, err := lookPath(override); err == nil {
			return resolved, true
		}
	}
	switch goos {
	case "windows":
		roots := []string{getenv("ProgramFiles(x86)"), getenv("ProgramFiles"), getenv("LOCALAPPDATA")}
		relative := []string{
			filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join("BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join("Vivaldi", "Application", "vivaldi.exe"),
			filepath.Join("Chromium", "Application", "chrome.exe"),
		}
		for _, rel := range relative {
			for _, root := range roots {
				if root == "" {
					continue
				}
				candidate := filepath.Join(root, rel)
				if exists(candidate) {
					return candidate, true
				}
			}
		}
		for _, name := range []string{"msedge", "chrome", "brave", "vivaldi", "chromium"} {
			if resolved, err := lookPath(name); err == nil {
				return resolved, true
			}
		}
	default:
		for _, name := range []string{
			"chromium", "chromium-browser",
			"google-chrome", "google-chrome-stable",
			"brave-browser", "brave",
			"microsoft-edge", "microsoft-edge-stable",
			"vivaldi", "vivaldi-stable",
		} {
			if resolved, err := lookPath(name); err == nil {
				return resolved, true
			}
		}
		for _, candidate := range []string{
			"/var/lib/flatpak/exports/bin/org.chromium.Chromium",
			"/var/lib/flatpak/exports/bin/com.google.Chrome",
			"/var/lib/flatpak/exports/bin/com.brave.Browser",
			"/snap/bin/chromium",
		} {
			if exists(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

// appModeArgs builds the Chromium command line for a popover-like window.
// A dedicated profile directory keeps the window in a process we own, so
// toggling from the tray can close it, and keeps the user's extensions out.
func appModeArgs(url, profileDir string, width, height int, pos *windowPosition) []string {
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-features=Translate,MediaRouter",
		"--noerrdialogs",
	}
	if pos != nil {
		args = append(args, fmt.Sprintf("--window-position=%d,%d", pos.X, pos.Y))
	}
	return args
}

// quickViewPosition places the window horizontally centered on the cursor
// (clamped to the work area) and anchored against whichever screen edge
// hosts the taskbar, so it opens next to the tray like the macOS popover.
func quickViewPosition(cursor cursorPoint, work screenRect, width, height int) windowPosition {
	x := cursor.X - width/2
	minX := work.Left + quickViewMargin
	maxX := work.Right - width - quickViewMargin
	if x > maxX {
		x = maxX
	}
	if x < minX {
		x = minX
	}
	y := work.Bottom - height - quickViewMargin
	if cursor.Y < work.Top {
		y = work.Top + quickViewMargin
	}
	if y < work.Top+quickViewMargin {
		y = work.Top + quickViewMargin
	}
	return windowPosition{X: x, Y: y}
}
