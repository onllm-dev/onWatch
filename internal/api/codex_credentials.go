package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CodexCredentials contains parsed Codex auth state.
type CodexCredentials struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	APIKey       string
	AccountID    string
	ExpiresAt    time.Time     // Token expiry time (parsed from id_token JWT)
	ExpiresIn    time.Duration // Time until expiry (computed)
}

// IsExpiringSoon returns true if the token expires within the given duration.
func (c *CodexCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false // Can't determine expiry, assume not expiring
	}
	return c.ExpiresIn < threshold
}

// IsExpired returns true if the token has already expired.
func (c *CodexCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false // Can't determine expiry, assume valid
	}
	return c.ExpiresIn <= 0
}

type codexAuthFile struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

// DetectCodexCredentials loads Codex credentials from CODEX_HOME/auth.json or ~/.codex/auth.json.
// Falls back to CODEX_TOKEN for environments without a persistent Codex auth file.
func DetectCodexCredentials(logger *slog.Logger) *CodexCredentials {
	if logger == nil {
		logger = slog.Default()
	}

	authPath := codexAuthPath()
	if authPath != "" {
		data, err := os.ReadFile(authPath)
		if err != nil {
			logger.Debug("Codex auth file not readable", "path", authPath, "error", err)
		} else {
			var auth codexAuthFile
			if err := json.Unmarshal(data, &auth); err != nil {
				logger.Debug("Codex auth file parse failed", "path", authPath, "error", err)
			} else {
				idToken := strings.TrimSpace(auth.Tokens.IDToken)
				expiresAt := ParseIDTokenExpiry(idToken)
				var expiresIn time.Duration
				if !expiresAt.IsZero() {
					expiresIn = time.Until(expiresAt)
				}

				creds := &CodexCredentials{
					AccessToken:  strings.TrimSpace(auth.Tokens.AccessToken),
					RefreshToken: strings.TrimSpace(auth.Tokens.RefreshToken),
					IDToken:      idToken,
					APIKey:       strings.TrimSpace(auth.OpenAIAPIKey),
					AccountID:    strings.TrimSpace(auth.Tokens.AccountID),
					ExpiresAt:    expiresAt,
					ExpiresIn:    expiresIn,
				}

				if creds.AccessToken != "" || creds.APIKey != "" {
					if !expiresAt.IsZero() {
						logger.Debug("Codex credentials loaded",
							"path", authPath,
							"expires_in", expiresIn.Round(time.Minute),
							"has_refresh_token", creds.RefreshToken != "")
					}
					return creds
				}

				logger.Debug("Codex auth file has no usable token", "path", authPath)
			}
		}
	} else {
		logger.Debug("Codex auth path unavailable")
	}

	if token := strings.TrimSpace(os.Getenv("CODEX_TOKEN")); token != "" {
		logger.Debug("Using CODEX_TOKEN environment variable")
		return &CodexCredentials{AccessToken: token}
	}

	logger.Debug("No Codex credentials found")
	return nil
}

// DetectCodexToken returns OAuth access token when available.
func DetectCodexToken(logger *slog.Logger) string {
	creds := DetectCodexCredentials(logger)
	if creds == nil {
		return ""
	}
	if creds.AccessToken != "" {
		return creds.AccessToken
	}
	return ""
}

func codexAuthPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "auth.json")
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

// WriteCodexCredentials updates the Codex credentials file with new OAuth tokens.
//
// IMPORTANT: This function MUST be called after every successful OAuth token refresh
// because OpenAI uses refresh token rotation (one-time use refresh tokens).
// Failing to save the new refresh token will break future refresh attempts.
//
// Safety features:
//   - Creates a backup (auth.json.bak) before modifying
//   - Uses atomic write (temp file + rename) to prevent corruption
//   - Preserves existing fields (OPENAI_API_KEY, account_id, etc.) from the original file
//
// Related: https://github.com/onllm-dev/onWatch/issues/30
func WriteCodexCredentials(accessToken, refreshToken, idToken string, expiresIn int) error {
	authPath := codexAuthPath()
	if authPath == "" {
		return os.ErrNotExist
	}

	// Read existing credentials to preserve other fields
	data, err := os.ReadFile(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new auth file with minimal structure
			data = []byte("{}")
		} else {
			return err
		}
	}

	// Create backup BEFORE modifying
	if len(data) > 2 { // More than just "{}"
		backupPath := authPath + ".bak"
		if err := os.WriteFile(backupPath, data, 0600); err != nil {
			// Log but don't fail - backup is nice-to-have
			slog.Debug("Failed to create Codex credentials backup", "error", err)
		}
	}

	// Parse into a map to preserve unknown fields
	var rawAuth map[string]interface{}
	if err := json.Unmarshal(data, &rawAuth); err != nil {
		// If parse fails, start fresh
		rawAuth = make(map[string]interface{})
	}

	// Get or create tokens section
	tokens, ok := rawAuth["tokens"].(map[string]interface{})
	if !ok {
		tokens = make(map[string]interface{})
		rawAuth["tokens"] = tokens
	}

	// Update tokens
	tokens["access_token"] = accessToken
	tokens["refresh_token"] = refreshToken
	if idToken != "" {
		tokens["id_token"] = idToken
	}

	// Marshal back to JSON with pretty printing for readability
	newData, err := json.MarshalIndent(rawAuth, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(authPath), 0700); err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempPath := authPath + ".tmp"
	if err := os.WriteFile(tempPath, newData, 0600); err != nil {
		return err
	}

	return os.Rename(tempPath, authPath)
}
