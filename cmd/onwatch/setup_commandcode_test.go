package main

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
)

func stubCommandCodeDetect(t *testing.T, creds *api.CommandCodeCredentials) {
	t.Helper()
	orig := detectCommandCodeCredentialsFunc
	detectCommandCodeCredentialsFunc = func(*slog.Logger) *api.CommandCodeCredentials { return creds }
	t.Cleanup(func() { detectCommandCodeCredentialsFunc = orig })
}

func TestCollectCommandCodeDetectedAndEnabled(t *testing.T) {
	stubCommandCodeDetect(t, &api.CommandCodeCredentials{APIKey: "user_login", Source: "commandcode-auth"})
	r := bufio.NewReader(strings.NewReader("y\n"))
	enabled, key := collectCommandCode(r, testLogger())
	if !enabled {
		t.Fatal("expected commandcode enabled")
	}
	if key != "" {
		t.Fatalf("auto-detect must not store a key, got %q", key)
	}
}

func TestCollectCommandCodeDetectedButDeclined(t *testing.T) {
	stubCommandCodeDetect(t, &api.CommandCodeCredentials{APIKey: "user_login", Source: "commandcode-auth"})
	r := bufio.NewReader(strings.NewReader("n\n"))
	enabled, key := collectCommandCode(r, testLogger())
	if enabled || key != "" {
		t.Fatalf("expected disabled, got enabled=%v key=%q", enabled, key)
	}
}

func TestCollectCommandCodeManualKeyVerified(t *testing.T) {
	stubCommandCodeDetect(t, nil)
	origVerify := verifyCommandCodeKey
	verifyCommandCodeKey = func(key, baseURL string) (string, error) {
		return "$50.99 credits remaining, 6190 requests this period", nil
	}
	t.Cleanup(func() { verifyCommandCodeKey = origVerify })

	r := bufio.NewReader(strings.NewReader("user_manual_key\n"))
	enabled, key := collectCommandCode(r, testLogger())
	if !enabled {
		t.Fatal("expected enabled")
	}
	if key != "user_manual_key" {
		t.Fatalf("key = %q", key)
	}
}

func TestCollectCommandCodeManualKeyRejectedThenEmpty(t *testing.T) {
	stubCommandCodeDetect(t, nil)
	origVerify := verifyCommandCodeKey
	verifyCommandCodeKey = func(key, baseURL string) (string, error) {
		return "", api.ErrCommandCodeUnauthorized
	}
	t.Cleanup(func() { verifyCommandCodeKey = origVerify })

	// First a rejected key (retries), then Enter to enable with auto-detect,
	// then decline so the call returns.
	r := bufio.NewReader(strings.NewReader("user_bad\n\nn\n"))
	enabled, key := collectCommandCode(r, testLogger())
	if enabled || key != "" {
		t.Fatalf("expected disabled, got enabled=%v key=%q", enabled, key)
	}
}

func TestCollectCommandCodeVerifyFailureSavesKey(t *testing.T) {
	stubCommandCodeDetect(t, nil)
	origVerify := verifyCommandCodeKey
	verifyCommandCodeKey = func(key, baseURL string) (string, error) {
		return "", api.ErrCommandCodeServerError
	}
	t.Cleanup(func() { verifyCommandCodeKey = origVerify })

	// A network/server failure must not block saving the key.
	r := bufio.NewReader(strings.NewReader("user_offline_key\n"))
	enabled, key := collectCommandCode(r, testLogger())
	if !enabled || key != "user_offline_key" {
		t.Fatalf("expected the key saved, got enabled=%v key=%q", enabled, key)
	}
}

