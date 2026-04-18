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

func TestCursorStateDBPathForOS(t *testing.T) {
	home := "/tmp/testhome"

	tests := []struct {
		name string
		goos string
		want string
	}{
		{
			name: "darwin path",
			goos: "darwin",
			want: "/tmp/testhome/Library/Application Support/Cursor/User/globalStorage/state.vscdb",
		},
		{
			name: "linux path",
			goos: "linux",
			want: "/tmp/testhome/.config/Cursor/User/globalStorage/state.vscdb",
		},
		{
			name: "windows path",
			goos: "windows",
			want: "/tmp/testhome/AppData/Roaming/Cursor/User/globalStorage/state.vscdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cursorStateDBPathForOS(home, tt.goos)
			if got != tt.want {
				t.Fatalf("cursorStateDBPathForOS() = %q, want %q", got, tt.want)
			}
		})
	}
}
