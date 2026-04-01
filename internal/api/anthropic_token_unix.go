//go:build !windows

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// testMode disables all keychain/keyring operations. Set to true in tests
// to prevent tests from reading or writing real Claude Code credentials.
// This is a critical safety guard - without it, tests can overwrite the user's
// real OAuth tokens in the macOS Keychain, logging them out of Claude Code.
var testMode bool

// SetTestMode enables or disables test mode. When enabled, all keychain and
// keyring operations are skipped, and only file-based credential storage is used.
// Files are redirected by setting HOME to a temp dir in tests.
func SetTestMode(enabled bool) {
	testMode = enabled
}

// getCredentialsFilePath returns the path to the Claude credentials file.
func getCredentialsFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		if u, err := user.Current(); err == nil {
			home = u.HomeDir
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// detectAnthropicTokenPlatform tries platform-specific credential stores.
func detectAnthropicTokenPlatform(logger *slog.Logger) string {
	if logger == nil {
		logger = slog.Default()
	}

	username := ""
	var homeDir string
	if u, err := user.Current(); err == nil {
		username = u.Username
		homeDir = u.HomeDir
	}

	// macOS: try Keychain (skip in test mode to avoid reading real credentials)
	if !testMode && runtime.GOOS == "darwin" && username != "" {
		out, err := exec.Command("security", "find-generic-password",
			"-s", "Claude Code-credentials",
			"-a", username,
			"-w").Output()
		if err == nil {
			token, err := parseClaudeCredentials(out)
			if err == nil && token != "" {
				logger.Info("Anthropic token auto-detected from macOS Keychain")
				return token
			}
		}
	}

	// Linux: try secret-tool (GNOME Keyring) (skip in test mode)
	if !testMode && runtime.GOOS == "linux" && username != "" {
		out, err := exec.Command("secret-tool", "lookup",
			"service", "Claude Code-credentials",
			"account", username).Output()
		if err == nil {
			token, err := parseClaudeCredentials(out)
			if err == nil && token != "" {
				logger.Info("Anthropic token auto-detected from Linux keyring")
				return token
			}
		}
	}

	// File fallback: ~/.claude/.credentials.json
	// Use os.UserHomeDir() first ($HOME), then fall back to user.Current().HomeDir
	// (handles systemd services where $HOME is not set)
	home, err := os.UserHomeDir()
	if err != nil {
		home = homeDir // fallback to passwd-based home from user.Current()
	}
	if home == "" {
		logger.Debug("Cannot determine home directory for credential file lookup")
		return ""
	}
	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		logger.Debug("Credential file not readable", "path", credPath, "error", err)
		return ""
	}
	token, err := parseClaudeCredentials(data)
	if err != nil {
		logger.Debug("Failed to parse credentials", "path", credPath, "error", err)
		return ""
	}
	if token == "" {
		logger.Debug("Credentials file has empty access token", "path", credPath)
		return ""
	}
	logger.Info("Anthropic token auto-detected from credentials file", "path", credPath)
	return strings.TrimSpace(token)
}

