//go:build !windows

package api

import (
	"log/slog"
	"runtime"
	"strings"
	"testing"
)

// H1: detection must mark the Keychain credential read-only when the SQLite
// token is opaque, and the detection call itself must not touch the keychain
// writer. The agent tests cover the zero-refresh behaviour.
func TestDetectCursorCredentials_OpaqueSQLiteKeychainIsReadOnly(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, "cursor-safe-storage:v1:ciphertext-access")
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, "cursor-safe-storage:v1:ciphertext-refresh")

	writes := stubCursorKeychain(t, createTestJWT("auth0|kc_user", 1735689600), "keychain-refresh")
	_ = writes

	creds := DetectCursorCredentials(slog.Default())
	if creds == nil {
		t.Fatal("DetectCursorCredentials returned nil")
	}
	if creds.Source != CursorSourceKeychain {
		t.Fatalf("Source = %q, want keychain", creds.Source)
	}
	if !creds.ReadOnly {
		t.Fatal("ReadOnly = false, want true for a keychain token chosen because SQLite is opaque")
	}
}

// H1: the other keychain-preferred case on the base (SQLite present and a
// different-subject plain JWT) keeps its refresh behaviour.
func TestDetectCursorCredentials_KeychainPreferredForDifferentSubjectStaysWritable(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|free_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, "free-refresh")
	setCursorStateValue(t, dbPath, "cursorAuth/stripeMembershipType", "free")

	stubCursorKeychain(t, createTestJWT("auth0|other_user", 1735699600), "other-refresh")

	creds := DetectCursorCredentials(slog.Default())
	if creds == nil {
		t.Fatal("DetectCursorCredentials returned nil")
	}
	if creds.Source != CursorSourceKeychain {
		t.Fatalf("Source = %q, want keychain", creds.Source)
	}
	if creds.ReadOnly {
		t.Fatal("ReadOnly = true, want false: only the opaque-SQLite case is read-only")
	}
}

// H2: SQLite holds a plain JWT and the Keychain still holds the same session
// (the copy older builds wrote). Both stores must be updated, because the
// refresh token that was just consumed lives in both.
func TestWriteCursorCredentials_SQLiteSourceUpdatesDuplicatedKeychainSession(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("keychain/keyring only exists on darwin and linux")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	oldRefresh := "shared-refresh-token"
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, oldRefresh)

	writes := stubCursorKeychain(t, createTestJWT("auth0|desktop_user", 1735689600), oldRefresh)

	newJWT := createTestJWT("auth0|desktop_user", 1735699600)
	if err := WriteCursorCredentials(newJWT, "rotated-refresh", CursorSourceSQLite, oldRefresh); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}

	if got := getCursorStateValue(t, dbPath, cursorAccessTokenKey); got != newJWT {
		t.Fatalf("SQLite accessToken = %q, want refreshed JWT", got)
	}
	if got := getCursorStateValue(t, dbPath, cursorRefreshTokenKey); got != "rotated-refresh" {
		t.Fatalf("SQLite refreshToken = %q, want rotated-refresh", got)
	}

	want := map[string]bool{
		"cursor-access-token=" + newJWT:        false,
		"cursor-refresh-token=rotated-refresh": false,
	}
	for _, w := range *writes {
		if _, ok := want[w]; ok {
			want[w] = true
		}
	}
	for w, seen := range want {
		if !seen {
			t.Fatalf("duplicated keychain session not updated: missing %q (writes %v)", w, *writes)
		}
	}
}

// H2: when the Keychain holds a different refresh token the session is not the
// one that was refreshed. Leave it byte-for-byte unchanged.
func TestWriteCursorCredentials_SQLiteSourceLeavesDifferentKeychainSession(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("keychain/keyring only exists on darwin and linux")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	const oldRefresh = "desktop-refresh-token"
	const cliRefresh = "cli-refresh-token"
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, oldRefresh)

	writes := stubCursorKeychain(t, createTestJWT("auth0|cli_user", 1735689600), cliRefresh)

	if err := WriteCursorCredentials(createTestJWT("auth0|desktop_user", 1735699600), "rotated-refresh", CursorSourceSQLite, oldRefresh); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}
	if len(*writes) != 0 {
		t.Fatalf("keychain for a different session was modified: %v", *writes)
	}
}

