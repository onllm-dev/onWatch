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

// Kimi Code OAuth client ID (public; same as kimi-cli / kimi-code).
const KimiCodeClientID = "17e5f671-d194-4dfb-9706-5516cb48c098"

// DefaultOAuth/API hosts. Overridable via env for testing.
const (
	DefaultKimiOAuthHost = "https://auth.kimi.com"
	DefaultKimiCodeBase  = "https://api.kimi.com/coding/v1"
)

// KimiCredentials is the on-disk OAuth payload from kimi-code or kimi-cli.
//
// Both CLIs store the same Kimi Code OAuth scope, but under different share dirs:
//   - kimi-code: ~/.kimi-code/credentials/kimi-code.json
//   - kimi-cli:  ~/.kimi/credentials/kimi-code.json  (also KIMI_SHARE_DIR)
type KimiCredentials struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	TokenType    string  `json:"token_type"`
	Scope        string  `json:"scope"`
	ExpiresAt    float64 `json:"expires_at"` // unix seconds (float in some writers)
	ExpiresIn    float64 `json:"expires_in"`
	// Path is the file credentials were loaded from (not serialized).
	Path string `json:"-"`
	// Source is a short label for logs: "kimi-code", "kimi-cli", or "env".
	Source string `json:"-"`
}

// Expired reports whether the access token is past expires_at (with a 60s skew).
func (c *KimiCredentials) Expired() bool {
	if c == nil || c.AccessToken == "" {
		return true
	}
	if c.ExpiresAt <= 0 {
		return false
	}
	return time.Now().Unix() >= int64(c.ExpiresAt)-60
}

// usable ranks whether credentials can still authenticate (fresh access or refreshable).
func (c *KimiCredentials) usable() bool {
	if c == nil {
		return false
	}
	if c.AccessToken != "" && !c.Expired() {
		return true
	}
	return c.RefreshToken != ""
}

// KimiCredentialsCandidates returns credential file paths to try, in preference order.
//
// Supported layout (same filename, different share dir):
//
//	$KIMI_CODE_CREDENTIALS          # explicit file
//	$KIMI_CODE_HOME/credentials/... # kimi-code home override
//	$KIMI_SHARE_DIR/credentials/... # kimi-cli share dir override
//	$KIMI_HOME/credentials/...      # optional alias
//	~/.kimi-code/credentials/kimi-code.json
//	~/.kimi/credentials/kimi-code.json
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
	add(credFile(os.Getenv("KIMI_SHARE_DIR"))) // kimi-cli official override
	add(credFile(os.Getenv("KIMI_HOME")))

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// kimi-code (current CLI)
		add(filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json"))
		// kimi-cli (legacy / still usable)
		add(filepath.Join(home, ".kimi", "credentials", "kimi-code.json"))
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
	// Default write location for kimi-code installs.
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
	// Check kimi-code before .kimi (prefix of .kimi-code would false-match).
	if strings.Contains(clean, "/.kimi-code/") || strings.HasSuffix(clean, "/.kimi-code") {
		return "kimi-code"
	}
	if strings.Contains(clean, "/.kimi/") || strings.HasSuffix(clean, "/.kimi") {
		return "kimi-cli"
	}
	if env := os.Getenv("KIMI_CODE_CREDENTIALS"); env != "" && filepath.Clean(path) == filepath.Clean(env) {
		return "env"
	}
	if share := os.Getenv("KIMI_SHARE_DIR"); share != "" {
		if strings.HasPrefix(filepath.Clean(path), filepath.Clean(share)) {
			return "kimi-cli"
		}
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

// rankKimiCredentials prefers fresh access tokens, then refreshable sets, then newer expiry/mtime.
func rankKimiCredentials(a, b *KimiCredentials) int {
	// higher is better; return >0 if a better than b
	score := func(c *KimiCredentials) int {
		if c == nil {
			return -1000
		}
		s := 0
		if c.AccessToken != "" && !c.Expired() {
			s += 100
		}
		if c.RefreshToken != "" {
			s += 50
		}
		if c.AccessToken != "" {
			s += 10
		}
		// slight preference for kimi-code when tied (current product)
		if c.Source == "kimi-code" {
			s += 1
		}
		return s
	}
	sa, sb := score(a), score(b)
	if sa != sb {
		return sa - sb
	}
	// Prefer later expires_at
	if a != nil && b != nil {
		if a.ExpiresAt != b.ExpiresAt {
			if a.ExpiresAt > b.ExpiresAt {
				return 1
			}
			return -1
		}
		// Prefer newer file mtime
		ia, ea := os.Stat(a.Path)
		ib, eb := os.Stat(b.Path)
		if ea == nil && eb == nil {
			if ia.ModTime().After(ib.ModTime()) {
				return 1
			}
			if ib.ModTime().After(ia.ModTime()) {
				return -1
			}
		}
	}
	return 0
}

// DetectAllKimiCredentials loads every readable credentials file from known locations.
func DetectAllKimiCredentials(logger *slog.Logger) []*KimiCredentials {
	if logger == nil {
		logger = slog.Default()
	}
	var out []*KimiCredentials
	for _, path := range KimiCredentialsCandidates() {
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			continue
		}
		if c := loadKimiCredentialsFile(path, logger); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// DetectKimiCredentials loads the best available Kimi Code OAuth credentials
// from kimi-code and/or kimi-cli config directories.
func DetectKimiCredentials(logger *slog.Logger) *KimiCredentials {
	if logger == nil {
		logger = slog.Default()
	}
	all := DetectAllKimiCredentials(logger)
	if len(all) == 0 {
		return nil
	}
	best := all[0]
	for _, c := range all[1:] {
		if rankKimiCredentials(c, best) > 0 {
			best = c
		}
	}
	if best != nil {
		logger.Debug("kimi: selected credentials",
			"source", best.Source,
			"path", best.Path,
			"expired", best.Expired(),
			"has_refresh", best.RefreshToken != "",
			"candidates", len(all),
		)
	}
	return best
}

// SaveKimiCredentials writes credentials back to disk (after refresh).
// Writes to the original Path so kimi-cli and kimi-code stores stay independent.
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
