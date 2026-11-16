package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func newTestCursorDeps(t *testing.T) (*store.Store, *tracker.CursorTracker, *SessionManager) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tr := tracker.NewCursorTracker(s, slog.Default())
	sm := NewSessionManager(s, "cursor", 5*time.Minute, slog.Default())
	return s, tr, sm
}

func TestNewCursorAgent(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	if agent == nil {
		t.Fatal("NewCursorAgent returned nil")
	}
}

func TestCursorAgent_SetTokenRefresh(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetTokenRefresh(func() string {
		return "refreshed_token"
	})
}

func TestCursorAgent_SetNotifier(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetNotifier(nil) // Should not panic
}

func TestCursorAgent_SetPollingCheck(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetPollingCheck(func() bool { return true })
}

func TestCursorAgent_PollWithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			w.Write([]byte(`{
				"billingCycleStart": "1768399334000",
				"billingCycleEnd": "1771077734000",
				"planUsage": {
					"totalSpend": 5000,
					"remaining": 35000,
					"limit": 40000,
					"totalPercentUsed": 12.5,
					"autoPercentUsed": 3.0,
					"apiPercentUsed": 9.5
				},
				"enabled": true
			}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Write([]byte(`{
				"planInfo": {
					"planName": "Pro",
					"includedAmountCents": 2000,
					"price": "$20/mo"
				}
			}`))
		case "/aiserver.v1.DashboardService/GetCreditGrantsBalance":
			w.Write([]byte(`{"hasCreditGrants": false, "totalCents": "0", "usedCents": "0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default(), api.WithCursorBaseURL(server.URL))
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Poll once manually
	agent.poll(ctx)

	// Verify snapshot was stored
	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot to be stored")
	}
	if latest.AccountType != api.CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", latest.AccountType, api.CursorAccountIndividual)
	}
	if latest.PlanName != "Pro" {
		t.Errorf("PlanName = %q, want %q", latest.PlanName, "Pro")
	}
}

func TestCursorAgent_PollDisabled(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	// Set polling check to return false
	agent.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Poll should be skipped
	agent.poll(ctx)

	// No snapshot should be stored
	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest != nil {
		t.Error("Expected nil snapshot when polling is disabled")
	}
}

func TestCursorAgent_SetCredentialsRefresh(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "test_access",
			RefreshToken: "test_refresh",
			ExpiresAt:    time.Now().Add(30 * time.Minute),
			ExpiresIn:    30 * time.Minute,
			Source:       "sqlite",
		}
	})
}

func TestCursorAgent_ApplyRefreshedCredentials_SavesTokens(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("expired_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	var savedAccess, savedRefresh, savedSource, savedPrevious string
	agent.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		savedAccess = accessToken
		savedRefresh = refreshToken
		savedSource = source
		savedPrevious = previousRefreshToken
		return nil
	})

	outcome := agent.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "fresh_access_token",
		RefreshToken: "fresh_refresh_token",
	}, api.CursorSourceKeychain, "old_refresh_token", "old_access_token")
	if !outcome.usable || !outcome.persisted {
		t.Fatalf("outcome = %+v, want usable and persisted", outcome)
	}
	if savedAccess != "fresh_access_token" {
		t.Fatalf("saved access token = %q, want fresh_access_token", savedAccess)
	}
	if savedRefresh != "fresh_refresh_token" {
		t.Fatalf("saved refresh token = %q, want fresh_refresh_token", savedRefresh)
	}
	if savedSource != api.CursorSourceKeychain {
		t.Fatalf("saved source = %q, want %q", savedSource, api.CursorSourceKeychain)
	}
	if savedPrevious != "old_refresh_token" {
		t.Fatalf("saved previous refresh token = %q, want old_refresh_token", savedPrevious)
	}
	if got := client.GetToken(); got != "fresh_access_token" {
		t.Fatalf("client token = %q, want fresh_access_token", got)
	}
	if agent.lastToken != "fresh_access_token" {
		t.Fatalf("lastToken = %q, want fresh_access_token", agent.lastToken)
	}
}

