//go:build !windows

package api

import (
	"errors"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// stubCursorMacKeychain serves Keychain lookups from items (keyed by
// "service|account") and records every lookup, so no test shells out to
// `security`. It also forces the macOS Keychain path on any host.
func stubCursorMacKeychain(t *testing.T, items map[string]string) *[]string {
	t.Helper()
	origLookup := cursorMacKeychainLookup
	origGOOS := cursorKeychainGOOS
	origNow := cursorKeychainNow
	// Pin the clock between the test JWTs' expiries (1.7e9 is past, 1.8e9 and
	// later are live), so results never depend on the day the tests run.
	cursorKeychainNow = func() time.Time { return time.Unix(1750000000, 0) }
	var lookups []string
	cursorMacKeychainLookup = func(service, account string) (string, error) {
		key := service + "|" + account
		lookups = append(lookups, key)
		if v, ok := items[key]; ok {
			return v + "\n", nil
		}
		return "", errCursorKeychainItemNotFound
	}
	cursorKeychainGOOS = "darwin"
	t.Cleanup(func() {
		cursorMacKeychainLookup = origLookup
		cursorKeychainGOOS = origGOOS
		cursorKeychainNow = origNow
	})
	return &lookups
}

func TestCursorMacKeychainAccounts(t *testing.T) {
	cases := []struct {
		username string
		want     []string
	}{
		{"alice", []string{"alice", "cursor-user"}},
		{"", []string{"cursor-user"}},
		{"cursor-user", []string{"cursor-user"}},
	}
	for _, tc := range cases {
		if got := cursorMacKeychainAccounts(tc.username); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("cursorMacKeychainAccounts(%q) = %v, want %v", tc.username, got, tc.want)
		}
	}
}

// Issue #166: `cursor-agent login` stores its tokens under account
// "cursor-user", not the macOS username. They must still be found.
func TestReadCursorMacKeychainTokens_FindsCursorCLIAccount(t *testing.T) {
	jwt := createTestJWT("auth0|cli_user", 1893456000)
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|cursor-user":  jwt,
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})

	access, refresh, account := readCursorMacKeychainTokens("alice")
	if access != jwt || refresh != "cli-refresh" || account != "cursor-user" {
		t.Fatalf("got (%q, %q, %q), want the cursor-user pair", access, refresh, account)
	}
}

func TestReadCursorMacKeychainTokens_UsernameOnlyStillWorks(t *testing.T) {
	jwt := createTestJWT("auth0|old_user", 1893456000)
	lookups := stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|alice":  jwt,
		"cursor-refresh-token|alice": "old-refresh",
	})

	access, refresh, account := readCursorMacKeychainTokens("alice")
	if access != jwt || refresh != "old-refresh" || account != "alice" {
		t.Fatalf("got (%q, %q, %q), want the username pair", access, refresh, account)
	}
	if (*lookups)[0] != "cursor-access-token|alice" {
		t.Fatalf("first lookup = %q, want the username account first", (*lookups)[0])
	}
}

func TestReadCursorMacKeychainTokens_PrefersLaterExpiry(t *testing.T) {
	older := createTestJWT("auth0|u", 1800000000)
	newer := createTestJWT("auth0|u", 1900000000)

	t.Run("cursor-user newer", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        older,
			"cursor-refresh-token|alice":       "stale-refresh",
			"cursor-access-token|cursor-user":  newer,
			"cursor-refresh-token|cursor-user": "fresh-refresh",
		})
		access, refresh, account := readCursorMacKeychainTokens("alice")
		if access != newer || refresh != "fresh-refresh" || account != "cursor-user" {
			t.Fatalf("got (%q, %q, %q), want the newer cursor-user pair", access, refresh, account)
		}
	})

	t.Run("a JWT with no expiry beats one that expires", func(t *testing.T) {
		forever := createTestJWT("auth0|u", 0)
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        forever,
			"cursor-refresh-token|alice":       "alice-refresh",
			"cursor-access-token|cursor-user":  newer,
			"cursor-refresh-token|cursor-user": "cli-refresh",
		})
		access, _, account := readCursorMacKeychainTokens("alice")
		if access != forever || account != "alice" {
			t.Fatalf("got (%q, %q), want the pair with no expiry", access, account)
		}
	})

	t.Run("username newer", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        newer,
			"cursor-refresh-token|alice":       "fresh-refresh",
			"cursor-access-token|cursor-user":  older,
			"cursor-refresh-token|cursor-user": "stale-refresh",
		})
		access, refresh, account := readCursorMacKeychainTokens("alice")
		if access != newer || refresh != "fresh-refresh" || account != "alice" {
			t.Fatalf("got (%q, %q, %q), want the newer username pair", access, refresh, account)
		}
	})

	t.Run("equal expiry keeps username", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":       newer,
			"cursor-access-token|cursor-user": newer,
		})
		if _, _, account := readCursorMacKeychainTokens("alice"); account != "alice" {
			t.Fatalf("account = %q, want the username account on a tie", account)
		}
	})
}

