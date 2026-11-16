//go:build !windows

package api

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// newCursorStateDB creates a throwaway state.vscdb under a sandbox home and
// returns its path. The real Cursor Desktop database is never touched.
func newCursorStateDB(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		t.Fatal("getCursorStateDBPath returned empty for sandbox home")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatalf("create ItemTable: %v", err)
	}
	return dbPath
}

func setCursorStateValue(t *testing.T, dbPath, key, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)", key, value); err != nil {
		t.Fatalf("insert %s: %v", key, err)
	}
}

func getCursorStateValue(t *testing.T, dbPath, key string) string {
	t.Helper()
	value, err := readCursorStateValue(dbPath, key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return value
}

// stubCursorKeychain installs a keychain reader/writer pair for the duration
// of the test, so no test shells out to `security` or the real keychain.
func stubCursorKeychain(t *testing.T, access, refresh string) (writes *[]string) {
	t.Helper()
	origRead := cursorReadKeychainAuth
	origWrite := cursorWriteKeychain
	origKeyring := cursorWriteLinuxKeyring
	writes = &[]string{}
	cursorReadKeychainAuth = func(logger *slog.Logger) (string, string) {
		return access, refresh
	}
	cursorWriteKeychain = func(service, value string) error {
		*writes = append(*writes, service+"="+value)
		return nil
	}
	cursorWriteLinuxKeyring = func(service, value string) error {
		*writes = append(*writes, service+"="+value)
		return nil
	}
	t.Cleanup(func() {
		cursorReadKeychainAuth = origRead
		cursorWriteKeychain = origWrite
		cursorWriteLinuxKeyring = origKeyring
	})
	return writes
}

// With an opaque token in Cursor's SQLite DB, detection prefers the Keychain
// JWT. A refresh of that Keychain credential must not write anything back into
// the SQLite DB: the encrypted Desktop values must stay byte-for-byte intact.
func TestWriteCursorCredentials_KeychainSourceLeavesSQLiteUntouched(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	const opaqueAccess = "cursor-safe-storage:v1:ciphertext-access"
	const opaqueRefresh = "cursor-safe-storage:v1:ciphertext-refresh"
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", opaqueAccess)
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", opaqueRefresh)

	keychainJWT := createTestJWT("auth0|kc_user", 1735689600)
	writes := stubCursorKeychain(t, keychainJWT, "keychain-refresh")

	creds := DetectCursorCredentials(slog.Default())
	if creds == nil {
		t.Fatal("DetectCursorCredentials returned nil")
	}
	if creds.Source != CursorSourceKeychain {
		t.Fatalf("Source = %q, want %q", creds.Source, CursorSourceKeychain)
	}

	if err := WriteCursorCredentials("refreshed-access", "refreshed-refresh", creds.Source, "keychain-refresh"); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}

	if got := getCursorStateValue(t, dbPath, "cursorAuth/accessToken"); got != opaqueAccess {
		t.Fatalf("SQLite accessToken = %q, want unchanged opaque value", got)
	}
	if got := getCursorStateValue(t, dbPath, "cursorAuth/refreshToken"); got != opaqueRefresh {
		t.Fatalf("SQLite refreshToken = %q, want unchanged opaque value", got)
	}

	if len(*writes) == 0 {
		t.Fatal("keychain writer was not called for a keychain-sourced refresh")
	}
	for _, w := range *writes {
		if w != "cursor-access-token=refreshed-access" && w != "cursor-refresh-token=refreshed-refresh" {
			t.Fatalf("unexpected keychain write %q", w)
		}
	}
}

// A SQLite-sourced refresh of a normal plain-JWT account keeps writing the new
// tokens back into the SQLite DB: no regression for existing users.
func TestWriteCursorCredentials_SQLiteSourceStillWritesBack(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	oldJWT := createTestJWT("auth0|desktop_user", 1735689600)
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", oldJWT)
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "old-refresh")

	stubCursorKeychain(t, "", "")

	creds := DetectCursorCredentials(slog.Default())
	if creds == nil || creds.Source != CursorSourceSQLite {
		t.Fatalf("creds = %+v, want a SQLite source", creds)
	}

	newJWT := createTestJWT("auth0|desktop_user", 1735699600)
	if err := WriteCursorCredentials(newJWT, "new-refresh", creds.Source, "old-refresh"); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}

	if got := getCursorStateValue(t, dbPath, "cursorAuth/accessToken"); got != newJWT {
		t.Fatalf("SQLite accessToken = %q, want refreshed JWT", got)
	}
	if got := getCursorStateValue(t, dbPath, "cursorAuth/refreshToken"); got != "new-refresh" {
		t.Fatalf("SQLite refreshToken = %q, want new-refresh", got)
	}
}

