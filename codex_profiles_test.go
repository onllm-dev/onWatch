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
	profilesDir := filepath.Join(home, ".onwatch", "data", "codex-profiles")
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
	path := filepath.Join(home, ".onwatch", "data", "codex-profiles", name+".json")
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

	path := filepath.Join(home, ".onwatch", "data", "codex-profiles", "work.json")
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

func TestCodexProfilesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := codexProfilesDir(); got != filepath.Join(home, ".onwatch", "data", "codex-profiles") {
		t.Fatalf("codexProfilesDir() = %q", got)
	}
}

func TestPrintCodexHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := printCodexHelp(); err != nil {
			t.Fatalf("printCodexHelp returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Codex Profile Management",
		"save <name>",
		"refresh <name>",
		"status",
		"Workflow:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestRunCodexCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	t.Run("missing subcommand prints help", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand returned error: %v", err)
			}
		})
		if !strings.Contains(out, "Codex Profile Management") {
			t.Fatalf("expected help output, got:\n%s", out)
		}
	})

	t.Run("list dispatch", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "list"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand(list) returned error: %v", err)
			}
		})
		if !strings.Contains(out, "No Codex profiles saved.") {
			t.Fatalf("unexpected list output:\n%s", out)
		}
	})

	t.Run("status dispatch", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "status"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand(status) returned error: %v", err)
			}
		})
		if !strings.Contains(out, "No Codex profiles saved.") {
			t.Fatalf("unexpected status output:\n%s", out)
		}
	})

	t.Run("save missing name returns usage error", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "save"}
		err := runCodexCommand()
		if err == nil || !strings.Contains(err.Error(), "usage: onwatch codex profile save <name>") {
			t.Fatalf("save missing name error = %v", err)
		}
	})

	t.Run("delete missing name returns usage error", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "delete"}
		err := runCodexCommand()
		if err == nil || !strings.Contains(err.Error(), "usage: onwatch codex profile delete <name>") {
			t.Fatalf("delete missing name error = %v", err)
		}
	})

	t.Run("refresh missing name returns usage error", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "refresh"}
		err := runCodexCommand()
		if err == nil || !strings.Contains(err.Error(), "usage: onwatch codex profile refresh <name>") {
			t.Fatalf("refresh missing name error = %v", err)
		}
	})

	t.Run("unknown subcommand prints help", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile", "unknown"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand returned error: %v", err)
			}
		})
		if !strings.Contains(out, "Codex Profile Management") {
			t.Fatalf("expected help output, got:\n%s", out)
		}
	})
}

func TestCodexProfileSaveListStatusDeleteFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "save_access", "save_refresh", "save_id", "acct_one")

	saveOut := captureStdout(t, func() {
		if err := codexProfileSave("work"); err != nil {
			t.Fatalf("codexProfileSave returned error: %v", err)
		}
	})
	if !strings.Contains(saveOut, `Saved Codex profile "work" (account: acct_one)`) {
		t.Fatalf("unexpected save output:\n%s", saveOut)
	}

	profile := loadProfileForTest(t, home, "work")
	if profile.Name != "work" || profile.AccountID != "acct_one" {
		t.Fatalf("saved profile = %+v", profile)
	}
	if profile.Tokens.AccessToken != "save_access" {
		t.Fatalf("saved access token = %q, want save_access", profile.Tokens.AccessToken)
	}

	listOut := captureStdout(t, func() {
		if err := codexProfileList(); err != nil {
			t.Fatalf("codexProfileList returned error: %v", err)
		}
	})
	if !strings.Contains(listOut, "Saved Codex profiles:") || !strings.Contains(listOut, "work (account: acct_one)") {
		t.Fatalf("unexpected list output:\n%s", listOut)
	}

	statusOut := captureStdout(t, func() {
		if err := codexProfileStatus(); err != nil {
			t.Fatalf("codexProfileStatus returned error: %v", err)
		}
	})
	if !strings.Contains(statusOut, "work (acct_one): ready") {
		t.Fatalf("unexpected status output:\n%s", statusOut)
	}

	deleteOut := captureStdout(t, func() {
		if err := codexProfileDelete("work"); err != nil {
			t.Fatalf("codexProfileDelete returned error: %v", err)
		}
	})
	if !strings.Contains(deleteOut, `Deleted Codex profile "work"`) {
		t.Fatalf("unexpected delete output:\n%s", deleteOut)
	}

	if _, err := os.Stat(filepath.Join(home, ".onwatch", "data", "codex-profiles", "work.json")); !os.IsNotExist(err) {
		t.Fatalf("expected profile to be deleted, stat err=%v", err)
	}
}

func TestCodexProfileSave_BlocksDuplicateAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "dup_access", "dup_refresh", "dup_id", "acct_dup")
	writeProfileFile(t, home, "personal", "old_access", "old_refresh", "old_id", "acct_dup")

	err := codexProfileSave("work")
	if err == nil {
		t.Fatal("expected error for duplicate account, got nil")
	}
	if !strings.Contains(err.Error(), "already saved as profile") {
		t.Fatalf("expected duplicate-account error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "codex profile refresh personal") {
		t.Fatalf("expected refresh hint in error, got: %v", err)
	}
}

func TestCodexProfileSave_InvalidNameAndMissingCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	if err := codexProfileSave("bad name"); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("invalid name error = %v", err)
	}

	if err := codexProfileSave("work"); err == nil || !strings.Contains(err.Error(), "no Codex credentials found") {
		t.Fatalf("missing credentials error = %v", err)
	}
}

