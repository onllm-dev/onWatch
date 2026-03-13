package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func discardLoggerCredentials() *slog.Logger {
	return slog.New(slog.DiscardHandler)
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
