package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// Additional Anthropic accounts.
//
// The primary account (ANTHROPIC_TOKEN or auto-detected Claude Code
// credentials) keeps working exactly as before and is stored with
// account_id 0. Extra accounts are credential files in
// <data dir>/anthropic-profiles/<name>.json using the same layout as Claude
// Code's .credentials.json ({"claudeAiOauth": {...}}), so a credentials file
// copied from another machine works as-is. For Docker, the files can be seeded
// from environment variables:
//
//	ANTHROPIC_TOKEN_<NAME>=<access token>
//	ANTHROPIC_REFRESH_TOKEN_<NAME>=<refresh token>   (optional, enables OAuth refresh)
//
// Each account owns its file. Rotated OAuth tokens (refresh tokens are one-time
// use) are written back to that file only, never to Claude Code's credentials
// or to another account. The env seed is fingerprinted, so a container restart
// keeps the rotated tokens while changing the env value re-seeds the file.

const (
	// AnthropicProfilesDirName is the directory (inside the data dir) holding
	// extra Anthropic account credential files.
	AnthropicProfilesDirName = "anthropic-profiles"

	// MaxAnthropicAccounts bounds the number of extra accounts (RAM budget).
	MaxAnthropicAccounts = 20

	anthropicTokenEnvPrefix   = "ANTHROPIC_TOKEN_"
	anthropicRefreshEnvPrefix = "ANTHROPIC_REFRESH_TOKEN_"
)

var anthropicAccountNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// anthropicProfileWriteMu serializes profile file writes within the process.
var anthropicProfileWriteMu sync.Mutex

// AnthropicAccountProfile is one extra Anthropic account.
type AnthropicAccountProfile struct {
	Name string // normalized account name (lowercase)
	Path string // credentials file path
}

// NormalizeAnthropicAccountName lowercases and validates an account name.
// "default" is reserved for the primary account.
func NormalizeAnthropicAccountName(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || name == "default" || !anthropicAccountNameRe.MatchString(name) {
		return "", false
	}
	return name, true
}

// AnthropicProfilesDir returns the extra-account directory inside dataDir.
func AnthropicProfilesDir(dataDir string) string {
	return filepath.Join(dataDir, AnthropicProfilesDirName)
}

// anthropicProfileFile is the on-disk layout (Claude Code compatible plus an
// onWatch seed fingerprint). Unknown fields are preserved on rewrite.
type anthropicProfileOAuth struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"` // Unix milliseconds, 0 = unknown
}

func anthropicSeedFingerprint(access, refresh string) string {
	sum := sha256.Sum256([]byte(access + "\x00" + refresh))
	return hex.EncodeToString(sum[:])
}

// readAnthropicProfileRaw returns the decoded top-level object, or nil.
func readAnthropicProfileRaw(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", filepath.Base(path), err)
	}
	return raw, nil
}

