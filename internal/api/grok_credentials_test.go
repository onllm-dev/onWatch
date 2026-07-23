package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseGrokAuth_PreferredOIDC(t *testing.T) {
	data := []byte(`{
		"https://auth.x.ai::some-client": {
			"key": "tok_abc123",
			"refresh_token": "ref_456",
			"auth_mode": "oidc",
			"email": "user@example.com",
			"team_id": "team_123",
			"user_id": "uid_789",
			"first_name": "Jane",
			"last_name": "Doe",
			"expires_at": "2099-01-01T00:00:00Z"
		},
		"https://accounts.x.ai/sign-in": {
			"key": "legacy_tok",
			"auth_mode": "session"
		}
	}`)

	creds := parseGrokAuth(data, "/tmp/auth.json")
	if creds == nil {
		t.Fatal("expected creds, got nil")
	}
	if creds.AccessToken != "tok_abc123" {
		t.Errorf("access token = %q, want tok_abc123", creds.AccessToken)
	}
	if creds.Email != "user@example.com" {
		t.Errorf("email = %q", creds.Email)
	}
	if creds.TeamID != "team_123" {
		t.Errorf("team_id = %q", creds.TeamID)
	}
	if creds.LoginMethod() != "SuperGrok" {
		t.Errorf("loginMethod = %q, want SuperGrok", creds.LoginMethod())
	}
	if creds.DisplayName() != "Jane Doe" {
		t.Errorf("display = %q", creds.DisplayName())
	}
	if creds.IsExpired() {
		t.Error("should not be expired")
	}
	if creds.SourcePath != "/tmp/auth.json" {
		t.Errorf("sourcePath = %q", creds.SourcePath)
	}
}

func TestParseGrokAuth_FallsBackToLegacy(t *testing.T) {
	data := []byte(`{
		"https://accounts.x.ai/sign-in": {
			"key": "legacy_key",
			"auth_mode": "session",
			"email": "legacy@example.com"
		}
	}`)
	creds := parseGrokAuth(data, "/tmp/auth.json")
	if creds == nil || creds.AccessToken != "legacy_key" {
		t.Fatalf("expected legacy creds")
	}
	if creds.LoginMethod() != "session" {
		t.Errorf("login = %q", creds.LoginMethod())
	}
}

func TestParseGrokAuth_IgnoresEmptyKey(t *testing.T) {
	data := []byte(`{
		"https://auth.x.ai::foo": { "key": "" },
		"https://accounts.x.ai/sign-in": { "key": "good" }
	}`)
	creds := parseGrokAuth(data, "")
	if creds == nil || creds.AccessToken != "good" {
		t.Error("should have selected legacy with good key")
	}
}

func TestParseGrokAuth_NoUsableToken(t *testing.T) {
	data := []byte(`{ "https://auth.x.ai::x": { "key": "" } }`)
	if parseGrokAuth(data, "") != nil {
		t.Error("expected nil when no usable key")
	}
}

func TestDetectGrokCredentials_FileNotFound(t *testing.T) {
	// Force a non-existent path via env for this test only
	t.Setenv("GROK_HOME", t.TempDir())
	if DetectGrokCredentials(nil) != nil {
		t.Error("expected nil when no file")
	}
}

func TestDetectGrokCredentials_ValidFile(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	content := `{
		"https://auth.x.ai::client": {
			"key": "live_token",
			"email": "test@ex.com",
			"expires_at": "2099-01-01T00:00:00Z"
		}
	}`
	if err := os.WriteFile(authPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROK_HOME", dir)

	creds := DetectGrokCredentials(nil)
	if creds == nil {
		t.Fatal("expected creds from temp file")
	}
	if creds.AccessToken != "live_token" || creds.Email != "test@ex.com" {
		t.Errorf("creds mismatch: %+v", creds)
	}
}

func TestGrokAuthPath_RespectsGROK_HOME(t *testing.T) {
	t.Setenv("GROK_HOME", "/custom/grok")
	p := grokAuthPath()
	if !strings.HasSuffix(p, filepath.Join("custom", "grok", "auth.json")) {
		t.Errorf("path = %s", p)
	}
}

func TestGrokCredentials_Expiry(t *testing.T) {
	c := &GrokCredentials{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !c.IsExpired() {
		t.Error("should be expired")
	}
	// An already-expired credential reports as "expiring soon" for any positive threshold
	// (time.Until is negative, which is < threshold). This is intentional for callers.
	if !c.IsExpiringSoon(24 * time.Hour) {
		t.Error("expired token should report IsExpiringSoon for positive threshold")
	}

	future := &GrokCredentials{ExpiresAt: time.Now().Add(10 * time.Minute)}
	if future.IsExpired() {
		t.Error("future should not be expired")
	}
	if !future.IsExpiringSoon(1 * time.Hour) {
		t.Error("future within threshold should be expiring soon")
	}
	if future.IsExpiringSoon(1 * time.Minute) {
		t.Error("10m future should not be expiring soon within 1m threshold")
	}
}

func TestGrokCredentials_LoginMethodFallback(t *testing.T) {
	c := &GrokCredentials{AuthMode: "", Scope: grokLegacyScope}
	if c.LoginMethod() != "session" {
		t.Errorf("fallback login = %q", c.LoginMethod())
	}
}
