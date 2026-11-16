//go:build !windows

package api

import (
	"errors"
	"testing"
)

// L4: the refresh token is one-time use, so it must be written before the
// access token. If the second write fails, the new refresh token is already
// saved and the session survives.
func TestWriteKeychainTokens_WritesRefreshTokenFirst(t *testing.T) {
	origKeychain := cursorWriteKeychain
	origKeyring := cursorWriteLinuxKeyring
	var order []string
	fail := false
	writer := func(service, value string) error {
		order = append(order, service)
		if fail && service == "cursor-access-token" {
			return errors.New("access write failed")
		}
		return nil
	}
	cursorWriteKeychain = writer
	cursorWriteLinuxKeyring = writer
	t.Cleanup(func() {
		cursorWriteKeychain = origKeychain
		cursorWriteLinuxKeyring = origKeyring
	})

	if err := writeKeychainTokens("fresh_access", "fresh_refresh"); err != nil {
		t.Fatalf("writeKeychainTokens: %v", err)
	}
	if len(order) != 2 || order[0] != "cursor-refresh-token" || order[1] != "cursor-access-token" {
		t.Fatalf("write order = %v, want refresh token then access token", order)
	}

	// A failed access write must still leave the refresh token written.
	order = nil
	fail = true
	if err := writeKeychainTokens("fresh_access", "fresh_refresh"); err == nil {
		t.Fatal("expected the access write failure to be reported")
	}
	if len(order) != 2 || order[0] != "cursor-refresh-token" {
		t.Fatalf("write order on failure = %v, want the refresh token written first", order)
	}
}

func TestWriteCursorCredentials_FailsWhenRefreshTokenIsNotPersisted(t *testing.T) {
	prevTestMode := cursorTestMode.Load()
	SetCursorTestMode(false)
	t.Cleanup(func() { SetCursorTestMode(prevTestMode) })

	origKeychain := cursorWriteKeychain
	origKeyring := cursorWriteLinuxKeyring
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
		cursorWriteKeychain = origKeychain
		cursorWriteLinuxKeyring = origKeyring
	})

	err := writeCursorCredentials("fresh_access", "fresh_refresh", CursorSourceKeychain, "old_refresh")
	if err == nil {
		t.Fatal("expected writeCursorCredentials to fail when refresh token persistence fails")
	}
}
