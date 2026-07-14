package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kimi Code OAuth client ID (public; same as the official kimi-code CLI).
const KimiCodeClientID = "17e5f671-d194-4dfb-9706-5516cb48c098"

// DefaultOAuth/API hosts. Overridable via env for testing.
const (
	DefaultKimiOAuthHost = "https://auth.kimi.com"
	DefaultKimiCodeBase  = "https://api.kimi.com/coding/v1"
)

// KimiCredentials is the on-disk OAuth payload written by the kimi-code CLI.
//
// Only the kimi-code credential store is supported:
//
//	~/.kimi-code/credentials/kimi-code.json
//
// (plus $KIMI_CODE_HOME / $KIMI_CODE_CREDENTIALS overrides).
// Legacy kimi-cli (~/.kimi) is intentionally not read — a single dashboard tab
// must not refresh two independent OAuth token chains.
type KimiCredentials struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	TokenType    string  `json:"token_type"`
	Scope        string  `json:"scope"`
	ExpiresAt    float64 `json:"expires_at"` // unix seconds (float in some writers)
	ExpiresIn    float64 `json:"expires_in"`
	// Path is the file credentials were loaded from (not serialized).
	Path string `json:"-"`
	// Source is a short label for logs: "kimi-code", "env", or "file".
	Source string `json:"-"`
}

// kimiExpiresAtUnix normalizes expires_at to unix seconds.
// Writers usually store seconds; some stores use milliseconds.
func kimiExpiresAtUnix(expiresAt float64) int64 {
	if expiresAt <= 0 {
		return 0
	}
	// Heuristic: values past year ~2001 in ms are > 1e12.
	if expiresAt > 1e12 {
		return int64(expiresAt / 1000)
	}
	return int64(expiresAt)
}

// Expired reports whether the access token is past expires_at (with a 60s skew).
// Unknown expiry (expires_at <= 0) is treated as not expired so we never
// proactive-refresh a still-working token just because the field is missing.
func (c *KimiCredentials) Expired() bool {
	if c == nil || c.AccessToken == "" {
		return true
	}
	exp := kimiExpiresAtUnix(c.ExpiresAt)
	if exp <= 0 {
		return false
	}
	return time.Now().Unix() >= exp-60
}

// SecondsUntilExpiry returns seconds until access-token expiry (0 if expired,
// -1 if expiry unknown).
func (c *KimiCredentials) SecondsUntilExpiry() int64 {
	if c == nil || c.AccessToken == "" {
		return 0
	}
	exp := kimiExpiresAtUnix(c.ExpiresAt)
	if exp <= 0 {
		return -1
	}
	d := exp - time.Now().Unix()
	if d < 0 {
		return 0
	}
	return d
}

// usable reports whether credentials can still authenticate (fresh access or refreshable).
func (c *KimiCredentials) usable() bool {
	if c == nil {
		return false
	}
	if c.AccessToken != "" && !c.Expired() {
		return true
	}
	return c.RefreshToken != ""
}

// KimiCredentialsCandidates returns kimi-code credential file paths, in preference order.
//
//	$KIMI_CODE_CREDENTIALS          # explicit file
//	$KIMI_CREDENTIALS               # alias for explicit file (Docker/CI)
//	$KIMI_CODE_HOME/credentials/kimi-code.json
//	~/.kimi-code/credentials/kimi-code.json
func KimiCredentialsCandidates() []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(p string) {
		if p == "" {
			return
		}
		// Expand leading ~/
		if len(p) >= 2 && p[:2] == "~/" {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	add(os.Getenv("KIMI_CODE_CREDENTIALS"))
	if v := os.Getenv("KIMI_CREDENTIALS"); v != "" {
		add(v)
	}

	credFile := func(shareDir string) string {
		if shareDir == "" {
			return ""
		}
		return filepath.Join(shareDir, "credentials", "kimi-code.json")
	}

	add(credFile(os.Getenv("KIMI_CODE_HOME")))

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json"))
	}
	return out
}

