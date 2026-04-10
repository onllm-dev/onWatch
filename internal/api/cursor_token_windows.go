//go:build windows

package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var _ = cursorTestMode // Package-level var defined in cursor_token.go

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
	return filepath.Join(home, "AppData", "Roaming", "Cursor", "User", "globalStorage", "state.vscdb")
}

func readCursorStateValue(dbPath, key string) (string, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
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

func detectCursorAuthPlatform(logger *slog.Logger) *cursorAuthState {
	if logger == nil {
		logger = slog.Default()
	}

	accessToken, refreshToken, _, _ := readCursorSQLiteAuth(logger)
	if accessToken == "" && refreshToken == "" {
		return nil
	}

	return buildCursorAuthState(accessToken, refreshToken, "sqlite", logger)
}

func readCursorSQLiteAuth(logger *slog.Logger) (string, string, string, string) {
	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		return "", "", "", ""
	}

	if _, err := os.Stat(dbPath); err != nil {
		logger.Debug("cursor: state.vscdb not found", "path", dbPath)
		return "", "", "", ""
	}

	accessToken, _ := readCursorStateValue(dbPath, "cursorAuth/accessToken")
	refreshToken, _ := readCursorStateValue(dbPath, "cursorAuth/refreshToken")
	email, _ := readCursorStateValue(dbPath, "cursorAuth/cachedEmail")
	membership, _ := readCursorStateValue(dbPath, "cursorAuth/stripeMembershipType")

	membership = strings.ToLower(strings.TrimSpace(membership))

	logger.Info("cursor: auth detected from SQLite",
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
		"membership", membership,
	)
	return accessToken, refreshToken, email, membership
}

func buildCursorAuthState(accessToken, refreshToken, source string, logger *slog.Logger) *cursorAuthState {
	state := &cursorAuthState{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Source:       source,
	}

	if accessToken != "" {
		expUnix := ExtractJWTExpiry(accessToken)
		if expUnix > 0 {
			state.ExpiresAt = time.Unix(expUnix, 0)
			state.ExpiresIn = time.Until(state.ExpiresAt)
		}
	}

	logger.Debug("cursor: auth state built",
		"source", source,
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
	)
	return state
}

func writeCursorCredentials(accessToken, refreshToken string) error {
	if cursorTestMode {
		return nil
	}

	dbPath := getCursorStateDBPath()
	if dbPath == "" {
		return os.ErrNotExist
	}
	if err := WriteCursorTokenToSQLite(dbPath, "cursorAuth/accessToken", accessToken); err != nil {
		return err
	}
	if refreshToken != "" {
		if err := WriteCursorTokenToSQLite(dbPath, "cursorAuth/refreshToken", refreshToken); err != nil {
			return err
		}
	}
	return nil
}