// A usable JWT wins over an opaque or missing access token, and the pair is
// never mixed across accounts.
func TestReadCursorMacKeychainTokens_PrefersUsableJWTAndNeverMixesAccounts(t *testing.T) {
	jwt := createTestJWT("auth0|cli_user", 1700000000)
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|alice":       "cursor-safe-storage:v1:opaque",
		"cursor-refresh-token|alice":      "alice-refresh",
		"cursor-access-token|cursor-user": jwt,
	})

	access, refresh, account := readCursorMacKeychainTokens("alice")
	if account != "cursor-user" || access != jwt {
		t.Fatalf("got (%q, %q), want the usable cursor-user JWT", access, account)
	}
	if refresh != "" {
		t.Fatalf("refresh = %q, want empty: the username refresh token must not be paired with the cursor-user access token", refresh)
	}
}

func TestReadCursorMacKeychainTokens_NothingFound(t *testing.T) {
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|alice": "   ",
	})
	access, refresh, account := readCursorMacKeychainTokens("alice")
	if access != "" || refresh != "" || account != "" {
		t.Fatalf("got (%q, %q, %q), want nothing", access, refresh, account)
	}
}

// The full #166 scenario: Cursor Desktop's SQLite DB holds an opaque token and
// the CLI's JWT is under "cursor-user". Detection must pick the Keychain JWT.
func TestDetectCursorCredentials_OpaqueSQLiteFallsBackToCursorUserKeychain(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })

	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", "cursor-safe-storage:v1:ciphertext-access")
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "cursor-safe-storage:v1:ciphertext-refresh")

	jwt := createTestJWT("auth0|cli_user", 1893456000)
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|cursor-user":  jwt,
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})

	creds := DetectCursorCredentials(slog.Default())
	if creds == nil {
		t.Fatal("DetectCursorCredentials returned nil")
	}
	if creds.Source != CursorSourceKeychain {
		t.Fatalf("Source = %q, want %q", creds.Source, CursorSourceKeychain)
	}
	if creds.AccessToken != jwt {
		t.Fatalf("AccessToken = %q, want the cursor-user JWT", creds.AccessToken)
	}
	// The CLI's own session never shared Desktop's refresh token, so it can
	// be refreshed and written back.
	if creds.ReadOnly {
		t.Fatal("ReadOnly = true for the Cursor CLI's own cursor-user session")
	}
}

// The same opaque-SQLite case with the JWT under the username account stays
// read-only: older onWatch builds wrote Desktop's session there.
func TestDetectCursorCredentials_OpaqueSQLiteUsernameKeychainStaysReadOnly(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	u, err := user.Current()
	if err != nil || u.Username == "" || u.Username == cursorCLIKeychainAccount {
		t.Skip("needs a macOS-style username distinct from cursor-user")
	}
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", "cursor-safe-storage:v1:ciphertext-access")
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "cursor-safe-storage:v1:ciphertext-refresh")
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|" + u.Username:  createTestJWT("auth0|u", 1893456000),
		"cursor-refresh-token|" + u.Username: "user-refresh",
	})
	creds := DetectCursorCredentials(slog.Default())
	if creds == nil || creds.Source != CursorSourceKeychain {
		t.Fatalf("creds = %+v, want the keychain pair", creds)
	}
	if !creds.ReadOnly {
		t.Fatal("ReadOnly = false for a username-account copy next to an opaque Desktop token")
	}
}

