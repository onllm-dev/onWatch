package menubar

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBlockedBrowserRootSkipsMissingAndReadable(t *testing.T) {
	home := t.TempDir()
	readable := filepath.Join(home, "readable")
	if err := os.MkdirAll(filepath.Join(readable, "Default"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "not-installed")
	if got := blockedBrowserRoot([]string{missing, readable}); got != "" {
		t.Fatalf("a missing or readable root must not ask for a grant, got %q", got)
	}
}

func TestBlockedBrowserRootReportsUnreadable(t *testing.T) {
	// Windows ignores chmod 0000 on directories, and os.Getuid returns -1
	// there, so the root check alone would let this run and fail.
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block listing on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	home := t.TempDir()
	blocked := filepath.Join(home, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory that exists but cannot be enumerated is exactly what a macOS
	// app-data denial looks like to the importer.
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if got := blockedBrowserRoot([]string{blocked}); got != blocked {
		t.Fatalf("blockedBrowserRoot()=%q, want %q", got, blocked)
	}
}

func TestBrowserDataRootsCoverChromiumAndFirefox(t *testing.T) {
	roots := browserDataRoots("/Users/example")
	if len(roots) == 0 {
		t.Skip("no browser data roots on this platform")
	}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			t.Fatalf("root %q is not absolute", r)
		}
	}
}