func TestListCodexProfiles_SkipsInvalidFilesAndDerivesName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	profilesDir := filepath.Join(home, ".onwatch", "data", "codex-profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(profilesDir, "derived.json"), []byte(`{"account_id":"acct-derived","saved_at":"2026-03-08T00:00:00Z","tokens":{"access_token":"tok"}}`), 0o600); err != nil {
		t.Fatalf("write valid derived profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "broken.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write invalid profile: %v", err)
	}

	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles returned error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profile count = %d, want 1", len(profiles))
	}
	if profiles[0].Name != "derived" {
		t.Fatalf("derived profile name = %q, want derived", profiles[0].Name)
	}
}

func TestCodexProfileStatus_NoCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	profilesDir := filepath.Join(home, ".onwatch", "data", "codex-profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "empty.json"), []byte(`{"name":"empty","account_id":"acct-empty","saved_at":"2026-03-08T00:00:00Z","tokens":{}}`), 0o600); err != nil {
		t.Fatalf("write empty profile: %v", err)
	}

	out := captureStdout(t, func() {
		if err := codexProfileStatus(); err != nil {
			t.Fatalf("codexProfileStatus returned error: %v", err)
		}
	})
	if !strings.Contains(out, "empty (acct-empty): no credentials") {
		t.Fatalf("unexpected status output:\n%s", out)
	}
}

func TestCodexAuthRefreshPath_UsesCODEXHOMEAndDeleteMissingProfile(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if got := codexAuthRefreshPath(); got != filepath.Join(codexHome, "auth.json") {
		t.Fatalf("codexAuthRefreshPath() = %q", got)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if err := codexProfileDelete("missing"); err == nil || !strings.Contains(err.Error(), `profile "missing" not found`) {
		t.Fatalf("codexProfileDelete(missing) = %v", err)
	}
}

func TestLoadCodexAuthForRefresh_FlatShapeAndErrors(t *testing.T) {
	t.Run("supports flat auth.json shape", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CODEX_HOME", "")

		codexDir := filepath.Join(home, ".codex")
		if err := os.MkdirAll(codexDir, 0o700); err != nil {
			t.Fatalf("mkdir .codex: %v", err)
		}
		authPath := filepath.Join(codexDir, "auth.json")
		content := `{
  "access_token":"flat_access",
  "refresh_token":"flat_refresh",
  "id_token":"flat_id",
  "account_id":"flat_account",
  "OPENAI_API_KEY":"flat_api_key"
}`
		if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
			t.Fatalf("write auth.json: %v", err)
		}

		creds, err := loadCodexAuthForRefresh()
		if err != nil {
			t.Fatalf("loadCodexAuthForRefresh: %v", err)
		}
		if creds.AccessToken != "flat_access" || creds.RefreshToken != "flat_refresh" || creds.IDToken != "flat_id" || creds.AccountID != "flat_account" || creds.APIKey != "flat_api_key" {
			t.Fatalf("unexpected creds: %+v", creds)
		}
	})

	t.Run("invalid json returns parse error", func(t *testing.T) {
		codexHome := t.TempDir()
		t.Setenv("CODEX_HOME", codexHome)
		if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte("{bad"), 0o600); err != nil {
			t.Fatalf("write invalid auth.json: %v", err)
		}

		_, err := loadCodexAuthForRefresh()
		if err == nil || !strings.Contains(err.Error(), "invalid auth.json format") {
			t.Fatalf("expected parse error, got %v", err)
		}
	})

	t.Run("missing access token returns guidance", func(t *testing.T) {
		codexHome := t.TempDir()
		t.Setenv("CODEX_HOME", codexHome)
		if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"tokens":{"refresh_token":"r"}}`), 0o600); err != nil {
			t.Fatalf("write auth.json: %v", err)
		}

		_, err := loadCodexAuthForRefresh()
		if err == nil || !strings.Contains(err.Error(), "no access_token") {
			t.Fatalf("expected missing access token error, got %v", err)
		}
	})
}

func TestRunCodexCommand_AdditionalHelpPaths(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	t.Run("non profile subcommand prints help", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "other"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand returned error: %v", err)
			}
		})
		if !strings.Contains(out, "Codex Profile Management") {
			t.Fatalf("expected help output, got:\n%s", out)
		}
	})

	t.Run("profile without command prints help", func(t *testing.T) {
		os.Args = []string{"onwatch", "codex", "profile"}
		out := captureStdout(t, func() {
			if err := runCodexCommand(); err != nil {
				t.Fatalf("runCodexCommand returned error: %v", err)
			}
		})
		if !strings.Contains(out, "Codex Profile Management") {
			t.Fatalf("expected help output, got:\n%s", out)
		}
	})
}

func TestListCodexProfiles_ReadDirError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	dataDir := filepath.Join(home, ".onwatch", "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir .onwatch/data: %v", err)
	}
	profilesPath := filepath.Join(dataDir, "codex-profiles")
	if err := os.WriteFile(profilesPath, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("write profiles placeholder file: %v", err)
	}

	_, err := listCodexProfiles()
	if err == nil || !strings.Contains(err.Error(), "failed to read profiles directory") {
		t.Fatalf("expected read-dir error, got %v", err)
	}
}

func TestCodexProfileSave_WarnsOnSameProfileAccountChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", "new_id", "acct_new")
	writeProfileFile(t, home, "work", "old_access", "old_refresh", "old_id", "acct_old")

	out := captureStdout(t, func() {
		if err := codexProfileSave("work"); err != nil {
			t.Fatalf("codexProfileSave returned error: %v", err)
		}
	})
	if !strings.Contains(out, `Warning: Profile "work" was for account acct_old, updating to account acct_new`) {
		t.Fatalf("expected profile account-change warning, got:\n%s", out)
	}
}

func TestCodexProfileRefresh_InvalidName(t *testing.T) {
	err := codexProfileRefresh("bad name")
	if err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("expected invalid profile name error, got %v", err)
	}
}
