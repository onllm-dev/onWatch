//go:build !windows

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func prependPathDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	return dir
}

func writeExecutableScript(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s script: %v", name, err)
	}
}

func TestWriteCredentialsToLinuxKeyring_Success(t *testing.T) {
	binDir := prependPathDir(t)
	capturePath := filepath.Join(t.TempDir(), "secret-store.json")
	t.Setenv("SECRET_LOOKUP_OUTPUT", `{"claudeAiOauth":{"accessToken":"old","refreshToken":"old-refresh","expiresAt":1},"subscriptionType":"pro"}`)
	t.Setenv("SECRET_STORE_CAPTURE", capturePath)

	writeExecutableScript(t, binDir, "secret-tool", `
cmd="$1"
shift
case "$cmd" in
  lookup)
    printf '%s' "${SECRET_LOOKUP_OUTPUT}"
    ;;
  store)
    cat > "${SECRET_STORE_CAPTURE}"
    ;;
  *)
    exit 2
    ;;
esac`)

	if err := writeCredentialsToLinuxKeyring("new-access", "new-refresh", 1800); err != nil {
		t.Fatalf("writeCredentialsToLinuxKeyring() error = %v", err)
	}

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read secret-tool capture: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}

	oauth, ok := raw["claudeAiOauth"].(map[string]any)
	if !ok {
		t.Fatalf("missing OAuth section: %+v", raw)
	}
	if got, _ := oauth["accessToken"].(string); got != "new-access" {
		t.Fatalf("accessToken = %q, want new-access", got)
	}
	if got, _ := oauth["refreshToken"].(string); got != "new-refresh" {
		t.Fatalf("refreshToken = %q, want new-refresh", got)
	}
	if got, _ := raw["subscriptionType"].(string); got != "pro" {
		t.Fatalf("subscriptionType = %q, want pro", got)
	}
}

func TestWriteCredentialsToLinuxKeyring_ErrorPaths(t *testing.T) {
	t.Run("secret-tool missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := writeCredentialsToLinuxKeyring("a", "b", 60)
		if err == nil || !strings.Contains(err.Error(), "secret-tool not found") {
			t.Fatalf("error = %v, want secret-tool not found", err)
		}
	})

	t.Run("lookup error", func(t *testing.T) {
		binDir := prependPathDir(t)
		writeExecutableScript(t, binDir, "secret-tool", `
cmd="$1"
shift
if [ "$cmd" = "lookup" ]; then
  exit 1
fi
cat >/dev/null`)
		err := writeCredentialsToLinuxKeyring("a", "b", 60)
		if err == nil || !strings.Contains(err.Error(), "read keyring") {
			t.Fatalf("error = %v, want read keyring", err)
		}
	})

	t.Run("invalid keyring JSON", func(t *testing.T) {
		binDir := prependPathDir(t)
		t.Setenv("SECRET_LOOKUP_OUTPUT", "not-json")
		writeExecutableScript(t, binDir, "secret-tool", `
cmd="$1"
shift
if [ "$cmd" = "lookup" ]; then
  printf '%s' "${SECRET_LOOKUP_OUTPUT}"
  exit 0
fi
cat >/dev/null`)
		err := writeCredentialsToLinuxKeyring("a", "b", 60)
		if err == nil || !strings.Contains(err.Error(), "parse keyring JSON") {
			t.Fatalf("error = %v, want parse keyring JSON", err)
		}
	})
}

func TestWriteCredentialsToKeychain_SuccessAndErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		binDir := prependPathDir(t)
		capturePath := filepath.Join(t.TempDir(), "security-add.json")
		t.Setenv("SECURITY_FIND_OUTPUT", `{"other":"keep"}`)
		t.Setenv("SECURITY_ADD_CAPTURE", capturePath)

		writeExecutableScript(t, binDir, "security", `
cmd="$1"
shift
case "$cmd" in
  find-generic-password)
    printf '%s' "${SECURITY_FIND_OUTPUT}"
    ;;
  delete-generic-password)
    exit 0
    ;;
  add-generic-password)
    value=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-w" ]; then
        value="$2"
        shift 2
        continue
      fi
      shift
    done
    printf '%s' "$value" > "${SECURITY_ADD_CAPTURE}"
    ;;
  *)
    exit 2
    ;;
esac`)

		if err := writeCredentialsToKeychain("new-access", "new-refresh", 1200); err != nil {
			t.Fatalf("writeCredentialsToKeychain() error = %v", err)
		}

		data, err := os.ReadFile(capturePath)
		if err != nil {
			t.Fatalf("read security capture: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal security capture: %v", err)
		}

		oauth, ok := raw["claudeAiOauth"].(map[string]any)
		if !ok {
			t.Fatalf("missing OAuth section in keychain payload: %+v", raw)
		}
		if got, _ := oauth["accessToken"].(string); got != "new-access" {
			t.Fatalf("accessToken = %q, want new-access", got)
		}
		if got, _ := oauth["refreshToken"].(string); got != "new-refresh" {
			t.Fatalf("refreshToken = %q, want new-refresh", got)
		}
		if got, _ := raw["other"].(string); got != "keep" {
			t.Fatalf("other = %q, want keep", got)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		binDir := prependPathDir(t)
		writeExecutableScript(t, binDir, "security", `
cmd="$1"
shift
if [ "$cmd" = "find-generic-password" ]; then
  exit 1
fi
exit 0`)
		err := writeCredentialsToKeychain("a", "b", 60)
		if err == nil || !strings.Contains(err.Error(), "read Keychain") {
			t.Fatalf("error = %v, want read Keychain", err)
		}
	})
}

func TestDarwinAnthropicKeychainPaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific keychain detection behavior")
	}

	binDir := prependPathDir(t)
	capturePath := filepath.Join(t.TempDir(), "security-add.json")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SECURITY_ADD_CAPTURE", capturePath)

	writeAnthropicCredentialsFile(t, home, `{"claudeAiOauth":{"accessToken":"file-old","refreshToken":"file-refresh","expiresAt":1}}`)

	t.Run("detect token and credentials from keychain", func(t *testing.T) {
		t.Setenv("SECURITY_FIND_OUTPUT", `{"claudeAiOauth":{"accessToken":"keychain-token","refreshToken":"keychain-refresh","expiresAt":4102444800000}}`)
		writeExecutableScript(t, binDir, "security", `
cmd="$1"
shift
case "$cmd" in
  find-generic-password)
    printf '%s' "${SECURITY_FIND_OUTPUT}"
    ;;
  delete-generic-password)
    exit 0
    ;;
  add-generic-password)
    value=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-w" ]; then
        value="$2"
        shift 2
        continue
      fi
      shift
    done
    printf '%s' "$value" > "${SECURITY_ADD_CAPTURE}"
    ;;
  *)
    exit 2
    ;;
esac`)

		token := detectAnthropicTokenPlatform(nil)
		if token != "keychain-token" {
			t.Fatalf("detectAnthropicTokenPlatform() = %q, want keychain-token", token)
		}

		creds := detectAnthropicCredentialsPlatform(nil)
		if creds == nil {
			t.Fatal("detectAnthropicCredentialsPlatform() returned nil")
		}
		if creds.AccessToken != "keychain-token" || creds.RefreshToken != "keychain-refresh" {
			t.Fatalf("unexpected keychain credentials: %+v", creds)
		}
	})

	t.Run("WriteAnthropicCredentials continues when keychain update fails", func(t *testing.T) {
		writeExecutableScript(t, binDir, "security", `
cmd="$1"
shift
if [ "$cmd" = "find-generic-password" ]; then
  exit 1
fi
exit 0`)

		if err := WriteAnthropicCredentials("new-file-access", "new-file-refresh", 600); err != nil {
			t.Fatalf("WriteAnthropicCredentials() error = %v", err)
		}

		data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
		if err != nil {
			t.Fatalf("read credential file: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal credential file: %v", err)
		}
		oauth, _ := raw["claudeAiOauth"].(map[string]any)
		if got, _ := oauth["accessToken"].(string); got != "new-file-access" {
			t.Fatalf("file accessToken = %q, want new-file-access", got)
		}
		if got, _ := oauth["refreshToken"].(string); got != "new-file-refresh" {
			t.Fatalf("file refreshToken = %q, want new-file-refresh", got)
		}
	})
}

func TestParseMiniMaxTimestamp_MoreBranches(t *testing.T) {
	rfc := "2026-03-09T10:11:12Z"
	ms := int64(1773051072000)
	tests := []struct {
		name    string
		input   any
		wantNil bool
	}{
		{name: "rfc3339 string", input: rfc},
		{name: "trimmed rfc3339", input: "  \n\t" + rfc + "\r "},
		{name: "int64", input: ms},
		{name: "int", input: int(ms)},
		{name: "json number", input: json.Number("1773051072000")},
		{name: "invalid json number", input: json.Number("bad"), wantNil: true},
		{name: "invalid string", input: "bad-value", wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMiniMaxTimestamp(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("parseMiniMaxTimestamp(%v) = %v, want nil", tc.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseMiniMaxTimestamp(%v) returned nil", tc.input)
			}
			if got.Location() != time.UTC {
				t.Fatalf("timestamp location = %v, want UTC", got.Location())
			}
		})
	}
}
