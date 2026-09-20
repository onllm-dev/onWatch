//go:build !windows

package api

import (
	"context"
	"path/filepath"
	"testing"
)

// isolateMuseCredentials points every Muse credential source at an empty temp
// home so detection cannot read the developer's real keychain or login file.
func isolateMuseCredentials(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("META_API_KEY", "")
	t.Setenv("MUSE_AUTH_PATH", filepath.Join(home, "missing-auth.json"))
	// MuseSettingsPath checks XDG_CONFIG_HOME before HOME, so without this
	// ResolveMuseModel reads the developer's real ~/.config/muse/settings.json
	// and the resolved model becomes environment-dependent.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	// Never let a miss fall through to the developer's real Keychain or
	// secret-service keyring: it would prompt, stall, or find a live key.
	origLookup := museKeychainLookup
	museKeychainLookup = func(context.Context, string, ...string) ([]byte, error) {
		return nil, context.Canceled
	}
	t.Cleanup(func() { museKeychainLookup = origLookup })
	InvalidateMuseCredentialsCache()
	t.Cleanup(InvalidateMuseCredentialsCache)
}

func TestDetectMuseCredentialsCachedServesRepeatCalls(t *testing.T) {
	isolateMuseCredentials(t)
	t.Setenv("META_API_KEY", "test-key")

	first := DetectMuseCredentialsCached(nil)
	if first == nil || first.APIKey != "test-key" {
		t.Fatalf("first detection = %+v, want the env key", first)
	}

	// A changed environment must not be observed until the cache is dropped:
	// that is what proves the second call did not re-probe.
	t.Setenv("META_API_KEY", "rotated-key")
	if second := DetectMuseCredentialsCached(nil); second == nil || second.APIKey != "test-key" {
		t.Fatalf("second detection = %+v, want the cached key", second)
	}

	InvalidateMuseCredentialsCache()
	if third := DetectMuseCredentialsCached(nil); third == nil || third.APIKey != "rotated-key" {
		t.Fatalf("after invalidation = %+v, want the rotated key", third)
	}
}

func TestDetectMuseCredentialsCachedCachesMisses(t *testing.T) {
	isolateMuseCredentials(t)

	if got := DetectMuseCredentialsCached(nil); got != nil {
		t.Fatalf("detection = %+v, want nil with no credentials present", got)
	}
	// The miss is cached, so a key appearing afterwards stays unseen until the
	// TTL lapses or the cache is invalidated.
	t.Setenv("META_API_KEY", "late-key")
	if got := DetectMuseCredentialsCached(nil); got != nil {
		t.Fatalf("detection = %+v, want the cached miss", got)
	}

	InvalidateMuseCredentialsCache()
	if got := DetectMuseCredentialsCached(nil); got == nil || got.APIKey != "late-key" {
		t.Fatalf("after invalidation = %+v, want the late key", got)
	}
}

func TestDetectMuseCredentialsCachedReturnsCopy(t *testing.T) {
	isolateMuseCredentials(t)
	t.Setenv("META_API_KEY", "test-key")

	first := DetectMuseCredentialsCached(nil)
	if first == nil {
		t.Fatal("expected credentials")
	}
	first.APIKey = "mutated"

	if second := DetectMuseCredentialsCached(nil); second == nil || second.APIKey != "test-key" {
		t.Fatalf("second detection = %+v, want the cache to be unaffected by caller mutation", second)
	}
}
