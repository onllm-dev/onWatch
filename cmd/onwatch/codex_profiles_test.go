package main

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
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

func makeProfileIDToken(accountID, userID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `","chatgpt_user_id":"` + userID + `","user_id":"` + userID + `"}}`
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + "."
}

// makeProfileIDTokenWithoutUserID creates a JWT id_token with the chatgpt_account_id
// claim but NO chatgpt_user_id. Used to test dedup behavior when user_id is absent.
func makeProfileIDTokenWithoutUserID(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + "."
}

func writeRefreshAuthJSONWithUser(t *testing.T, home, access, refresh, account, userID string) {
	t.Helper()
	idToken := makeProfileIDToken(account, userID)
	writeRefreshAuthJSON(t, home, access, refresh, idToken, account)
}

func writeProfileFileWithUser(t *testing.T, home, name, access, refresh, account, userID string) string {
	t.Helper()
	idToken := makeProfileIDToken(account, userID)
	return writeProfileFile(t, home, name, access, refresh, idToken, account)
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

	if err := codexProfileRefresh("work", ""); err != nil {
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

	if err := codexProfileRefresh("work", ""); err != nil {
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

	err = codexProfileRefresh("work", "")
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

	if err := codexProfileRefresh("personal", ""); err != nil {
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

	err := codexProfileRefresh("work", "")
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
		if err := codexProfileSave("work", ""); err != nil {
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

	// Both profile and new auth have the same account AND same user_id -> duplicate.
	writeRefreshAuthJSONWithUser(t, home, "dup_access", "dup_refresh", "acct_dup", "user-same")
	writeProfileFileWithUser(t, home, "personal", "old_access", "old_refresh", "acct_dup", "user-same")

	err := codexProfileSave("work", "")
	if err == nil {
		t.Fatal("expected error for duplicate account+user, got nil")
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

	if err := codexProfileSave("bad name", ""); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("invalid name error = %v", err)
	}

	if err := codexProfileSave("work", ""); err == nil || !strings.Contains(err.Error(), "no Codex credentials found") {
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
		if err := codexProfileSave("work", ""); err != nil {
			t.Fatalf("codexProfileSave returned error: %v", err)
		}
	})
	if !strings.Contains(out, `Warning: Profile "work" was for account acct_old, updating to account acct_new`) {
		t.Fatalf("expected profile account-change warning, got:\n%s", out)
	}
}

func TestCodexProfileRefresh_InvalidName(t *testing.T) {
	err := codexProfileRefresh("bad name", "")
	if err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("expected invalid profile name error, got %v", err)
	}
}

func TestCodexProfileSave_AllowsSameAccountDifferentUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeProfileFileWithUser(t, home, "personal", "old_access", "old_refresh", "acct_team", "user-one")
	writeRefreshAuthJSONWithUser(t, home, "new_access", "new_refresh", "acct_team", "user-two")

	if err := codexProfileSave("work", ""); err != nil {
		t.Fatalf("codexProfileSave returned error: %v", err)
	}

	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profile count = %d, want 2", len(profiles))
	}
}

func TestCodexProfileSave_StoresUserIDFromIDToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeRefreshAuthJSONWithUser(t, home, "save_access", "save_refresh", "acct_one", "user-one")

	if err := codexProfileSave("work", ""); err != nil {
		t.Fatalf("codexProfileSave returned error: %v", err)
	}

	profile := loadProfileForTest(t, home, "work")
	if profile.UserID != "user-one" {
		t.Fatalf("profile.UserID = %q, want user-one", profile.UserID)
	}
}

func TestCodexProfileRefresh_UpdatesUserIDFromIDToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	writeProfileFileWithUser(t, home, "work", "old_access", "old_refresh", "acct_team", "user-one")
	writeRefreshAuthJSONWithUser(t, home, "new_access", "new_refresh", "acct_team", "user-two")

	if err := codexProfileRefresh("work", ""); err != nil {
		t.Fatalf("codexProfileRefresh returned error: %v", err)
	}

	profile := loadProfileForTest(t, home, "work")
	if profile.UserID != "user-two" {
		t.Fatalf("profile.UserID = %q, want user-two", profile.UserID)
	}
}