func TestCursorAgent_ApplyRefreshedCredentials_FailsWhenSaveFails(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("expired_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	agent.lastFailedToken = "expired_token"

	agent.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})

	outcome := agent.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "fresh_access_token",
		RefreshToken: "fresh_refresh_token",
	}, api.CursorSourceSQLite, "old_refresh_token", "old_access_token")
	if !outcome.usable {
		t.Fatal("outcome.usable = false, want true: the new access token is set")
	}
	if outcome.persisted {
		t.Fatal("outcome.persisted = true despite save failure")
	}
	if agent.pinnedDiskToken != "old_access_token" {
		t.Fatalf("pinnedDiskToken = %q, want old_access_token", agent.pinnedDiskToken)
	}
	// A refresh already consumed the old refresh token, so the new access
	// token must be kept in memory: discarding it would throw away the only
	// usable credential.
	if got := client.GetToken(); got != "fresh_access_token" {
		t.Fatalf("client token = %q, want fresh_access_token", got)
	}
	if agent.lastToken != "fresh_access_token" {
		t.Fatalf("lastToken = %q, want fresh_access_token", agent.lastToken)
	}
	if !agent.autoRefreshDisabled {
		t.Fatal("autoRefreshDisabled = false, want true after an unsaveable refresh")
	}
}

