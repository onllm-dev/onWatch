//go:build windows

package api

import (
	"log/slog"
	"os"
	"os/user"
	"runtime"
)

func getCursorStateDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		if u, err := user.Current(); err == nil {
			home = u.HomeDir
		}
	}
	if home == "" {
		return ""
	}
	return cursorStateDBPathForOS(home, runtime.GOOS)
}

func detectCursorAuthPlatform(logger *slog.Logger) *cursorAuthState {
	if logger == nil {
		logger = slog.Default()
	}

	accessToken, refreshToken, _, _ := readCursorSQLiteAuth(logger)
	if accessToken == "" && refreshToken == "" {
		return nil
	}

	return buildCursorAuthState(accessToken, refreshToken, CursorSourceSQLite, logger, false, false)
}

// cursorCredentialsWritable reports whether a refresh result could be saved.
// Windows has only Cursor Desktop's SQLite DB, and its encrypted value is
// always the selected one, so the guard is what prevents a doomed refresh:
// a refresh consumes a one-time refresh token.
func cursorCredentialsWritable(source string) error {
	switch source {
	case CursorSourceSQLite:
	case CursorSourceKeychain:
		return os.ErrNotExist
	default:
		return errCursorUnknownCredentialSource
	}

	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		return os.ErrNotExist
	}
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	return cursorSQLiteValuesWritable(dbPath)
}

func writeCursorCredentials(accessToken, refreshToken, source, previousRefreshToken string) error {
	if cursorTestMode.Load() {
		return nil
	}

	// Windows has no keychain: Cursor Desktop's SQLite DB is the only store,
	// so even a keychain-sourced credential cannot exist here.
	switch source {
	case CursorSourceSQLite:
	case CursorSourceKeychain:
		return os.ErrNotExist
	default:
		return errCursorUnknownCredentialSource
	}

	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		return os.ErrNotExist
	}
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	// The write transaction re-checks both guards, so the check and the write
	// are one locked section: a refused write leaves the DB untouched and a
	// busy window cannot abandon a save whose refresh token is already used.
	return writeCursorTokensToSQLite(dbPath, accessToken, refreshToken)
}
