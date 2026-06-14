package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeminiProfilesDir(t *testing.T) {
	dir := geminiProfilesDir()
	if dir == "" {
		t.Fatal("geminiProfilesDir() returned empty string")
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".onwatch", "data", "gemini-profiles")
	if dir != expected {
		t.Errorf("geminiProfilesDir() = %q, want %q", dir, expected)
	}
}

func TestSanitizeProfileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"work", "work"},
		{"work-123", "work-123"},
		{"work_123", "work_123"},
		{"../../ssh", ""},
		{"path/to/profile", ""},
		{"!@#$%^&*()", ""},
		{" work ", "work"},
	}

	for _, tt := range tests {
		got := sanitizeProfileName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeProfileName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