// M1: when the save fails after a refresh, the new access token must be used
// for the very next fetch and the agent must not pause. Saving fails on every
// poll, so the auth-failure path must fall back to the in-memory token.
func TestCursorAgent_UnsaveableRefreshUsesTokenInsteadOfPausing(t *testing.T) {
	usageCalls := new(int32)
	tokensSeen := make([]string, 0, 4)
	primary := func(w http.ResponseWriter, r *http.Request, want string) {
		atomic.AddInt32(usageCalls, 1)
		tokensSeen = append(tokensSeen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			primary(w, r, "new-access")
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	stubCursorRefresh(t, func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error) {
		return &api.CursorOAuthResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
	})

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("old-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "old-access",
			RefreshToken: "old-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
			ExpiresIn:    -time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	ag.SetTokenRefresh(func() string { return "old-access" })

	prevWritable := cursorCredentialsWritable
	cursorCredentialsWritable = func(source string) error { return nil }
	t.Cleanup(func() { cursorCredentialsWritable = prevWritable })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < cursorMaxAuthFailures; i++ {
		ag.poll(ctx)
	}

	if ag.authPaused {
		t.Fatal("agent paused despite having a usable refreshed token in memory")
	}
	if got := client.GetToken(); got != "new-access" {
		t.Fatalf("client token = %q, want new-access", got)
	}
	if len(tokensSeen) == 0 || tokensSeen[len(tokensSeen)-1] != "Bearer new-access" {
		t.Fatalf("fetches used %v, want the last one to use the refreshed token", tokensSeen)
	}
}

// M1: on the auth-failure path, an unsaveable refresh that still yields a
// usable access token must be retried instead of pausing. The credential is
// not near expiry here, so only the repeated 401 reaches the refresh.
func TestCursorAgent_AuthFailurePathRetriesUnsaveableRefreshedToken(t *testing.T) {
	usageCalls := new(int32)
	authCalls := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			atomic.AddInt32(usageCalls, 1)
			// Only the token minted by the refresh is accepted, so a successful
			// poll proves the retry used it.
			if r.Header.Get("Authorization") != "Bearer recovered-access" {
				atomic.AddInt32(authCalls, 1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	stubCursorRefresh(t, func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error) {
		return &api.CursorOAuthResponse{AccessToken: "recovered-access", RefreshToken: "recovered-refresh"}, nil
	})

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("rejected-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "rejected-access",
			RefreshToken: "still-valid-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	ag.SetTokenRefresh(func() string { return "rejected-access" })

	prevWritable := cursorCredentialsWritable
	cursorCredentialsWritable = func(source string) error { return nil }
	t.Cleanup(func() { cursorCredentialsWritable = prevWritable })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < cursorMaxAuthFailures; i++ {
		ag.poll(ctx)
	}

	if ag.authPaused {
		t.Fatal("auth-failure path paused despite a usable refreshed token")
	}
	if n := atomic.LoadInt32(authCalls); n != cursorMaxAuthFailures {
		t.Fatalf("auth failures = %d, want %d: the retry must not add another rejected call", n, cursorMaxAuthFailures)
	}
	// Without the retry, the final poll's 401 is the last usage call and the
	// agent pauses. With it, one more call uses the refreshed token.
	if n := atomic.LoadInt32(usageCalls); n != cursorMaxAuthFailures+1 {
		t.Fatalf("usage endpoint called %d times, want %d (a retry after the refresh)", n, cursorMaxAuthFailures+1)
	}
}

// A refresh that succeeds but cannot be saved must not pause the agent when the
// single retry fails for a non-auth reason (5xx). The pinned in-memory token is
// the only working credential left, so the next poll must try it again without
// refreshing a second time.
func TestCursorAgent_RetryTransientFailureDoesNotPausePinnedToken(t *testing.T) {
	refreshCalls := new(int32)
	usageCalls := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			n := atomic.AddInt32(usageCalls, 1)
			switch r.Header.Get("Authorization") {
			case "Bearer new-access":
				// The retry right after the refresh fails transiently: the
				// only usage call with the refreshed token that is a retry.
				if n == cursorMaxAuthFailures+1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	stubCursorRefresh(t, func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error) {
		atomic.AddInt32(refreshCalls, 1)
		return &api.CursorOAuthResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
	})

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("old-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "old-access",
			RefreshToken: "old-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	ag.SetTokenRefresh(func() string { return "old-access" })

	prevWritable := cursorCredentialsWritable
	cursorCredentialsWritable = func(source string) error { return nil }
	t.Cleanup(func() { cursorCredentialsWritable = prevWritable })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < cursorMaxAuthFailures; i++ {
		ag.poll(ctx)
	}

	if ag.authPaused {
		t.Fatal("agent paused after a transient retry failure while holding a usable token")
	}
	if !ag.refreshedTokenPinned {
		t.Fatal("agent is not pinned after an unsaveable refresh")
	}
	if n := atomic.LoadInt32(refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}

	// Next poll: the pinned token must be tried again, with no new refresh.
	ag.poll(ctx)
	if ag.authPaused {
		t.Fatal("agent paused on the next poll")
	}
	if n := atomic.LoadInt32(refreshCalls); n != 1 {
		t.Fatalf("refresh calls after the retry poll = %d, want 1", n)
	}
	if got := client.GetToken(); got != "new-access" {
		t.Fatalf("client token = %q, want the pinned new-access token", got)
	}
	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest == nil {
		t.Fatal("no snapshot stored: the next poll did not succeed with the pinned token")
	}
}

// Item 4a: a re-read that falls back to a different credential store is not
// evidence that the store the pin was recorded against changed. The agent must
// stay pinned instead of adopting the fallback credential.
func TestCursorAgent_PinnedReReadFromDifferentSourceStaysPinned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aiserver.v1.DashboardService/GetPlanInfo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
	}))
	defer server.Close()

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("pinned-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	ag.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "pinned-access",
		RefreshToken: "pinned-refresh",
	}, api.CursorSourceSQLite, "old-refresh", "old-access")
	if !ag.refreshedTokenPinned {
		t.Fatal("setup did not pin")
	}

	// The SQLite read failed, so detection fell back to the keychain.
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken: "keychain-access",
			ExpiresAt:   time.Time{},
			Source:      api.CursorSourceKeychain,
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if !ag.refreshedTokenPinned {
		t.Fatal("agent unpinned on a re-read from a different source")
	}
	if got := client.GetToken(); got != "pinned-access" {
		t.Fatalf("client token = %q, want the pinned in-memory token", got)
	}
}

