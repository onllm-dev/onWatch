package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type geminiManagerFixture struct {
	manager     *GeminiAgentManager
	store       *store.Store
	logger      *slog.Logger
	profilesDir string
}

func newGeminiManagerFixture(t *testing.T) *geminiManagerFixture {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := tracker.NewGeminiTracker(str, logger)
	manager := NewGeminiAgentManager(str, tr, time.Hour, logger)
	manager.profilesDir = filepath.Join(home, ".gemini", "profiles")
	if err := os.MkdirAll(manager.profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	manager.SetPollingCheck(func() bool { return false })
	manager.ctx, manager.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		manager.stopAllAgents()
		if manager.cancel != nil {
			manager.cancel()
		}
	})

	return &geminiManagerFixture{
		manager:     manager,
		store:       str,
		logger:      logger,
		profilesDir: manager.profilesDir,
	}
}

func (f *geminiManagerFixture) writeProfile(t *testing.T, profile GeminiProfile) string {
	t.Helper()

	filename := profile.Name
	if filename == "" {
		filename = "unnamed"
	}
	path := filepath.Join(f.profilesDir, filename+".json")
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func TestGeminiAgentManager_LoadAndStartProfiles(t *testing.T) {
	f := newGeminiManagerFixture(t)

	// Create two profiles
	p1 := GeminiProfile{
		Name:      "personal",
		ProjectID: "project-1",
		UserID:    "user-1",
	}
	p1.Tokens.AccessToken = "access-1"

	p2 := GeminiProfile{
		Name:      "work",
		ProjectID: "project-2",
		UserID:    "user-2",
	}
	p2.Tokens.AccessToken = "access-2"

	f.writeProfile(t, p1)
	f.writeProfile(t, p2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go f.manager.Run(ctx)

	// Wait a bit for agents to start
	time.Sleep(100 * time.Millisecond)

	f.manager.mu.RLock()
	count := len(f.manager.instances)
	f.manager.mu.RUnlock()

	if count != 2 {
		t.Errorf("expected 2 instances, got %d", count)
	}

	// Verify DB accounts
	acc1, _ := f.store.GetOrCreateProviderAccountByExternalID("gemini", "personal", "project-1:user-1")
	if acc1 == nil {
		t.Error("account 1 not created in DB")
	}

	acc2, _ := f.store.GetOrCreateProviderAccountByExternalID("gemini", "work", "project-2:user-2")
	if acc2 == nil {
		t.Error("account 2 not created in DB")
	}
}

func TestGeminiAgentManager_CompositeIDs(t *testing.T) {
	tests := []struct {
		projectID string
		userID    string
		expected  string
	}{
		{"p1", "u1", "p1:u1"},
		{"", "u1", "u1"},
		{"p1", "", "p1"},
		{"", "", ""},
	}

	for _, tt := range tests {
		got := geminiCompositeExternalID(tt.projectID, tt.userID)
		if got != tt.expected {
			t.Errorf("geminiCompositeExternalID(%q, %q) = %q, want %q", tt.projectID, tt.userID, got, tt.expected)
		}
	}
}

func TestGeminiAgentManager_IsDuplicateGeminiProfile(t *testing.T) {
	profile := GeminiProfile{
		Name:      "existing",
		ProjectID: "p1",
		UserID:    "u1",
	}

	tests := []struct {
		name      string
		projectID string
		creds     *api.GeminiCredentials
		want      bool
	}{
		{
			name:      "exact match",
			projectID: "p1",
			creds:     &api.GeminiCredentials{UserID: "u1"},
			want:      true,
		},
		{
			name:      "different user",
			projectID: "p1",
			creds:     &api.GeminiCredentials{UserID: "u2"},
			want:      false,
		},
		{
			name:      "different project",
			projectID: "p2",
			creds:     &api.GeminiCredentials{UserID: "u1"},
			want:      false,
		},
		{
			name:      "no project in creds, match by project only",
			projectID: "p1",
			creds:     &api.GeminiCredentials{UserID: ""},
			want:      true, // matches by ProjectID fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDuplicateGeminiProfile(profile, tt.creds, tt.projectID)
			if got != tt.want {
				t.Errorf("isDuplicateGeminiProfile() = %v, want %v", got, tt.want)
			}
		})
	}
}
