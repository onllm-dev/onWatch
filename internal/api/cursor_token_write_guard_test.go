package api

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// The shared SQLite guard and atomic-write tests in this file are deliberately
// platform-neutral: Windows has no keychain and its encrypted SQLite value is
// the selected one, so the pre-check is the only thing that prevents a doomed
// refresh there.

// newGuardTestStateDB creates a throwaway state.vscdb under a sandbox home. The
// real Cursor Desktop database is never touched.
func newGuardTestStateDB(t *testing.T) string {
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

func setGuardStateValue(t *testing.T, dbPath, key, value string) {
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

func getGuardStateValue(t *testing.T, dbPath, key string) string {
	t.Helper()
	value, err := readCursorStateValue(dbPath, key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return value
}

// M4: CursorCredentialsWritable must reject a store whose values would fail
// the guard, so the agent can skip a refresh whose result could not be saved.
func TestCursorCredentialsWritable_SQLiteOpaqueRefused(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newGuardTestStateDB(t)
	setGuardStateValue(t, dbPath, cursorAccessTokenKey, "cursor-safe-storage:v1:ciphertext-access")
	setGuardStateValue(t, dbPath, cursorRefreshTokenKey, "cursor-safe-storage:v1:ciphertext-refresh")

	if err := CursorCredentialsWritable(CursorSourceSQLite); err == nil {
		t.Fatal("CursorCredentialsWritable accepted an opaque SQLite store")
	}
}

func TestCursorCredentialsWritable_SQLitePlainJWTAccepted(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newGuardTestStateDB(t)
	setGuardStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setGuardStateValue(t, dbPath, cursorRefreshTokenKey, "plain-refresh")

	if err := CursorCredentialsWritable(CursorSourceSQLite); err != nil {
		t.Fatalf("CursorCredentialsWritable refused a plain-JWT store: %v", err)
	}
}

// M4: the SQLite write is all-or-nothing. A failure writing the refresh key
// must leave the access key at its previous value.
func TestWriteCursorTokensToSQLite_IsAtomic(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newGuardTestStateDB(t)
	oldJWT := createTestJWT("auth0|desktop_user", 1735689600)
	setGuardStateValue(t, dbPath, cursorAccessTokenKey, oldJWT)
	setGuardStateValue(t, dbPath, cursorRefreshTokenKey, "old-refresh")

	// Drop the table between the guard and the write by pointing the write at a
	// DB where ItemTable does not accept the refresh key: the whole transaction
	// must roll back.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_refresh BEFORE INSERT ON ItemTable
		WHEN NEW.key = 'cursorAuth/refreshToken'
		BEGIN SELECT RAISE(ABORT, 'refresh write refused'); END`); err != nil {
		db.Close()
		t.Fatalf("create trigger: %v", err)
	}
	db.Close()

	newJWT := createTestJWT("auth0|desktop_user", 1735699600)
	if err := writeCursorTokensToSQLite(dbPath, newJWT, "new-refresh"); err == nil {
		t.Fatal("writeCursorTokensToSQLite succeeded despite the refused refresh write")
	}
	if got := getGuardStateValue(t, dbPath, cursorAccessTokenKey); got != oldJWT {
		t.Fatalf("accessToken = %q, want the previous value (transaction not rolled back)", got)
	}
	if got := getGuardStateValue(t, dbPath, cursorRefreshTokenKey); got != "old-refresh" {
		t.Fatalf("refreshToken = %q, want the previous value", got)
	}
}

// Item 2: the read-only open still never creates a missing DB. A user with no
// Cursor Desktop install has no state.vscdb, and detection must not invent one.
func TestReadCursorStateValue_DoesNotCreateMissingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	if _, err := readCursorStateValue(dbPath, cursorAccessTokenKey); err == nil {
		t.Fatal("read succeeded against a missing state DB")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("state DB was created by the read path: %v", err)
	}
}

// M2: mode=rw must not create the file. A missing state DB is a user with no
// Cursor Desktop install, and writing there would invent a store.
func TestOpenCursorStateDB_DoesNotCreateMissingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s already exists", dbPath)
	}

	if err := writeCursorTokensToSQLite(dbPath, "access", "refresh"); err == nil {
		t.Fatal("write succeeded against a missing state DB")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("state DB was created by the write path: %v", err)
	}
}

// Item 3: the driver enables extended result codes, so a busy or locked
// condition can arrive as a subtype (SQLITE_BUSY_SNAPSHOT, SQLITE_LOCKED_SHAREDCACHE,
// ...). Only the primary code (low byte) decides the class.
func TestIsCursorSQLiteBusyCode(t *testing.T) {
	tests := []struct {
		name string
		code int
		want bool
	}{
		{name: "SQLITE_BUSY", code: 5, want: true},
		{name: "SQLITE_LOCKED", code: 6, want: true},
		{name: "SQLITE_BUSY_RECOVERY", code: 261, want: true},
		{name: "SQLITE_BUSY_SNAPSHOT", code: 517, want: true},
		{name: "SQLITE_LOCKED_SHAREDCACHE", code: 262, want: true},
		{name: "SQLITE_READONLY", code: 8, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCursorSQLiteBusyCode(tc.code); got != tc.want {
				t.Fatalf("isCursorSQLiteBusyCode(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// Item 2 (full check-plus-write under a held lock) needs a keychain stub and
// lives in cursor_token_writeback_test.go, which is !windows.

// M2: a busy timeout plus one retry must carry a write through a brief lock
// held by another connection, which is what Cursor Desktop looks like.
func TestWriteCursorTokensToSQLite_WaitsOutBriefLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		// modernc's Windows VFS does not implement the same POSIX locking
		// semantics this test relies on.
		t.Skip("lock timing test is POSIX-only")
	}
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })
	dbPath := newGuardTestStateDB(t)
	setGuardStateValue(t, dbPath, cursorAccessTokenKey, createTestJWT("auth0|desktop_user", 1735689600))
	setGuardStateValue(t, dbPath, cursorRefreshTokenKey, "old-refresh")

	holder, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			done <- err
			return
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)", "lock-probe", "x"); err != nil {
			_ = tx.Rollback()
			done <- err
			return
		}
		close(held)
		time.Sleep(200 * time.Millisecond)
		close(release)
		done <- tx.Commit()
	}()

	<-held
	err = writeCursorTokensToSQLite(dbPath, createTestJWT("auth0|desktop_user", 1735699600), "new-refresh")
	<-release
	if holdErr := <-done; holdErr != nil {
		t.Fatalf("lock holder: %v", holdErr)
	}
	if err != nil {
		t.Fatalf("write failed against a brief lock: %v", err)
	}
	if got := getGuardStateValue(t, dbPath, cursorRefreshTokenKey); got != "new-refresh" {
		t.Fatalf("refreshToken = %q, want new-refresh after the retry", got)
	}
}
