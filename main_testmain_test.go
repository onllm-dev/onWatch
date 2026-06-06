package main

import (
	"os"
	"testing"
)

// TestMain runs before all tests in the main package. It unsets
// OPENCODE_HOME/XDG_DATA_HOME so the interactive setup flow's codex credential
// auto-detection never resolves to the host's real
// ~/.local/share/opencode/auth.json. Setup tests set a temp HOME and drive the
// prompts with fixed input; reading a real OpenCode ChatGPT login would shift
// those input sequences and make the tests environment-dependent (flaky).
func TestMain(m *testing.M) {
	os.Unsetenv("OPENCODE_HOME")
	os.Unsetenv("XDG_DATA_HOME")
	os.Exit(m.Run())
}
