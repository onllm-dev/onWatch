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

// quickViewPosition anchors the window against whichever edge of the work
// area hosts the taskbar (the cursor sits outside the work area on that side)
// and centres it on the cursor along that edge, clamped so it stays on the
// display. All coordinates are in the virtual screen, so a display left of
// the primary one simply has negative X values.
func quickViewPosition(cursor cursorPoint, work screenRect, width, height int) windowPosition {
	minX, maxX := work.Left+quickViewMargin, work.Right-width-quickViewMargin
	minY, maxY := work.Top+quickViewMargin, work.Bottom-height-quickViewMargin
	centredX := clampInt(cursor.X-width/2, minX, maxX)
	centredY := clampInt(cursor.Y-height/2, minY, maxY)
	switch {
	case cursor.X < work.Left: // taskbar docked left
		return windowPosition{X: minX, Y: centredY}
	case cursor.X >= work.Right: // taskbar docked right
		return windowPosition{X: maxX, Y: centredY}
	case cursor.Y < work.Top: // taskbar docked top
		return windowPosition{X: centredX, Y: minY}
	default: // taskbar at the bottom, or cursor inside the work area
		return windowPosition{X: centredX, Y: maxY}
	}
}

func clampInt(v, lo, hi int) int {
	if v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return v
}
