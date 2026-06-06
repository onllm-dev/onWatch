package api

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func discardLoggerCredentials() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// isolateOpenCodeEnv prevents codex-credential detection from reading the real
// developer/CI OpenCode auth file. DetectCodexCredentials falls back to
// OPENCODE_HOME, then XDG_DATA_HOME/opencode, then HOME/.local/share/opencode;
// clearing the first two pins resolution under the (already temp) HOME so tests
// are hermetic regardless of the host's real ChatGPT/OpenCode login state.
func isolateOpenCodeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENCODE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
}

func TestDetectCodexCredentials_ParsesOAuthTokens(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	authPath := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"tokens": {
			"access_token": "oauth_access",
			"refresh_token": "oauth_refresh",
			"id_token": "oauth_id",
			"account_id": "acct_123"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.AccessToken != "oauth_access" {
		t.Fatalf("AccessToken = %q, want oauth_access", creds.AccessToken)
	}
	if creds.RefreshToken != "oauth_refresh" {
		t.Fatalf("RefreshToken = %q, want oauth_refresh", creds.RefreshToken)
	}
	if creds.IDToken != "oauth_id" {
		t.Fatalf("IDToken = %q, want oauth_id", creds.IDToken)
	}
	if creds.AccountID != "acct_123" {
		t.Fatalf("AccountID = %q, want acct_123", creds.AccountID)
	}
}

func TestDetectCodexCredentials_ParsesAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}

	authPath := filepath.Join(codexDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"OPENAI_API_KEY":"sk-openai-key"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.APIKey != "sk-openai-key" {
		t.Fatalf("APIKey = %q, want sk-openai-key", creds.APIKey)
	}
}

// TestDetectCodexCredentials_EnvVarFallback is a regression test for Issue #26.
// Docker/cloud users cannot access ~/.codex/auth.json and rely on CODEX_TOKEN env var.
// This ensures the fallback path works when no auth file is available.
func TestDetectCodexCredentials_EnvVarFallback(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	isolateOpenCodeEnv(t)
	t.Setenv("CODEX_TOKEN", "env_access_token")

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.AccessToken != "env_access_token" {
		t.Fatalf("AccessToken = %q, want env_access_token", creds.AccessToken)
	}
	if creds.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q, want empty", creds.RefreshToken)
	}
	if creds.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", creds.APIKey)
	}
	if creds.AccountID != "" {
		t.Fatalf("AccountID = %q, want empty", creds.AccountID)
	}
}

func TestDetectCodexToken_PrefersAccessToken(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	authPath := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"OPENAI_API_KEY": "sk-openai-key",
		"tokens": {"access_token": "oauth_access"}
	}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	token := DetectCodexToken(discardLoggerCredentials())
	if token != "oauth_access" {
		t.Fatalf("DetectCodexToken() = %q, want oauth_access", token)
	}
}

func TestDetectCodexToken_RejectsAPIKeyOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}

	authPath := filepath.Join(codexDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"OPENAI_API_KEY":"sk-openai-key"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	token := DetectCodexToken(discardLoggerCredentials())
	if token != "" {
		t.Fatalf("DetectCodexToken() = %q, want empty", token)
	}
}

