package api

import (
	"log/slog"
	"testing"
)

func TestDetectCursorToken_Empty(t *testing.T) {
	SetCursorTestMode(true)
	defer SetCursorTestMode(false)

	token := DetectCursorToken(slog.Default())
	if token != "" {
		t.Errorf("Expected empty token in test mode, got %q", token)
	}
}

func TestDetectCursorCredentials_Empty(t *testing.T) {
	SetCursorTestMode(true)
	defer SetCursorTestMode(false)

	creds := DetectCursorCredentials(slog.Default())
	if creds != nil {
		t.Errorf("Expected nil credentials in test mode, got %+v", creds)
	}
}

func TestParseCursorStateRows(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected map[string]string
	}{
		{
			name:     "empty json",
			data:     `[]`,
			expected: map[string]string{},
		},
		{
			name: "single key",
			data: `[{"key":"cursorAuth/accessToken","value":"test_token"}]`,
			expected: map[string]string{
				"cursorAuth/accessToken": "test_token",
			},
		},
		{
			name: "multiple keys",
			data: `[{"key":"cursorAuth/accessToken","value":"access_tok"},{"key":"cursorAuth/refreshToken","value":"refresh_tok"},{"key":"cursorAuth/cachedEmail","value":"test@example.com"}]`,
			expected: map[string]string{
				"cursorAuth/accessToken":  "access_tok",
				"cursorAuth/refreshToken": "refresh_tok",
				"cursorAuth/cachedEmail":  "test@example.com",
			},
		},
		{
			name:     "invalid json",
			data:     `{invalid`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCursorStateRows([]byte(tt.data))
			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %v", result)
				}
				return
			}
			if len(result) != len(tt.expected) {
				t.Errorf("Result len = %d, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("result[%q] = %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestCursorAuthToCredentials(t *testing.T) {
	state := &cursorAuthState{
		AccessToken:  "access_token_123",
		RefreshToken: "refresh_token_456",
		Source:       "sqlite",
	}

	creds := cursorAuthToCredentials(state)
	if creds == nil {
		t.Fatal("cursorAuthToCredentials returned nil")
	}
	if creds.AccessToken != "access_token_123" {
		t.Errorf("AccessToken = %q, want %q", creds.AccessToken, "access_token_123")
	}
	if creds.RefreshToken != "refresh_token_456" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "refresh_token_456")
	}
	if creds.Source != "sqlite" {
		t.Errorf("Source = %q, want %q", creds.Source, "sqlite")
	}
}

func TestCursorAuthToCredentials_Nil(t *testing.T) {
	creds := cursorAuthToCredentials(nil)
	if creds != nil {
		t.Errorf("Expected nil for nil state, got %+v", creds)
	}
}
