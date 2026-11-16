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
)

// stubCursorRefresh points the agent's refresh seam at fn for one test, so no
// test reaches the real api2.cursor.sh endpoint. The api package keeps its
// endpoint unexported on purpose.
func stubCursorRefresh(t *testing.T, fn func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error)) {
	t.Helper()
	prev := cursorRefreshFn
	cursorRefreshFn = fn
	t.Cleanup(func() { cursorRefreshFn = prev })
}

// cursorAuthFailureServer serves a primary endpoint that always returns 401 and
// a refresh seam that records calls. If anything reaches the refresh seam the
// test fails. It never talks to api2.cursor.sh.
func cursorAuthFailureServer(t *testing.T) (server *httptest.Server, refreshCalls *int32) {
	t.Helper()
	refreshCalls = new(int32)
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(refreshCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"should-not-be-used","refresh_token":"should-not-be-used"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	stubCursorRefresh(t, func(ctx context.Context, refreshToken string) (*api.CursorOAuthResponse, error) {
		atomic.AddInt32(refreshCalls, 1)
		return &api.CursorOAuthResponse{AccessToken: "should-not-be-used", RefreshToken: "should-not-be-used"}, nil
	})
	t.Cleanup(server.Close)
	return server, refreshCalls
}

// forbidCursorTokenSave fails the test if a credential save is ever attempted.
// A read-only credential must never be refreshed, so no save can happen.
func forbidCursorTokenSave(t *testing.T, ag *CursorAgent) {
	t.Helper()
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		t.Fatal("token save attempted for a credential that must not be refreshed")
		return nil
	})
}

// H1: with the SQLite token opaque, detection selects the Keychain JWT and
// marks it read-only. Even when it is expired, the proactive path must perform
// zero refresh calls.
func TestCursorAgent_ReadOnlyCredentialNeverProactivelyRefreshes(t *testing.T) {
	server, refreshCalls := cursorAuthFailureServer(t)
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("read_only_token", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "read_only_token",
			RefreshToken: "shared-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
			ExpiresIn:    -time.Hour,
			Source:       api.CursorSourceKeychain,
			ReadOnly:     true,
		}
	})
	forbidCursorTokenSave(t, ag)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if n := atomic.LoadInt32(refreshCalls); n != 0 {
		t.Fatalf("proactive refresh hit the refresh endpoint %d times for a read-only credential", n)
	}
}

// H1: after max auth failures the agent must not refresh a read-only
// credential either, and it must pause instead of retrying in a loop.
func TestCursorAgent_ReadOnlyCredentialAuthFailureDoesNotRefresh(t *testing.T) {
	server, refreshCalls := cursorAuthFailureServer(t)
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("read_only_token", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "read_only_token",
			RefreshToken: "shared-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceKeychain,
			ReadOnly:     true,
		}
	})
	forbidCursorTokenSave(t, ag)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < cursorMaxAuthFailures+2; i++ {
		ag.poll(ctx)
	}

	if n := atomic.LoadInt32(refreshCalls); n != 0 {
		t.Fatalf("auth-failure path hit the refresh endpoint %d times for a read-only credential", n)
	}
	if !ag.authPaused {
		t.Fatal("agent did not pause after repeated auth failures")
	}
}

// H3: a fallback 401/403 must not drive the agent's auth-failure count or
// refresh path, because the primary endpoint accepted the token.
func TestCursorAgent_FallbackAuthStatusDoesNotTriggerAuthFailurePath(t *testing.T) {
	primaryCalls := new(int32)
	refreshCalls := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			atomic.AddInt32(refreshCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh"}`))
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			atomic.AddInt32(primaryCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"enabled": true, "billingCycleStart": "1", "billingCycleEnd": "2", "displayThreshold": 100}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo": {"planName": "Enterprise"}}`))
		case "/api/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"gpt-4": {"numRequests": 0, "maxRequestUsage": null}}`))
		case "/api/usage-summary":
			w.WriteHeader(http.StatusUnauthorized)
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
	client := api.NewCursorClient("token", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "token",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ExpiresIn:    time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})

	saves := new(int32)
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		atomic.AddInt32(saves, 1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < cursorMaxAuthFailures+1; i++ {
		ag.poll(ctx)
	}

	if n := atomic.LoadInt32(primaryCalls); int(n) != cursorMaxAuthFailures+1 {
		t.Fatalf("primary endpoint called %d times, want %d", n, cursorMaxAuthFailures+1)
	}
	if ag.authFailCount != 0 {
		t.Fatalf("authFailCount = %d, want 0: a fallback 401 is not an auth failure", ag.authFailCount)
	}
	if n := atomic.LoadInt32(refreshCalls); n != 0 {
		t.Fatalf("refresh endpoint called %d times for a fallback 401", n)
	}
	if n := atomic.LoadInt32(saves); n != 0 {
		t.Fatalf("token save attempted %d times for a fallback 401", n)
	}
}

// M4: when the target store is not writable the agent must skip the refresh
// entirely - the refresh token is one-time use. The shared pre-check seam is
// forced to fail, which is the same path an opaque SQLite store takes on
// Windows and on Unix.
func TestCursorAgent_SkipsRefreshWhenResultCannotBeSaved(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	server, refreshCalls := cursorAuthFailureServer(t)
	client := api.NewCursorClient("token", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "token",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
			ExpiresIn:    -time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})

	prev := cursorCredentialsWritable
	cursorCredentialsWritable = func(source string) error { return errors.New("store not writable") }
	t.Cleanup(func() { cursorCredentialsWritable = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if n := atomic.LoadInt32(refreshCalls); n != 0 {
		t.Fatalf("refresh hit the endpoint %d times when the result could not be saved", n)
	}
}

// M4: a refused save after a successful refresh keeps the new token in memory,
// continues polling, and never refreshes again in this process.
func TestCursorAgent_UnsaveableRefreshKeepsTokenAndStopsRefreshing(t *testing.T) {
	refreshCalls := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			atomic.AddInt32(refreshCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh"}`))
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
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
		atomic.AddInt32(refreshCalls, 1)
		return &api.CursorOAuthResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
	})

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("expired-token", slog.Default(), api.WithCursorBaseURL(server.URL), api.WithCursorWebBaseURL(server.URL))
	ag := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	ag.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "expired-token",
			RefreshToken: "old-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
			ExpiresIn:    -time.Hour,
			Source:       api.CursorSourceSQLite,
		}
	})
	ag.SetTokenSave(func(accessToken, refreshToken, source, previousRefreshToken string) error {
		return context.DeadlineExceeded
	})

	// The pre-check passes; the save is what fails.
	prevWritable := cursorCredentialsWritable
	cursorCredentialsWritable = func(source string) error { return nil }
	t.Cleanup(func() { cursorCredentialsWritable = prevWritable })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if got := client.GetToken(); got != "new-access" {
		t.Fatalf("client token = %q, want the refreshed new-access token to be kept", got)
	}
	if !ag.autoRefreshDisabled {
		t.Fatal("autoRefreshDisabled = false, want true after an unsaveable refresh")
	}

	// Poll again: no further refresh may be attempted, and the stale on-disk
	// token must not replace the pinned in-memory token.
	ag.SetTokenRefresh(func() string { return "expired-token" })
	ag.poll(ctx)
	if n := atomic.LoadInt32(refreshCalls); n != 1 {
		t.Fatalf("refresh endpoint called %d times, want exactly 1", n)
	}
	if got := client.GetToken(); got != "new-access" {
		t.Fatalf("client token after a second poll = %q, want the pinned new-access token", got)
	}
}
