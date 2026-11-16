package api

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var cursorTestMode atomic.Bool

const cursorStateDBFilename = "state.vscdb"

func SetCursorTestMode(enabled bool) {
	cursorTestMode.Store(enabled)
}

type cursorAuthState struct {
	AccessToken  string
	RefreshToken string
	Source       string // CursorSourceSQLite or CursorSourceKeychain
	Email        string
	Membership   string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration
	ReadOnly     bool
}

// DetectCursorToken attempts to auto-detect the Cursor access token.
// Returns empty string if not found.
func DetectCursorToken(logger *slog.Logger) string {
	state := detectCursorAuthPlatform(logger)
	if state == nil {
		return ""
	}
	if state.AccessToken != "" {
		return state.AccessToken
	}
	return ""
}

// DetectCursorCredentials attempts to auto-detect full Cursor credentials.
// Returns nil if not found.
func DetectCursorCredentials(logger *slog.Logger) *CursorCredentials {
	state := detectCursorAuthPlatform(logger)
	if state == nil {
		return nil
	}
	return &CursorCredentials{
		AccessToken:  state.AccessToken,
		RefreshToken: state.RefreshToken,
		ExpiresAt:    state.ExpiresAt,
		ExpiresIn:    state.ExpiresIn,
		Source:       state.Source,
		ReadOnly:     state.ReadOnly,
	}
}

type cursorStateRow struct {
	Key   string
	Value string
}

func parseCursorStateRows(data []byte) map[string]string {
	var rows []cursorStateRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil
	}
	result := make(map[string]string)
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result
}

func cursorStateDBPathForOS(home, goos string) string {
	if home == "" {
		return ""
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", cursorStateDBFilename)
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User", "globalStorage", cursorStateDBFilename)
	default:
		return filepath.Join(home, ".config", "Cursor", "User", "globalStorage", cursorStateDBFilename)
	}
}

func readCursorSQLiteAuth(logger *slog.Logger) (accessToken, refreshToken, email, membership string) {
	if logger == nil {
		logger = slog.Default()
	}
	if cursorTestMode.Load() {
		return
	}

	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		logger.Debug("cursor: state.vscdb not found", "path", dbPath)
		return
	}

	at, err := readCursorStateValue(dbPath, "cursorAuth/accessToken")
	if err != nil {
		logger.Debug("cursor: failed to read accessToken from SQLite", "error", err)
	} else {
		accessToken = strings.TrimSpace(at)
	}

	rt, err := readCursorStateValue(dbPath, "cursorAuth/refreshToken")
	if err != nil {
		logger.Debug("cursor: failed to read refreshToken from SQLite", "error", err)
	} else {
		refreshToken = strings.TrimSpace(rt)
	}

	em, err := readCursorStateValue(dbPath, "cursorAuth/cachedEmail")
	if err == nil {
		email = strings.TrimSpace(em)
	}

	mt, err := readCursorStateValue(dbPath, "cursorAuth/stripeMembershipType")
	if err == nil {
		membership = strings.ToLower(strings.TrimSpace(mt))
	}

	logger.Info("cursor: auth detected from SQLite",
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
		"membership", membership,
	)
	return
}

// readCursorStateValue reads a single key from Cursor's state.vscdb (ItemTable).
func readCursorStateValue(dbPath, key string) (string, error) {
	// mode=ro is only honoured in a file: URI; a bare "path?mode=ro" DSN would
	// open the DB read-write. Keep the read path read-only. The busy timeout is
	// the same as the write connection's: Cursor Desktop can be mid-commit, and
	// without it a read fails SQLITE_BUSY immediately. mode=ro never creates a
	// missing DB.
	db, err := sql.Open("sqlite", cursorStateDBURI(dbPath, "mode=ro&_pragma=busy_timeout(3000)"))
	if err != nil {
		return "", fmt.Errorf("cursor: open state db: %w", err)
	}
	defer db.Close()

	var value string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ? LIMIT 1", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("cursor: query state db: %w", err)
	}
	return value, nil
}

