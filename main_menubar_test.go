package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
)

func TestMenubarHelpText(t *testing.T) {
	help := menubarHelpText()
	for _, fragment := range []string{
		"onWatch Menubar Companion",
		"Usage: onwatch menubar [OPTIONS]",
		"--port PORT",
		"--debug",
		"--help",
	} {
		if !strings.Contains(help, fragment) {
			t.Fatalf("expected help text to contain %q, got %q", fragment, help)
		}
	}
}

func TestMenubarLogPath_UsesNewName(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DBPath: filepath.Join(dir, "onwatch.db")}

	want := filepath.Join(dir, "menubar.log")
	if got := menubarLogPath(cfg); got != want {
		t.Fatalf("menubarLogPath() = %q, want %q", got, want)
	}
}

func TestMenubarLogPath_TestModeUsesNewTestName(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DBPath: filepath.Join(dir, "onwatch.db"), TestMode: true}

	want := filepath.Join(dir, "menubar-test.log")
	if got := menubarLogPath(cfg); got != want {
		t.Fatalf("menubarLogPath() = %q, want %q", got, want)
	}
}
