//go:build !windows

package api

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

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
	return cursorStateDBPathForOS(home, runtime.GOOS)
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
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, "keychain", logger, true)
		}
		return buildCursorAuthState(sqliteAccessToken, sqliteRefreshToken, "sqlite", logger, true)
	}

	if keychainAccessToken != "" || keychainRefreshToken != "" {
		return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, "keychain", logger, true)
	}

	return nil
}

func readCursorKeychainAuth(logger *slog.Logger) (accessToken, refreshToken string) {
	if cursorTestMode.Load() {
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

func writeCursorCredentials(accessToken, refreshToken string) error {
	if cursorTestMode.Load() {
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
