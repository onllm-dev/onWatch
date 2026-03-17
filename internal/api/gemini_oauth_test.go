package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshGeminiToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("expected form-encoded content type, got %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("expected grant_type=refresh_token, got %q", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "test-refresh" {
			t.Errorf("expected refresh_token=test-refresh, got %q", r.FormValue("refresh_token"))
		}
		if r.FormValue("client_id") != "test-client-id" {
			t.Errorf("expected client_id=test-client-id, got %q", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "test-client-secret" {
			t.Errorf("expected client_secret=test-client-secret, got %q", r.FormValue("client_secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GeminiOAuthTokenResponse{
			AccessToken: "new-access-token",
			ExpiresIn:   3600,
			TokenType:   "Bearer",
		})
	}))
	defer srv.Close()

	resp, err := RefreshGeminiTokenWithURL(context.Background(), "test-refresh", "test-client-id", "test-client-secret", srv.URL)
	if err != nil {
		t.Fatalf("RefreshGeminiTokenWithURL() error = %v", err)
	}

	if resp.AccessToken != "new-access-token" {
		t.Errorf("expected 'new-access-token', got %q", resp.AccessToken)
	}
	if resp.ExpiresIn != 3600 {
		t.Errorf("expected 3600, got %d", resp.ExpiresIn)
	}
}

func TestRefreshGeminiToken_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(geminiOAuthErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "Token has been revoked",
		})
	}))
	defer srv.Close()

	_, err := RefreshGeminiTokenWithURL(context.Background(), "bad-token", "id", "secret", srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRefreshGeminiToken_EmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GeminiOAuthTokenResponse{
			AccessToken: "",
			ExpiresIn:   3600,
		})
	}))
	defer srv.Close()

	_, err := RefreshGeminiTokenWithURL(context.Background(), "refresh", "id", "secret", srv.URL)
	if err == nil {
		t.Fatal("expected error for empty access token")
	}
}
