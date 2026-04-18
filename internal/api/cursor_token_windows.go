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

	return buildCursorAuthState(accessToken, refreshToken, "sqlite", logger, false)
}

func writeCursorCredentials(accessToken, refreshToken string) error {
	if cursorTestMode.Load() {
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