// buildCursorAuthState builds cursorAuthState from tokens. When includeStripeMembership
// is true, stripe membership type is read from state.vscdb when the DB path exists.
// readOnly marks a credential that must never be refreshed (see CursorCredentials.ReadOnly).
func buildCursorAuthState(accessToken, refreshToken, source string, logger *slog.Logger, includeStripeMembership, readOnly bool) *cursorAuthState {
	if logger == nil {
		logger = slog.Default()
	}
	state := &cursorAuthState{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Source:       source,
		ReadOnly:     readOnly,
	}

	if accessToken != "" {
		expUnix := ExtractJWTExpiry(accessToken)
		if expUnix > 0 {
			state.ExpiresAt = time.Unix(expUnix, 0)
			state.ExpiresIn = time.Until(state.ExpiresAt)
		}
		if includeStripeMembership {
			membership := ""
			dbPath := getCursorStateDBPath()
			if dbPath != "" {
				mt, err := readCursorStateValue(dbPath, "cursorAuth/stripeMembershipType")
				if err == nil {
					membership = strings.ToLower(strings.TrimSpace(mt))
				}
			}
			state.Membership = membership
		}
	}

	logger.Debug("cursor: auth state built",
		"source", source,
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
		"expires_in", state.ExpiresIn.Round(time.Minute),
	)
	return state
}

// errCursorUnknownCredentialSource is returned when writeCursorCredentials is
// called without knowing which store the credential came from. Writing
// anywhere in that case risks replacing a value the user's Cursor expects.
var errCursorUnknownCredentialSource = errors.New("cursor: refusing to write credentials with an unknown source")

// errCursorOpaqueSQLiteToken is returned when a write to Cursor's SQLite DB is
// refused because the stored value is opaque (encrypted by Cursor Desktop).
// It carries no token material.
var errCursorOpaqueSQLiteToken = errors.New("cursor: refusing to overwrite an opaque SQLite token")

const (
	cursorAccessTokenKey  = "cursorAuth/accessToken"
	cursorRefreshTokenKey = "cursorAuth/refreshToken"
)

// cursorSQLiteTokenWritableValue refuses to replace an opaque/encrypted value.
// It takes the current value so the check can run inside the write transaction,
// with no window between the check and the write.
func cursorSQLiteTokenWritableValue(key, current string) error {
	if strings.TrimSpace(current) == "" {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(current), cursorOpaqueTokenPrefix) {
		return fmt.Errorf("%w: key %s", errCursorOpaqueSQLiteToken, key)
	}
	if key == cursorAccessTokenKey && !CursorAccessTokenUsableForAPI(current) {
		return fmt.Errorf("%w: key %s", errCursorOpaqueSQLiteToken, key)
	}
	return nil
}

// cursorSQLiteTokenWritable refuses to replace an opaque/encrypted value in
// Cursor's state DB. It reads the current value first. A value carrying the
// `cursor-safe-storage:` prefix is always refused. The access token must also
// look like a plain JWT, since an encrypted value is what this protects; a
// refresh token is normally an opaque string, so only the prefix rule applies
// there. The error never contains the stored or new token.
func cursorSQLiteTokenWritable(dbPath, key string) error {
	current, err := readCursorStateValue(dbPath, key)
	if err != nil {
		return err
	}
	return cursorSQLiteTokenWritableValue(key, current)
}

// cursorSQLiteValuesWritable requires both token slots to pass the guard, which
// is what makes a two-key SQLite write all-or-nothing: a refresh whose result
// could not be persisted in either slot must not be attempted at all.
//
// Cursor Desktop can hold a write lock when this post-refresh check runs, and a
// SQLITE_BUSY here would abandon the save after the refresh token was already
// consumed. One retry covers that window.
func cursorSQLiteValuesWritable(dbPath string) error {
	err := cursorSQLiteValuesWritableOnce(dbPath)
	if err != nil && isCursorSQLiteBusy(err) {
		err = cursorSQLiteValuesWritableOnce(dbPath)
	}
	return err
}

func cursorSQLiteValuesWritableOnce(dbPath string) error {
	if err := cursorSQLiteTokenWritable(dbPath, cursorAccessTokenKey); err != nil {
		return err
	}
	return cursorSQLiteTokenWritable(dbPath, cursorRefreshTokenKey)
}