func TestCursorKeychainAccountKind(t *testing.T) {
	cases := map[[2]string]string{
		{"cursor-user", "alice"}: "cursor-cli",
		{"alice", "alice"}:       "username",
		{"", "alice"}:            "other",
		{"bob", "alice"}:         "other",
	}
	for in, want := range cases {
		if got := cursorKeychainAccountKind(in[0], in[1]); got != want {
			t.Errorf("cursorKeychainAccountKind(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestCursorKeychainPairRank(t *testing.T) {
	const now = int64(1750000000)
	live := createTestJWT("auth0|u", 1800000000)
	expired := createTestJWT("auth0|u", 1700000000)
	noExp := createTestJWT("auth0|u", 0)
	tests := []struct {
		name            string
		access, refresh string
		want            int
	}{
		{"live JWT", live, "r", 3},
		{"live JWT without refresh", live, "", 3},
		{"JWT without exp claim counts as live", noExp, "", 3},
		{"expired JWT with refresh", expired, "r", 2},
		{"expired JWT without refresh", expired, "", 1},
		{"expired JWT with opaque refresh cannot be refreshed", expired, "cursor-safe-storage:v1:abc", 1},
		{"expiry exactly now has expired", createTestJWT("auth0|u", now), "", 1},
		{"opaque access token", "cursor-safe-storage:v1:abc", "r", 0},
		{"refresh token alone", "", "r", 2},
		{"opaque refresh token alone", "", "cursor-safe-storage:v1:abc", 0},
		{"garbage access token", "not-a-jwt", "r", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cursorKeychainPairRank(tt.access, tt.refresh, ExtractJWTExpiry(tt.access), now)
			if got != tt.want {
				t.Fatalf("rank = %d, want %d", got, tt.want)
			}
		})
	}
}

// A pair that works now beats one that needs a refresh, and a pair that can be
// refreshed beats one that cannot, whichever account holds it.
func TestReadCursorMacKeychainTokens_PrefersWorkingPairOverExpired(t *testing.T) {
	live := createTestJWT("auth0|u", 1800000000)
	expired := createTestJWT("auth0|u", 1700000000)
	olderExpired := createTestJWT("auth0|u", 1600000000)

	t.Run("live beats expired with refresh", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":       expired,
			"cursor-refresh-token|alice":      "alice-refresh",
			"cursor-access-token|cursor-user": live,
		})
		if _, _, account := readCursorMacKeychainTokens("alice"); account != "cursor-user" {
			t.Fatalf("account = %q, want cursor-user (the live token)", account)
		}
	})

	t.Run("refreshable expired beats newer dead pair", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        expired,
			"cursor-access-token|cursor-user":  olderExpired,
			"cursor-refresh-token|cursor-user": "cli-refresh",
		})
		access, refresh, account := readCursorMacKeychainTokens("alice")
		if account != "cursor-user" || access != olderExpired || refresh != "cli-refresh" {
			t.Fatalf("got (%q, %q, %q), want the refreshable cursor-user pair", access, refresh, account)
		}
	})

	// The reviewer's case: a stale username pair with no refresh token must
	// not win over a cursor-user account that only has its refresh token.
	t.Run("refresh-only beats dead expired pair", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        expired,
			"cursor-refresh-token|cursor-user": "cli-refresh",
		})
		access, refresh, account := readCursorMacKeychainTokens("alice")
		if account != "cursor-user" || access != "" || refresh != "cli-refresh" {
			t.Fatalf("got (%q, %q, %q), want the cursor-user refresh token", access, refresh, account)
		}
	})

	t.Run("both expired and refreshable picks later expiry", func(t *testing.T) {
		stubCursorMacKeychain(t, map[string]string{
			"cursor-access-token|alice":        olderExpired,
			"cursor-refresh-token|alice":       "a",
			"cursor-access-token|cursor-user":  expired,
			"cursor-refresh-token|cursor-user": "c",
		})
		if _, refresh, _ := readCursorMacKeychainTokens("alice"); refresh != "c" {
			t.Fatalf("refresh = %q, want the pair with the later expiry", refresh)
		}
	})
}

