//go:build !windows

package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func discardAnthropicTokenLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func writeAnthropicCredentialsFile(t *testing.T, home string, content string) string {
	t.Helper()

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}

	path := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}

	return path
}

func TestGetCredentialsFilePath_UsesHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := getCredentialsFilePath()
	want := filepath.Join(home, ".claude", ".credentials.json")
	if got != want {
		t.Fatalf("getCredentialsFilePath() = %q, want %q", got, want)
	}
}

func TestDetectAnthropicCredentialsPlatform_ReturnsNilForMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	creds := detectAnthropicCredentialsPlatform(discardAnthropicTokenLogger())
	if creds != nil {
		t.Fatalf("detectAnthropicCredentialsPlatform() = %+v, want nil", creds)
	}
}

func TestDetectAnthropicCredentialsPlatform_ParsesCredentialsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeAnthropicCredentialsFile(t, home, `{
		"claudeAiOauth": {
			"accessToken": "token-123",
			"refreshToken": "refresh-456",
			"expiresAt": 4102444800000,
			"scopes": ["org:read"]
		}
	}`)

	creds := detectAnthropicCredentialsPlatform(discardAnthropicTokenLogger())
	if creds == nil {
		t.Fatal("detectAnthropicCredentialsPlatform() returned nil")
	}
	if creds.AccessToken != "token-123" {
		t.Fatalf("AccessToken = %q, want token-123", creds.AccessToken)
	}
	if creds.RefreshToken != "refresh-456" {
		t.Fatalf("RefreshToken = %q, want refresh-456", creds.RefreshToken)
	}
	if len(creds.Scopes) != 1 || creds.Scopes[0] != "org:read" {
		t.Fatalf("Scopes = %v, want [org:read]", creds.Scopes)
	}
}

func TestDetectAnthropicTokenPlatform_FileFallbackWithTrimmedToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	writeAnthropicCredentialsFile(t, home, `{
		"claudeAiOauth": {
			"accessToken": "  token-from-file  "
		}
	}`)

	token := detectAnthropicTokenPlatform(discardAnthropicTokenLogger())
	if token != "token-from-file" {
		t.Fatalf("detectAnthropicTokenPlatform() = %q, want token-from-file", token)
	}
}

func TestDetectAnthropicTokenPlatform_ReturnsEmptyForInvalidFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	writeAnthropicCredentialsFile(t, home, `{invalid json}`)

	token := detectAnthropicTokenPlatform(discardAnthropicTokenLogger())
	if token != "" {
		t.Fatalf("detectAnthropicTokenPlatform() = %q, want empty", token)
	}
}

func TestDetectAnthropicToken_WrapperUsesPlatformDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	writeAnthropicCredentialsFile(t, home, `{
		"claudeAiOauth": {
			"accessToken": "wrapper-token"
		}
	}`)

	token := DetectAnthropicToken(discardAnthropicTokenLogger())
	if token != "wrapper-token" {
		t.Fatalf("DetectAnthropicToken() = %q, want wrapper-token", token)
	}
}

func TestDetectAnthropicCredentials_WrapperUsesPlatformDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeAnthropicCredentialsFile(t, home, `{
		"claudeAiOauth": {
			"accessToken": "wrapper-access",
			"refreshToken": "wrapper-refresh",
			"expiresAt": 4102444800000
		}
	}`)

	creds := DetectAnthropicCredentials(discardAnthropicTokenLogger())
	if creds == nil {
		t.Fatal("DetectAnthropicCredentials() returned nil")
	}
	if creds.AccessToken != "wrapper-access" {
		t.Fatalf("AccessToken = %q, want wrapper-access", creds.AccessToken)
	}
	if creds.RefreshToken != "wrapper-refresh" {
		t.Fatalf("RefreshToken = %q, want wrapper-refresh", creds.RefreshToken)
	}
}

func TestWriteAnthropicCredentials_UpdatesAndPreservesFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	credPath := writeAnthropicCredentialsFile(t, home, `{
		"claudeAiOauth": {
			"accessToken": "old-access",
			"refreshToken": "old-refresh",
			"expiresAt": 1700000000000,
			"scopes": ["read", "write"],
			"subscriptionType": "pro"
		},
		"customField": "preserve-me"
	}`)
	oldData, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read original credentials: %v", err)
	}

	if err := WriteAnthropicCredentials("new-access", "new-refresh", 3600); err != nil {
		t.Fatalf("WriteAnthropicCredentials() error = %v", err)
	}

	newData, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read updated credentials: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(newData, &raw); err != nil {
		t.Fatalf("unmarshal updated credentials: %v", err)
	}

	oauth, ok := raw["claudeAiOauth"].(map[string]any)
	if !ok {
		t.Fatal("claudeAiOauth missing or wrong type")
	}

	if got, _ := oauth["accessToken"].(string); got != "new-access" {
		t.Fatalf("accessToken = %q, want new-access", got)
	}
	if got, _ := oauth["refreshToken"].(string); got != "new-refresh" {
		t.Fatalf("refreshToken = %q, want new-refresh", got)
	}

	if got, _ := raw["customField"].(string); got != "preserve-me" {
		t.Fatalf("customField = %q, want preserve-me", got)
	}

	scopes, ok := oauth["scopes"].([]any)
	if !ok || len(scopes) != 2 {
		t.Fatalf("scopes = %v, want [read write]", oauth["scopes"])
	}

	expiresAtVal, ok := oauth["expiresAt"].(float64)
	if !ok {
		t.Fatalf("expiresAt type = %T, want float64", oauth["expiresAt"])
	}
	expiresAt := int64(expiresAtVal)
	lowerBound := time.Now().Add(3500 * time.Second).UnixMilli()
	upperBound := time.Now().Add(3700 * time.Second).UnixMilli()
	if expiresAt < lowerBound || expiresAt > upperBound {
		t.Fatalf("expiresAt = %d outside expected range [%d, %d]", expiresAt, lowerBound, upperBound)
	}

	backupData, err := os.ReadFile(credPath + ".bak")
	if err != nil {
		t.Fatalf("read backup credentials: %v", err)
	}
	if string(backupData) != string(oldData) {
		t.Fatal("backup file content does not match original credentials")
	}
}

func TestWriteAnthropicCredentials_CreatesOAuthSectionWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	credPath := writeAnthropicCredentialsFile(t, home, `{"other": "value"}`)

	if err := WriteAnthropicCredentials("created-access", "created-refresh", 1800); err != nil {
		t.Fatalf("WriteAnthropicCredentials() error = %v", err)
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read updated credentials: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal updated credentials: %v", err)
	}

	oauth, ok := raw["claudeAiOauth"].(map[string]any)
	if !ok {
		t.Fatal("claudeAiOauth section missing")
	}
	if got, _ := oauth["accessToken"].(string); got != "created-access" {
		t.Fatalf("accessToken = %q, want created-access", got)
	}
	if got, _ := oauth["refreshToken"].(string); got != "created-refresh" {
		t.Fatalf("refreshToken = %q, want created-refresh", got)
	}
	if got, _ := raw["other"].(string); got != "value" {
		t.Fatalf("other field = %q, want value", got)
	}
}

func TestWriteAnthropicCredentials_ReturnsErrorForInvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeAnthropicCredentialsFile(t, home, `{invalid json}`)

	if err := WriteAnthropicCredentials("a", "b", 10); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