// Item 4b: the keychain save can write the rotated refresh token but fail on the
// access token, so the on-disk access token is unchanged while the refresh token
// is new. That must release the pin and adopt the on-disk credential.
func TestCursorAgent_PinnedRefreshTokenChangeUnpins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aiserver.v1.DashboardService/GetPlanInfo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
	}))
	defer server.Close()

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("pinned-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	ag.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "pinned-access",
		RefreshToken: "pinned-refresh",
	}, api.CursorSourceSQLite, "old-refresh", "old-access")
	if !ag.refreshedTokenPinned {
		t.Fatal("setup did not pin")
	}

	// Same source and same access token, but the refresh token is the rotated
	// one that did get written.
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "old-access",
			RefreshToken: "rotated-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if ag.refreshedTokenPinned {
		t.Fatal("agent stayed pinned although the on-disk refresh token changed")
	}
	if ag.autoRefreshDisabled {
		t.Fatal("autoRefreshDisabled stayed true after unpinning")
	}
	if got := client.GetToken(); got != "old-access" {
		t.Fatalf("client token = %q, want the adopted on-disk token", got)
	}
}

// M1: a pinned agent re-reads the store each poll. A changed on-disk token
// (the user logged in again) must be adopted and normal polling resumed.
func TestCursorAgent_PinnedCredentialChangeUnpinsAndResumes(t *testing.T) {
	usageCalls := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			atomic.AddInt32(usageCalls, 1)
			if r.Header.Get("Authorization") != "Bearer user-relogin" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"billingCycleStart":"1768399334000","billingCycleEnd":"1771077734000","planUsage":{"totalSpend":5000,"remaining":35000,"limit":40000},"enabled":true}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo":{"planName":"Pro"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	stubCursorRefresh(t, func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error) {
		return &api.CursorOAuthResponse{AccessToken: "pinned-access", RefreshToken: "pinned-refresh"}, nil
	})

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("pinned-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})

	// Land in the pinned state the way the agent does: an unsaveable refresh.
	ag.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "pinned-access",
		RefreshToken: "pinned-refresh",
	}, api.CursorSourceSQLite, "old-refresh", "old-access")
	if !ag.refreshedTokenPinned || ag.pinnedDiskToken != "old-access" {
		t.Fatalf("setup did not pin: pinned=%v disk=%q", ag.refreshedTokenPinned, ag.pinnedDiskToken)
	}

	// The user logs in again: the store now holds a new credential.
	ag.SetTokenRefresh(func() string { return "user-relogin" })
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "user-relogin",
			RefreshToken: "user-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ag.poll(ctx)

	if ag.refreshedTokenPinned {
		t.Fatal("agent stayed pinned after the on-disk credential changed")
	}
	if ag.autoRefreshDisabled {
		t.Fatal("autoRefreshDisabled stayed true after the on-disk credential changed")
	}
	if ag.authPaused {
		t.Fatal("authPaused = true, want the agent resumed")
	}
	if got := client.GetToken(); got != "user-relogin" {
		t.Fatalf("client token = %q, want the adopted user-relogin token", got)
	}
	if n := atomic.LoadInt32(usageCalls); n == 0 {
		t.Fatal("no fetch attempted after unpinning")
	}

	// A later poll with an unchanged on-disk token must not re-pin or refresh.
	ag.poll(ctx)
	if got := client.GetToken(); got != "user-relogin" {
		t.Fatalf("client token after a second poll = %q, want user-relogin", got)
	}

	// A pinned agent that sees no change keeps using the in-memory token.
	pinnedClient := api.NewCursorClient("pinned-access", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	pinned := NewCursorAgent(pinnedClient, s, tr, 30*time.Second, slog.Default(), sm)
	pinned.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return errors.New("save failed")
	})
	pinned.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "pinned-access",
		RefreshToken: "pinned-refresh",
	}, api.CursorSourceSQLite, "old-refresh", "old-access")
	pinned.SetTokenRefresh(func() string { return "old-access" })
	pinned.poll(ctx)
	if got := pinnedClient.GetToken(); got != "pinned-access" {
		t.Fatalf("pinned client token = %q, want the in-memory pinned-access token", got)
	}
	if !pinned.refreshedTokenPinned {
		t.Fatal("agent unpinned despite an unchanged on-disk token")
	}
}