func TestCodexCredentials_IsExpiringSoon(t *testing.T) {
	tests := []struct {
		name      string
		creds     CodexCredentials
		threshold time.Duration
		want      bool
	}{
		{
			name: "expiring soon",
			creds: CodexCredentials{
				ExpiresAt: time.Now().Add(5 * time.Minute),
				ExpiresIn: 5 * time.Minute,
			},
			threshold: 10 * time.Minute,
			want:      true,
		},
		{
			name: "not expiring soon",
			creds: CodexCredentials{
				ExpiresAt: time.Now().Add(2 * time.Hour),
				ExpiresIn: 2 * time.Hour,
			},
			threshold: 10 * time.Minute,
			want:      false,
		},
		{
			name: "zero expiry - assume not expiring",
			creds: CodexCredentials{
				ExpiresAt: time.Time{},
				ExpiresIn: 0,
			},
			threshold: 10 * time.Minute,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.creds.IsExpiringSoon(tt.threshold); got != tt.want {
				t.Errorf("IsExpiringSoon() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodexCredentials_IsExpired(t *testing.T) {
	tests := []struct {
		name  string
		creds CodexCredentials
		want  bool
	}{
		{
			name: "expired",
			creds: CodexCredentials{
				ExpiresAt: time.Now().Add(-1 * time.Hour),
				ExpiresIn: -1 * time.Hour,
			},
			want: true,
		},
		{
			name: "not expired",
			creds: CodexCredentials{
				ExpiresAt: time.Now().Add(1 * time.Hour),
				ExpiresIn: 1 * time.Hour,
			},
			want: false,
		},
		{
			name: "zero expiry - assume valid",
			creds: CodexCredentials{
				ExpiresAt: time.Time{},
				ExpiresIn: 0,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.creds.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteCodexCredentials(t *testing.T) {
	// Create a temp directory for testing
	tmpDir := t.TempDir()

	// Set CODEX_HOME to our temp directory
	t.Setenv("CODEX_HOME", tmpDir)

	// Create an initial auth.json with existing fields
	authPath := filepath.Join(tmpDir, "auth.json")
	initialContent := `{
  "OPENAI_API_KEY": "sk-test-key",
  "tokens": {
    "access_token": "old-access-token",
    "refresh_token": "old-refresh-token",
    "id_token": "old-id-token",
    "account_id": "test-account-123"
  }
}`
	if err := os.WriteFile(authPath, []byte(initialContent), 0o600); err != nil {
		t.Fatalf("failed to write initial auth.json: %v", err)
	}

	// Write new credentials
	err := WriteCodexCredentials("new-access-token", "new-refresh-token", "new-id-token", 604800)
	if err != nil {
		t.Fatalf("WriteCodexCredentials failed: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("failed to read auth.json: %v", err)
	}

	content := string(data)

	// Check that new tokens are present
	if !strings.Contains(content, "new-access-token") {
		t.Error("expected new-access-token in output")
	}
	if !strings.Contains(content, "new-refresh-token") {
		t.Error("expected new-refresh-token in output")
	}
	if !strings.Contains(content, "new-id-token") {
		t.Error("expected new-id-token in output")
	}

	// Check that existing fields are preserved
	if !strings.Contains(content, "sk-test-key") {
		t.Error("expected OPENAI_API_KEY to be preserved")
	}
	if !strings.Contains(content, "test-account-123") {
		t.Error("expected account_id to be preserved")
	}

	// Check that backup was created
	backupPath := authPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("expected backup file to be created")
	}
}

func TestWriteCodexCredentials_NewFile(t *testing.T) {
	// Create a temp directory for testing (no auth.json exists yet)
	tmpDir := t.TempDir()
	t.Setenv("CODEX_HOME", tmpDir)

	// Write credentials to new file
	err := WriteCodexCredentials("new-access-token", "new-refresh-token", "new-id-token", 604800)
	if err != nil {
		t.Fatalf("WriteCodexCredentials failed: %v", err)
	}

	// Read back and verify
	authPath := filepath.Join(tmpDir, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("failed to read auth.json: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "new-access-token") {
		t.Error("expected new-access-token in output")
	}
}

func TestDetectCodexCredentials_ParsesUserIDFromIDToken(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	header := "eyJhbGciOiJub25lIn0"
	payloadJSON := `{"https://api.openai.com/auth":{"chatgpt_user_id":"user-123"}}`
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	idToken := header + "." + payload + "."

	authPath := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"tokens": {
			"access_token": "oauth_access",
			"refresh_token": "oauth_refresh",
			"id_token": "`+idToken+`",
			"account_id": "acct_123"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.UserID != "user-123" {
		t.Fatalf("UserID = %q, want user-123", creds.UserID)
	}
}

// openCodeAuthJSON builds an OpenCode auth.json body with the given access token
// and expiry (ms). accessToken should be a JWT if UserID extraction is expected.
func openCodeAuthJSON(access, refresh, accountID string, expiresMs int64) string {
	return `{
		"openai": {
			"type": "oauth",
			"access": "` + access + `",
			"refresh": "` + refresh + `",
			"expires": ` + strconv.FormatInt(expiresMs, 10) + `,
			"accountId": "` + accountID + `"
		}
	}`
}

func setOpenCodeOnly(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("OPENCODE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	dir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	return filepath.Join(dir, "auth.json")
}

func TestDetectCodexCredentials_OpenCodeFormat(t *testing.T) {
	authPath := setOpenCodeOnly(t)
	expiresMs := time.Now().Add(2 * time.Hour).UnixMilli()
	if err := os.WriteFile(authPath, []byte(openCodeAuthJSON("oc_access", "oc_refresh", "oc_acct", expiresMs)), 0o600); err != nil {
		t.Fatalf("write opencode auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.AccessToken != "oc_access" {
		t.Fatalf("AccessToken = %q, want oc_access", creds.AccessToken)
	}
	if creds.RefreshToken != "oc_refresh" {
		t.Fatalf("RefreshToken = %q, want oc_refresh", creds.RefreshToken)
	}
	if creds.AccountID != "oc_acct" {
		t.Fatalf("AccountID = %q, want oc_acct", creds.AccountID)
	}
	if creds.Source != CredentialSourceOpenCode {
		t.Fatalf("Source = %v, want CredentialSourceOpenCode", creds.Source)
	}
	if creds.SourcePath != authPath {
		t.Fatalf("SourcePath = %q, want %q", creds.SourcePath, authPath)
	}
	// expires (ms) maps to ExpiresAt within ~2h.
	if creds.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt is zero, want parsed from ms timestamp")
	}
	if d := time.Until(creds.ExpiresAt); d < 90*time.Minute || d > 150*time.Minute {
		t.Fatalf("ExpiresIn ~%v, want ~2h", d)
	}
}

func TestDetectCodexCredentials_CodexPriorityOverOpenCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("OPENCODE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{"access_token":"codex_wins"}}`), 0o600); err != nil {
		t.Fatalf("write codex auth.json: %v", err)
	}

	ocDir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ocDir, "auth.json"), []byte(openCodeAuthJSON("oc_loses", "r", "a", time.Now().Add(time.Hour).UnixMilli())), 0o600); err != nil {
		t.Fatalf("write opencode auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.AccessToken != "codex_wins" {
		t.Fatalf("AccessToken = %q, want codex_wins (Codex priority)", creds.AccessToken)
	}
	if creds.Source != CredentialSourceCodex {
		t.Fatalf("Source = %v, want CredentialSourceCodex", creds.Source)
	}
}

func TestDetectCodexCredentials_OpenCodeHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("XDG_DATA_HOME", "")

	ocHome := t.TempDir()
	t.Setenv("OPENCODE_HOME", ocHome)
	if err := os.WriteFile(filepath.Join(ocHome, "auth.json"), []byte(openCodeAuthJSON("home_override", "r", "a", time.Now().Add(time.Hour).UnixMilli())), 0o600); err != nil {
		t.Fatalf("write opencode auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil || creds.AccessToken != "home_override" {
		t.Fatalf("OPENCODE_HOME override not honored: %+v", creds)
	}
}

func TestOpenCodeAuthPath_XDGDataHome(t *testing.T) {
	t.Setenv("OPENCODE_HOME", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	want := filepath.Join(xdg, "opencode", "auth.json")
	if got := OpenCodeAuthPath(); got != want {
		t.Fatalf("OpenCodeAuthPath() = %q, want %q", got, want)
	}
}

func TestDetectCodexCredentials_OpenCodeUserIDFromAccessJWT(t *testing.T) {
	authPath := setOpenCodeOnly(t)
	header := "eyJhbGciOiJub25lIn0"
	payloadJSON := `{"https://api.openai.com/auth":{"chatgpt_user_id":"oc-user-9"}}`
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	accessJWT := header + "." + payload + "."
	if err := os.WriteFile(authPath, []byte(openCodeAuthJSON(accessJWT, "r", "a", time.Now().Add(time.Hour).UnixMilli())), 0o600); err != nil {
		t.Fatalf("write opencode auth.json: %v", err)
	}

	creds := DetectCodexCredentials(discardLoggerCredentials())
	if creds == nil {
		t.Fatal("DetectCodexCredentials returned nil")
	}
	if creds.UserID != "oc-user-9" {
		t.Fatalf("UserID = %q, want oc-user-9 (parsed from access JWT)", creds.UserID)
	}
}

func TestDetectCodexCredentials_OpenCodeMalformedAndEmpty(t *testing.T) {
	authPath := setOpenCodeOnly(t)

	// Malformed JSON -> nil (no CODEX_TOKEN set).
	if err := os.WriteFile(authPath, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if creds := DetectCodexCredentials(discardLoggerCredentials()); creds != nil {
		t.Fatalf("malformed OpenCode auth should yield nil, got %+v", creds)
	}

	// Valid JSON but empty access -> nil.
	if err := os.WriteFile(authPath, []byte(`{"openai":{"access":""}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if creds := DetectCodexCredentials(discardLoggerCredentials()); creds != nil {
		t.Fatalf("empty OpenCode access should yield nil, got %+v", creds)
	}
}

func TestWriteOpenCodeCredentials(t *testing.T) {
	ocHome := t.TempDir()
	t.Setenv("OPENCODE_HOME", ocHome)
	authPath := filepath.Join(ocHome, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai":{"type":"oauth","access":"old","refresh":"old_r","expires":1,"accountId":"keep_me"},"other":"preserved"}`), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	if err := WriteOpenCodeCredentials("new_access", "new_refresh", expiresAt); err != nil {
		t.Fatalf("WriteOpenCodeCredentials: %v", err)
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var parsed openCodeAuthFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse back: %v", err)
	}
	if parsed.OpenAI.Access != "new_access" || parsed.OpenAI.Refresh != "new_refresh" {
		t.Fatalf("tokens not updated: %+v", parsed.OpenAI)
	}
	if parsed.OpenAI.AccountID != "keep_me" {
		t.Fatalf("accountId not preserved: %q", parsed.OpenAI.AccountID)
	}
	if parsed.OpenAI.Expires != expiresAt.UnixMilli() {
		t.Fatalf("expires = %d, want %d (ms)", parsed.OpenAI.Expires, expiresAt.UnixMilli())
	}
	// Unknown top-level field preserved.
	if !strings.Contains(string(data), "preserved") {
		t.Error("expected unknown field 'other' to be preserved")
	}
	// Backup created.
	if _, err := os.Stat(authPath + ".bak"); os.IsNotExist(err) {
		t.Error("expected backup file")
	}
}

func TestWriteCredentialsBySource_Dispatch(t *testing.T) {
	// OpenCode source -> writes OpenCode format.
	ocHome := t.TempDir()
	t.Setenv("OPENCODE_HOME", ocHome)
	t.Setenv("CODEX_HOME", t.TempDir())

	if err := WriteCredentialsBySource(CredentialSourceOpenCode, "a", "r", "", 3600); err != nil {
		t.Fatalf("dispatch opencode: %v", err)
	}
	ocData, err := os.ReadFile(filepath.Join(ocHome, "auth.json"))
	if err != nil {
		t.Fatalf("read opencode: %v", err)
	}
	if !strings.Contains(string(ocData), `"openai"`) || !strings.Contains(string(ocData), `"access"`) {
		t.Fatalf("OpenCode dispatch did not write OpenCode format: %s", ocData)
	}

	// Codex source -> writes Codex format.
	if err := WriteCredentialsBySource(CredentialSourceCodex, "ca", "cr", "ci", 3600); err != nil {
		t.Fatalf("dispatch codex: %v", err)
	}
	cxData, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"))
	if err != nil {
		t.Fatalf("read codex: %v", err)
	}
	if !strings.Contains(string(cxData), `"tokens"`) || !strings.Contains(string(cxData), `"access_token"`) {
		t.Fatalf("Codex dispatch did not write Codex format: %s", cxData)
	}
}

func TestCodexCredentials_CompositeExternalID(t *testing.T) {
	tests := []struct {
		name  string
		creds CodexCredentials
		want  string
	}{
		{
			name: "account and user present",
			creds: CodexCredentials{
				AccountID: "acc-1",
				UserID:    "user-1",
			},
			want: "acc-1:user-1",
		},
		{
			name: "only account present - returns empty (ambiguous, caller must handle)",
			creds: CodexCredentials{
				AccountID: "acc-1",
			},
			want: "", // user_id missing -> ambiguous identity, caller must dedupe at account level
		},
		{
			name: "neither present",
			creds: CodexCredentials{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.creds.CompositeExternalID(); got != tt.want {
				t.Fatalf("CompositeExternalID() = %q, want %q", got, tt.want)
			}
		})
	}
}
