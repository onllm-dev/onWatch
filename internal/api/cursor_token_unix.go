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
	cursorWriteKeychain     = writeCursorTokenToKeychain
	cursorWriteLinuxKeyring = writeCursorTokenToLinuxKeyring
	// cursorReadKeychainAuth is a test seam so tests never shell out to
	// `security` or the Secret Service.
	cursorReadKeychainAuth = readCursorKeychainAuth
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
	keychainAccessToken, keychainRefreshToken := cursorReadKeychainAuth(logger)

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
	sqliteUsable := CursorAccessTokenUsableForAPI(sqliteAccessToken)
	keychainUsable := CursorAccessTokenUsableForAPI(keychainAccessToken)

	if sqliteAccessToken != "" || sqliteRefreshToken != "" {
		if (keychainAccessToken != "" || keychainRefreshToken != "") && !sqliteUsable && keychainUsable {
			logger.Info("cursor: SQLite token is opaque; preferring keychain JWT")
			// The Keychain JWT might be a copy older onWatch builds wrote from
			// this very Desktop session, so its refresh token may be the one
			// Desktop now holds encrypted. Refreshing it would consume that
			// token and log Desktop out, and the two cases cannot be told
			// apart. Treat the credential as read-only.
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, true)
		}
		if (keychainAccessToken != "" || keychainRefreshToken != "") && sqliteLooksFree && hasDifferentSubjects {
			logger.Info("cursor: SQLite auth looks free and differs from keychain; preferring keychain token")
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, false)
		}
		return buildCursorAuthState(sqliteAccessToken, sqliteRefreshToken, CursorSourceSQLite, logger, true, false)
	}

	if keychainAccessToken != "" || keychainRefreshToken != "" {
		return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, false)
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

// cursorWriteSQLiteTokens writes both token slots in one transaction. It is a
// seam so tests can refuse a write without touching a real database.
var cursorWriteSQLiteTokens = writeCursorTokensToSQLite

// keychainReadTokens reads the current keychain/keyring token pair. It is a
// seam over the test-only cursorReadKeychainAuth override.
func keychainReadTokens() (accessToken, refreshToken string) {
	return cursorReadKeychainAuth(slog.Default())
}

// cursorCredentialsWritable reports whether a refresh result could be saved for
// this source. A refresh consumes a one-time refresh token, so the agent checks
// this first and skips the refresh when the result could not be persisted.
func cursorCredentialsWritable(source string) error {
	switch source {
	case CursorSourceSQLite:
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return os.ErrNotExist
		}
		if _, err := os.Stat(dbPath); err != nil {
			return err
		}
		return cursorSQLiteValuesWritable(dbPath)
	case CursorSourceKeychain:
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return os.ErrNotExist
		}
		return nil
	default:
		return errCursorUnknownCredentialSource
	}
}

// writeKeychainTokens writes the token pair to the platform keychain/keyring.
func writeKeychainTokens(accessToken, refreshToken string) error {
	var errs []error
	writeValue := func(service, value string) error {
		if runtime.GOOS == "darwin" {
			return cursorWriteKeychain(service, value)
		}
		return cursorWriteLinuxKeyring(service, value)
	}

	// The refresh token is one-time use: write it first, so a failure on the
	// second write leaves the new refresh token usable instead of a consumed
	// one. Writing the access token first would strand the CLI otherwise.
	if refreshToken != "" {
		if err := writeValue("cursor-refresh-token", refreshToken); err != nil {
			errs = append(errs, err)
		}
	}
	if err := writeValue("cursor-access-token", accessToken); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func writeCursorCredentials(accessToken, refreshToken, source, previousRefreshToken string) error {
	if cursorTestMode.Load() {
		return nil
	}

	switch source {
	case CursorSourceSQLite:
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return os.ErrNotExist
		}
		if _, err := os.Stat(dbPath); err != nil {
			return err
		}
		// The write transaction re-checks both guards, so the check and the
		// write are one locked section: a busy window cannot land between them
		// and abandon a save whose refresh token is already consumed.
		if err := cursorWriteSQLiteTokens(dbPath, accessToken, refreshToken); err != nil {
			return err
		}

		// The base wrote every refresh to both stores, so the keychain may
		// still hold the session whose refresh token was just consumed. Update
		// it only when it is exactly the same session; never create it. A
		// failure here is not fatal: the source store already holds the new
		// tokens, and a stale copy later becomes read-only (H1).
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return nil
		}
		_, keychainRefreshToken := keychainReadTokens()
		if !cursorRefreshTokensEqual(keychainRefreshToken, previousRefreshToken) {
			return nil
		}
		_ = writeKeychainTokens(accessToken, refreshToken)
		return nil

	case CursorSourceKeychain:
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return os.ErrNotExist
		}
		if err := writeKeychainTokens(accessToken, refreshToken); err != nil {
			return err
		}

		// The other store is Cursor Desktop's SQLite DB. Update it only when it
		// still holds the session that was just refreshed, and only if its
		// stored values pass the opaque-token guard. The keychain source was
		// already saved, so the duplicate is best-effort.
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return nil
		}
		if _, err := os.Stat(dbPath); err != nil {
			return nil
		}
		sqliteRefreshToken, err := readCursorStateValue(dbPath, cursorRefreshTokenKey)
		if err != nil {
			// The keychain save already succeeded; an unreadable other store
			// just means the duplicate cannot be confirmed, so leave it alone.
			return nil
		}
		if !cursorRefreshTokensEqual(strings.TrimSpace(sqliteRefreshToken), previousRefreshToken) {
			return nil
		}
		// Guards are re-checked inside the write transaction. The keychain
		// source is already saved, so a refused duplicate is best-effort.
		_ = cursorWriteSQLiteTokens(dbPath, accessToken, refreshToken)
		return nil

	default:
		return errCursorUnknownCredentialSource
	}
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
