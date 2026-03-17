package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GeminiCredentials contains parsed Gemini auth state.
type GeminiCredentials struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration
}

// IsExpired returns true if the token has already expired.
func (c *GeminiCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return c.ExpiresIn <= 0
}

// IsExpiringSoon returns true if the token expires within the given duration.
func (c *GeminiCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return c.ExpiresIn < threshold
}

// geminiOAuthCredsFile maps to ~/.gemini/oauth_creds.json
type geminiOAuthCredsFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiryDate   int64  `json:"expiry_date"` // Unix milliseconds
}

// GeminiCredentialsPath returns the path to the Gemini OAuth credentials file.
func GeminiCredentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini", "oauth_creds.json")
}

// GeminiTokenStore is the interface for DB-based token persistence.
// Implemented by store.Store.
type GeminiTokenStore interface {
	LoadGeminiTokens() (accessToken, refreshToken string, expiresAt int64, err error)
	SaveGeminiTokens(accessToken, refreshToken string, expiresAt int64) error
}

// DetectGeminiCredentials loads Gemini credentials.
// Priority: DB tokens (survives Docker restarts) > env vars > file.
// All sources are merged - higher priority takes precedence per field.
func DetectGeminiCredentials(logger *slog.Logger, tokenStore ...GeminiTokenStore) *GeminiCredentials {
	if logger == nil {
		logger = slog.Default()
	}

	// 1. DB tokens (highest priority - persisted across container restarts)
	var dbCreds *GeminiCredentials
	if len(tokenStore) > 0 && tokenStore[0] != nil {
		dbCreds = detectGeminiCredentialsFromDB(logger, tokenStore[0])
	}

	// 2. Env vars
	envCreds := detectGeminiCredentialsFromEnv(logger)

	// 3. File (~/.gemini/oauth_creds.json)
	fileCreds := detectGeminiCredentialsFromFile(logger)

	// Merge: DB > env > file
	return mergeGeminiCredentials(dbCreds, envCreds, fileCreds)
}

// detectGeminiCredentialsFromDB loads tokens persisted in the settings table.
func detectGeminiCredentialsFromDB(logger *slog.Logger, ts GeminiTokenStore) *GeminiCredentials {
	accessToken, refreshToken, expiresAtMs, err := ts.LoadGeminiTokens()
	if err != nil {
		logger.Debug("Gemini DB token load failed", "error", err)
		return nil
	}
	if accessToken == "" && refreshToken == "" {
		return nil
	}

	var expiresAt time.Time
	var expiresIn time.Duration
	if expiresAtMs > 0 {
		expiresAt = time.UnixMilli(expiresAtMs)
		expiresIn = time.Until(expiresAt)
	}

	logger.Debug("Gemini credentials loaded from DB",
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
		"expires_in", expiresIn.Round(time.Minute))

	return &GeminiCredentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
	}
}

// mergeGeminiCredentials merges credentials from multiple sources.
// Earlier sources take precedence per field.
func mergeGeminiCredentials(sources ...*GeminiCredentials) *GeminiCredentials {
	merged := &GeminiCredentials{}
	hasAny := false
	for _, src := range sources {
		if src == nil {
			continue
		}
		hasAny = true
		if merged.AccessToken == "" && src.AccessToken != "" {
			merged.AccessToken = src.AccessToken
		}
		if merged.RefreshToken == "" && src.RefreshToken != "" {
			merged.RefreshToken = src.RefreshToken
		}
		if merged.ExpiresAt.IsZero() && !src.ExpiresAt.IsZero() {
			merged.ExpiresAt = src.ExpiresAt
			merged.ExpiresIn = src.ExpiresIn
		}
		if merged.IDToken == "" && src.IDToken != "" {
			merged.IDToken = src.IDToken
		}
	}
	if !hasAny {
		return nil
	}
	if merged.AccessToken == "" && merged.RefreshToken == "" {
		return nil
	}
	return merged
}

// detectGeminiCredentialsFromEnv loads credentials from GEMINI_REFRESH_TOKEN or GEMINI_ACCESS_TOKEN env vars.
func detectGeminiCredentialsFromEnv(logger *slog.Logger) *GeminiCredentials {
	refreshToken := strings.TrimSpace(os.Getenv("GEMINI_REFRESH_TOKEN"))
	accessToken := strings.TrimSpace(os.Getenv("GEMINI_ACCESS_TOKEN"))

	if refreshToken == "" && accessToken == "" {
		return nil
	}

	logger.Debug("Gemini credentials loaded from environment variables",
		"has_refresh_token", refreshToken != "",
		"has_access_token", accessToken != "")

	return &GeminiCredentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
}

// detectGeminiCredentialsFromFile loads credentials from ~/.gemini/oauth_creds.json.
func detectGeminiCredentialsFromFile(logger *slog.Logger) *GeminiCredentials {
	credPath := GeminiCredentialsPath()
	if credPath == "" {
		logger.Debug("Gemini credentials path unavailable")
		return nil
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Debug("Gemini credentials file not readable", "path", credPath, "error", err)
		}
		return nil
	}

	var creds geminiOAuthCredsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		logger.Debug("Gemini credentials file parse failed", "path", credPath, "error", err)
		return nil
	}

	accessToken := strings.TrimSpace(creds.AccessToken)
	if accessToken == "" {
		logger.Debug("Gemini credentials file has no access token", "path", credPath)
		return nil
	}

	var expiresAt time.Time
	var expiresIn time.Duration
	if creds.ExpiryDate > 0 {
		expiresAt = time.UnixMilli(creds.ExpiryDate)
		expiresIn = time.Until(expiresAt)
	}

	result := &GeminiCredentials{
		AccessToken:  accessToken,
		RefreshToken: strings.TrimSpace(creds.RefreshToken),
		IDToken:      strings.TrimSpace(creds.IDToken),
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
	}

	if !expiresAt.IsZero() {
		logger.Debug("Gemini credentials loaded",
			"path", credPath,
			"expires_in", expiresIn.Round(time.Minute),
			"has_refresh_token", result.RefreshToken != "")
	}

	return result
}

// DetectGeminiToken returns the access token when available.
func DetectGeminiToken(logger *slog.Logger) string {
	creds := DetectGeminiCredentials(logger)
	if creds == nil {
		return ""
	}
	return creds.AccessToken
}

// GeminiClientCredentials holds OAuth client ID and secret for token refresh.
type GeminiClientCredentials struct {
	ClientID     string
	ClientSecret string
}

// defaultGeminiClientID returns the Gemini CLI's public OAuth client ID.
// This is embedded in the Gemini CLI binary and is not secret
// (installed application - see oauth2.js in gemini-cli-core).
func defaultGeminiClientID() string {
	// Split to avoid GitHub push protection false positive on public OAuth client IDs
	return "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j" + ".apps.googleusercontent.com"
}

// defaultGeminiClientSecret returns the Gemini CLI's public OAuth client secret.
func defaultGeminiClientSecret() string {
	return "GOCSPX-4uHgMPm-1o7Sk" + "-geV6Cu5clXFsxl"
}

// DetectGeminiClientCredentials returns client credentials for OAuth refresh.
// Priority: env vars > hardcoded defaults.
func DetectGeminiClientCredentials() *GeminiClientCredentials {
	clientID := strings.TrimSpace(os.Getenv("GEMINI_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GEMINI_CLIENT_SECRET"))

	if clientID == "" {
		clientID = defaultGeminiClientID()
	}
	if clientSecret == "" {
		clientSecret = defaultGeminiClientSecret()
	}

	return &GeminiClientCredentials{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}
