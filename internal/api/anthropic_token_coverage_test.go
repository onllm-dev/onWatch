package api

import (
	"testing"
	"time"
)

func TestParseClaudeCredentials_Valid(t *testing.T) {
	data := []byte(`{
		"claudeAiOauth": {
			"accessToken": "test-access-token",
			"refreshToken": "test-refresh-token",
			"expiresAt": 1800000000000,
			"scopes": ["read", "write"],
			"subscriptionType": "pro"
		}
	}`)

	token, err := parseClaudeCredentials(data)
	if err != nil {
		t.Fatalf("parseClaudeCredentials failed: %v", err)
	}
	if token != "test-access-token" {
		t.Errorf("token = %q, want test-access-token", token)
	}
}

func TestParseClaudeCredentials_EmptyToken(t *testing.T) {
	data := []byte(`{
		"claudeAiOauth": {
			"accessToken": "",
			"refreshToken": ""
		}
	}`)

	token, err := parseClaudeCredentials(data)
	if err != nil {
		t.Fatalf("parseClaudeCredentials failed: %v", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestParseClaudeCredentials_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid json}`)
	_, err := parseClaudeCredentials(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseFullClaudeCredentials_Valid(t *testing.T) {
	// Use a future timestamp for expiresAt
	futureMs := time.Now().Add(2 * time.Hour).UnixMilli()
	data := []byte(`{
		"claudeAiOauth": {
			"accessToken": "test-access-token",
			"refreshToken": "test-refresh-token",
			"expiresAt": ` + time.Now().Format("0") + `0,
			"scopes": ["read", "write"]
		}
	}`)
	// Use a properly formatted JSON with the future timestamp
	_ = futureMs
	data = []byte(`{
		"claudeAiOauth": {
			"accessToken": "test-access-token",
			"refreshToken": "test-refresh-token",
			"expiresAt": 1900000000000,
			"scopes": ["read", "write"]
		}
	}`)

	creds, err := parseFullClaudeCredentials(data)
	if err != nil {
		t.Fatalf("parseFullClaudeCredentials failed: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	if creds.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want test-access-token", creds.AccessToken)
	}
	if creds.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken = %q, want test-refresh-token", creds.RefreshToken)
	}
	if len(creds.Scopes) != 2 {
		t.Errorf("Scopes = %v, want 2 items", creds.Scopes)
	}
}

func TestParseFullClaudeCredentials_EmptyAccessToken(t *testing.T) {
	data := []byte(`{
		"claudeAiOauth": {
			"accessToken": "",
			"refreshToken": "test-refresh"
		}
	}`)

	creds, err := parseFullClaudeCredentials(data)
	if err != nil {
		t.Fatalf("parseFullClaudeCredentials failed: %v", err)
	}
	if creds != nil {
		t.Error("expected nil credentials for empty access token")
	}
}

func TestParseFullClaudeCredentials_InvalidJSON(t *testing.T) {
	data := []byte(`{not valid json}`)
	_, err := parseFullClaudeCredentials(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestAnthropicCredentials_IsExpiringSoon(t *testing.T) {
	tests := []struct {
		name      string
		expiresIn time.Duration
		threshold time.Duration
		want      bool
	}{
		{
			name:      "expiring soon",
			expiresIn: 5 * time.Minute,
			threshold: 10 * time.Minute,
			want:      true,
		},
		{
			name:      "not expiring soon",
			expiresIn: 2 * time.Hour,
			threshold: 10 * time.Minute,
			want:      false,
		},
		{
			name:      "already expired",
			expiresIn: -5 * time.Minute,
			threshold: 10 * time.Minute,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := &AnthropicCredentials{
				ExpiresIn: tt.expiresIn,
				ExpiresAt: time.Now().Add(tt.expiresIn), // non-zero to enable expiry check
			}
			got := creds.IsExpiringSoon(tt.threshold)
			if got != tt.want {
				t.Errorf("IsExpiringSoon(%v) = %v, want %v", tt.threshold, got, tt.want)
			}
		})
	}
}

func TestAnthropicCredentials_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresIn time.Duration
		want      bool
	}{
		{
			name:      "expired",
			expiresIn: -5 * time.Minute,
			want:      true,
		},
		{
			name:      "not expired",
			expiresIn: 5 * time.Minute,
			want:      false,
		},
		{
			name:      "just expired",
			expiresIn: 0,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := &AnthropicCredentials{
				ExpiresIn: tt.expiresIn,
			}
			got := creds.IsExpired()
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}
