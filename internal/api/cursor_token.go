package api

import (
	"encoding/json"
	"log/slog"
	"time"
)

var cursorTestMode bool

func SetCursorTestMode(enabled bool) {
	cursorTestMode = enabled
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

func cursorAuthToCredentials(state *cursorAuthState) *CursorCredentials {
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

// WriteCursorCredentials persists refreshed Cursor OAuth tokens.
// This must be called after refresh because Cursor rotates refresh tokens.
func WriteCursorCredentials(accessToken, refreshToken string) error {
	return writeCursorCredentials(accessToken, refreshToken)
}