// cursorRefreshTokensEqual reports whether two refresh tokens name the same
// session. Comparison is constant time because these values are credentials.
func cursorRefreshTokensEqual(stored, previous string) bool {
	if stored == "" || previous == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(previous)) == 1
}

// writeCursorTokensToSQLite writes both token slots in a single transaction, so
// a failure leaves neither slot updated. This is shared by every platform: the
// SQLite store has the same all-or-nothing requirement everywhere.
//
// Cursor Desktop can hold a write lock at the moment a refresh finishes, and
// the busy timeout only covers lock acquisition; a late lock still surfaces as
// SQLITE_BUSY. One retry handles that window without ever partially writing.
func writeCursorTokensToSQLite(dbPath, accessToken, refreshToken string) error {
	err := writeCursorTokensToSQLiteOnce(dbPath, accessToken, refreshToken)
	if err != nil && isCursorSQLiteBusy(err) {
		err = writeCursorTokensToSQLiteOnce(dbPath, accessToken, refreshToken)
	}
	return err
}

func writeCursorTokensToSQLiteOnce(dbPath, accessToken, refreshToken string) error {
	db, err := openCursorStateDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("cursor: begin state db transaction: %w", err)
	}
	defer tx.Rollback()

	// Check and write in the same transaction: the guard cannot go stale
	// between the check and the write, and there is one locked section, so a
	// busy window around a separate pre-flight read cannot abandon the save.
	for _, key := range []string{cursorAccessTokenKey, cursorRefreshTokenKey} {
		var current string
		err := tx.QueryRow("SELECT value FROM ItemTable WHERE key = ? LIMIT 1", key).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("cursor: read state db: %w", err)
		}
		if err := cursorSQLiteTokenWritableValue(key, current); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
		cursorAccessTokenKey, accessToken,
	); err != nil {
		return fmt.Errorf("cursor: write access token: %w", err)
	}
	if refreshToken != "" {
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
			cursorRefreshTokenKey, refreshToken,
		); err != nil {
			return fmt.Errorf("cursor: write refresh token: %w", err)
		}
	}
	return tx.Commit()
}

// isCursorSQLiteBusy reports whether err is a SQLITE_BUSY or SQLITE_LOCKED
// failure that a single retry can recover from.
func isCursorSQLiteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return isCursorSQLiteBusyCode(sqliteErr.Code())
}

// isCursorSQLiteBusyCode reports whether a SQLite result code is a busy or
// locked condition. The driver enables extended result codes, so the value can
// carry a subtype in the high bits (SQLITE_BUSY_SNAPSHOT 517,
// SQLITE_LOCKED_SHAREDCACHE 262, ...). Only the primary code (low byte)
// decides the class.
func isCursorSQLiteBusyCode(code int) bool {
	primary := code & 0xff
	return primary == int(sqlite3.SQLITE_BUSY) || primary == int(sqlite3.SQLITE_LOCKED)
}

// CursorCredentialsWritable reports whether a refreshed token pair could be
// saved for the given source. The agent checks this before calling the refresh
// endpoint: a refresh consumes a one-time refresh token, so it must never run
// when its result cannot be persisted.
func CursorCredentialsWritable(source string) error {
	return cursorCredentialsWritable(source)
}

// WriteCursorCredentials persists refreshed Cursor OAuth tokens.
// This must be called after refresh because Cursor rotates refresh tokens.
//
// source is the store the credential was read from (CursorSourceSQLite or
// CursorSourceKeychain). Tokens are always written back to that store, and the
// SQLite write path refuses to replace an opaque/encrypted value.
//
// previousRefreshToken is the refresh token that was just consumed. When the
// other store still holds exactly that token, the same session is duplicated
// there (the base wrote every refresh to both stores), so it is updated too.
// Otherwise the other store belongs to a different session and is left alone;
// an absent store is never created.
func WriteCursorCredentials(accessToken, refreshToken, source, previousRefreshToken string) error {
	return writeCursorCredentials(accessToken, refreshToken, source, previousRefreshToken)
}
