package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeKimiCred(t *testing.T, dir string, access, refresh string, expiresAt float64) string {
	t.Helper()
	credDir := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(credDir, "kimi-code.json")
	payload := map[string]interface{}{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"scope":         "kimi-code",
		"expires_at":    expiresAt,
		"expires_in":    900,
	}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectKimiCredentials_KimiCodeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_CREDENTIALS", "")
	t.Setenv("KIMI_CREDENTIALS", "")
	t.Setenv("KIMI_CODE_HOME", "")
	// Even if legacy kimi-cli env/paths are set, they must be ignored.
	t.Setenv("KIMI_SHARE_DIR", filepath.Join(home, ".kimi"))
	t.Setenv("KIMI_HOME", filepath.Join(home, ".kimi"))

	// Poison: legacy kimi-cli store with "better" token — must NOT be selected.
	cliHome := filepath.Join(home, ".kimi")
	writeKimiCred(t, cliHome, "cli-access", "cli-refresh", float64(time.Now().Unix()+7200))

	codeHome := filepath.Join(home, ".kimi-code")
	writeKimiCred(t, codeHome, "code-access", "code-refresh", float64(time.Now().Unix()+600))
	t.Setenv("KIMI_CODE_HOME", codeHome)

	InvalidateKimiCredentialsCache()
	best := DetectKimiCredentials(nil)
	if best == nil {
		t.Fatal("expected kimi-code credentials")
	}
	if best.AccessToken != "code-access" {
		t.Fatalf("expected kimi-code token, got source=%s token=%s path=%s", best.Source, best.AccessToken, best.Path)
	}
	if best.Source != "kimi-code" {
		t.Fatalf("source=%s", best.Source)
	}
}

func TestDetectKimiCredentials_IgnoresKimiCLIAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_CREDENTIALS", "")
	t.Setenv("KIMI_CREDENTIALS", "")
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, "no-code"))
	t.Setenv("KIMI_SHARE_DIR", "")
	t.Setenv("KIMI_HOME", "")

	cliHome := filepath.Join(home, ".kimi")
	writeKimiCred(t, cliHome, "cli-access", "cli-refresh", float64(time.Now().Unix()+3600))

	// No kimi-code store — detection must return nil (no kimi-cli fallback).
	InvalidateKimiCredentialsCache()
	best := DetectKimiCredentials(nil)
	if best != nil {
		t.Fatalf("expected no credentials without kimi-code, got source=%s path=%s", best.Source, best.Path)
	}
}

func TestDetectKimiCredentials_ExplicitEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeKimiCred(t, filepath.Join(home, "custom"), "env-access", "env-refresh", float64(time.Now().Unix()+3600))
	t.Setenv("KIMI_CODE_CREDENTIALS", path)
	t.Setenv("KIMI_CODE_HOME", "")
	InvalidateKimiCredentialsCache()
	best := DetectKimiCredentials(nil)
	if best == nil || best.AccessToken != "env-access" {
		t.Fatalf("expected explicit env file, got %+v", best)
	}
}

func TestKimiCredentials_ExpiredSkew(t *testing.T) {
	c := &KimiCredentials{AccessToken: "t", ExpiresAt: float64(time.Now().Unix() + 120)}
	if c.Expired() {
		t.Fatal("should not be expired")
	}
	if c.SecondsUntilExpiry() <= 0 {
		t.Fatalf("expires_in=%d", c.SecondsUntilExpiry())
	}
	c.ExpiresAt = float64(time.Now().Unix() - 1)
	if !c.Expired() {
		t.Fatal("should be expired")
	}
}

func TestKimiSourceLabel(t *testing.T) {
	if got := kimiSourceLabel("/home/u/.kimi-code/credentials/kimi-code.json"); got != "kimi-code" {
		t.Fatalf("got %s", got)
	}
	// Legacy path is no longer a recognized source label for selection.
	if got := kimiSourceLabel("/home/u/.kimi/credentials/kimi-code.json"); got == "kimi-code" {
		t.Fatalf("legacy path should not label as kimi-code, got %s", got)
	}
}