// A locked Keychain or a denied lookup on one account must not hide the
// other account's tokens, and must not panic or leak an error.
func TestReadCursorMacKeychainTokens_LookupErrorOnOneAccount(t *testing.T) {
	jwt := createTestJWT("auth0|u", 1800000000)
	origLookup := cursorMacKeychainLookup
	stubCursorMacKeychain(t, nil)
	cursorMacKeychainLookup = func(service, account string) (string, error) {
		if account == "alice" {
			return "", errors.New("security: User interaction is not allowed. (exit 36)")
		}
		if service == "cursor-access-token" {
			return jwt, nil
		}
		return "cli-refresh", nil
	}
	t.Cleanup(func() { cursorMacKeychainLookup = origLookup })

	access, refresh, account := readCursorMacKeychainTokens("alice")
	if access != jwt || refresh != "cli-refresh" || account != "cursor-user" {
		t.Fatalf("got (%q, %q, %q), want the readable cursor-user pair", access, refresh, account)
	}
}

// When the username lookup fails, readCursorKeychainAuth still finds the
// cursor-user account, and an empty username never queries an empty account.
func TestCursorMacKeychainAccounts_EmptyUsernameSkipsBlankAccount(t *testing.T) {
	lookups := stubCursorMacKeychain(t, nil)
	readCursorMacKeychainTokens("")
	for _, l := range *lookups {
		if l == "cursor-access-token|" || l == "cursor-refresh-token|" {
			t.Fatalf("looked up a blank account: %v", *lookups)
		}
	}
	if len(*lookups) != 2 {
		t.Fatalf("lookups = %v, want only the two cursor-user lookups", *lookups)
	}
}

// A Keychain read that hangs (a locked Keychain waiting on an unlock prompt)
// is cut off, and detection moves on as if the account were empty.
func TestLookupCursorMacKeychainValue_HangIsBounded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origCmd, origTimeout := cursorSecurityCommand, cursorKeychainLookupTimeout
	t.Cleanup(func() { cursorSecurityCommand, cursorKeychainLookupTimeout = origCmd, origTimeout })
	cursorSecurityCommand, cursorKeychainLookupTimeout = script, 200*time.Millisecond

	t.Cleanup(resetCursorKeychainStuck)

	start := time.Now()
	value, err := lookupCursorMacKeychainValue("cursor-access-token", "cursor-user")
	if err == nil || value != "" {
		t.Fatalf("got %q, %v; want an error", value, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("lookup took %v, want it cut off near the timeout", elapsed)
	}

	// The next reads of that account fail at once instead of each waiting
	// out its own timeout. Another account is still read.
	start = time.Now()
	if _, err := lookupCursorMacKeychainValue("cursor-refresh-token", "cursor-user"); !errors.Is(err, errCursorKeychainStuck) {
		t.Fatalf("err = %v, want errCursorKeychainStuck", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("follow-up read took %v, want it skipped", elapsed)
	}
	if _, err := lookupCursorMacKeychainValue("cursor-access-token", "alice"); errors.Is(err, errCursorKeychainStuck) {
		t.Fatal("a stuck cursor-user item hid the alice account")
	}

	// After the backoff, reads are tried again.
	resetCursorKeychainStuck()
	if _, err := lookupCursorMacKeychainValue("cursor-access-token", "cursor-user"); errors.Is(err, errCursorKeychainStuck) {
		t.Fatal("reads still skipped after the backoff")
	}
}

// A helper that `security` started and that keeps the output pipe open does
// not hold the read past the timeout: the wait after the kill is bounded too.
func TestLookupCursorMacKeychainValue_HeldPipeIsBounded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origCmd, origTimeout, origDelay := cursorSecurityCommand, cursorKeychainLookupTimeout, cursorKeyringWaitDelay
	t.Cleanup(func() {
		cursorSecurityCommand, cursorKeychainLookupTimeout, cursorKeyringWaitDelay = origCmd, origTimeout, origDelay
	})
	cursorSecurityCommand, cursorKeychainLookupTimeout, cursorKeyringWaitDelay = script, 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(resetCursorKeychainStuck)

	start := time.Now()
	if _, err := lookupCursorMacKeychainValue("cursor-access-token", "cursor-user"); err == nil {
		t.Fatal("want an error from a read that never answered")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("lookup took %v, want it cut off near the timeout", elapsed)
	}

	start = time.Now()
	if err := writeCursorTokenToKeychain("cursor-access-token", "alice", "v"); err == nil {
		t.Fatal("want an error from a write that never answered")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("write took %v, want it cut off near the timeout", elapsed)
	}
}

