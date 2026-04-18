package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func newTestGeminiStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGeminiAgent_Poll(t *testing.T) {
	t.Parallel()
	quotaResp := api.GeminiQuotaResponse{
		Quotas: []api.GeminiQuotaBucket{
			{RemainingFraction: 0.993, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-flash"},
			{RemainingFraction: 1.0, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-pro"},
		},
	}

	tierResp := api.GeminiTierResponse{
		Tier:                    "standard",
		CloudAICompanionProject: "gen-lang-client-12345",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:retrieveUserQuota":
			json.NewEncoder(w).Encode(quotaResp)
		case "/v1internal:loadCodeAssist":
			json.NewEncoder(w).Encode(tierResp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	st := newTestGeminiStore(t)
	tr := tracker.NewGeminiTracker(st, nil)
	client := api.NewGeminiClient("test-token", nil, api.WithGeminiBaseURL(srv.URL))
	sm := NewSessionManager(st, "gemini", 10*time.Minute, nil)

	agent := NewGeminiAgent(client, st, tr, 1*time.Second, nil, sm)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := agent.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Verify data was stored
	latest, err := st.QueryLatestGemini()
	if err != nil {
		t.Fatalf("QueryLatestGemini() error = %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest snapshot")
	}
	if len(latest.Quotas) != 2 {
		t.Errorf("expected 2 quotas, got %d", len(latest.Quotas))
	}
}

func TestGeminiAgent_AuthFailurePause(t *testing.T) {
	t.Parallel()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/v1internal:loadCodeAssist" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	st := newTestGeminiStore(t)
	tr := tracker.NewGeminiTracker(st, nil)
	client := api.NewGeminiClient("bad-token", nil, api.WithGeminiBaseURL(srv.URL))

	agent := NewGeminiAgent(client, st, tr, 500*time.Millisecond, nil, nil)
	// No creds refresh = can't retry on auth failure, just logs and returns
	agent.SetPollingCheck(func() bool { return true })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = agent.Run(ctx)

	// Should have attempted multiple polls
	if callCount == 0 {
		t.Error("expected at least 1 API call")
	}
}

func TestGeminiAgent_SetNotifier(t *testing.T) {
	t.Parallel()
	agent := NewGeminiAgent(nil, nil, nil, time.Second, nil, nil)
	// Should not panic
	agent.SetNotifier(nil)
	agent.SetPollingCheck(func() bool { return false })
}

// TestGeminiAgent_TokenPersistenceOnRefresh verifies that after an OAuth token refresh,
// tokens are persisted to the DB so they survive container restarts.
func TestGeminiAgent_TokenPersistenceOnRefresh(t *testing.T) {
	t.Parallel()
	refreshedAccessToken := "refreshed-access-token-xyz"
	originalRefreshToken := "original-refresh-token-abc"
	quotaCallCount := 0

	// Mock server: first quota call returns 401, OAuth refresh succeeds, retry succeeds
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			json.NewEncoder(w).Encode(api.GeminiTierResponse{Tier: "free"})
		case "/v1internal:retrieveUserQuota":
			quotaCallCount++
			if quotaCallCount == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// After refresh, return quota data
			json.NewEncoder(w).Encode(api.GeminiQuotaResponse{
				Quotas: []api.GeminiQuotaBucket{
					{RemainingFraction: 0.8, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-pro"},
				},
			})
		case "/token": // OAuth refresh endpoint
			json.NewEncoder(w).Encode(api.GeminiOAuthTokenResponse{
				AccessToken: refreshedAccessToken,
				ExpiresIn:   3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	st := newTestGeminiStore(t)
	tr := tracker.NewGeminiTracker(st, nil)
	client := api.NewGeminiClient("expired-token", nil, api.WithGeminiBaseURL(srv.URL))

	ag := NewGeminiAgent(client, st, tr, 1*time.Second, nil, nil)
	ag.SetCredentialsRefresh(func() *api.GeminiCredentials {
		// Simulate loading creds that have a refresh token
		return &api.GeminiCredentials{
			AccessToken:  "expired-token",
			RefreshToken: originalRefreshToken,
		}
	})
	ag.SetClientCredentials(&api.GeminiClientCredentials{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
	})
	// Override OAuth URL to use mock server
	api.SetGeminiOAuthTokenURLForTest(srv.URL + "/token")
	defer api.SetGeminiOAuthTokenURLForTest("")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ag.Run(ctx)

	// Verify tokens were persisted to DB
	access, refresh, expiresAt, err := st.LoadGeminiTokens()
	if err != nil {
		t.Fatalf("LoadGeminiTokens() error = %v", err)
	}
	if access != refreshedAccessToken {
		t.Errorf("expected DB access_token=%q, got %q", refreshedAccessToken, access)
	}
	if refresh != originalRefreshToken {
		t.Errorf("expected DB refresh_token=%q, got %q", originalRefreshToken, refresh)
	}
	if expiresAt == 0 {
		t.Error("expected non-zero expires_at in DB")
	}

	// Verify quota data was stored (retry after refresh succeeded)
	latest, err := st.QueryLatestGemini()
	if err != nil {
		t.Fatalf("QueryLatestGemini() error = %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after token refresh retry")
	}

	// Simulate "container restart": create new agent loading creds from DB
	dbCreds := api.DetectGeminiCredentials(nil, st)
	if dbCreds == nil {
		t.Fatal("expected credentials from DB after restart")
	}
	if dbCreds.AccessToken != refreshedAccessToken {
		t.Errorf("restart: expected access=%q, got %q", refreshedAccessToken, dbCreds.AccessToken)
	}
	if dbCreds.RefreshToken != originalRefreshToken {
		t.Errorf("restart: expected refresh=%q, got %q", originalRefreshToken, dbCreds.RefreshToken)
	}
}
