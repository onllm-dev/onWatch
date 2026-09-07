package menubar

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFindAppModeBrowserPrefersEdgeOnWindows(t *testing.T) {
	t.Parallel()
	present := map[string]bool{
		filepath.Join(`C:\Program Files (x86)`, "Microsoft", "Edge", "Application", "msedge.exe"): true,
		filepath.Join(`C:\Program Files`, "Google", "Chrome", "Application", "chrome.exe"):        true,
	}
	exists := func(p string) bool { return present[p] }
	lookPath := func(string) (string, error) { return "", errNotFound }
	getenv := func(k string) string {
		switch k {
		case "ProgramFiles(x86)":
			return `C:\Program Files (x86)`
		case "ProgramFiles":
			return `C:\Program Files`
		case "LOCALAPPDATA":
			return `C:\Users\me\AppData\Local`
		}
		return ""
	}
	got, ok := findAppModeBrowser("windows", lookPath, exists, getenv)
	if !ok {
		t.Fatal("expected a browser")
	}
	if !strings.HasSuffix(got, "msedge.exe") {
		t.Fatalf("expected Edge first, got %q", got)
	}
}

func TestFindAppModeBrowserFallsBackToChromiumFamilyOnLinux(t *testing.T) {
	t.Parallel()
	lookPath := func(name string) (string, error) {
		if name == "brave-browser" {
			return "/usr/bin/brave-browser", nil
		}
		return "", errNotFound
	}
	exists := func(string) bool { return false }
	getenv := func(string) string { return "" }
	got, ok := findAppModeBrowser("linux", lookPath, exists, getenv)
	if !ok || got != "/usr/bin/brave-browser" {
		t.Fatalf("expected brave, got %q ok=%v", got, ok)
	}
}

func TestFindAppModeBrowserHonoursOverride(t *testing.T) {
	t.Parallel()
	lookPath := func(string) (string, error) { return "", errNotFound }
	exists := func(p string) bool { return p == "/opt/mybrowser" }
	getenv := func(k string) string {
		if k == "ONWATCH_QUICKVIEW_BROWSER" {
			return "/opt/mybrowser"
		}
		return ""
	}
	got, ok := findAppModeBrowser("linux", lookPath, exists, getenv)
	if !ok || got != "/opt/mybrowser" {
		t.Fatalf("expected override, got %q ok=%v", got, ok)
	}
}

func TestFindAppModeBrowserReportsNone(t *testing.T) {
	t.Parallel()
	lookPath := func(string) (string, error) { return "", errNotFound }
	exists := func(string) bool { return false }
	getenv := func(string) string { return "" }
	if _, ok := findAppModeBrowser("linux", lookPath, exists, getenv); ok {
		t.Fatal("expected no browser")
	}
}

func TestAppModeArgsBuildsFramelessWindow(t *testing.T) {
	t.Parallel()
	args := appModeArgs("http://localhost:9211/menubar", "/tmp/profile", 360, 680, &windowPosition{X: 100, Y: 200})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--app=http://localhost:9211/menubar",
		"--user-data-dir=/tmp/profile",
		"--window-size=360,680",
		"--window-position=100,200",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in args %v", want, args)
		}
	}
	noPos := appModeArgs("http://x", "/p", 1, 1, nil)
	if strings.Contains(strings.Join(noPos, " "), "--window-position") {
		t.Fatalf("did not expect position arg: %v", noPos)
	}
}

func TestQuickViewPositionAnchorsAboveTaskbar(t *testing.T) {
	t.Parallel()
	work := screenRect{Left: 0, Top: 0, Right: 1920, Bottom: 1040}
	pos := quickViewPosition(cursorPoint{X: 1800, Y: 1060}, work, 360, 680)
	if pos.X != 1920-360-8 {
		t.Fatalf("expected right-clamped x, got %d", pos.X)
	}
	if pos.Y != 1040-680-8 {
		t.Fatalf("expected bottom-anchored y, got %d", pos.Y)
	}
	left := quickViewPosition(cursorPoint{X: 10, Y: 1060}, work, 360, 680)
	if left.X != 8 {
		t.Fatalf("expected left clamp, got %d", left.X)
	}
	top := quickViewPosition(cursorPoint{X: 900, Y: 5}, screenRect{Left: 0, Top: 40, Right: 1920, Bottom: 1080}, 360, 680)
	if top.Y != 48 {
		t.Fatalf("expected top-anchored y below a top taskbar, got %d", top.Y)
	}
}

func TestQuickViewPositionSideTaskbars(t *testing.T) {
	t.Parallel()
	// Taskbar docked left: work area starts at x=60, cursor is in the bar.
	work := screenRect{Left: 60, Top: 0, Right: 1920, Bottom: 1080}
	left := quickViewPosition(cursorPoint{X: 30, Y: 500}, work, 360, 680)
	if left.X != 68 || left.Y != 500-340 {
		t.Fatalf("left taskbar: got %+v, want x=68 y=160", left)
	}
	// Taskbar docked right, cursor near the bottom: y clamps onto the display.
	work = screenRect{Left: 0, Top: 0, Right: 1860, Bottom: 1080}
	right := quickViewPosition(cursorPoint{X: 1890, Y: 1000}, work, 360, 680)
	if right.X != 1860-360-8 || right.Y != 1080-680-8 {
		t.Fatalf("right taskbar: got %+v", right)
	}
}

func TestQuickViewPositionSecondaryDisplay(t *testing.T) {
	t.Parallel()
	// A 1440x2560-ish portrait display left of the primary one: negative X,
	// taskbar at its bottom. The window must land on that display, not at the
	// primary display's bottom-left corner.
	work := screenRect{Left: -1440, Top: -300, Right: 0, Bottom: 2112}
	pos := quickViewPosition(cursorPoint{X: -589, Y: 2115}, work, 360, 680)
	if pos.X != -589-180 {
		t.Fatalf("expected x centred on the cursor, got %d", pos.X)
	}
	if pos.Y != 2112-680-8 {
		t.Fatalf("expected y above the secondary taskbar, got %d", pos.Y)
	}
}
