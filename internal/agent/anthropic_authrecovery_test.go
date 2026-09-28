package agent

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// authRecoveryFixture wires an agent against a usage API that rejects the stale
// token with 401 and accepts a specific refreshed token, plus a stubbed OAuth
// endpoint. Credentials are isolated to a temp HOME so no real token is touched.
type authRecoveryFixture struct {
	agent      *AnthropicAgent
	oauthCalls *atomic.Int32
	apiCalls   *atomic.Int32
	logs       *bytes.Buffer
}

func newAuthRecoveryFixture(t *testing.T, oauthHandler http.HandlerFunc) *authRecoveryFixture {
	t.Helper()

	// Isolate credential writes from the developer's real Claude Code session.
	testhome.SetTestHome(t, t.TempDir())

	var apiCalls, oauthCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		if r.Header.Get("Authorization") == "Bearer fresh-access-token" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(anthropicResponse(31.5, 12.0)))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	t.Cleanup(apiServer.Close)

	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oauthCalls.Add(1)
		oauthHandler(w, r)
	}))
	t.Cleanup(oauthServer.Close)
	api.SetOAuthURLForTest(oauthServer.URL)
	t.Cleanup(func() { api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL) })

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := api.NewAnthropicClient("stale-access-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	ag := NewAnthropicAgent(client, str, tracker.NewAnthropicTracker(str, logger), time.Minute, logger, nil)
	ag.isClaudeCodeRunning = func() bool { return false }
	ag.SetTokenRefresh(func() string { return "stale-access-token" })
	// Credentials look valid on disk (not expiring soon), so proactive refresh
	// stays out of the way - the 401 path is what is under test. This is also
	// the real-world shape of the bug: the server rejects a token the local
	// expiry claims is still good.
	ag.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "stale-access-token",
			RefreshToken: "stored-refresh-token",
			ExpiresIn:    8 * time.Hour,
			ExpiresAt:    time.Now().Add(8 * time.Hour),
		}
	})

	return &authRecoveryFixture{agent: ag, oauthCalls: &oauthCalls, apiCalls: &apiCalls, logs: logs}
}

func oauthSuccessHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"access_token":"fresh-access-token","refresh_token":"fresh-refresh-token","expires_in":28800}`))
}

// TestAnthropicAgent_AuthFailure_RefreshesBeforePausing verifies that repeated
// 401s trigger an OAuth refresh instead of pausing on an expired access token.
// Regression test for https://github.com/onllm-dev/onWatch/issues/111.
func TestAnthropicAgent_AuthFailure_RefreshesBeforePausing(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)

	// Each poll costs one auth failure; recovery kicks in at maxAuthFailures.
	for i := 0; i < maxAuthFailures; i++ {
		f.agent.poll(context.Background())
	}

	if got := f.oauthCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 OAuth refresh, got %d", got)
	}
	if f.agent.authPaused {
		t.Error("agent paused despite a successful OAuth refresh")
	}
	if f.agent.authFailCount != 0 {
		t.Errorf("authFailCount = %d, want 0 after recovery", f.agent.authFailCount)
	}
	if f.agent.lastToken != "fresh-access-token" {
		t.Errorf("lastToken = %q, want the refreshed token", f.agent.lastToken)
	}

	// The recovered poll must have been stored, not dropped.
	if snap, err := f.agent.store.QueryLatestAnthropic(); err != nil || snap == nil {
		t.Fatalf("expected a stored snapshot after recovery, got snap=%v err=%v", snap, err)
	}
}

// TestAnthropicAgent_AuthFailure_SkipsRefreshWhenClaudeCodeRunning verifies the
// refresh-token guard still holds: onWatch must never burn Claude Code's
// one-time-use refresh token while a session is live.
func TestAnthropicAgent_AuthFailure_SkipsRefreshWhenClaudeCodeRunning(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)
	f.agent.isClaudeCodeRunning = func() bool { return true }

	for i := 0; i < maxAuthFailures; i++ {
		f.agent.poll(context.Background())
	}

	if got := f.oauthCalls.Load(); got != 0 {
		t.Errorf("expected no OAuth refresh while Claude Code runs, got %d", got)
	}
	if !f.agent.authPaused {
		t.Error("expected polling to pause after repeated auth failures")
	}
}

// TestAnthropicAgent_AuthFailure_InvalidGrantPauses verifies a revoked refresh
// token is terminal: the agent pauses and does not retry immediately.
func TestAnthropicAgent_AuthFailure_InvalidGrantPauses(t *testing.T) {
	f := newAuthRecoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	})

	for i := 0; i < maxAuthFailures; i++ {
		f.agent.poll(context.Background())
	}

	if !f.agent.authPaused {
		t.Fatal("expected polling to pause after invalid_grant")
	}
	if got := f.oauthCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 OAuth attempt, got %d", got)
	}
	if f.agent.authRetryAt.IsZero() {
		t.Error("expected a bounded recovery retry to be scheduled")
	}

	// A second poll while paused must not hit the OAuth endpoint again.
	f.agent.poll(context.Background())
	if got := f.oauthCalls.Load(); got != 1 {
		t.Errorf("expected no further OAuth attempts while paused, got %d", got)
	}
}

// TestAnthropicAgent_PausedState_SelfRecovers verifies the agent leaves the
// paused state on its own once the retry deadline passes, without waiting for
// Claude Code to rewrite credentials.
func TestAnthropicAgent_PausedState_SelfRecovers(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)

	// Simulate an agent that paused a while ago.
	f.agent.authPaused = true
	f.agent.authFailCount = maxAuthFailures
	f.agent.lastFailedToken = "stale-access-token"
	f.agent.lastToken = "stale-access-token"
	f.agent.authRetryAt = time.Now().Add(-time.Minute)

	f.agent.poll(context.Background())

	if f.agent.authPaused {
		t.Fatal("expected the agent to recover from the paused state")
	}
	if got := f.oauthCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 OAuth refresh, got %d", got)
	}
	if snap, err := f.agent.store.QueryLatestAnthropic(); err != nil || snap == nil {
		t.Fatalf("expected polling to resume and store a snapshot, got snap=%v err=%v", snap, err)
	}
}

// TestAnthropicAgent_PausedState_WaitsForRetryDeadline verifies the self-recovery
// is bounded - a paused agent does not hit the OAuth endpoint every poll.
func TestAnthropicAgent_PausedState_WaitsForRetryDeadline(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)

	f.agent.authPaused = true
	f.agent.authFailCount = maxAuthFailures
	f.agent.lastFailedToken = "stale-access-token"
	f.agent.lastToken = "stale-access-token"
	f.agent.authRetryAt = time.Now().Add(time.Hour)

	f.agent.poll(context.Background())

	if got := f.oauthCalls.Load(); got != 0 {
		t.Errorf("expected no OAuth refresh before the retry deadline, got %d", got)
	}
	if got := f.apiCalls.Load(); got != 0 {
		t.Errorf("expected no usage API calls while paused, got %d", got)
	}
	if !f.agent.authPaused {
		t.Error("agent should still be paused")
	}
}

// TestAuthRetryBackoff verifies the paused-state retry schedule escalates and
// is capped.
func TestAuthRetryBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, authPausedRetryInterval},
		{1, 15 * time.Minute},
		{2, 30 * time.Minute},
		{3, time.Hour},
		{5, 4 * time.Hour},
		{6, authPausedRetryMaxInterval},
		{99, authPausedRetryMaxInterval},
	}
	for _, tt := range tests {
		if got := authRetryBackoff(tt.attempt); got != tt.want {
			t.Errorf("authRetryBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// TestAnthropicAgent_AuthFailure_NoTokenBurnLoop verifies the recovery path
// cannot hammer the OAuth endpoint when the refreshed token is *also* rejected
// by the usage API. Refresh tokens are one-time use, so an unbounded retry loop
// would burn through them and log the user out of Claude Code.
func TestAnthropicAgent_AuthFailure_NoTokenBurnLoop(t *testing.T) {
	// OAuth succeeds but hands back a token the usage API still rejects.
	f := newAuthRecoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"still-rejected","refresh_token":"rotated","expires_in":28800}`))
	})

	for i := 0; i < maxAuthFailures; i++ {
		f.agent.poll(context.Background())
	}
	if got := f.oauthCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 OAuth refresh, got %d", got)
	}
	if !f.agent.authPaused {
		t.Fatal("expected the agent to pause when the refreshed token is still rejected")
	}

	// Many further polls must not touch the OAuth endpoint again - the paused
	// retry deadline (15m) gates it.
	for i := 0; i < 20; i++ {
		f.agent.poll(context.Background())
	}
	if got := f.oauthCalls.Load(); got != 1 {
		t.Errorf("OAuth endpoint hit %d times while paused, want 1 - refresh tokens are being burned", got)
	}
}