func TestLookupCursorMacKeychainValue_ReturnsOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$3|$5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := cursorSecurityCommand
	t.Cleanup(func() { cursorSecurityCommand = orig })
	cursorSecurityCommand = script
	value, err := lookupCursorMacKeychainValue("cursor-access-token", "cursor-user")
	if err != nil || value != "cursor-access-token|cursor-user\n" {
		t.Fatalf("got %q, %v", value, err)
	}
}

func TestWriteCursorTokenToKeychain_HangIsBounded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origCmd, origTimeout := cursorSecurityCommand, cursorKeychainLookupTimeout
	t.Cleanup(func() { cursorSecurityCommand, cursorKeychainLookupTimeout = origCmd, origTimeout })
	cursorSecurityCommand, cursorKeychainLookupTimeout = script, 200*time.Millisecond

	start := time.Now()
	if err := writeCursorTokenToKeychain("cursor-access-token", "cursor-user", "tok"); err == nil {
		t.Fatal("a hung Keychain write reported success")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("write took %v, want it cut off near the timeout", elapsed)
	}
}

func TestWriteCursorTokenToKeychain_PassesServiceAccountAndValue(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$*\" > \""+out+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := cursorSecurityCommand
	t.Cleanup(func() { cursorSecurityCommand = orig })
	cursorSecurityCommand = script
	if err := writeCursorTokenToKeychain("cursor-refresh-token", "alice", "tok"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if want := "add-generic-password -U -s cursor-refresh-token -a alice -w tok\n"; string(got) != want {
		t.Fatalf("security args = %q, want %q", got, want)
	}
	if err := writeCursorTokenToKeychain("cursor-refresh-token", "", "tok"); err == nil {
		t.Fatal("an empty account must not be written")
	}
}

// A Cursor Desktop (SQLite) refresh updates the Keychain copy of that same
// session even when the copy sits under the username account and the
// cursor-user account holds a better-ranked, different session. The other
// account is left alone.
func TestWriteCursorCredentials_SQLiteRefreshUpdatesMatchingKeychainAccount(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })

	dbPath := newCursorStateDB(t)
	desktopJWT := createTestJWT("auth0|desktop", 1700000000) // expired
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", desktopJWT)
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "desktop-refresh")

	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" || username == cursorCLIKeychainAccount {
		t.Skip("needs a macOS-style username distinct from cursor-user")
	}
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|" + username:  desktopJWT,
		"cursor-refresh-token|" + username: "desktop-refresh",
		"cursor-access-token|cursor-user":  createTestJWT("auth0|cli", 1800000000),
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})
	origWrite := cursorWriteKeychain
	t.Cleanup(func() { cursorWriteKeychain = origWrite })
	var writes []string
	cursorWriteKeychain = func(service, account, value string) error {
		writes = append(writes, account+"/"+service+"="+value)
		return nil
	}

	newJWT := createTestJWT("auth0|desktop", 1800000500)
	if err := writeCursorCredentials(newJWT, "desktop-refresh-2", CursorSourceSQLite, "desktop-refresh"); err != nil {
		t.Fatalf("writeCursorCredentials: %v", err)
	}
	want := []string{
		username + "/cursor-refresh-token=desktop-refresh-2",
		username + "/cursor-access-token=" + newJWT,
	}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("keychain writes = %v, want %v", writes, want)
	}
	if got := getCursorStateValue(t, dbPath, "cursorAuth/refreshToken"); got != "desktop-refresh-2" {
		t.Fatalf("SQLite refresh = %q", got)
	}
}