// TestCodexProfileSave_AllowsSameAccountNoUserIDRegression tests the fix for a false
// positive in isDuplicateCodexProfile: when both the existing profile and the new auth
// session have an empty user_id (account_id matches but no user_id is present in the
// JWT), the function must NOT treat them as duplicates (return false), allowing the
// save to proceed. Previously it incorrectly returned true, blocking saves.

func TestIsDuplicateCodexProfile_Direct(t *testing.T) {
	// Case: same account_id, both user_ids empty -> NOT duplicate (allow)
	p := CodexProfile{Name: "personal", AccountID: "acct_team", UserID: "", Tokens: struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}{IDToken: makeProfileIDTokenWithoutUserID("acct_team")}}
	c := &api.CodexCredentials{AccountID: "acct_team", UserID: "", IDToken: makeProfileIDTokenWithoutUserID("acct_team")}
	if got := isDuplicateCodexProfile(p, c); got {
		t.Fatalf("isDuplicateCodexProfile with both empty user_ids: got %v, want false", got)
	}
}

func TestCodexProfileSave_AllowsSameAccountNoUserIDRegression(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	// Existing profile: same account, no user_id in JWT (legacy tokens)
	idTokenNoUser := makeProfileIDTokenWithoutUserID("acct_team")
	writeProfileFile(t, home, "personal", "old_access", "old_refresh", idTokenNoUser, "acct_team")

	// New auth session: same account, no user_id in JWT
	authIDTokenNoUser := makeProfileIDTokenWithoutUserID("acct_team")
	writeRefreshAuthJSON(t, home, "new_access", "new_refresh", authIDTokenNoUser, "acct_team")

	// This should succeed: same account_id but neither has user_id, so they are
	// NOT duplicates (we can't distinguish them without user_id).
	if err := codexProfileSave("work", ""); err != nil {
		t.Fatalf("codexProfileSave returned error (false positive): %v", err)
	}

	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profile count = %d, want 2 (personal + work)", len(profiles))
	}
}

// TestCodexProfileSave_AllowsNewUserAlongsideLegacyProfile verifies that a new user
// with a known user_id can save a profile when an existing legacy profile for the same
// account has no user_id. This is the Team upgrade scenario.
func TestCodexProfileSave_AllowsNewUserAlongsideLegacyProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	// Legacy profile: same account, no user_id in JWT
	idTokenNoUser := makeProfileIDTokenWithoutUserID("acct_team")
	writeProfileFile(t, home, "legacy", "old_access", "old_refresh", idTokenNoUser, "acct_team")

	// New auth session: same account but WITH a user_id
	writeRefreshAuthJSONWithUser(t, home, "new_access", "new_refresh", "acct_team", "user-new")

	if err := codexProfileSave("teammate", ""); err != nil {
		t.Fatalf("codexProfileSave returned error (should allow new user alongside legacy): %v", err)
	}

	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profile count = %d, want 2 (legacy + teammate)", len(profiles))
	}
}

func TestIsDuplicateCodexProfile_LegacyVsKnownUser(t *testing.T) {
	// Existing profile: account_id present, no user_id (legacy)
	p := CodexProfile{Name: "legacy", AccountID: "acct_team", UserID: "", Tokens: struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}{IDToken: makeProfileIDTokenWithoutUserID("acct_team")}}

	// New creds: same account but with a known user_id
	c := &api.CodexCredentials{AccountID: "acct_team", UserID: "user-new"}
	if got := isDuplicateCodexProfile(p, c); got {
		t.Fatalf("isDuplicateCodexProfile(legacy vs known user): got %v, want false", got)
	}
}

