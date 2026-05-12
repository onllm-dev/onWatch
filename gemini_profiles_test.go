package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestIsDuplicateGeminiProfile(t *testing.T) {
	profile := agent.GeminiProfile{
		Name:      "existing",
		ProjectID: "project-1",
		UserID:    "user-1",
	}

	tests := []struct {
		name      string
		projectID string
		creds     *api.GeminiCredentials
		expected  bool
	}{
		{
			name:      "match both",
			projectID: "project-1",
			creds:     &api.GeminiCredentials{UserID: "user-1"},
			expected:  true,
		},
		{
			name:      "match project only",
			projectID: "project-1",
			creds:     &api.GeminiCredentials{UserID: "user-2"},
			expected:  true, // Matches by ProjectID in current implementation fallback
		},
		{
			name:      "match user only",
			projectID: "project-2",
			creds:     &api.GeminiCredentials{UserID: "user-1"},
			expected:  false, // Composite IDs differ: p1:u1 != p2:u1
		},
		{
			name:      "match nothing",
			projectID: "project-2",
			creds:     &api.GeminiCredentials{UserID: "user-2"},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDuplicateGeminiProfile(profile, tt.creds, tt.projectID)
			if got != tt.expected {
				t.Errorf("isDuplicateGeminiProfile() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGeminiProfiles_List(t *testing.T) {
	tempDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", origHome)

	profilesDir := filepath.Join(tempDir, ".gemini", "profiles")
	os.MkdirAll(profilesDir, 0o700)

	// Create a profile file
	profileData := `{"name":"test-profile","project_id":"p1"}`
	os.WriteFile(filepath.Join(profilesDir, "test-profile.json"), []byte(profileData), 0o600)

	profiles, err := listGeminiProfiles()
	if err != nil {
		t.Fatalf("listGeminiProfiles: %v", err)
	}

	if len(profiles) != 1 {
		t.Errorf("expected 1 profile, got %d", len(profiles))
	}

	if profiles[0].Name != "test-profile" {
		t.Errorf("expected profile name 'test-profile', got %q", profiles[0].Name)
	}
}