// TestAnthropicAgent_AuthFailure_RespectsRateLimitBackoff verifies the new 401
// recovery path honours the shared OAuth backoff, like the 429 bypass and
// proactive refresh already do.
func TestAnthropicAgent_AuthFailure_RespectsRateLimitBackoff(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)
	// Prime lastToken so the first poll sees no credential change - a change
	// deliberately clears the OAuth backoff (new credentials, new window).
	f.agent.lastToken = "stale-access-token"
	f.agent.rateLimitPaused = true
	f.agent.rateLimitResumeAt = time.Now().Add(time.Hour)

	for i := 0; i < maxAuthFailures; i++ {
		f.agent.poll(context.Background())
	}

	if got := f.oauthCalls.Load(); got != 0 {
		t.Errorf("expected no OAuth refresh during rate limit backoff, got %d", got)
	}
	if !f.agent.authPaused {
		t.Error("expected polling to pause after repeated auth failures")
	}
}

// TestAnthropicAgent_ProactiveRefresh_AppliesTokenWhenSaveFails verifies that a
// failed credential write does not throw away the freshly minted access token.
// The refresh token is consumed server-side by the refresh call, so discarding
// the result leaves the agent polling with a dead token and a dead refresh
// token - one of the ways issue #111 becomes unrecoverable.
func TestAnthropicAgent_ProactiveRefresh_AppliesTokenWhenSaveFails(t *testing.T) {
	f := newAuthRecoveryFixture(t, oauthSuccessHandler)

	// Corrupt credentials file on disk makes WriteAnthropicCredentials fail.
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := api.WriteAnthropicCredentials("a", "b", 1); err == nil {
		t.Fatal("precondition failed: expected WriteAnthropicCredentials to error")
	}

	// Credentials are expiring soon, so proactive refresh runs.
	f.agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "stale-access-token",
			RefreshToken: "stored-refresh-token",
			ExpiresIn:    time.Minute,
			ExpiresAt:    time.Now().Add(time.Minute),
		}
	})

	f.agent.poll(context.Background())

	if got := f.oauthCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 proactive OAuth refresh, got %d", got)
	}
	if f.agent.lastToken != "fresh-access-token" {
		t.Errorf("lastToken = %q, want the refreshed token to be applied despite the save failure", f.agent.lastToken)
	}
	if snap, err := f.agent.store.QueryLatestAnthropic(); err != nil || snap == nil {
		t.Fatalf("expected the poll to succeed with the in-memory token, got snap=%v err=%v", snap, err)
	}

	// A later poll must not downgrade back to the stale on-disk token, and must
	// not spend another one-time-use refresh token to stay alive.
	f.agent.poll(context.Background())
	if f.agent.lastToken != "fresh-access-token" {
		t.Errorf("lastToken = %q after a second poll, want the in-memory token to survive", f.agent.lastToken)
	}
	if got := f.oauthCalls.Load(); got != 1 {
		t.Errorf("OAuth endpoint hit %d times, want 1 - the unsaved token should be reused, not re-minted", got)
	}
	if f.agent.authPaused {
		t.Error("agent paused despite holding a working in-memory token")
	}
}
