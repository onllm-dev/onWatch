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

func TestDetectKimiCredentials_BothCLIs_PrefersFresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// clear overrides
	t.Setenv("KIMI_CODE_CREDENTIALS", "")
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("KIMI_SHARE_DIR", "")
	t.Setenv("KIMI_HOME", "")
	t.Setenv("KIMI_CREDENTIALS", "")

	// kimi-cli: expired access, has refresh
	cliHome := filepath.Join(home, ".kimi")
	writeKimiCred(t, cliHome, "cli-access", "cli-refresh", float64(time.Now().Unix()-3600))

	// kimi-code: fresh access
	codeHome := filepath.Join(home, ".kimi-code")
	writeKimiCred(t, codeHome, "code-access", "code-refresh", float64(time.Now().Unix()+3600))

	// Force re-detect without cache pollution
	InvalidateKimiCredentialsCache()

	// Candidates should include both when HOME is set - but os.UserHomeDir may not use HOME on all systems.
	// Set both via env overrides for portability.
	t.Setenv("KIMI_CODE_HOME", codeHome)
	t.Setenv("KIMI_SHARE_DIR", cliHome)
	InvalidateKimiCredentialsCache()

	all := DetectAllKimiCredentials(nil)
	if len(all) < 2 {
		t.Fatalf("expected both credential stores, got %d: %+v", len(all), pathsOf(all))
	}

	best := DetectKimiCredentials(nil)
	if best == nil {
		t.Fatal("expected credentials")
	}
	if best.AccessToken != "code-access" {
		t.Fatalf("expected fresh kimi-code token, got source=%s token=%s path=%s", best.Source, best.AccessToken, best.Path)
	}
	if best.Source != "kimi-code" {
		t.Fatalf("source=%s", best.Source)
	}
}

func TestDetectKimiCredentials_FallsBackToKimiCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_CREDENTIALS", "")
	t.Setenv("KIMI_CREDENTIALS", "")

	cliHome := filepath.Join(home, ".kimi")
	writeKimiCred(t, cliHome, "cli-access", "cli-refresh", float64(time.Now().Unix()+3600))

	// No kimi-code dir
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, "missing-code"))
	t.Setenv("KIMI_SHARE_DIR", cliHome)
	InvalidateKimiCredentialsCache()

	best := DetectKimiCredentials(nil)
	if best == nil {
		t.Fatal("expected kimi-cli credentials")
	}
	if best.AccessToken != "cli-access" {
		t.Fatalf("token=%s path=%s", best.AccessToken, best.Path)
	}
	if best.Source != "kimi-cli" {
		t.Fatalf("source=%s", best.Source)
	}
}

func TestKimiSourceLabel(t *testing.T) {
	if got := kimiSourceLabel("/home/u/.kimi-code/credentials/kimi-code.json"); got != "kimi-code" {
		t.Fatalf("got %s", got)
	}
	if got := kimiSourceLabel("/home/u/.kimi/credentials/kimi-code.json"); got != "kimi-cli" {
		t.Fatalf("got %s", got)
	}
}

func pathsOf(cs []*KimiCredentials) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}
