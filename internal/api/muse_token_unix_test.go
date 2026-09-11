//go:build darwin

package api

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// A hanging Keychain approval dialog must never block daemon startup:
// the lookup gives up at the deadline and yields no key.
func TestReadMusePlatformKey_HangingKeychainTimesOut(t *testing.T) {
	origLookup := museKeychainLookup
	origTimeout := museKeychainTimeout
	museKeychainLookup = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done() // simulate an unanswered approval dialog
		return nil, ctx.Err()
	}
	museKeychainTimeout = 50 * time.Millisecond
	defer func() {
		museKeychainLookup = origLookup
		museKeychainTimeout = origTimeout
	}()

	start := time.Now()
	key, source := readMusePlatformKey(slog.Default())
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("lookup blocked for %v, must time out", elapsed)
	}
	if key != "" || source != "" {
		t.Fatalf("timed-out lookup must yield nothing, got %q/%q", key, source)
	}
}
