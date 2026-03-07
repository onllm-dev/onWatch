package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRefreshAuthJSON(t *testing.T, home, access, refresh, idToken, account string) {
	t.Helper()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	authPath := filepath.Join(codexDir, "auth.json")
	content := `{
  "tokens": {
    "access_token": "` + access + `",
    "refresh_token": "` + refresh + `",
    "id_token": "` + idToken + `",
    "account_id": "` + account + `"
  }
}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func writeProfileFile(t *testing.T, home, name, access, refresh, idToken, account string) string {
	t.Helper()
	profilesDir := filepath.Join(home, ".onwatch", "codex-profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	path := filepath.Join(profilesDir, name+".json")
	content := `{
  "name": "` + name + `",
  "account_id": "` + account + `",
  "saved_at": "2026-03-08T00:00:00Z",
  "tokens": {
    "access_token": "` + access + `",
    "refresh_token": "` + refresh + `",
    "id_token": "` + idToken + `"
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func withStdinInput(t *testing.T, input string) func() {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin input: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	return func() {
		_ = r.Close()
		os.Stdin = orig
	}
}

func loadProfileForTest(t *testing.T, home, name string) *CodexProfile {
	t.Helper()
	path := filepath.Join(home, ".onwatch", "codex-profiles", name+".json")
	profile, err := loadCodexProfile(path)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	return profile
}

func TestRefreshCodexProfile_SameAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", "new_id", "acc_same")
	writeProfileFile(t, home, "work", "old_access", "old_refresh", "old_id", "acc_same")

	if err := codexProfileRefresh("work"); err != nil {
		t.Fatalf("codexProfileRefresh returned error: %v", err)
	}

	profile := loadProfileForTest(t, home, "work")
	if profile.AccountID != "acc_same" {
		t.Fatalf("AccountID = %q, want acc_same", profile.AccountID)
	}
	if profile.Tokens.AccessToken != "new_access" {
		t.Fatalf("AccessToken = %q, want new_access", profile.Tokens.AccessToken)
	}
	if profile.Tokens.RefreshToken != "new_refresh" {
		t.Fatalf("RefreshToken = %q, want new_refresh", profile.Tokens.RefreshToken)
	}
	if profile.Tokens.IDToken != "new_id" {
		t.Fatalf("IDToken = %q, want new_id", profile.Tokens.IDToken)
	}
	if profile.SavedAt.IsZero() || time.Since(profile.SavedAt) > time.Minute {
		t.Fatalf("SavedAt should be updated recently, got %v", profile.SavedAt)
	}

	path := filepath.Join(home, ".onwatch", "codex-profiles", "work.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("profile permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestRefreshCodexProfile_DifferentAccount_UserConfirms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", "new_id", "acc_new")
	writeProfileFile(t, home, "work", "old_access", "old_refresh", "old_id", "acc_old")

	restore := withStdinInput(t, "y\n")
	defer restore()

	if err := codexProfileRefresh("work"); err != nil {
		t.Fatalf("codexProfileRefresh returned error: %v", err)
	}

	profile := loadProfileForTest(t, home, "work")
	if profile.AccountID != "acc_new" {
		t.Fatalf("AccountID = %q, want acc_new", profile.AccountID)
	}
	if profile.Tokens.AccessToken != "new_access" {
		t.Fatalf("AccessToken = %q, want new_access", profile.Tokens.AccessToken)
	}
}

func TestRefreshCodexProfile_DifferentAccount_UserDeclines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", "new_id", "acc_new")
	path := writeProfileFile(t, home, "work", "old_access", "old_refresh", "old_id", "acc_old")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile before: %v", err)
	}

	restore := withStdinInput(t, "n\n")
	defer restore()

	err = codexProfileRefresh("work")
	if !errors.Is(err, errCodexProfileRefreshAborted) {
		t.Fatalf("expected errCodexProfileRefreshAborted, got %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("profile changed despite decline")
	}
}

func TestRefreshCodexProfile_NewProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", "new_id", "acc_new")

	if err := codexProfileRefresh("personal"); err != nil {
		t.Fatalf("codexProfileRefresh returned error: %v", err)
	}

	profile := loadProfileForTest(t, home, "personal")
	if profile.Name != "personal" {
		t.Fatalf("Name = %q, want personal", profile.Name)
	}
	if profile.AccountID != "acc_new" {
		t.Fatalf("AccountID = %q, want acc_new", profile.AccountID)
	}
	if profile.Tokens.AccessToken != "new_access" {
		t.Fatalf("AccessToken = %q, want new_access", profile.Tokens.AccessToken)
	}
}

func TestRefreshCodexProfile_NoAuthJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	err := codexProfileRefresh("work")
	if err == nil {
		t.Fatal("expected error when auth.json is missing")
	}
	if !strings.Contains(err.Error(), "Run 'codex auth' first") {
		t.Fatalf("error = %q, want auth hint", err.Error())
	}
}