// TestCommandCodeEitherSourceSatisfies is the requirement check: an API key
// alone, or an auth.json alone, must each enable the provider. Neither is
// mandatory when the other is present, and no credential at all leaves the
// provider off rather than erroring.
func TestCommandCodeEitherSourceSatisfies(t *testing.T) {
	isolate := func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("COMMAND_CODE_API_KEY", "")
		t.Setenv("COMMANDCODE_API_KEY", "")
		t.Setenv("COMMANDCODE_ENABLED", "")
		t.Setenv("COMMANDCODE_AUTH_PATH", "")
	}

	// 1. API key only, no auth file.
	t.Run("api key only", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMAND_CODE_API_KEY", "user_from_env")
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.HasProvider("commandcode") {
			t.Fatal("an API key alone must enable the provider")
		}
	})

	// 2. auth.json only, no API key anywhere.
	t.Run("auth file only", func(t *testing.T) {
		isolate(t)
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, ".commandcode", "auth.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"apiKey":"user_from_file"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		creds := api.DetectCommandCodeCredentials(nil)
		if creds == nil || creds.APIKey != "user_from_file" {
			t.Fatalf("auth.json alone must resolve a key, got %+v", creds)
		}
		// What the main.go preflight does with a detected credential.
		cfg := &config.Config{}
		cfg.CommandCodeAPIKey = creds.APIKey
		cfg.CommandCodeAutoToken = true
		if !cfg.HasProvider("commandcode") {
			t.Fatal("a resolved auth.json key must enable the provider")
		}
	})

	// 3. Neither present: off, not an error.
	t.Run("neither", func(t *testing.T) {
		isolate(t)
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HasProvider("commandcode") {
			t.Fatal("no credentials must leave the provider off")
		}
		if creds := api.DetectCommandCodeCredentials(nil); creds != nil {
			t.Fatalf("detection = %+v, want nil", creds)
		}
	})
}

func TestCommandCodePreflightDetectionEnablesProvider(t *testing.T) {
	// Mirrors the main.go preflight block: a key found in any auth file must
	// flip CommandCodeEnabled so HasProvider("commandcode") - the actual gate
	// in run() - reports true. An explicit key must not be overwritten, and an
	// explicit opt-out must win.
	t.Run("detected key enables provider", func(t *testing.T) {
		cfg := &config.Config{}
		creds := &api.CommandCodeCredentials{APIKey: "user_from_file", Source: "commandcode-auth"}
		if !cfg.CommandCodeDisabled {
			if creds != nil && creds.APIKey != "" {
				if cfg.CommandCodeAPIKey == "" {
					cfg.CommandCodeAPIKey = creds.APIKey
					cfg.CommandCodeAutoToken = true
				}
				cfg.CommandCodeEnabled = true
			}
		}
		if !cfg.HasProvider("commandcode") {
			t.Fatal("a detected auth-file key must enable the provider")
		}
		if !cfg.CommandCodeAutoToken {
			t.Error("auto-detected keys must be marked CommandCodeAutoToken")
		}
	})

	t.Run("explicit key is not overwritten", func(t *testing.T) {
		cfg := &config.Config{CommandCodeAPIKey: "user_explicit"}
		creds := &api.CommandCodeCredentials{APIKey: "user_from_file", Source: "pi-auth"}
		if !cfg.CommandCodeDisabled {
			if creds != nil && creds.APIKey != "" {
				if cfg.CommandCodeAPIKey == "" {
					cfg.CommandCodeAPIKey = creds.APIKey
					cfg.CommandCodeAutoToken = true
				}
				cfg.CommandCodeEnabled = true
			}
		}
		if cfg.CommandCodeAPIKey != "user_explicit" {
			t.Fatalf("explicit key was overwritten: %q", cfg.CommandCodeAPIKey)
		}
		if cfg.CommandCodeAutoToken {
			t.Error("explicit keys must not be marked auto-detected")
		}
		if !cfg.HasProvider("commandcode") {
			t.Fatal("an explicit key must enable the provider")
		}
	})

	t.Run("opt out wins over detection", func(t *testing.T) {
		cfg := &config.Config{CommandCodeDisabled: true, CommandCodeAPIKey: "user_from_file"}
		creds := &api.CommandCodeCredentials{APIKey: "user_from_file", Source: "commandcode-auth"}
		if !cfg.CommandCodeDisabled {
			if creds != nil && creds.APIKey != "" {
				cfg.CommandCodeEnabled = true
			}
		}
		if cfg.HasProvider("commandcode") {
			t.Fatal("COMMANDCODE_ENABLED=false must keep the provider off")
		}
	})
}
