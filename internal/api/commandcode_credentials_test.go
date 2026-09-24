package api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateCommandCodeCredentials points every auth-file source at an empty temp
// home so detection cannot read the developer's real Command Code login.
func isolateCommandCodeCredentials(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COMMAND_CODE_API_KEY", "")
	t.Setenv("COMMANDCODE_API_KEY", "")
	t.Setenv("COMMANDCODE_AUTH_PATH", "")
	return home
}

func writeCommandCodeAuthFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectCommandCodeCredentialsFromEnv(t *testing.T) {
	for _, env := range []string{"COMMAND_CODE_API_KEY", "COMMANDCODE_API_KEY"} {
		t.Run(env, func(t *testing.T) {
			isolateCommandCodeCredentials(t)
			t.Setenv(env, "user_envkey")
			creds := DetectCommandCodeCredentials(nil)
			if creds == nil || creds.APIKey != "user_envkey" || creds.Source != "env" {
				t.Fatalf("creds = %+v", creds)
			}
		})
	}
}

func TestDetectCommandCodeCredentialsEnvWinsOverFile(t *testing.T) {
	home := isolateCommandCodeCredentials(t)
	writeCommandCodeAuthFile(t, filepath.Join(home, ".commandcode", "auth.json"), `{"apiKey":"user_fromfile"}`)
	t.Setenv("COMMAND_CODE_API_KEY", "user_fromenv")

	creds := DetectCommandCodeCredentials(nil)
	if creds == nil || creds.APIKey != "user_fromenv" {
		t.Fatalf("creds = %+v, want the env key", creds)
	}
}

func TestCommandCodeCredentialFileShapes(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"cli apiKey", `{"apiKey":"user_cli","userId":"u_1","keyName":"laptop"}`, "user_cli"},
		{"bare commandcode string", `{"commandcode":"user_bare"}`, "user_bare"},
		{"pi oauth entry", `{"commandcode":{"type":"oauth","access":"user_oauth","refresh":"r","expires":2105604769064}}`, "user_oauth"},
		{"pi api entry", `{"commandcode":{"type":"api","key":"user_apikey"}}`, "user_apikey"},
		{"kebab api key", `{"command-code":"user_kebab"}`, "user_kebab"},
		{"kebab oauth entry", `{"command-code":{"type":"oauth","access":"user_kebab_oauth"}}`, "user_kebab_oauth"},
		{"unrelated provider only", `{"openai":{"type":"oauth","access":"sk-openai"}}`, ""},
		{"empty object", `{}`, ""},
		{"malformed json", `{not json`, ""},
		{"blank value", `{"apiKey":"   "}`, ""},
		{"whitespace inside value", `{"apiKey":"user key with spaces"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateCommandCodeCredentials(t)
			path := filepath.Join(home, ".commandcode", "auth.json")
			writeCommandCodeAuthFile(t, path, tc.content)
			t.Setenv("COMMANDCODE_AUTH_PATH", path)

			got := readCommandCodeAuthFile(path)
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectCommandCodeCredentialsFilePriority(t *testing.T) {
	home := isolateCommandCodeCredentials(t)
	writeCommandCodeAuthFile(t, filepath.Join(home, ".commandcode", "auth.json"), `{"apiKey":"user_cli"}`)
	writeCommandCodeAuthFile(t, filepath.Join(home, ".pi", "agent", "auth.json"), `{"commandcode":{"type":"oauth","access":"user_pi"}}`)
	writeCommandCodeAuthFile(t, filepath.Join(home, ".omp", "agent", "auth.json"), `{"commandcode":{"type":"oauth","access":"user_omp"}}`)

	creds := DetectCommandCodeCredentials(nil)
	if creds == nil || creds.APIKey != "user_cli" || creds.Source != "commandcode-auth" {
		t.Fatalf("creds = %+v, want the CLI store to win", creds)
	}

	// Removing the CLI store must fall through to the pi agent store.
	if err := os.Remove(filepath.Join(home, ".commandcode", "auth.json")); err != nil {
		t.Fatal(err)
	}
	creds = DetectCommandCodeCredentials(nil)
	if creds == nil || creds.APIKey != "user_pi" || creds.Source != "pi-auth" {
		t.Fatalf("creds = %+v, want the pi store", creds)
	}

	if err := os.Remove(filepath.Join(home, ".pi", "agent", "auth.json")); err != nil {
		t.Fatal(err)
	}
	creds = DetectCommandCodeCredentials(nil)
	if creds == nil || creds.APIKey != "user_omp" || creds.Source != "omp-auth" {
		t.Fatalf("creds = %+v, want the omp store", creds)
	}
}

func TestDetectCommandCodeCredentialsNone(t *testing.T) {
	isolateCommandCodeCredentials(t)
	if creds := DetectCommandCodeCredentials(nil); creds != nil {
		t.Fatalf("creds = %+v, want nil with no key anywhere", creds)
	}
}

func TestReadCommandCodeAuthFileRejectsPermissiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports plain files as 0666; the unix permission check does not apply")
	}
	home := isolateCommandCodeCredentials(t)
	path := filepath.Join(home, "auth.json")
	writeCommandCodeAuthFile(t, path, `{"apiKey":"user_secret"}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readCommandCodeAuthFile(path); got != "" {
		t.Fatalf("group/world-readable auth file must be ignored, got %q", got)
	}
}

func TestReadCommandCodeAuthFileMissing(t *testing.T) {
	home := isolateCommandCodeCredentials(t)
	if got := readCommandCodeAuthFile(filepath.Join(home, "nope.json")); got != "" {
		t.Fatalf("missing file = %q", got)
	}
}

func TestCommandCodeAuthPathOverride(t *testing.T) {
	isolateCommandCodeCredentials(t)
	t.Setenv("COMMANDCODE_AUTH_PATH", "/tmp/override.json")
	if got := CommandCodeAuthPath(); got != "/tmp/override.json" {
		t.Fatalf("override = %q", got)
	}
	t.Setenv("COMMANDCODE_AUTH_PATH", "  ")
	if got := CommandCodeAuthPath(); got != "" {
		t.Fatalf("blank override = %q, want empty", got)
	}
}
