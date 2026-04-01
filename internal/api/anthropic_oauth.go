package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// AnthropicOAuthClientID is the Claude Code OAuth client ID.
	AnthropicOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// anthropicOAuthTokenURL is the endpoint for OAuth token operations.
// Variable (not const) to allow test overrides.
var anthropicOAuthTokenURL = "https://console.anthropic.com/v1/oauth/token"

// AnthropicOAuthTokenURL is the public accessor for the OAuth token URL.
const AnthropicOAuthTokenURL = "https://console.anthropic.com/v1/oauth/token"

// setOAuthURL overrides the OAuth token URL (for testing).
func setOAuthURL(url string) { anthropicOAuthTokenURL = url }

// resetOAuthURL restores the OAuth token URL (for testing).
func resetOAuthURL(url string) { anthropicOAuthTokenURL = url }

// SetOAuthURLForTest overrides the OAuth token URL for external test packages.
func SetOAuthURLForTest(url string) { anthropicOAuthTokenURL = url }

// ErrOAuthRefreshFailed indicates the OAuth token refresh failed.
var ErrOAuthRefreshFailed = errors.New("oauth: token refresh failed")

// ErrOAuthRateLimited indicates the OAuth endpoint returned 429.
// The backoff package provides RetryAfter() to get the Retry-After duration.
var ErrOAuthRateLimited = errors.New("oauth: rate limited (429)")

// oauthRateLimitedError wraps ErrOAuthRateLimited with a Retry-After duration.
// When the server sends a Retry-After header, we use it directly instead of guessing.
type oauthRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *oauthRateLimitedError) Error() string   { return ErrOAuthRateLimited.Error() }
func (e *oauthRateLimitedError) Is(target error) bool { return errors.Is(target, ErrOAuthRateLimited) }

// RetryAfter returns the Retry-After duration, or 0 if not available.
func RetryAfter(err error) time.Duration {
	var rle *oauthRateLimitedError
	if errors.As(err, &rle) {
		return rle.RetryAfter
	}
	return 0
}

// parseRetryAfterHeader parses a Retry-After header value.
// Supports: seconds (integer), HTTP-date (RFC 7231), and relative seconds.
func parseRetryAfterHeader(value string) time.Duration {
	if value == "" {
		return 0
	}
	value = strings.TrimSpace(value)

	// Try parsing as seconds
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP-date (RFC 7231)
	if t, err := time.Parse(time.RFC1123, value); err == nil {
		delay := time.Until(t)
		if delay > 0 {
			return delay
		}
	}

	return 0
}

// ErrOAuthInvalidGrant indicates the refresh token is revoked or expired (terminal).
var ErrOAuthInvalidGrant = errors.New("oauth: invalid_grant")

// OAuthTokenResponse represents the response from the OAuth token endpoint.
type OAuthTokenResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	Scope        string `json:"scope"`
}

// oauthRefreshRequest represents the request body for token refresh.
type oauthRefreshRequest struct {
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
}

// oauthErrorResponse represents an error response from the OAuth endpoint.
type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RefreshAnthropicToken exchanges a refresh token for a new access token.
// Returns the new tokens and expiry, or an error if the refresh fails.
func RefreshAnthropicToken(ctx context.Context, refreshToken string) (*OAuthTokenResponse, error) {
	reqBody := oauthRefreshRequest{
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
		ClientID:     AnthropicOAuthClientID,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("oauth: marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, anthropicOAuthTokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("oauth: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-code/2.1.69")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("oauth: network error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth: read response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode != http.StatusOK {
		// 429 from the OAuth endpoint itself - check for Retry-After header
		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
			if backoff > 0 {
				return nil, &oauthRateLimitedError{RetryAfter: backoff}
			}
			return nil, ErrOAuthRateLimited
		}

		var errResp oauthErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			// invalid_grant means the refresh token is revoked/expired (terminal)
			if errResp.Error == "invalid_grant" {
				return nil, fmt.Errorf("%w: %s", ErrOAuthInvalidGrant, errResp.ErrorDescription)
			}
			return nil, fmt.Errorf("%w: %s - %s", ErrOAuthRefreshFailed, errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrOAuthRefreshFailed, resp.StatusCode)
	}

	var tokenResp OAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("oauth: parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty access token in response", ErrOAuthRefreshFailed)
	}

	return &tokenResp, nil
}