// Even if the write path is called directly with a SQLite source, an opaque
// value already in the DB must never be replaced.
func TestWriteCursorCredentials_SQLiteSourceRefusesOpaqueStoredValue(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	const opaqueAccess = "cursor-safe-storage:v1:ciphertext-access"
	const opaqueRefresh = "cursor-safe-storage:v1:ciphertext-refresh"
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", opaqueAccess)
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", opaqueRefresh)

	stubCursorKeychain(t, "", "")

	err := WriteCursorCredentials(createTestJWT("auth0|desktop_user", 1735699600), "new-refresh", CursorSourceSQLite, "old-refresh")
	if err == nil {
		t.Fatal("expected an error refusing to overwrite an opaque SQLite token")
	}

	if got := getCursorStateValue(t, dbPath, "cursorAuth/accessToken"); got != opaqueAccess {
		t.Fatalf("SQLite accessToken = %q, want unchanged opaque value", got)
	}
	if got := getCursorStateValue(t, dbPath, "cursorAuth/refreshToken"); got != opaqueRefresh {
		t.Fatalf("SQLite refreshToken = %q, want unchanged opaque value", got)
	}
}

// Item 2: the full check-plus-write call while another connection holds an
// EXCLUSIVE lock for ~200ms. An exclusive lock blocks the read-only check as
// well as the write, which is the busy window that used to abandon a save after
// the refresh token was consumed. The busy timeout must carry both through.
func TestWriteCursorCredentials_FullWriteWaitsOutBriefLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock timing test is POSIX-only")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setCursorStateValue(t, dbPath, cursorRefreshTokenKey, "old-refresh")
	stubCursorKeychain(t, "", "")

	// The holder takes an EXCLUSIVE lock when BeginTx runs.
	holder, err := sql.Open("sqlite", cursorStateDBURI(dbPath, "mode=rw&_txlock=exclusive"))
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	// Prove the exclusive lock is held: the read-only pre-check must see a busy
	// database now, not stale data. The busy timeout means this reports the
	// busy error after its 3s wait.
	if _, err := readCursorStateValue(dbPath, cursorAccessTokenKey); !isCursorSQLiteBusy(err) {
		_ = tx.Rollback()
		t.Fatalf("read-only check error = %v, want a busy/locked error while the exclusive lock is held", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = tx.Commit()
	}()

	if err := WriteCursorCredentials(createTestJWT("auth0|desktop_user", 1735699600), "new-refresh", CursorSourceSQLite, "old-refresh"); err != nil {
		t.Fatalf("WriteCursorCredentials failed while a brief lock was held: %v", err)
	}
	if got := getCursorStateValue(t, dbPath, cursorRefreshTokenKey); got != "new-refresh" {
		t.Fatalf("refreshToken = %q, want new-refresh", got)
	}
}

// The platform-specific write path is exercised above via WriteCursorCredentials;
// assert that on this host the keychain/keyring writer is only used for a
// keychain-sourced credential and never for a SQLite-sourced one.
func TestWriteCursorCredentials_SQLiteSourceDoesNotTouchKeychain(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("keychain/keyring only exists on darwin and linux")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newCursorStateDB(t)
	setCursorStateValue(t, dbPath, "cursorAuth/accessToken", createTestJWT("auth0|desktop_user", 1735689600))
	setCursorStateValue(t, dbPath, "cursorAuth/refreshToken", "old-refresh")

	writes := stubCursorKeychain(t, "", "")

	if err := WriteCursorCredentials(createTestJWT("auth0|desktop_user", 1735699600), "new-refresh", CursorSourceSQLite, "old-refresh"); err != nil {
		t.Fatalf("WriteCursorCredentials: %v", err)
	}
	if len(*writes) != 0 {
		t.Fatalf("keychain writer used for a SQLite-sourced refresh: %v", *writes)
	}
}
