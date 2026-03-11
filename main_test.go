package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/web"
)

func TestConfigLoad_WithOnlyCodexAuthFile_AllowsEmptyProviderConfig(t *testing.T) {
	homeDir := t.TempDir()
	codexHome := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", homeDir)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("SYNTHETIC_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("ANTHROPIC_TOKEN", "")
	t.Setenv("COPILOT_TOKEN", "")
	t.Setenv("CODEX_TOKEN", "")
	t.Setenv("ANTIGRAVITY_ENABLED", "")
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "")

	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"codex_oauth_access"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() returned unexpected error: %v", err)
	}
	if len(cfg.AvailableProviders()) != 0 {
		t.Fatalf("expected no configured providers, got %v", cfg.AvailableProviders())
	}
}

func TestHasFlagAndHasCommand(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() {
		os.Args = origArgs
	})

	os.Args = []string{"onwatch", "--debug", "status", "--test"}

	if !hasFlag("--debug") {
		t.Fatal("hasFlag should find existing flag")
	}
	if hasFlag("--missing") {
		t.Fatal("hasFlag should return false for missing flag")
	}
	if !hasCommand("status", "start") {
		t.Fatal("hasCommand should match any provided command")
	}
	if hasCommand("update", "start") {
		t.Fatal("hasCommand should return false when no command matches")
	}
}

func TestSha256hexAndDeriveEncryptionKey(t *testing.T) {
	input := "onwatch"
	want := sha256.Sum256([]byte(input))
	wantHex := hex.EncodeToString(want[:])
	originalSalt := append([]byte(nil), web.GetEncryptionSalt()...)
	web.SetEncryptionSalt(nil)
	t.Cleanup(func() {
		web.SetEncryptionSalt(originalSalt)
	})

	if got := sha256hex(input); got != wantHex {
		t.Fatalf("sha256hex mismatch: got %q want %q", got, wantHex)
	}

	if got := deriveEncryptionKey(wantHex); got != wantHex {
		t.Fatalf("deriveEncryptionKey should return pre-hashed value, got %q", got)
	}

	nonHash := "plain-password"
	if got := deriveEncryptionKey(nonHash); got != sha256hex(nonHash) {
		t.Fatalf("deriveEncryptionKey should hash non-hex input, got %q", got)
	}

	if got := deriveLegacyEncryptionKey(wantHex); got != wantHex {
		t.Fatalf("deriveLegacyEncryptionKey should return pre-hashed value, got %q", got)
	}
	if got := deriveLegacyEncryptionKey(nonHash); got != sha256hex(nonHash) {
		t.Fatalf("deriveLegacyEncryptionKey should hash non-hex input, got %q", got)
	}
}

func TestDeriveEncryptionKey_UsesEncryptionSalt(t *testing.T) {
	passwordHash := sha256hex("onwatch-admin")
	salt := []byte("0123456789abcdef")
	originalSalt := append([]byte(nil), web.GetEncryptionSalt()...)
	web.SetEncryptionSalt(salt)
	t.Cleanup(func() {
		web.SetEncryptionSalt(originalSalt)
	})

	got := deriveEncryptionKey(passwordHash)
	want := web.DeriveEncryptionKey(passwordHash, nil)
	if got != want {
		t.Fatalf("deriveEncryptionKey() = %q, want %q", got, want)
	}
	if got == passwordHash {
		t.Fatalf("deriveEncryptionKey() unexpectedly used legacy unsalted key %q", got)
	}
}

