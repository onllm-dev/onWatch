//go:build !windows

package api

import (
	"errors"
	"testing"
)

func TestWriteCursorCredentials_FailsWhenRefreshTokenIsNotPersisted(t *testing.T) {
	SetCursorTestMode(false)

	origSQLite := cursorWriteSQLiteToken
	origKeychain := cursorWriteKeychain
	origKeyring := cursorWriteLinuxKeyring
	cursorWriteSQLiteToken = func(dbPath, key, value string) error {
		if key == "cursorAuth/refreshToken" {
			return errors.New("refresh write failed")
		}
		return nil
	}
	cursorWriteKeychain = func(service, value string) error {
		if service == "cursor-refresh-token" {
			return errors.New("refresh write failed")
		}
		return nil
	}
	cursorWriteLinuxKeyring = func(service, value string) error {
		if service == "cursor-refresh-token" {
			return errors.New("refresh write failed")
		}
		return nil
	}
	t.Cleanup(func() {
		cursorWriteSQLiteToken = origSQLite
		cursorWriteKeychain = origKeychain
		cursorWriteLinuxKeyring = origKeyring
	})

	err := writeCursorCredentials("fresh_access", "fresh_refresh")
	if err == nil {
		t.Fatal("expected writeCursorCredentials to fail when refresh token persistence fails")
	}
}