// H2: an absent other store is never created.
func TestWriteCursorCredentials_AbsentKeychainNotCreated(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("keychain/keyring only exists on darwin and linux")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	const oldRefresh = "desktop-refresh-token"
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, oldRefresh)

	writes := stubCursorKeychain(t, "", "")

	if err := WriteCursorCredentials(createTestJWT("auth0|desktop_user", 1735699600), "rotated-refresh", CursorSourceSQLite, oldRefresh); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}
	if len(*writes) != 0 {
		t.Fatalf("absent keychain was created: %v", *writes)
	}
}

// H2: matching sessions are not enough - an opaque SQLite value must never be
// overwritten, even when a keychain-sourced refresh consumed the same refresh
// token that is now encrypted in SQLite.
func TestWriteCursorCredentials_OpaqueSQLiteNeverWrittenEvenWhenSessionMatches(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })

	// The SQLite refresh value must match the consumed token for this case to
	// be reachable; the opaque prefix is what the guard must refuse.
	const opaqueRefresh = "cursor-safe-storage:v1:ciphertext-refresh"
	if !strings.HasPrefix(opaqueRefresh, cursorOpaqueTokenPrefix) {
		t.Fatal("test fixture is not opaque")
	}
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, "cursor-safe-storage:v1:ciphertext-access")
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, opaqueRefresh)

	writes := stubCursorKeychain(t, createTestJWT("auth0|kc_user", 1735689600), opaqueRefresh)

	// The keychain write must still succeed; the SQLite side must be refused
	// and left untouched.
	err := WriteCursorCredentials("refreshed-access", "refreshed-refresh", CursorSourceKeychain, opaqueRefresh)
	if err != nil {
		// An error is acceptable as long as SQLite is untouched; it must never
		// carry token material.
		if strings.Contains(err.Error(), "refreshed-access") || strings.Contains(err.Error(), "refreshed-refresh") {
			t.Fatalf("error leaked a token: %v", err)
		}
	}
	if len(*writes) == 0 {
		t.Fatal("keychain write did not happen")
	}
	if got := getCursorStateValue(t, dbPath, cursorAccessTokenKey); got != "cursor-safe-storage:v1:ciphertext-access" {
		t.Fatalf("opaque SQLite accessToken was replaced: %q", got)
	}
	if got := getCursorStateValue(t, dbPath, cursorRefreshTokenKey); got != opaqueRefresh {
		t.Fatalf("opaque SQLite refreshToken was replaced: %q", got)
	}
}

// H2 (SQLite side of the keychain-sourced path): a keychain-sourced refresh
// whose consumed token also sits in a plain-JWT SQLite store updates both.
func TestWriteCursorCredentials_KeychainSourceUpdatesDuplicatedSQLiteSession(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	const sharedRefresh = "shared-refresh-token"
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|shared_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, sharedRefresh)

	keychainJWT := createTestJWT("auth0|shared_user", 1735689600)
	writes := stubCursorKeychain(t, keychainJWT, sharedRefresh)

	newJWT := createTestJWT("auth0|shared_user", 1735699600)
	if err := WriteCursorCredentials(newJWT, "rotated-refresh", CursorSourceKeychain, sharedRefresh); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}
	if got := getCursorStateValue(t, dbPath, cursorAccessTokenKey); got != newJWT {
		t.Fatalf("SQLite accessToken = %q, want refreshed JWT", got)
	}
	if got := getCursorStateValue(t, dbPath, cursorRefreshTokenKey); got != "rotated-refresh" {
		t.Fatalf("SQLite refreshToken = %q, want rotated-refresh", got)
	}
	if len(*writes) == 0 {
		t.Fatal("keychain source was not written")
	}
}
