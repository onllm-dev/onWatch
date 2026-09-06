package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePortFileWritesPlainInteger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "port")
	if err := writePortFileTo(path, 9311); err != nil {
		t.Fatalf("writePortFileTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "9311" {
		t.Fatalf("port file = %q, want 9311", got)
	}
}

func TestWritePortFileRejectsInvalidPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "port")
	if err := writePortFileTo(path, 0); err == nil {
		t.Fatal("expected error for port 0")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file written, stat err=%v", err)
	}
}

func TestPortFilePathLivesNextToPIDFiles(t *testing.T) {
	if got := portFilePath(); got != filepath.Join(pidDir, "port") {
		t.Fatalf("portFilePath() = %q, want %q", got, filepath.Join(pidDir, "port"))
	}
}

func TestMenubarHelpTextMentionsPlatforms(t *testing.T) {
	help := menubarHelpText()
	for _, want := range []string{"macOS", "Linux", "Windows"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected help to mention %s: %q", want, help)
		}
	}
}
