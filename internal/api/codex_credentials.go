package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CredentialSource identifies where Codex credentials were loaded from, so
// refreshed tokens are written back to the correct file in the correct format.
type CredentialSource int

const (
	CredentialSourceNone     CredentialSource = iota
	CredentialSourceCodex                     // ~/.codex/auth.json or CODEX_HOME
	CredentialSourceOpenCode                  // ~/.local/share/opencode/auth.json or OPENCODE_HOME
	CredentialSourceEnvVar                    // CODEX_TOKEN environment variable
)

// CodexCredentials contains parsed Codex auth state.
type CodexCredentials struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	APIKey       string
	AccountID    string
	UserID       string
	ExpiresAt    time.Time        // Token expiry time (parsed from id_token JWT)
	ExpiresIn    time.Duration    // Time until expiry (computed)
	Source       CredentialSource // Where these credentials were loaded from
	SourcePath   string           // Absolute path to the auth file (for write-back)
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

// CompositeExternalID returns a dedup identity for provider account rows.
// Uses account_id:user_id for Team-safe uniqueness when both are present.
// Returns empty string if user_id is absent, forcing callers to fall back to
// account-level comparison and handle ambiguous identity cases explicitly.
func (c *CodexCredentials) CompositeExternalID() string {
	accountID := strings.TrimSpace(c.AccountID)
	userID := strings.TrimSpace(c.UserID)
	if accountID == "" {
		return ""
	}
	if userID == "" {
		return "" // Ambiguous - fall back to caller for account-level dedup
	}
	return accountID + ":" + userID
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

// openCodeAuthFile is the OpenCode credential shape at
// ~/.local/share/opencode/auth.json. Unlike Codex, "expires" is a Unix
// timestamp in MILLISECONDS (Date.now() + expires_in*1000), not a JWT claim.
type openCodeAuthFile struct {
	OpenAI struct {
		Type      string `json:"type"`
		Refresh   string `json:"refresh"`
		Access    string `json:"access"`
		Expires   int64  `json:"expires"` // Unix timestamp in milliseconds
		AccountID string `json:"accountId"`
	} `json:"openai"`
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
				accessToken := strings.TrimSpace(auth.Tokens.AccessToken)
				// Use access_token expiry as the source of truth - it controls
				// API access. id_token expires several days sooner, which would
				// trigger spurious refreshes (and pause polling on refresh failure)
				// even when the access_token is still valid for days.
				expiresAt := ParseIDTokenExpiry(accessToken)
				if expiresAt.IsZero() {
					// Fall back to id_token if access_token can't be parsed.
					expiresAt = ParseIDTokenExpiry(idToken)
				}
				var expiresIn time.Duration
				if !expiresAt.IsZero() {
					expiresIn = time.Until(expiresAt)
				}

				creds := &CodexCredentials{
					AccessToken:  accessToken,
					RefreshToken: strings.TrimSpace(auth.Tokens.RefreshToken),
					IDToken:      idToken,
					APIKey:       strings.TrimSpace(auth.OpenAIAPIKey),
					AccountID:    strings.TrimSpace(auth.Tokens.AccountID),
					UserID:       ParseIDTokenUserID(idToken),
					ExpiresAt:    expiresAt,
					ExpiresIn:    expiresIn,
					Source:       CredentialSourceCodex,
					SourcePath:   authPath,
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

	// OpenCode fallback: ChatGPT OAuth login stored by the OpenCode CLI.
	// Same OpenAI OAuth backend as Codex, so the existing refresh flow works.
	if creds := detectOpenCodeCredentials(logger); creds != nil {
		return creds
	}

	if token := strings.TrimSpace(os.Getenv("CODEX_TOKEN")); token != "" {
		logger.Debug("Using CODEX_TOKEN environment variable")
		return &CodexCredentials{AccessToken: token, Source: CredentialSourceEnvVar}
	}

	logger.Debug("No Codex credentials found")
	return nil
}

// detectOpenCodeCredentials loads ChatGPT OAuth credentials from OpenCode's
// auth.json. Returns nil if the file is absent, malformed, or has no token.
func detectOpenCodeCredentials(logger *slog.Logger) *CodexCredentials {
	authPath := OpenCodeAuthPath()
	if authPath == "" {
		return nil
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		logger.Debug("OpenCode auth file not readable", "path", authPath, "error", err)
		return nil
	}

	var auth openCodeAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		logger.Debug("OpenCode auth file parse failed", "path", authPath, "error", err)
		return nil
	}

	accessToken := strings.TrimSpace(auth.OpenAI.Access)
	if accessToken == "" {
		logger.Debug("OpenCode auth file has no access token", "path", authPath)
		return nil
	}

	var expiresAt time.Time
	var expiresIn time.Duration
	if auth.OpenAI.Expires > 0 {
		expiresAt = time.UnixMilli(auth.OpenAI.Expires)
		expiresIn = time.Until(expiresAt)
	}

	creds := &CodexCredentials{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(auth.OpenAI.Refresh),
		AccountID:    strings.TrimSpace(auth.OpenAI.AccountID),
		UserID:       ParseIDTokenUserID(accessToken),
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
		Source:       CredentialSourceOpenCode,
		SourcePath:   authPath,
	}

	if !expiresAt.IsZero() {
		logger.Debug("OpenCode credentials loaded",
			"path", authPath,
			"expires_in", expiresIn.Round(time.Minute),
			"has_refresh_token", creds.RefreshToken != "")
	}
	return creds
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

// OpenCodeAuthPath resolves the path to OpenCode's auth.json, honoring
// OPENCODE_HOME, then XDG_DATA_HOME, then the default ~/.local/share/opencode.
func OpenCodeAuthPath() string {
	if openCodeHome := strings.TrimSpace(os.Getenv("OPENCODE_HOME")); openCodeHome != "" {
		return filepath.Join(openCodeHome, "auth.json")
	}
	if xdgData := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdgData != "" {
		return filepath.Join(xdgData, "opencode", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "auth.json")
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

// WriteOpenCodeCredentials updates OpenCode's auth.json with refreshed OAuth
// tokens, preserving the OpenCode format (the "openai" wrapper, "access"/
// "refresh" field names, and "expires" as a millisecond Unix timestamp).
//
// IMPORTANT: Like Codex, OpenAI rotates refresh tokens (one-time use), so this
// MUST be called after every successful refresh or future refreshes break.
//
// Safety: backs up to auth.json.bak, preserves unknown fields, atomic write.
func WriteOpenCodeCredentials(accessToken, refreshToken string, expiresAt time.Time) error {
	authPath := OpenCodeAuthPath()
	if authPath == "" {
		return os.ErrNotExist
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte("{}")
		} else {
			return err
		}
	}

	if len(data) > 2 {
		backupPath := authPath + ".bak"
		if err := os.WriteFile(backupPath, data, 0600); err != nil {
			slog.Debug("Failed to create OpenCode credentials backup", "error", err)
		}
	}

	var rawAuth map[string]interface{}
	if err := json.Unmarshal(data, &rawAuth); err != nil {
		rawAuth = make(map[string]interface{})
	}

	openai, ok := rawAuth["openai"].(map[string]interface{})
	if !ok {
		openai = make(map[string]interface{})
		rawAuth["openai"] = openai
	}

	openai["access"] = accessToken
	openai["refresh"] = refreshToken
	if !expiresAt.IsZero() {
		openai["expires"] = expiresAt.UnixMilli()
	}
	// Mark as an OAuth credential if the field is absent (new file).
	if _, has := openai["type"]; !has {
		openai["type"] = "oauth"
	}

	newData, err := json.MarshalIndent(rawAuth, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(authPath), 0700); err != nil {
		return err
	}

	tempPath := authPath + ".tmp"
	if err := os.WriteFile(tempPath, newData, 0600); err != nil {
		return err
	}

	return os.Rename(tempPath, authPath)
}

// WriteCredentialsBySource writes refreshed tokens back to the correct file in
// the correct format based on where the credentials originated. OpenCode-sourced
// tokens are written in OpenCode format; everything else uses Codex format.
func WriteCredentialsBySource(source CredentialSource, accessToken, refreshToken, idToken string, expiresIn int) error {
	if source == CredentialSourceOpenCode {
		var expiresAt time.Time
		if expiresIn > 0 {
			expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
		}
		return WriteOpenCodeCredentials(accessToken, refreshToken, expiresAt)
	}
	return WriteCodexCredentials(accessToken, refreshToken, idToken, expiresIn)
}
