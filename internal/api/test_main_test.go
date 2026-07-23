package api

import (
	"os"
	"testing"
)

// TestMain runs before all tests in the api package. It enables test mode
// to prevent tests from reading or writing real credentials in the macOS
// Keychain or Linux keyring. Without this, tests that call
// WriteAnthropicCredentials or DetectAnthropicToken can overwrite the user's
// real Claude Code OAuth tokens, causing Claude Code to be logged out.
//
// It also unsets OPENCODE_HOME/XDG_DATA_HOME so DetectCodexCredentials never
// resolves to the host's real ~/.local/share/opencode/auth.json; detection
// then follows each test's temp HOME, keeping the suite hermetic regardless of
// whether the developer/CI has a ChatGPT-via-OpenCode login.
func TestMain(m *testing.M) {
	SetTestMode(true)
	os.Unsetenv("OPENCODE_HOME")
	os.Unsetenv("XDG_DATA_HOME")
	os.Exit(m.Run())
}
