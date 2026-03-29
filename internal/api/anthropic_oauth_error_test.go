package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