func writeAnthropicProfileRaw(path string, raw map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".anthropic-profile-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	// os.CreateTemp already uses 0600; Chmod is best-effort for platforms
	// that ignore it.
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ReadAnthropicProfileCredentials reads one account's credentials file.
// Returns nil when the file is missing, unreadable or has no access token.
func ReadAnthropicProfileCredentials(path string) *api.AnthropicCredentials {
	raw, err := readAnthropicProfileRaw(path)
	if err != nil || raw == nil {
		return nil
	}
	var oauth anthropicProfileOAuth
	if msg, ok := raw["claudeAiOauth"]; !ok || json.Unmarshal(msg, &oauth) != nil {
		return nil
	}
	if strings.TrimSpace(oauth.AccessToken) == "" {
		return nil
	}
	creds := &api.AnthropicCredentials{
		AccessToken:  strings.TrimSpace(oauth.AccessToken),
		RefreshToken: strings.TrimSpace(oauth.RefreshToken),
	}
	if oauth.ExpiresAt > 0 {
		creds.ExpiresAt = time.UnixMilli(oauth.ExpiresAt)
		creds.ExpiresIn = time.Until(creds.ExpiresAt)
	}
	return creds
}

// WriteAnthropicProfileCredentials persists a rotated token pair to one
// account's file, preserving every other field. It never touches Claude Code's
// own credentials, so accounts can't consume each other's refresh tokens.
func WriteAnthropicProfileCredentials(path, accessToken, refreshToken string, expiresIn int) error {
	anthropicProfileWriteMu.Lock()
	defer anthropicProfileWriteMu.Unlock()

	raw, err := readAnthropicProfileRaw(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	oauth := map[string]interface{}{}
	if msg, ok := raw["claudeAiOauth"]; ok {
		_ = json.Unmarshal(msg, &oauth)
	}
	oauth["accessToken"] = accessToken
	if refreshToken != "" {
		oauth["refreshToken"] = refreshToken
	}
	if expiresIn > 0 {
		oauth["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	}
	encoded, err := json.Marshal(oauth)
	if err != nil {
		return err
	}
	raw["claudeAiOauth"] = encoded
	return writeAnthropicProfileRaw(path, raw)
}

// SeedAnthropicProfilesFromEnv writes profile files for every
// ANTHROPIC_TOKEN_<NAME> variable in environ. A file whose seed fingerprint
// matches the env value is left alone, so rotated tokens survive restarts.
// Returns the normalized names that were seeded or already up to date.
func SeedAnthropicProfilesFromEnv(dir string, environ []string, logger *slog.Logger) []string {
	if logger == nil {
		logger = slog.Default()
	}
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}

	var names []string
	for key, value := range env {
		if !strings.HasPrefix(key, anthropicTokenEnvPrefix) {
			continue
		}
		suffix := strings.TrimPrefix(key, anthropicTokenEnvPrefix)
		access := strings.TrimSpace(value)
		if access == "" {
			continue
		}
		name, ok := NormalizeAnthropicAccountName(suffix)
		if !ok {
			logger.Warn("Ignoring Anthropic account env var with invalid name", "variable", key)
			continue
		}
		refresh := strings.TrimSpace(env[anthropicRefreshEnvPrefix+suffix])
		seed := anthropicSeedFingerprint(access, refresh)
		path := filepath.Join(dir, name+".json")

		if raw, err := readAnthropicProfileRaw(path); err == nil && raw != nil {
			var existing string
			if msg, ok := raw["onwatchSeed"]; ok {
				_ = json.Unmarshal(msg, &existing)
			}
			if existing == seed {
				names = append(names, name)
				continue // keep rotated tokens
			}
		}

		oauth, _ := json.Marshal(anthropicProfileOAuth{AccessToken: access, RefreshToken: refresh})
		seedJSON, _ := json.Marshal(seed)
		raw := map[string]json.RawMessage{
			"claudeAiOauth": oauth,
			"onwatchSeed":   seedJSON,
		}
		anthropicProfileWriteMu.Lock()
		err := writeAnthropicProfileRaw(path, raw)
		anthropicProfileWriteMu.Unlock()
		if err != nil {
			logger.Error("Failed to seed Anthropic account profile", "account", name, "error", err)
			continue
		}
		logger.Info("Seeded Anthropic account profile from environment", "account", name, "has_refresh_token", refresh != "")
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LoadAnthropicProfiles lists the extra-account credential files in dir,
// sorted by name and capped at MaxAnthropicAccounts.
func LoadAnthropicProfiles(dir string, logger *slog.Logger) []AnthropicAccountProfile {
	if logger == nil {
		logger = slog.Default()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var profiles []AnthropicAccountProfile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name, ok := NormalizeAnthropicAccountName(strings.TrimSuffix(e.Name(), ".json"))
		if !ok || name+".json" != e.Name() {
			logger.Warn("Ignoring Anthropic profile with invalid file name", "file", e.Name())
			continue
		}
		path := filepath.Join(dir, e.Name())
		if ReadAnthropicProfileCredentials(path) == nil {
			logger.Warn("Ignoring Anthropic profile without an access token", "account", name)
			continue
		}
		profiles = append(profiles, AnthropicAccountProfile{Name: name, Path: path})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	if len(profiles) > MaxAnthropicAccounts {
		logger.Warn("Too many Anthropic accounts, extra profiles ignored", "max", MaxAnthropicAccounts, "found", len(profiles))
		profiles = profiles[:MaxAnthropicAccounts]
	}
	return profiles
}

// AnthropicAccountsRunner runs the primary Anthropic agent (optional) and one
// agent per extra account under a single AgentManager key ("anthropic").
type AnthropicAccountsRunner struct {
	Primary *AnthropicAgent
	Extras  []*AnthropicAgent
}

// Run starts every agent and blocks until all of them return.
func (r *AnthropicAccountsRunner) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	agents := make([]*AnthropicAgent, 0, len(r.Extras)+1)
	if r.Primary != nil {
		agents = append(agents, r.Primary)
	}
	agents = append(agents, r.Extras...)
	for _, ag := range agents {
		wg.Add(1)
		go func(a *AnthropicAgent) {
			defer wg.Done()
			_ = a.Run(ctx)
		}(ag)
	}
	wg.Wait()
	return nil
}
