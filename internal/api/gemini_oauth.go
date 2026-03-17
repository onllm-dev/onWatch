package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// GeminiOAuthTokenURL is the Google OAuth2 token endpoint.
	GeminiOAuthTokenURL = "https://oauth2.googleapis.com/token"
)

// ErrGeminiOAuthRefreshFailed indicates the OAuth token refresh failed.
var ErrGeminiOAuthRefreshFailed = errors.New("gemini oauth: token refresh failed")

// GeminiOAuthTokenResponse represents the response from Google's OAuth token endpoint.
type GeminiOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
	IDToken     string `json:"id_token"`
}

// geminiOAuthErrorResponse represents an error from the OAuth endpoint.
type geminiOAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// geminiOAuthTokenURLOverride allows tests to redirect OAuth requests to a mock server.
var geminiOAuthTokenURLOverride string

// SetGeminiOAuthTokenURLForTest overrides the OAuth token URL for testing.
// Pass empty string to reset to default.
func SetGeminiOAuthTokenURLForTest(url string) {
	geminiOAuthTokenURLOverride = url
}

// RefreshGeminiToken exchanges a refresh token for a new access token via Google's OAuth endpoint.
// Google does NOT rotate refresh tokens - the same refresh token can be reused.
func RefreshGeminiToken(ctx context.Context, refreshToken, clientID, clientSecret string) (*GeminiOAuthTokenResponse, error) {
	tokenURL := GeminiOAuthTokenURL
	if geminiOAuthTokenURLOverride != "" {
		tokenURL = geminiOAuthTokenURLOverride
	}
	return RefreshGeminiTokenWithURL(ctx, refreshToken, clientID, clientSecret, tokenURL)
}

// RefreshGeminiTokenWithURL allows specifying a custom OAuth URL (for testing).
func RefreshGeminiTokenWithURL(ctx context.Context, refreshToken, clientID, clientSecret, tokenURL string) (*GeminiOAuthTokenResponse, error) {
	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)
	formData.Set("client_id", clientID)
	formData.Set("client_secret", clientSecret)

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("gemini oauth: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "onwatch/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gemini oauth: network error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("gemini oauth: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp geminiOAuthErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%w: %s - %s", ErrGeminiOAuthRefreshFailed, errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrGeminiOAuthRefreshFailed, resp.StatusCode)
	}

	var tokenResp GeminiOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("gemini oauth: parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty access token in response", ErrGeminiOAuthRefreshFailed)
	}

	return &tokenResp, nil
}

// WriteGeminiCredentials updates ~/.gemini/oauth_creds.json with new access token and expiry.
// Google does NOT rotate refresh tokens, so we only update access_token and expiry_date.
func WriteGeminiCredentials(accessToken string, expiresIn int) error {
	credPath := GeminiCredentialsPath()
	if credPath == "" {
		return os.ErrNotExist
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte("{}")
		} else {
			return err
		}
	}

	// Create backup
	if len(data) > 2 {
		backupPath := credPath + ".bak"
		if err := os.WriteFile(backupPath, data, 0600); err != nil {
			slog.Debug("Failed to create Gemini credentials backup", "error", err)
		}
	}

	// Parse into map to preserve existing fields (refresh_token, scope, etc.)
	var rawCreds map[string]interface{}
	if err := json.Unmarshal(data, &rawCreds); err != nil {
		rawCreds = make(map[string]interface{})
	}

	rawCreds["access_token"] = accessToken
	// expiry_date is Unix milliseconds
	rawCreds["expiry_date"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()

	newData, err := json.MarshalIndent(rawCreds, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		return err
	}

	// Atomic write
	tempPath := credPath + ".tmp"
	if err := os.WriteFile(tempPath, newData, 0600); err != nil {
		return err
	}

	return os.Rename(tempPath, credPath)
}
