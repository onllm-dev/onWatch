package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GrokCredentials contains parsed Grok auth state from ~/.grok/auth.json (or $GROK_HOME).
// Primary source for identity and bearer token for grok.com billing probes.
// Mirrors the structure and selection logic from CodexBar's GrokAuth.swift.
type GrokCredentials struct {
	AccessToken  string
	RefreshToken string
	Scope        string // the OIDC or legacy scope key under which this entry lives
	AuthMode     string
	UserID       string
	Email        string
	FirstName    string
	LastName     string
	TeamID       string
	ExpiresAt    time.Time
	SourcePath   string // absolute path to the auth file used
}

// IsExpired returns true if the token has an expires_at in the past.
func (c *GrokCredentials) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// IsExpiringSoon returns true if expiry is within threshold (and known).
func (c *GrokCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) < threshold
}

// LoginMethod returns a friendly label ("SuperGrok" for OIDC, else raw auth_mode or "session").
func (c *GrokCredentials) LoginMethod() string {
	switch strings.ToLower(strings.TrimSpace(c.AuthMode)) {
	case "oidc":
		return "SuperGrok"
	case "session":
		return "session"
	case "":
		if c.Scope != "" {
			return "session"
		}
		return ""
	default:
		return c.AuthMode
	}
}

// DisplayName returns "First Last" or just a name part if available.
func (c *GrokCredentials) DisplayName() string {
	parts := []string{}
	if fn := strings.TrimSpace(c.FirstName); fn != "" {
		parts = append(parts, fn)
	}
	if ln := strings.TrimSpace(c.LastName); ln != "" {
		parts = append(parts, ln)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

const (
	grokOIDCScopePrefix = "https://auth.x.ai::"
	grokLegacyScope     = "https://accounts.x.ai/sign-in"
)

// grokAuthFileEntry is the shape of one scope entry in auth.json.
type grokAuthFileEntry struct {
	Key          string `json:"key"`
	RefreshToken string `json:"refresh_token"`
	AuthMode     string `json:"auth_mode"`
	Email        string `json:"email"`
	TeamID       string `json:"team_id"`
	UserID       string `json:"user_id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	ExpiresAt    string `json:"expires_at"`
}

// DetectGrokCredentials loads from GROK_HOME/auth.json or ~/.grok/auth.json.
// Returns nil if file missing, unreadable, or contains no usable bearer "key".
// Prefers OIDC SuperGrok scope entry; falls back to legacy session scope (matching CodexBar).
func DetectGrokCredentials(logger *slog.Logger) *GrokCredentials {
	if logger == nil {
		logger = slog.Default()
	}

	authPath := grokAuthPath()
	if authPath == "" {
		logger.Debug("Grok auth path unavailable")
		return nil
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		logger.Debug("Grok auth file not readable", "path", authPath, "error", err)
		return nil
	}

	creds := parseGrokAuth(data, authPath)
	if creds == nil || creds.AccessToken == "" {
		logger.Debug("Grok auth file has no usable token", "path", authPath)
		return nil
	}

	if !creds.ExpiresAt.IsZero() {
		logger.Debug("Grok credentials loaded",
			"path", authPath,
			"email", creds.Email,
			"team_id", creds.TeamID,
			"expires_in", time.Until(creds.ExpiresAt).Round(time.Minute),
			"login_method", creds.LoginMethod(),
		)
	} else {
		logger.Debug("Grok credentials loaded (no expiry)", "path", authPath, "email", creds.Email)
	}
	return creds
}

// grokAuthPath resolves the auth.json location honoring GROK_HOME then default ~/.grok.
func grokAuthPath() string {
	if grokHome := strings.TrimSpace(os.Getenv("GROK_HOME")); grokHome != "" {
		return filepath.Join(grokHome, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".grok", "auth.json")
}

// GrokAuthPath returns the resolved path (exported for tests and scanners).
func GrokAuthPath() string {
	return grokAuthPath()
}

// parseGrokAuth implements the scope selection + field extraction.
func parseGrokAuth(data []byte, sourcePath string) *GrokCredentials {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}

	scope, entry := selectPreferredGrokEntry(root)
	if scope == "" || entry == nil {
		return nil
	}

	key := strings.TrimSpace(entry.Key)
	if key == "" {
		return nil
	}

	var expiresAt time.Time
	if exp := strings.TrimSpace(entry.ExpiresAt); exp != "" {
		// Try RFC3339Nano then RFC3339
		if t, err := time.Parse(time.RFC3339Nano, exp); err == nil {
			expiresAt = t
		} else if t, err := time.Parse(time.RFC3339, exp); err == nil {
			expiresAt = t
		}
	}

	return &GrokCredentials{
		AccessToken:  key,
		RefreshToken: strings.TrimSpace(entry.RefreshToken),
		Scope:        scope,
		AuthMode:     strings.TrimSpace(entry.AuthMode),
		UserID:       strings.TrimSpace(entry.UserID),
		Email:        strings.TrimSpace(entry.Email),
		FirstName:    strings.TrimSpace(entry.FirstName),
		LastName:     strings.TrimSpace(entry.LastName),
		TeamID:       strings.TrimSpace(entry.TeamID),
		ExpiresAt:    expiresAt,
		SourcePath:   sourcePath,
	}
}

func selectPreferredGrokEntry(root map[string]json.RawMessage) (string, *grokAuthFileEntry) {
	var oidcScope string
	var oidcEntry *grokAuthFileEntry
	var legacyScope string
	var legacyEntry *grokAuthFileEntry

	for scope, raw := range root {
		var ent grokAuthFileEntry
		if err := json.Unmarshal(raw, &ent); err != nil {
			continue
		}
		if strings.TrimSpace(ent.Key) == "" {
			continue
		}
		if strings.HasPrefix(scope, grokOIDCScopePrefix) {
			oidcScope = scope
			oidcEntry = &ent
		} else if scope == grokLegacyScope || strings.Contains(scope, "/sign-in") {
			legacyScope = scope
			legacyEntry = &ent
		}
	}

	if oidcEntry != nil {
		return oidcScope, oidcEntry
	}
	if legacyEntry != nil {
		return legacyScope, legacyEntry
	}
	return "", nil
}