// No Keychain account holds the refreshed session: nothing is written there.
func TestWriteCursorCredentials_SQLiteRefreshLeavesUnrelatedKeychainAlone(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })

	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", createTestJWT("auth0|desktop", 1700000000))
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "desktop-refresh")
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|cursor-user":  createTestJWT("auth0|cli", 1800000000),
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})
	origWrite := cursorWriteKeychain
	t.Cleanup(func() { cursorWriteKeychain = origWrite })
	cursorWriteKeychain = func(service, _, value string) error {
		t.Errorf("unexpected keychain write %s", service)
		return nil
	}
	if err := writeCursorCredentials(createTestJWT("auth0|desktop", 1800000500), "desktop-refresh-2", CursorSourceSQLite, "desktop-refresh"); err != nil {
		t.Fatalf("writeCursorCredentials: %v", err)
	}
}

// A Keychain-source refresh is written to the account that holds the
// refreshed session, even when a later detection remembered the other
// account. Writing there would overwrite the Cursor CLI's own session.
func TestWriteCursorCredentials_KeychainRefreshFollowsTheSession(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	testhome.SetTestHome(t, t.TempDir()) // no Cursor Desktop DB

	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" || username == cursorCLIKeychainAccount {
		t.Skip("needs a macOS-style username distinct from cursor-user")
	}
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|" + username:  createTestJWT("auth0|old", 1700000000),
		"cursor-refresh-token|" + username: "user-refresh",
		"cursor-access-token|cursor-user":  createTestJWT("auth0|cli", 1800000000),
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})
	origWrite := cursorWriteKeychain
	t.Cleanup(func() { cursorWriteKeychain = origWrite })
	var writes []string
	cursorWriteKeychain = func(service, account, value string) error {
		writes = append(writes, account+"/"+service)
		return nil
	}

	if err := writeCursorCredentials("new-access", "user-refresh-2", CursorSourceKeychain, "user-refresh"); err != nil {
		t.Fatalf("writeCursorCredentials: %v", err)
	}
	want := []string{username + "/cursor-refresh-token", username + "/cursor-access-token"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes = %v, want %v", writes, want)
	}

	// No account holds the refreshed token any more (the CLI logged in
	// again): nothing is overwritten and the save is reported as failed.
	writes = nil
	if err := writeCursorCredentials("a", "r", CursorSourceKeychain, "unknown-refresh"); !errors.Is(err, errCursorKeychainSessionGone) {
		t.Fatalf("err = %v, want errCursorKeychainSessionGone", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %v, want none", writes)
	}
}

// Before a refresh spends the token, the Keychain must still hold that
// session somewhere, since the save writes nowhere else.
func TestCursorCredentialsWritable_KeychainNeedsTheSession(t *testing.T) {
	stubCursorMacKeychain(t, map[string]string{
		"cursor-access-token|cursor-user":  createTestJWT("auth0|cli", 1800000000),
		"cursor-refresh-token|cursor-user": "cli-refresh",
	})
	if err := cursorCredentialsWritable(CursorSourceKeychain, "cli-refresh"); err != nil {
		t.Fatalf("held session refused: %v", err)
	}
	if err := cursorCredentialsWritable(CursorSourceKeychain, "replaced-refresh"); !errors.Is(err, errCursorKeychainSessionGone) {
		t.Fatalf("err = %v, want errCursorKeychainSessionGone", err)
	}
	cursorKeychainGOOS = "linux" // no accounts on the keyring
	if err := cursorCredentialsWritable(CursorSourceKeychain, "replaced-refresh"); err != nil {
		t.Fatalf("linux keyring refused: %v", err)
	}
}

