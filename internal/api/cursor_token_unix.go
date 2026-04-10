//go:build !windows

package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var _ = cursorTestMode // Package-level var defined in cursor_token.go

var (
	cursorWriteSQLiteToken  = WriteCursorTokenToSQLite
	cursorWriteKeychain     = writeCursorTokenToKeychain
	cursorWriteLinuxKeyring = writeCursorTokenToLinuxKeyring
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
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
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

	sqliteAccessToken, sqliteRefreshToken, _, sqliteMembership := readCursorSQLiteAuth(logger)
	keychainAccessToken, keychainRefreshToken := readCursorKeychainAuth(logger)

	sqliteSubject := ""
	if sqliteAccessToken != "" {
		sqliteSubject = ExtractJWTSubject(sqliteAccessToken)
	}
	keychainSubject := ""
	if keychainAccessToken != "" {
		keychainSubject = ExtractJWTSubject(keychainAccessToken)
	}

	hasDifferentSubjects := sqliteSubject != "" && keychainSubject != "" && sqliteSubject != keychainSubject
	sqliteLooksFree := sqliteMembership == "free"

	if sqliteAccessToken != "" || sqliteRefreshToken != "" {
		if (keychainAccessToken != "" || keychainRefreshToken != "") && sqliteLooksFree && hasDifferentSubjects {
			logger.Info("cursor: SQLite auth looks free and differs from keychain; preferring keychain token")
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, "keychain", logger)
		}
		return buildCursorAuthState(sqliteAccessToken, sqliteRefreshToken, "sqlite", logger)
	}

	if keychainAccessToken != "" || keychainRefreshToken != "" {
		return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, "keychain", logger)
	}

	return nil
}

func readCursorSQLiteAuth(logger *slog.Logger) (accessToken, refreshToken, email, membership string) {
	if cursorTestMode {
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

func readCursorKeychainAuth(logger *slog.Logger) (accessToken, refreshToken string) {
	if cursorTestMode {
		return
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return
	}

	if runtime.GOOS == "darwin" {
		username := ""
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		if username == "" {
			return
		}

		out, err := exec.Command("security", "find-generic-password",
			"-s", "cursor-access-token",
			"-a", username,
			"-w").Output()
		if err == nil {
			accessToken = strings.TrimSpace(string(out))
		}

		out, err = exec.Command("security", "find-generic-password",
			"-s", "cursor-refresh-token",
			"-a", username,
			"-w").Output()
		if err == nil {
			refreshToken = strings.TrimSpace(string(out))
		}

		if accessToken != "" || refreshToken != "" {
			logger.Info("cursor: auth detected from macOS Keychain")
		}
		return
	}

	if runtime.GOOS == "linux" {
		out, err := exec.Command("secret-tool", "lookup",
			"service", "cursor-access-token").Output()
		if err == nil {
			accessToken = strings.TrimSpace(string(out))
		}

		out, err = exec.Command("secret-tool", "lookup",
			"service", "cursor-refresh-token").Output()
		if err == nil {
			refreshToken = strings.TrimSpace(string(out))
		}

		if accessToken != "" || refreshToken != "" {
			logger.Info("cursor: auth detected from Linux keyring")
		}
		return
	}

	return
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

	logger.Debug("cursor: auth state built",
		"source", source,
		"has_access_token", accessToken != "",
		"has_refresh_token", refreshToken != "",
		"expires_in", state.ExpiresIn.Round(time.Minute),
	)
	return state
}

func writeCursorCredentials(accessToken, refreshToken string) error {
	if cursorTestMode {
		return nil
	}

	var errs []error
	accessSaved := false
	refreshSaved := refreshToken == ""

	if dbPath := getCursorStateDBPath(); dbPath != "" {
		if _, err := os.Stat(dbPath); err == nil {
			if err := cursorWriteSQLiteToken(dbPath, "cursorAuth/accessToken", accessToken); err != nil {
				errs = append(errs, err)
			} else {
				accessSaved = true
			}
			if refreshToken != "" {
				if err := cursorWriteSQLiteToken(dbPath, "cursorAuth/refreshToken", refreshToken); err != nil {
					errs = append(errs, err)
				} else {
					refreshSaved = true
				}
			}
		}
	}

	if runtime.GOOS == "darwin" {
		if err := cursorWriteKeychain("cursor-access-token", accessToken); err != nil {
			errs = append(errs, err)
		} else {
			accessSaved = true
		}
		if refreshToken != "" {
			if err := cursorWriteKeychain("cursor-refresh-token", refreshToken); err != nil {
				errs = append(errs, err)
			} else {
				refreshSaved = true
			}
		}
	}

	if runtime.GOOS == "linux" {
		if err := cursorWriteLinuxKeyring("cursor-access-token", accessToken); err != nil {
			errs = append(errs, err)
		} else {
			accessSaved = true
		}
		if refreshToken != "" {
			if err := cursorWriteLinuxKeyring("cursor-refresh-token", refreshToken); err != nil {
				errs = append(errs, err)
			} else {
				refreshSaved = true
			}
		}
	}

	if accessSaved && refreshSaved {
		return nil
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return os.ErrNotExist
}

func writeCursorTokenToKeychain(service, value string) error {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" {
		return errors.New("cannot determine username for Keychain")
	}
	cmd := exec.Command("security", "add-generic-password",
		"-U",
		"-s", service,
		"-a", username,
		"-w", value,
	)
	return cmd.Run()
}

func writeCursorTokenToLinuxKeyring(service, value string) error {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return fmt.Errorf("secret-tool not found: %w", err)
	}
	cmd := exec.Command("secret-tool", "store",
		"--label", service,
		"service", service)
	cmd.Stdin = strings.NewReader(value)
	return cmd.Run()
}