// KimiCredentialsPath returns the first existing candidate path, or the default
// kimi-code path for new writes when none exist.
func KimiCredentialsPath() string {
	for _, p := range KimiCredentialsCandidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json")
	}
	return ""
}

func kimiSourceLabel(path string) string {
	if path == "" {
		return "unknown"
	}
	clean := filepath.ToSlash(path)
	if strings.Contains(clean, "/.kimi-code/") || strings.HasSuffix(clean, "/.kimi-code") {
		return "kimi-code"
	}
	if env := os.Getenv("KIMI_CODE_CREDENTIALS"); env != "" && filepath.Clean(path) == filepath.Clean(env) {
		return "env"
	}
	if env := os.Getenv("KIMI_CREDENTIALS"); env != "" && filepath.Clean(path) == filepath.Clean(env) {
		return "env"
	}
	if home := os.Getenv("KIMI_CODE_HOME"); home != "" {
		if strings.HasPrefix(filepath.Clean(path), filepath.Clean(home)) {
			return "kimi-code"
		}
	}
	return "file"
}

func loadKimiCredentialsFile(path string, logger *slog.Logger) *KimiCredentials {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var creds KimiCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		if logger != nil {
			logger.Debug("kimi: failed to parse credentials", "path", path, "error", err)
		}
		return nil
	}
	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return nil
	}
	creds.Path = path
	creds.Source = kimiSourceLabel(path)
	return &creds
}

// DetectKimiCredentials loads kimi-code OAuth credentials from the first
// readable path in KimiCredentialsCandidates (explicit env, then ~/.kimi-code).
// Legacy kimi-cli paths under ~/.kimi are never consulted.
func DetectKimiCredentials(logger *slog.Logger) *KimiCredentials {
	if logger == nil {
		logger = slog.Default()
	}
	for _, path := range KimiCredentialsCandidates() {
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			continue
		}
		if c := loadKimiCredentialsFile(path, logger); c != nil {
			logger.Debug("kimi: selected credentials",
				"source", c.Source,
				"path", c.Path,
				"expired", c.Expired(),
				"expires_in_sec", c.SecondsUntilExpiry(),
				"has_refresh", c.RefreshToken != "",
			)
			return c
		}
	}
	return nil
}

// SaveKimiCredentials writes credentials back to disk (after refresh).
// Writes to the original Path (the single kimi-code store).
func SaveKimiCredentials(creds *KimiCredentials) error {
	if creds == nil {
		return nil
	}
	path := creds.Path
	if path == "" {
		path = KimiCredentialsPath()
	}
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out := map[string]interface{}{
		"access_token":  creds.AccessToken,
		"refresh_token": creds.RefreshToken,
		"token_type":    creds.TokenType,
		"scope":         creds.Scope,
		"expires_at":    creds.ExpiresAt,
		"expires_in":    creds.ExpiresIn,
	}
	if out["token_type"] == "" {
		out["token_type"] = "Bearer"
	}
	if out["scope"] == "" {
		out["scope"] = "kimi-code"
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// tokenCache avoids hammering the filesystem on every poll when tokens are fresh.
var (
	kimiCredMu    sync.Mutex
	kimiCredCache *KimiCredentials
	kimiCredAt    time.Time
)

// LoadKimiCredentialsCached returns credentials, re-reading disk at most every 30s
// unless force is true.
func LoadKimiCredentialsCached(logger *slog.Logger, force bool) *KimiCredentials {
	kimiCredMu.Lock()
	defer kimiCredMu.Unlock()
	if !force && kimiCredCache != nil && time.Since(kimiCredAt) < 30*time.Second {
		cp := *kimiCredCache
		return &cp
	}
	creds := DetectKimiCredentials(logger)
	kimiCredCache = creds
	kimiCredAt = time.Now()
	if creds == nil {
		return nil
	}
	cp := *creds
	return &cp
}

// InvalidateKimiCredentialsCache clears the in-memory credential cache.
func InvalidateKimiCredentialsCache() {
	kimiCredMu.Lock()
	defer kimiCredMu.Unlock()
	kimiCredCache = nil
	kimiCredAt = time.Time{}
}