// When both accounts hold the refreshed session and one write fails, the
// other still saves the tokens and the refresh counts as saved.
func TestWriteCursorCredentials_KeychainOneAccountFailing(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	testhome.SetTestHome(t, t.TempDir())
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" || username == cursorCLIKeychainAccount {
		t.Skip("needs a macOS-style username distinct from cursor-user")
	}
	stubCursorMacKeychain(t, map[string]string{
		"cursor-refresh-token|" + username: "shared-refresh",
		"cursor-refresh-token|cursor-user": "shared-refresh",
	})
	origWrite := cursorWriteKeychain
	t.Cleanup(func() { cursorWriteKeychain = origWrite })
	var saved []string
	cursorWriteKeychain = func(service, account, value string) error {
		if account == cursorCLIKeychainAccount {
			return errors.New("security: timed out")
		}
		saved = append(saved, account+"/"+service)
		return nil
	}
	if err := writeCursorCredentials("a", "r2", CursorSourceKeychain, "shared-refresh"); err != nil {
		t.Fatalf("err = %v, want the save to count", err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved = %v, want both tokens under the username account", saved)
	}

	cursorWriteKeychain = func(string, string, string) error { return errors.New("security: timed out") }
	if err := writeCursorCredentials("a", "r2", CursorSourceKeychain, "shared-refresh"); err == nil {
		t.Fatal("every write failed but the save counted")
	}
}

// `security` exits 44 for an item that does not exist; only that counts as
// "not there". Any other failure is a read that failed.
func TestLookupCursorMacKeychainValue_NotFoundIsItsOwnError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "security")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit \"$CURSOR_TEST_EXIT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := cursorSecurityCommand
	t.Cleanup(func() { cursorSecurityCommand = orig })
	cursorSecurityCommand = script
	t.Setenv("CURSOR_TEST_EXIT", "44")
	if _, err := lookupCursorMacKeychainValue("cursor-refresh-token", "alice"); !errors.Is(err, errCursorKeychainItemNotFound) {
		t.Fatalf("exit 44: err = %v, want errCursorKeychainItemNotFound", err)
	}
	t.Setenv("CURSOR_TEST_EXIT", "36")
	if _, err := lookupCursorMacKeychainValue("cursor-refresh-token", "alice"); err == nil || errors.Is(err, errCursorKeychainItemNotFound) {
		t.Fatalf("exit 36: err = %v, want a read failure", err)
	}
}

// The pre-refresh check finds the account holding the session. If the
// Keychain then cannot be read when the refreshed tokens are saved, they still
// go to that account, since the old token is already spent. A Keychain that
// cannot be read at the check stops the refresh instead.
func TestWriteCursorCredentials_KeychainUnreadableAtSaveUsesCheckedAccount(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	testhome.SetTestHome(t, t.TempDir())
	stubCursorMacKeychain(t, map[string]string{
		"cursor-refresh-token|cursor-user": "r1",
	})
	if err := cursorCredentialsWritable(CursorSourceKeychain, "r1"); err != nil {
		t.Fatalf("check: %v", err)
	}

	locked := errors.New("security: User interaction is not allowed.")
	cursorMacKeychainLookup = func(string, string) (string, error) { return "", locked }
	origWrite := cursorWriteKeychain
	t.Cleanup(func() { cursorWriteKeychain = origWrite })
	var writes []string
	cursorWriteKeychain = func(service, account, value string) error {
		writes = append(writes, account+"/"+service)
		return nil
	}
	if err := writeCursorCredentials("a2", "r2", CursorSourceKeychain, "r1"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(writes) != 2 || writes[0] != "cursor-user/cursor-refresh-token" {
		t.Fatalf("writes = %v, want the checked cursor-user account", writes)
	}

	// Another token was never checked: nothing is written.
	writes = nil
	if err := writeCursorCredentials("a", "b", CursorSourceKeychain, "other"); !errors.Is(err, errCursorKeychainSessionGone) || len(writes) != 0 {
		t.Fatalf("err = %v, writes = %v; want no write for an unchecked token", err, writes)
	}
	if err := cursorCredentialsWritable(CursorSourceKeychain, "r2"); !errors.Is(err, errCursorKeychainUnreadable) {
		t.Fatalf("check with a locked Keychain: err = %v, want errCursorKeychainUnreadable", err)
	}
}

func resetCursorKeychainStuck() {
	cursorKeychainStuck.Lock()
	cursorKeychainStuck.until = map[string]time.Time{}
	cursorKeychainStuck.Unlock()
}