func TestHasSavedCodexProfiles(t *testing.T) {
	// Empty string returns false
	if hasSavedCodexProfiles("") {
		t.Fatal("expected false for empty dir")
	}

	// Nonexistent directory returns false
	if hasSavedCodexProfiles("/nonexistent/path/12345") {
		t.Fatal("expected false for nonexistent dir")
	}

	// Empty directory returns false
	emptyDir := t.TempDir()
	if hasSavedCodexProfiles(emptyDir) {
		t.Fatal("expected false for empty dir")
	}

	// Directory with non-JSON files returns false
	if err := os.WriteFile(filepath.Join(emptyDir, "readme.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasSavedCodexProfiles(emptyDir) {
		t.Fatal("expected false for dir with only non-JSON files")
	}

	// Directory with a JSON file returns true
	if err := os.WriteFile(filepath.Join(emptyDir, "work.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasSavedCodexProfiles(emptyDir) {
		t.Fatal("expected true for dir with JSON file")
	}

	// Subdirectory with JSON does not count (not recursive)
	subDir := t.TempDir()
	nested := filepath.Join(subDir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "profile.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasSavedCodexProfiles(subDir) {
		t.Fatal("expected false when JSON is only in subdirectory")
	}
}

func TestParseCodexProfileArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantName     string
		wantAuthFile string
	}{
		{"empty", []string{}, "", ""},
		{"name only", []string{"Plus"}, "Plus", ""},
		{"name with auth-file", []string{"Plus", "--auth-file", "/import/auth.json"}, "Plus", "/import/auth.json"},
		{"dangling auth-file flag", []string{"Plus", "--auth-file"}, "Plus", ""},
		{"flag as first arg", []string{"--auth-file", "/x"}, "", ""},
		{"name with extra args", []string{"Work", "--verbose", "--auth-file", "/path"}, "Work", "/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, authFile := parseCodexProfileArgs(tt.args)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if authFile != tt.wantAuthFile {
				t.Errorf("authFile = %q, want %q", authFile, tt.wantAuthFile)
			}
		})
	}
}

