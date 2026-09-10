//go:build !windows

package api

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Muse system credential store location written by `muse login`.
const (
	museKeychainService = "ai.meta.dev.credentials"
	museKeychainAccount = "meta"
)

// museKeychainTimeout bounds the credential-store lookup. macOS may show
// a Keychain approval dialog for a newly built binary; the daemon must
// never block startup on it - on timeout detection simply yields no key
// (set META_API_KEY to skip the lookup entirely). A variable so tests can
// shorten the deadline.
var museKeychainTimeout = 15 * time.Second

// readMusePlatformKey reads the Muse credential from the macOS Keychain or
// the Linux secret-service keyring. Returns the key and its source label.
func readMusePlatformKey(logger *slog.Logger) (string, string) {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithTimeout(context.Background(), museKeychainTimeout)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		out, err := museKeychainLookup(ctx, "security", "find-generic-password",
			"-s", museKeychainService,
			"-a", museKeychainAccount,
			"-w")
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				logger.Warn("muse: keychain lookup timed out (approve access for onwatch in Keychain, or set META_API_KEY)")
			}
			return "", ""
		}
		if key := musePlatformKey(strings.TrimSpace(string(out))); key != "" {
			return key, "keychain"
		}
		return "", ""
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return "", ""
		}
		out, err := exec.CommandContext(ctx, "secret-tool", "lookup",
			"service", museKeychainService,
			"account", museKeychainAccount).Output()
		if err != nil {
			return "", ""
		}
		if key := musePlatformKey(strings.TrimSpace(string(out))); key != "" {
			return key, "keyring"
		}
		return "", ""
	default:
		return "", ""
	}
}

// museKeychainLookup runs the platform credential lookup. A variable so
// tests can simulate a hanging Keychain approval dialog.
var museKeychainLookup = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
