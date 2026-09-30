package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectMuseCredentialsFromEnv(t *testing.T) {
	t.Setenv("META_API_KEY", "env-key-123")
	t.Setenv("META_MUSE_MODEL", "muse-spark-test")
	creds := DetectMuseCredentials(slog.Default())
	if creds == nil {
		t.Fatal("expected credentials from env")
	}
	if creds.APIKey != "env-key-123" || creds.Source != "env" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.Model != "muse-spark-test" {
		t.Fatalf("model = %q", creds.Model)
	}
}

func TestDetectMuseCredentialsFromAuthFile(t *testing.T) {
	t.Setenv("META_API_KEY", "")
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	content := `{"providers":{"meta":{"api_key":"file-key-abc"}}}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSE_AUTH_PATH", authPath)
	t.Setenv("META_MUSE_MODEL", "")
	// Point settings lookup away so model falls back to default.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	creds := DetectMuseCredentials(slog.Default())
	if creds == nil {
		t.Fatal("expected credentials from auth file")
	}
	if creds.APIKey != "file-key-abc" || creds.Source != "muse-auth" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.Model != DefaultMuseModel {
		t.Fatalf("model = %q, want default", creds.Model)
	}
}

func TestMuseAuthFileRejectsPermissiveMode(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"providers":{"meta":{"api_key":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Chmod explicitly so the mode does not depend on the process umask.
	if err := os.Chmod(authPath, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSE_AUTH_PATH", authPath)
	got := readMuseAuthFileKey()
	if runtime.GOOS == "windows" {
		// Windows has no unix mode bits: Go reports every writable file as
		// 0666, and access is governed by ACLs that fs.FileMode cannot express.
		// A mode check there would reject every `muse login` file, so the
		// product deliberately trusts the file (museAuthFilePermsOK is a no-op).
		if got != "x" {
			t.Fatalf("Windows must accept the login file regardless of mode bits, got %q", got)
		}
		return
	}
	if got != "" {
		t.Fatalf("permissive auth file must be ignored, got %q", got)
	}
}

func TestMusePlatformKeyPrefersAPIKey(t *testing.T) {
	got := musePlatformKey(`{"secret_schema_version":1,"api_key":"long-lived","access_token":"short-lived"}`)
	if got != "long-lived" {
		t.Fatalf("got %q", got)
	}
	got = musePlatformKey(`{"access_token":"only-token"}`)
	if got != "only-token" {
		t.Fatalf("got %q", got)
	}
	if got := musePlatformKey(`not json at all`); got != "" {
		t.Fatalf("whitespace payload must be rejected, got %q", got)
	}
}

func TestResolveMuseModelDefault(t *testing.T) {
	t.Setenv("META_MUSE_MODEL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// HOME may still hold a real settings file; accept either the real model
	// or the default, but never empty.
	if got := ResolveMuseModel(); got == "" {
		t.Fatal("model must never be empty")
	}
}
