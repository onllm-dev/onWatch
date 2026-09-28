package agent

import (
	"fmt"
	"os"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// TestMain runs before all tests in the agent package. It enables test mode
// on the api package to prevent any keychain/keyring operations during tests.
// This ensures tests never read or write real Claude Code OAuth tokens.
//
// It then points the home directory at a throwaway sandbox for the whole run
// and clears provider location overrides (CODEX_HOME, OPENCODE_HOME,
// XDG_DATA_HOME, ...), so codex/opencode credential detection never resolves
// to the host's real files. os.UserHomeDir reads HOME on Unix but USERPROFILE
// on Windows, so a test that only overrides HOME would otherwise read and
// write the real Windows profile - including ~/.claude/.credentials.json and
// ~/.claude/settings.json. Tests that need their own home call
// testhome.SetTestHome, which sets both.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// SetTestMode must run before the home is redirected: its first enable
	// records the real home that the credential-file guard refuses.
	api.SetTestMode(true)

	_, cleanup, err := testhome.SandboxHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent tests: %v\n", err)
		return 1
	}
	defer cleanup()

	return m.Run()
}

// testHomeDir returns the home directory the code under test will resolve,
// using the same lookup as production (os.UserHomeDir) so paths built from it
// are correct on every platform.
func testHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	return home
}
