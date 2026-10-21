package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertNoConsumeReferences guards Phase 1's read-only promise: onWatch must
// never implement, stub, or call the reset-credit redemption endpoint. It walks
// the repository source (including tests) and fails on any reference to the
// consume endpoint path or a consume client method.
func assertNoConsumeReferences(t *testing.T) {
	t.Helper()

	root := repoRoot(t)
	forbidden := []string{
		"rate-limit-reset-credits/consume",
		"ConsumeRateLimitResetCredit",
		"ConsumeCodexResetCredit",
		"consume_codex",
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "result":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".js") {
			return nil
		}
		// These tests' own sources necessarily contain the forbidden strings.
		switch filepath.Base(path) {
		case "codex_reset_credits_test.go", "codex_no_consume_test.go":
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(data))
		for _, needle := range forbidden {
			if strings.Contains(lower, strings.ToLower(needle)) {
				t.Errorf("%s references %q; onWatch must stay read-only for reset credits", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
