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
func TestMain(m *testing.M) {
	SetTestMode(true)
	os.Exit(m.Run())
}
