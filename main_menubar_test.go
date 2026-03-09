package main

import (
	"strings"
	"testing"
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