// detectAnthropicCredentialsPlatform tries to detect full OAuth credentials.
// On macOS, tries Keychain first, then falls back to file.
// On Linux, tries keyring first, then falls back to file.
func detectAnthropicCredentialsPlatform(logger *slog.Logger) *AnthropicCredentials {
	if logger == nil {
		logger = slog.Default()
	}

	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	// macOS: try Keychain first (skip in test mode)
	if !testMode && runtime.GOOS == "darwin" && username != "" {
		out, err := exec.Command("security", "find-generic-password",
			"-s", "Claude Code-credentials",
			"-a", username,
			"-w").Output()
		if err == nil {
			creds, err := parseFullClaudeCredentials(out)
			if err == nil && creds != nil {
				logger.Debug("Full Anthropic credentials detected from macOS Keychain",
					"expires_in", creds.ExpiresIn.Round(time.Minute),
					"has_refresh_token", creds.RefreshToken != "",
				)
				return creds
			}
		}
	}

	// Linux: try keyring first (skip in test mode)
	if !testMode && runtime.GOOS == "linux" && username != "" {
		out, err := exec.Command("secret-tool", "lookup",
			"service", "Claude Code-credentials",
			"account", username).Output()
		if err == nil {
			creds, err := parseFullClaudeCredentials(out)
			if err == nil && creds != nil {
				logger.Debug("Full Anthropic credentials detected from Linux keyring",
					"expires_in", creds.ExpiresIn.Round(time.Minute),
					"has_refresh_token", creds.RefreshToken != "",
				)
				return creds
			}
		}
	}

	// File fallback
	credPath := getCredentialsFilePath()
	if credPath == "" {
		logger.Debug("Cannot determine credentials file path")
		return nil
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		logger.Debug("Credential file not readable", "path", credPath, "error", err)
		return nil
	}

	creds, err := parseFullClaudeCredentials(data)
	if err != nil {
		logger.Debug("Failed to parse full credentials", "path", credPath, "error", err)
		return nil
	}
	if creds == nil {
		logger.Debug("Credentials file has no OAuth data", "path", credPath)
		return nil
	}

	logger.Debug("Full Anthropic credentials detected",
		"path", credPath,
		"expires_in", creds.ExpiresIn.Round(time.Minute),
		"has_refresh_token", creds.RefreshToken != "",
	)
	return creds
}

// WriteAnthropicCredentials updates the credentials with new OAuth tokens.
//
// IMPORTANT: This function MUST be called after every successful OAuth token refresh
// because Anthropic uses refresh token rotation (one-time use refresh tokens).
// Failing to save the new refresh token will break future refresh attempts.
//
// Platform behavior:
//   - macOS: Updates BOTH the Keychain AND the credentials file for redundancy.
//     Keychain is the primary store (what Claude Code reads), file is backup.
//   - Linux: Updates BOTH the GNOME Keyring (via secret-tool) AND the credentials file.
//     Keyring is the primary store, file is backup.
//   - Windows: Updates the credentials file (see anthropic_token_windows.go).
//
// Safety features:
//   - Creates a backup (.credentials.json.bak) before modifying
//   - Uses atomic write (temp file + rename) to prevent corruption
//   - Preserves existing fields (scopes, subscriptionType, etc.) from the original file
//
// Related: https://github.com/onllm-dev/onWatch/issues/16
func WriteAnthropicCredentials(accessToken, refreshToken string, expiresIn int) error {
	if !testMode {
		// On macOS, update Keychain first (primary credential store)
		if runtime.GOOS == "darwin" {
			if err := writeCredentialsToKeychain(accessToken, refreshToken, expiresIn); err != nil {
				// Log but continue - file write may still succeed
				slog.Debug("Failed to update macOS Keychain", "error", err)
			}
		}

		// On Linux, update GNOME Keyring first (primary credential store)
		if runtime.GOOS == "linux" {
			if err := writeCredentialsToLinuxKeyring(accessToken, refreshToken, expiresIn); err != nil {
				// Log but continue - file write may still succeed
				slog.Debug("Failed to update Linux keyring", "error", err)
			}
		}
	}

	// Also write to file for redundancy and cross-platform compatibility
	return writeCredentialsToFile(accessToken, refreshToken, expiresIn)
}

