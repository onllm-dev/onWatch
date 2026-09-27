package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// testMode disables all keychain/keyring operations. Set to true in tests
// to prevent tests from reading or writing real Claude Code credentials.
// This is a critical safety guard - without it, tests can overwrite the user's
// real OAuth tokens in the macOS Keychain, logging them out of Claude Code.
// It also makes the credentials-file helpers refuse the real account home
// (see anthropicHomeBlocked), which matters most on Windows where the file is
// the only store.
var testMode bool

// SetTestMode enables or disables test mode. When enabled, all keychain and
// keyring operations are skipped, and only file-based credential storage is used.
// Files are redirected by pointing the home directory (HOME, and USERPROFILE on
// Windows) at a temp dir in tests.
//
// The first enable records the account's real home directories, before a test
// harness redirects HOME/USERPROFILE, so later file access can be checked
// against them.
func SetTestMode(enabled bool) {
	if enabled {
		captureRealHomes()
	}
	testMode = enabled
}

// IsTestMode reports whether SetTestMode(true) is in effect. Test harnesses in
// other packages use it to assert their TestMain enabled the guard.
func IsTestMode() bool {
	return testMode
}

// errRealCredentialsInTestMode is returned when test mode would touch the real
// account's ~/.claude/.credentials.json.
var errRealCredentialsInTestMode = errors.New("anthropic: test mode refuses the real Claude credentials file")

var (
	realHomesMu       sync.Mutex
	realHomesCaptured bool
	realHomes         []string
)

// captureRealHomes records the home directory as seen at first test-mode
// enable, both from the environment (os.UserHomeDir) and from the OS account
// database (user.Current, which ignores HOME and USERPROFILE).
func captureRealHomes() {
	realHomesMu.Lock()
	defer realHomesMu.Unlock()
	if realHomesCaptured {
		return
	}
	realHomesCaptured = true
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		realHomes = append(realHomes, home)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		realHomes = append(realHomes, u.HomeDir)
	}
}

// anthropicHomeBlocked reports whether home is the account's real home while
// test mode is on. Credential-file helpers use it so a test that failed to
// redirect the home directory can never read or rotate the developer's real
// Claude Code tokens (a rotated refresh token logs Claude Code out). On
// Windows this is the only guard: there is no keychain, the file is the store.
func anthropicHomeBlocked(home string) bool {
	if !testMode {
		return false
	}
	realHomesMu.Lock()
	homes := realHomes
	realHomesMu.Unlock()
	for _, real := range homes {
		if sameDirPath(home, real) {
			return true
		}
	}
	return false
}

// sameDirPath reports whether a and b name the same directory, tolerating
// case differences on Windows and aliases such as symlinks.
func sameDirPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// claudeCredentials represents the Claude Code credentials JSON structure.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken      string   `json:"accessToken"`
		RefreshToken     string   `json:"refreshToken"`
		ExpiresAt        int64    `json:"expiresAt"` // Unix milliseconds
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
		RateLimitTier    string   `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

// AnthropicCredentials contains the parsed OAuth credentials with computed fields.
type AnthropicCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration // time until expiry
	Scopes       []string
}

// IsExpiringSoon returns true if the token expires within the given duration.
// Returns false if expiry is unknown (zero ExpiresAt) to avoid spurious refreshes.
func (c *AnthropicCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return c.ExpiresIn < threshold
}

// IsExpired returns true if the token has already expired.
func (c *AnthropicCredentials) IsExpired() bool {
	return c.ExpiresIn <= 0
}

// parseClaudeCredentials extracts the OAuth access token from Claude Code credentials JSON.
func parseClaudeCredentials(data []byte) (string, error) {
	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", err
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

// parseFullClaudeCredentials extracts all OAuth fields from Claude Code credentials JSON.
func parseFullClaudeCredentials(data []byte) (*AnthropicCredentials, error) {
	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}

	oauth := creds.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return nil, nil // no credentials
	}

	// Convert expiresAt from Unix milliseconds to time.Time
	expiresAt := time.UnixMilli(oauth.ExpiresAt)
	expiresIn := time.Until(expiresAt)

	return &AnthropicCredentials{
		AccessToken:  oauth.AccessToken,
		RefreshToken: oauth.RefreshToken,
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
		Scopes:       oauth.Scopes,
	}, nil
}

// DetectAnthropicToken attempts to auto-detect the Anthropic OAuth token
// from the Claude Code credentials stored in the system keychain or file.
// Returns empty string if not found.
func DetectAnthropicToken(logger *slog.Logger) string {
	return detectAnthropicTokenPlatform(logger)
}

// DetectAnthropicCredentials attempts to auto-detect the full Anthropic OAuth credentials
// from the Claude Code credentials stored in the system keychain or file.
// Returns nil if not found.
func DetectAnthropicCredentials(logger *slog.Logger) *AnthropicCredentials {
	return detectAnthropicCredentialsPlatform(logger)
}