func TestStatusLogCandidates(t *testing.T) {
	t.Run("prefers db directory then home then cwd", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		dbPath := filepath.Join(t.TempDir(), "data", "onwatch.db")
		got := statusLogCandidates(dbPath, "main.log", "menubar.log")

		want := []string{
			filepath.Join(filepath.Dir(dbPath), "main.log"),
			filepath.Join(filepath.Dir(dbPath), "menubar.log"),
			filepath.Join(homeDir, ".onwatch", "main.log"),
			filepath.Join(homeDir, ".onwatch", "menubar.log"),
			filepath.Join(".", "main.log"),
			filepath.Join(".", "menubar.log"),
		}
		if len(got) != len(want) {
			t.Fatalf("candidate count = %d, want %d (%v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("adds pid dir when db path missing", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		oldPIDDir := pidDir
		pidDir = t.TempDir()
		t.Cleanup(func() { pidDir = oldPIDDir })

		got := statusLogCandidates("", "main.log")
		want := []string{
			filepath.Join(homeDir, ".onwatch", "main.log"),
			filepath.Join(pidDir, "main.log"),
			filepath.Join(".", "main.log"),
		}
		if len(got) != len(want) {
			t.Fatalf("candidate count = %d, want %d (%v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("deduplicates repeated names", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		dbPath := filepath.Join(t.TempDir(), "data", "onwatch.db")
		got := statusLogCandidates(dbPath, "main.log", "main.log")

		for i := range got {
			for j := i + 1; j < len(got); j++ {
				if got[i] == got[j] {
					t.Fatalf("duplicate candidate %q in %v", got[i], got)
				}
			}
		}
	})
}

func TestFirstExistingFile(t *testing.T) {
	tmp := t.TempDir()
	first := filepath.Join(tmp, "first.log")
	second := filepath.Join(tmp, "second.log")

	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatalf("write first file: %v", err)
	}

	path, size, ok := firstExistingFile([]string{filepath.Join(tmp, "missing.log"), first, second})
	if !ok {
		t.Fatal("expected to find existing file")
	}
	if path != first {
		t.Fatalf("path = %q, want %q", path, first)
	}
	if size != int64(len("first")) {
		t.Fatalf("size = %d, want %d", size, len("first"))
	}

	if _, _, ok := firstExistingFile([]string{filepath.Join(tmp, "none-1"), filepath.Join(tmp, "none-2")}); ok {
		t.Fatal("expected no file match")
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{name: "bytes", bytes: 1023, want: "1023B"},
		{name: "one_kb", bytes: 1024, want: "1.0KB"},
		{name: "fractional_kb", bytes: 1536, want: "1.5KB"},
		{name: "one_mb", bytes: 1024 * 1024, want: "1.0MB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanSize(tt.bytes); got != tt.want {
				t.Fatalf("humanSize(%d): got %q want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestRedactAPIKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "(not set)"},
		{name: "short", in: "abcd", want: "***"},
		{name: "normal_len_8", in: "abcdefgh", want: "abcd***"},
		{name: "normal_long", in: "abcdefghijkl", want: "abcd***ijkl"},
		{name: "synthetic_len_8", in: "syn_abcdefgh", want: "syn_abcd***"},
		{name: "synthetic_long", in: "syn_abcdefghijkl", want: "syn_abcd***ijkl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactAPIKey(tt.in); got != tt.want {
				t.Fatalf("redactAPIKey(%q): got %q want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadExistingEnvAndProviderChecks(t *testing.T) {
	t.Run("missing file returns empty", func(t *testing.T) {
		env := loadExistingEnv(filepath.Join(t.TempDir(), "missing.env"))
		if anyProviderConfigured(env) {
			t.Fatal("no provider should be configured for missing file")
		}
		if allProvidersConfigured(env) {
			t.Fatal("allProvidersConfigured should be false for missing file")
		}
	})

	t.Run("parse configured values", func(t *testing.T) {
		envPath := filepath.Join(t.TempDir(), ".env")
		content := strings.Join([]string{
			"# comment",
			"SYNTHETIC_API_KEY=syn_123",
			"ZAI_API_KEY=zai_abc",
			"ANTHROPIC_TOKEN=anth_tok",
			"CODEX_TOKEN=codex_tok",
			"ANTIGRAVITY_ENABLED=true",
			"MALFORMED_LINE",
		}, "\n")
		if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
			t.Fatalf("write env file: %v", err)
		}

		env := loadExistingEnv(envPath)
		if env.syntheticKey != "syn_123" || env.zaiKey != "zai_abc" || env.anthropicToken != "anth_tok" || env.codexToken != "codex_tok" || !env.antigravityEnabled {
			t.Fatalf("unexpected parsed env: %+v", env)
		}
		if !anyProviderConfigured(env) {
			t.Fatal("expected at least one provider to be configured")
		}
		if !allProvidersConfigured(env) {
			t.Fatal("expected all providers to be configured")
		}
	})
}

func TestWriteEnvFile(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	cfg := &setupConfig{
		syntheticKey:       "syn_abc123",
		zaiKey:             "zai_secret",
		zaiBaseURL:         "https://api.z.ai/api",
		anthropicToken:     "anth_token",
		codexToken:         "codex_token",
		antigravityEnabled: true,
		adminUser:          "admin",
		adminPass:          "password",
		port:               9211,
		pollInterval:       60,
	}

	if err := writeEnvFile(envPath, cfg); err != nil {
		t.Fatalf("writeEnvFile returned error: %v", err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read written env file: %v", err)
	}
	got := string(data)

	checks := []string{
		"# onWatch Configuration",
		"# Generated by 'onwatch setup' on ",
		"SYNTHETIC_API_KEY=syn_abc123",
		"ZAI_API_KEY=zai_secret",
		"ZAI_BASE_URL=https://api.z.ai/api",
		"ANTHROPIC_TOKEN=anth_token",
		"CODEX_TOKEN=codex_token",
		"ANTIGRAVITY_ENABLED=true",
		"ONWATCH_ADMIN_USER=admin",
		"ONWATCH_ADMIN_PASS=password",
		"ONWATCH_POLL_INTERVAL=60",
		"ONWATCH_PORT=9211",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Fatalf("expected .env to contain %q\nfull content:\n%s", check, got)
		}
	}

	stat, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("expected mode 0600, got %o", stat.Mode().Perm())
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "tiny", in: "ab", want: "***"},
		{name: "mid", in: "abcd", want: "abc..."},
		{name: "long", in: "abcdefghijk", want: "abcdef...hijk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskValue(tt.in); got != tt.want {
				t.Fatalf("maskValue(%q): got %q want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGeneratePassword_Format(t *testing.T) {
	got := generatePassword()

	if strings.HasPrefix(got, "onwatch") {
		if !regexp.MustCompile(`^onwatch\d{1,5}$`).MatchString(got) {
			t.Fatalf("fallback password format mismatch: %q", got)
		}
		return
	}

	if len(got) != 12 {
		t.Fatalf("expected 12-char hex password, got length %d (%q)", len(got), got)
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("expected hex password, got %q: %v", got, err)
	}
}