// writeCredentialsToLinuxKeyring updates the Linux GNOME Keyring with new OAuth tokens.
// This is critical for the rate limit bypass workaround to persist across restarts,
// since Claude Code reads credentials from keyring (not the file) on Linux.
// Uses secret-tool (libsecret) which works with GNOME Keyring, KWallet, etc.
func writeCredentialsToLinuxKeyring(accessToken, refreshToken string, expiresIn int) error {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" {
		return errors.New("cannot determine username for keyring")
	}

	// Check if secret-tool is available
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return fmt.Errorf("secret-tool not found: %w", err)
	}

	// Read existing keyring entry to preserve other fields
	out, err := exec.Command("secret-tool", "lookup",
		"service", "Claude Code-credentials",
		"account", username).Output()
	if err != nil {
		return fmt.Errorf("read keyring: %w", err)
	}

	// Parse existing credentials
	var rawCreds map[string]interface{}
	if err := json.Unmarshal(out, &rawCreds); err != nil {
		return fmt.Errorf("parse keyring JSON: %w", err)
	}

	// Update OAuth section
	oauth, ok := rawCreds["claudeAiOauth"].(map[string]interface{})
	if !ok {
		oauth = make(map[string]interface{})
		rawCreds["claudeAiOauth"] = oauth
	}
	oauth["accessToken"] = accessToken
	oauth["refreshToken"] = refreshToken
	oauth["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()

	// Marshal back to JSON
	newData, err := json.Marshal(rawCreds)
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	// Store in keyring using secret-tool
	// secret-tool store reads from stdin
	cmd := exec.Command("secret-tool", "store",
		"--label", "Claude Code-credentials",
		"service", "Claude Code-credentials",
		"account", username)
	cmd.Stdin = strings.NewReader(string(newData))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write keyring: %w", err)
	}

	return nil
}

// writeCredentialsToKeychain updates the macOS Keychain with new OAuth tokens.
// This is critical for the rate limit bypass workaround to persist across restarts,
// since Claude Code reads credentials from Keychain (not the file) on macOS.
func writeCredentialsToKeychain(accessToken, refreshToken string, expiresIn int) error {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" {
		return errors.New("cannot determine username for Keychain")
	}

	// Read existing Keychain entry to preserve other fields
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials",
		"-a", username,
		"-w").Output()
	if err != nil {
		return fmt.Errorf("read Keychain: %w", err)
	}

	// Parse existing credentials
	var rawCreds map[string]interface{}
	if err := json.Unmarshal(out, &rawCreds); err != nil {
		return fmt.Errorf("parse Keychain JSON: %w", err)
	}

	// Update OAuth section
	oauth, ok := rawCreds["claudeAiOauth"].(map[string]interface{})
	if !ok {
		oauth = make(map[string]interface{})
		rawCreds["claudeAiOauth"] = oauth
	}
	oauth["accessToken"] = accessToken
	oauth["refreshToken"] = refreshToken
	oauth["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()

	// Marshal back to JSON
	newData, err := json.Marshal(rawCreds)
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	// Delete old entry (ignore error if not exists)
	exec.Command("security", "delete-generic-password",
		"-s", "Claude Code-credentials",
		"-a", username).Run()

	// Add new entry
	cmd := exec.Command("security", "add-generic-password",
		"-s", "Claude Code-credentials",
		"-a", username,
		"-w", string(newData),
		"-U")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write Keychain: %w", err)
	}

	return nil
}

// writeCredentialsToFile updates the credentials file with new OAuth tokens.
func writeCredentialsToFile(accessToken, refreshToken string, expiresIn int) error {
	credPath := getCredentialsFilePath()
	if credPath == "" {
		return os.ErrNotExist
	}

	// Read existing credentials to preserve other fields
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - this is OK on macOS where Keychain is primary.
			// Skip file write silently; Keychain update already succeeded.
			return nil
		}
		return err
	}

	// Create backup BEFORE modifying (overwrites previous backup)
	backupPath := credPath + ".bak"
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		// Log but don't fail - backup is nice-to-have, not critical
		// The atomic write still protects against corruption
	}

	// Parse into a map to preserve unknown fields
	var rawCreds map[string]interface{}
	if err := json.Unmarshal(data, &rawCreds); err != nil {
		return err
	}

	// Get or create claudeAiOauth section
	oauth, ok := rawCreds["claudeAiOauth"].(map[string]interface{})
	if !ok {
		oauth = make(map[string]interface{})
		rawCreds["claudeAiOauth"] = oauth
	}

	// Update tokens and expiry
	oauth["accessToken"] = accessToken
	oauth["refreshToken"] = refreshToken
	oauth["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()

	// Marshal back to JSON
	newData, err := json.Marshal(rawCreds)
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempPath := credPath + ".tmp"
	if err := os.WriteFile(tempPath, newData, 0600); err != nil {
		return err
	}

	return os.Rename(tempPath, credPath)
}
