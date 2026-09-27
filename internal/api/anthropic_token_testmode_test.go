package api

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// fakeRealHome makes dir stand in for the account's real home directory for
// the duration of the test, so the test-mode guard can be exercised without
// ever pointing a test at the developer's actual profile. The recorded real
// homes stay guarded: dir is added to them, never swapped in for them.
func fakeRealHome(t *testing.T, dir string) {
	t.Helper()
	realHomesMu.Lock()
	saved := realHomes
	realHomes = append(append([]string(nil), saved...), dir)
	realHomesMu.Unlock()
	t.Cleanup(func() {
		realHomesMu.Lock()
		realHomes = saved
		realHomesMu.Unlock()
	})
}

func TestSetTestModeCapturesRealHome(t *testing.T) {
	realHomesMu.Lock()
	captured, homes := realHomesCaptured, len(realHomes)
	realHomesMu.Unlock()
	if !captured || homes == 0 {
		t.Fatalf("TestMain enabled test mode, so the real home must be recorded (captured=%v, homes=%d)", captured, homes)
	}
}

func TestAnthropicCredentialsFileRefusesRealHomeInTestMode(t *testing.T) {
	realHome := t.TempDir()
	fakeRealHome(t, realHome)
	testhome.SetTestHome(t, realHome)

	claudeDir := filepath.Join(realHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(claudeDir, ".credentials.json")
	original := `{"claudeAiOauth":{"accessToken":"real-access","refreshToken":"real-refresh","expiresAt":4102444800000}}`
	if err := os.WriteFile(credPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := DetectAnthropicToken(nil); got != "" {
		t.Errorf("DetectAnthropicToken read the real credentials file in test mode: %q", got)
	}
	if got := DetectAnthropicCredentials(nil); got != nil {
		t.Errorf("DetectAnthropicCredentials read the real credentials file in test mode: %+v", got)
	}
	if err := WriteAnthropicCredentials("new-access", "new-refresh", 3600); err == nil {
		t.Error("WriteAnthropicCredentials must fail rather than rotate the real credentials file")
	} else if runtime.GOOS == "windows" && !errors.Is(err, errRealCredentialsInTestMode) {
		t.Errorf("err = %v, want errRealCredentialsInTestMode", err)
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("real credentials file was modified in test mode: %s", data)
	}
	if _, err := os.Stat(credPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("no backup may be written next to the real credentials file, stat err = %v", err)
	}
}

func TestAnthropicCredentialsFileAllowsSandboxHomeInTestMode(t *testing.T) {
	fakeRealHome(t, t.TempDir())
	home := t.TempDir()
	testhome.SetTestHome(t, home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"sandbox-access","refreshToken":"r","expiresAt":4102444800000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DetectAnthropicToken(nil); got != "sandbox-access" {
		t.Fatalf("DetectAnthropicToken() = %q, want sandbox-access", got)
	}
}

func TestAnthropicHomeBlocked(t *testing.T) {
	// Test mode stays on here: turning it off, even briefly, would let any
	// concurrently running test reach the real keychain.
	realHome := t.TempDir()
	fakeRealHome(t, realHome)

	if !anthropicHomeBlocked(realHome) {
		t.Fatal("real home must be blocked in test mode")
	}
	if !anthropicHomeBlocked(realHome + string(filepath.Separator)) {
		t.Fatal("real home with a trailing separator must be blocked")
	}
	if anthropicHomeBlocked(t.TempDir()) {
		t.Fatal("a sandbox home must not be blocked")
	}
}

// fakeRealHome must add to the recorded real homes, not replace them:
// otherwise the developer's actual home is unguarded while such a test runs.
func TestFakeRealHomeKeepsRecordedRealHomesBlocked(t *testing.T) {
	realHomesMu.Lock()
	recorded := append([]string(nil), realHomes...)
	realHomesMu.Unlock()
	if len(recorded) == 0 {
		t.Fatal("TestMain enabled test mode, so a real home must be recorded")
	}

	fakeRealHome(t, t.TempDir())

	for _, home := range recorded {
		if !anthropicHomeBlocked(home) {
			t.Errorf("recorded real home %q is no longer blocked while a fake real home is set", home)
		}
	}
}
