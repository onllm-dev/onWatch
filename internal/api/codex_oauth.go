package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// CodexOAuthClientID is the Codex CLI OAuth client ID (from OmniRoute).
	CodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// CodexOAuthTokenURL is the OpenAI OAuth token endpoint.
	CodexOAuthTokenURL = "https://auth.openai.com/oauth/token"

	// CodexOAuthScope is the required OAuth scope.
	CodexOAuthScope = "openid profile email offline_access"
)

// ErrCodexOAuthRefreshFailed indicates the OAuth token refresh failed.
var ErrCodexOAuthRefreshFailed = errors.New("codex oauth: token refresh failed")

// ErrCodexRefreshTokenReused indicates the refresh token has already been used.
// This is unrecoverable - the user must re-authenticate via Codex CLI.
var ErrCodexRefreshTokenReused = errors.New("codex oauth: refresh token already used (re-authenticate via 'codex auth')")

// CodexOAuthTokenResponse represents the response from OpenAI's OAuth token endpoint.
type CodexOAuthTokenResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	IDToken      string `json:"id_token"`   // JWT containing expiry and workspace info
	Scope        string `json:"scope"`
}

// codexOAuthErrorResponse represents an error response from the OAuth endpoint.
type codexOAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RefreshCodexToken exchanges a refresh token for a new access token via OpenAI's OAuth endpoint.
// Returns the new tokens, or an error if the refresh fails.
//
// IMPORTANT: OpenAI uses one-time-use refresh tokens (OAuth refresh token rotation).
// The new refresh token MUST be saved immediately after a successful refresh.
// If you receive ErrCodexRefreshTokenReused, the token is dead and the user must
// re-authenticate via 'codex auth'.
func RefreshCodexToken(ctx context.Context, refreshToken string) (*CodexOAuthTokenResponse, error) {
	return RefreshCodexTokenWithURL(ctx, refreshToken, CodexOAuthTokenURL)
}

// RefreshCodexTokenWithURL allows specifying a custom OAuth URL (for testing).
func RefreshCodexTokenWithURL(ctx context.Context, refreshToken, tokenURL string) (*CodexOAuthTokenResponse, error) {
	// Build form-encoded request body (OpenAI uses form encoding, not JSON)
	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)
	formData.Set("client_id", CodexOAuthClientID)
	formData.Set("scope", CodexOAuthScope)

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("codex oauth: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "codex-cli/1.0.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("codex oauth: network error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex oauth: read response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode != http.StatusOK {
		var errResp codexOAuthErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			// Check for refresh token reuse (unrecoverable)
			if errResp.Error == "invalid_grant" && strings.Contains(errResp.ErrorDescription, "reused") {
				return nil, ErrCodexRefreshTokenReused
			}
			return nil, fmt.Errorf("%w: %s - %s", ErrCodexOAuthRefreshFailed, errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrCodexOAuthRefreshFailed, resp.StatusCode)
	}

	var tokenResp CodexOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("codex oauth: parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty access token in response", ErrCodexOAuthRefreshFailed)
	}

	return &tokenResp, nil
}

// ParseIDTokenExpiry extracts the expiry time from a JWT id_token.
// OpenAI's id_token contains the 'exp' claim which indicates when the token expires.
// Returns zero time if parsing fails.
func ParseIDTokenExpiry(idToken string) time.Time {
	if idToken == "" {
		return time.Time{}
	}

	// JWT format: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return time.Time{}
	}

	// Decode payload (base64url)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 with padding
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}

	// Parse JSON payload
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}

	if claims.Exp == 0 {
		return time.Time{}
	}

	return time.Unix(claims.Exp, 0)
}