func TestParseCodexAuthData(t *testing.T) {
	// Nested tokens shape
	nested := `{"tokens":{"access_token":"at","refresh_token":"rt","id_token":"it","account_id":"acc1"}}`
	creds, err := parseCodexAuthData([]byte(nested))
	if err != nil {
		t.Fatalf("nested shape error: %v", err)
	}
	if creds.AccessToken != "at" || creds.RefreshToken != "rt" || creds.IDToken != "it" || creds.AccountID != "acc1" {
		t.Fatalf("nested shape: unexpected creds: %+v", creds)
	}

	// Flat shape
	flat := `{"access_token":"at2","refresh_token":"rt2","id_token":"it2","account_id":"acc2"}`
	creds, err = parseCodexAuthData([]byte(flat))
	if err != nil {
		t.Fatalf("flat shape error: %v", err)
	}
	if creds.AccessToken != "at2" || creds.RefreshToken != "rt2" || creds.AccountID != "acc2" {
		t.Fatalf("flat shape: unexpected creds: %+v", creds)
	}

	// API key
	apiKey := `{"OPENAI_API_KEY":"sk-test"}`
	creds, err = parseCodexAuthData([]byte(apiKey))
	if err != nil {
		t.Fatalf("api key error: %v", err)
	}
	if creds.APIKey != "sk-test" {
		t.Fatalf("api key: got %q, want sk-test", creds.APIKey)
	}

	// Invalid JSON
	_, err = parseCodexAuthData([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// Empty JSON - no error from parse itself (validation is caller's job)
	creds, err = parseCodexAuthData([]byte("{}"))
	if err != nil {
		t.Fatalf("empty JSON error: %v", err)
	}
	if creds.AccessToken != "" {
		t.Fatalf("empty JSON: expected empty access token, got %q", creds.AccessToken)
	}
}

func TestLoadCodexAuthFromFile(t *testing.T) {
	// Valid nested auth file
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	content := `{"tokens":{"access_token":"tok","refresh_token":"ref","id_token":"id","account_id":"acc"}}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	creds, err := loadCodexAuthFromFile(authPath)
	if err != nil {
		t.Fatalf("loadCodexAuthFromFile error: %v", err)
	}
	if creds.AccessToken != "tok" || creds.AccountID != "acc" {
		t.Fatalf("unexpected creds: %+v", creds)
	}

	// Relative path rejected
	_, err = loadCodexAuthFromFile("relative/path.json")
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got: %v", err)
	}

	// Missing file
	_, err = loadCodexAuthFromFile(filepath.Join(dir, "nonexistent.json"))
	if err == nil || !strings.Contains(err.Error(), "cannot read auth file") {
		t.Fatalf("expected read error, got: %v", err)
	}

	// File with no credentials
	emptyAuth := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(emptyAuth, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = loadCodexAuthFromFile(emptyAuth)
	if err == nil || !strings.Contains(err.Error(), "no access_token or API key") {
		t.Fatalf("expected empty credentials error, got: %v", err)
	}

	// API key only - should succeed
	apiKeyAuth := filepath.Join(dir, "apikey.json")
	if err := os.WriteFile(apiKeyAuth, []byte(`{"OPENAI_API_KEY":"sk-abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err = loadCodexAuthFromFile(apiKeyAuth)
	if err != nil {
		t.Fatalf("api key auth error: %v", err)
	}
	if creds.APIKey != "sk-abc" {
		t.Fatalf("expected APIKey sk-abc, got %q", creds.APIKey)
	}
}

func TestCodexProfileSaveWithAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_TOKEN", "")

	// Write an auth file (not in the default location)
	importDir := filepath.Join(home, "import")
	if err := os.MkdirAll(importDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(importDir, "auth.json")
	content := `{"tokens":{"access_token":"import_at","refresh_token":"import_rt","id_token":"","account_id":"acct_import"}}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := codexProfileSave("imported", authPath); err != nil {
		t.Fatalf("codexProfileSave with --auth-file error: %v", err)
	}

	// Verify profile was created
	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "imported" {
		t.Fatalf("expected profile name 'imported', got %q", profiles[0].Name)
	}
	if profiles[0].AccountID != "acct_import" {
		t.Fatalf("expected account_id 'acct_import', got %q", profiles[0].AccountID)
	}
	if profiles[0].Tokens.AccessToken != "import_at" {
		t.Fatalf("expected access_token 'import_at', got %q", profiles[0].Tokens.AccessToken)
	}
}

func TestCodexProfileRefreshWithAuthFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_TOKEN", "")

	// Create an existing profile first
	profilesDir := filepath.Join(home, ".onwatch", "data", "codex-profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existingProfile := `{"name":"work","account_id":"acct_one","tokens":{"access_token":"old_at","refresh_token":"old_rt"}}`
	if err := os.WriteFile(filepath.Join(profilesDir, "work.json"), []byte(existingProfile), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write an import auth file with same account
	importDir := filepath.Join(home, "import")
	if err := os.MkdirAll(importDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(importDir, "auth.json")
	content := `{"tokens":{"access_token":"new_at","refresh_token":"new_rt","id_token":"","account_id":"acct_one"}}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := codexProfileRefresh("work", authPath); err != nil {
		t.Fatalf("codexProfileRefresh with --auth-file error: %v", err)
	}

	// Verify profile was updated
	profiles, err := listCodexProfiles()
	if err != nil {
		t.Fatalf("listCodexProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Tokens.AccessToken != "new_at" {
		t.Fatalf("expected refreshed access_token 'new_at', got %q", profiles[0].Tokens.AccessToken)
	}
	if profiles[0].Tokens.RefreshToken != "new_rt" {
		t.Fatalf("expected refreshed refresh_token 'new_rt', got %q", profiles[0].Tokens.RefreshToken)
	}
}
