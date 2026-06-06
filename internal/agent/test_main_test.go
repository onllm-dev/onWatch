package agent

import (
	"os"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// TestMain runs before all tests in the agent package. It enables test mode
// on the api package to prevent any keychain/keyring operations during tests.
// This ensures tests never read or write real Claude Code OAuth tokens.
//
// It also unsets OPENCODE_HOME/XDG_DATA_HOME so codex credential detection
// never resolves to the host's real ~/.local/share/opencode/auth.json; tests
// that set a temp HOME stay fully isolated regardless of host env.
func TestMain(m *testing.M) {
	api.SetTestMode(true)
	os.Unsetenv("OPENCODE_HOME")
	os.Unsetenv("XDG_DATA_HOME")
	os.Exit(m.Run())
}
