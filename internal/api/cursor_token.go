package api

import (
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

	_ "modernc.org/sqlite"
)

var cursorTestMode atomic.Bool

const cursorStateDBFilename = "state.vscdb"

func SetCursorTestMode(enabled bool) {
	cursorTestMode.Store(enabled)
}

type cursorAuthState struct {
	AccessToken  string
	RefreshToken string
	Source       string // "sqlite" or "keychain"
	Email        string
	Membership   string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration
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

// buildCursorAuthState builds cursorAuthState from tokens. When includeStripeMembership
// is true, stripe membership type is read from state.vscdb when the DB path exists.
func buildCursorAuthState(accessToken, refreshToken, source string, logger *slog.Logger, includeStripeMembership bool) *cursorAuthState {
	if logger == nil {
		logger = slog.Default()
	}
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

// WriteCursorCredentials persists refreshed Cursor OAuth tokens.
// This must be called after refresh because Cursor rotates refresh tokens.
func WriteCursorCredentials(accessToken, refreshToken string) error {
	return writeCursorCredentials(accessToken, refreshToken)
}
