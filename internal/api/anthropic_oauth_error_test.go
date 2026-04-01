package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshAnthropicToken_Returns_ErrOAuthRateLimited_On429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded"}`))
	}))
	defer server.Close()

	// Override the OAuth URL to point at our test server
	origURL := AnthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	_, err := RefreshAnthropicToken(context.Background(), "test-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOAuthRateLimited) {
		t.Errorf("expected ErrOAuthRateLimited, got: %v", err)
	}
	// Should NOT match generic ErrOAuthRefreshFailed
	if errors.Is(err, ErrOAuthRefreshFailed) {
		t.Error("ErrOAuthRateLimited should not wrap ErrOAuthRefreshFailed")
	}
}

func TestRefreshAnthropicToken_Returns_ErrOAuthInvalidGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := oauthErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "The refresh token has been revoked",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := AnthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	_, err := RefreshAnthropicToken(context.Background(), "test-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Errorf("expected ErrOAuthInvalidGrant, got: %v", err)
	}
	// Should NOT match generic ErrOAuthRefreshFailed
	if errors.Is(err, ErrOAuthRefreshFailed) {
		t.Error("ErrOAuthInvalidGrant should not wrap ErrOAuthRefreshFailed")
	}
}

func TestRefreshAnthropicToken_Returns_ErrOAuthRefreshFailed_OnOtherErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := oauthErrorResponse{
			Error:            "invalid_request",
			ErrorDescription: "Missing required parameter",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := AnthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	_, err := RefreshAnthropicToken(context.Background(), "test-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOAuthRefreshFailed) {
		t.Errorf("expected ErrOAuthRefreshFailed, got: %v", err)
	}
	// Should NOT match rate limited or invalid grant
	if errors.Is(err, ErrOAuthRateLimited) {
		t.Error("generic error should not match ErrOAuthRateLimited")
	}
	if errors.Is(err, ErrOAuthInvalidGrant) {
		t.Error("generic error should not match ErrOAuthInvalidGrant")
	}
}

func TestRefreshAnthropicToken_Returns_ErrOAuthRefreshFailed_On500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`Internal Server Error`))
	}))
	defer server.Close()

	origURL := AnthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	_, err := RefreshAnthropicToken(context.Background(), "test-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOAuthRefreshFailed) {
		t.Errorf("expected ErrOAuthRefreshFailed, got: %v", err)
	}
}

func TestRefreshAnthropicToken_ExtractsRetryAfterHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Headers must be set BEFORE WriteHeader in Go's buffered response writer
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	origURL := AnthropicOAuthTokenURL
	defer func() { resetOAuthURL(origURL) }()
	setOAuthURL(server.URL)

	_, err := RefreshAnthropicToken(context.Background(), "test-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrOAuthRateLimited) {
		t.Errorf("expected ErrOAuthRateLimited, got: %v", err)
	}
	delay := RetryAfter(err)
	if delay != 120*time.Second {
		t.Errorf("RetryAfter() = %v, want 120s", delay)
	}
}

func TestRetryAfter_ReturnsZeroForNonRateLimitedErrors(t *testing.T) {
	err := errors.New("something else")
	if RetryAfter(err) != 0 {
		t.Errorf("RetryAfter(something else) = %v, want 0", RetryAfter(err))
	}

	if RetryAfter(ErrOAuthRefreshFailed) != 0 {
		t.Errorf("RetryAfter(ErrOAuthRefreshFailed) = %v, want 0", RetryAfter(ErrOAuthRefreshFailed))
	}

	if RetryAfter(ErrOAuthInvalidGrant) != 0 {
		t.Errorf("RetryAfter(ErrOAuthInvalidGrant) = %v, want 0", RetryAfter(ErrOAuthInvalidGrant))
	}
}

func TestParseRetryAfterHeader(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"120", 120 * time.Second},
		{"0", 0},
		{"", 0},
		{"invalid", 0},
		{"3600", 3600 * time.Second},
	}
	for _, tt := range tests {
		got := parseRetryAfterHeader(tt.value)
		if got != tt.want {
			t.Errorf("parseRetryAfterHeader(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}
