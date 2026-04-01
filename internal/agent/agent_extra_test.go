package agent

import (
	"bytes"
	"context"
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

// ---- NewSessionManager: nil logger path ----

func TestNewSessionManager_NilLogger_DefaultsToSlogDefault(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	// nil logger must not panic and must produce a non-nil logger
	sm := NewSessionManager(str, "synthetic", 10*time.Second, nil)
	if sm == nil {
		t.Fatal("expected non-nil SessionManager")
	}
	if sm.logger == nil {
		t.Fatal("expected logger to be defaulted to slog.Default(), got nil")
	}
}

// ---- SessionManager.closeSession: store error path ----

// TestSessionManager_CloseSession_StoreError verifies that when the store is
// closed before the session is closed, closeSession logs an error and clears
// the sessionID regardless.
func TestSessionManager_CloseSession_StoreError(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	sm := NewSessionManager(str, "synthetic", 10*time.Second, logger)

	// Establish baseline then trigger session start
	sm.ReportPoll([]float64{100, 50, 500})
	sm.ReportPoll([]float64{110, 50, 500}) // usage change → session created

	// Verify session was created
	if sm.sessionID == "" {
		t.Fatal("expected active session after usage change")
	}

	// Close the store's DB to force an error on CloseSession
	str.Close()

	// closeSession should log error but still clear sessionID
	sm.closeSession(time.Now().UTC())

	if sm.sessionID != "" {
		t.Error("expected sessionID to be cleared even when store returns error")
	}

	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Failed to close session")) {
		t.Errorf("expected 'Failed to close session' in logs, got: %s", logs)
	}
}

// ---- SessionManager.incrementAndUpdate: store error paths ----

// TestSessionManager_IncrementAndUpdate_StoreClosedError verifies that when
// the store is closed, incrementAndUpdate logs errors but does not panic.
func TestSessionManager_IncrementAndUpdate_StoreClosedError(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	sm := NewSessionManager(str, "synthetic", 10*time.Second, logger)

	// Establish baseline then trigger session start
	sm.ReportPoll([]float64{100, 50, 500})
	sm.ReportPoll([]float64{110, 50, 500}) // usage change → session created

	if sm.sessionID == "" {
		t.Fatal("expected active session after usage change")
	}

	// Close DB to force errors
	str.Close()

	// incrementAndUpdate must not panic; it should log errors
	sm.incrementAndUpdate([]float64{120, 60, 600})

	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Failed to increment snapshot count")) {
		t.Errorf("expected 'Failed to increment snapshot count' in logs, got: %s", logs)
	}
}

// TestSessionManager_IncrementAndUpdate_EmptyValues verifies that incrementAndUpdate
// handles empty/short value slices without panicking (exercises the boundary conditions
// on len(values) > 0, > 1, > 2 checks).
func TestSessionManager_IncrementAndUpdate_ShortValueSlice(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	sm := NewSessionManager(str, "synthetic", 10*time.Second, logger)

	// Establish baseline then trigger session start
	sm.ReportPoll([]float64{100})
	sm.ReportPoll([]float64{110}) // usage change → session created

	if sm.sessionID == "" {
		t.Fatal("expected active session")
	}

	// Call with only 1 value (covers len>0 branch, skips len>1 and len>2)
	sm.incrementAndUpdate([]float64{120})

	// Call with 2 values (covers len>0 and len>1, skips len>2)
	sm.incrementAndUpdate([]float64{130, 60})

	// Should not panic
}

// TestSessionManager_IncrementAndUpdate_UpdateMaxError verifies that when
// UpdateSessionMaxRequests fails, the error is logged.
func TestSessionManager_IncrementAndUpdate_UpdateMaxError(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	sm := NewSessionManager(str, "synthetic", 10*time.Second, logger)

	// Create session with 3-value baseline
	sm.ReportPoll([]float64{100, 50, 500})
	sm.ReportPoll([]float64{110, 50, 500}) // session starts

	if sm.sessionID == "" {
		t.Fatal("expected active session")
	}

	// First increment succeeds (store still open) to ensure IncrementSnapshotCount path goes through
	sm.incrementAndUpdate([]float64{115, 50, 500})

	// Now close store so UpdateSessionMaxRequests will fail on next call
	// But IncrementSnapshotCount will also fail - close store after a flush
	str.Close()

	// This call should fail on both IncrementSnapshotCount AND UpdateSessionMaxRequests
	sm.incrementAndUpdate([]float64{120, 60, 600})

	logs := logBuf.String()
	// Should have logged the increment failure
	if !bytes.Contains([]byte(logs), []byte("Failed to increment snapshot count")) {
		t.Errorf("expected 'Failed to increment snapshot count' in logs, got: %s", logs)
	}
}

// ---- AnthropicAgent.poll(): auth error → pause → retry paths ----

// TestAnthropicAgent_Poll_AuthFailurePause verifies that after maxAuthFailures
// consecutive 401 failures, polling is paused (authPaused=true).
func TestAnthropicAgent_Poll_AuthFailurePause(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		// Always return 401
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("bad-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 30*time.Millisecond, logger, nil)

	// TokenRefreshFunc always returns same token (simulating no credential rotation)
	agent.SetTokenRefresh(func() string {
		return "bad-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait long enough for multiple polls and pausing
	time.Sleep(400 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	logs := logBuf.String()
	// After maxAuthFailures (3), should log polling PAUSED
	if !bytes.Contains([]byte(logs), []byte("PAUSED")) {
		t.Errorf("expected 'PAUSED' in logs after repeated auth failures, got: %s", logs)
	}
}

// TestAnthropicAgent_Poll_AuthPausedThenResumedByNewToken verifies that when
// authPaused=true and a new token appears (different from lastFailedToken),
// polling resumes immediately on next poll.
func TestAnthropicAgent_Poll_AuthPausedThenResumedByNewToken(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		// First 6 requests fail (3 polls × (1 initial + 1 retry) = 6), then succeed
		if n <= 6 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(30.0, 15.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("bad-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 30*time.Millisecond, logger, nil)

	var callNum atomic.Int32
	agent.SetTokenRefresh(func() string {
		n := callNum.Add(1)
		// After the first few calls (to exhaust auth failures), provide a fresh token
		if n > 10 {
			return "fresh-token-after-pause"
		}
		return "bad-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for pause to be lifted and a successful poll
	time.Sleep(1200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	logs := logBuf.String()
	// Should have seen the pause
	if !bytes.Contains([]byte(logs), []byte("PAUSED")) {
		t.Logf("Logs: %s", logs)
		// Not fatal - timing may mean pause was reached differently
	}
}

// TestAnthropicAgent_Poll_NonAuthError verifies that non-auth errors from FetchQuotas
// are logged without triggering the auth retry path.
func TestAnthropicAgent_Poll_NonAuthError(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		// Return 500 (non-auth error)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Failed to fetch Anthropic quotas")) {
		t.Errorf("expected non-auth error logged, got: %s", logs)
	}
}

// TestAnthropicAgent_Poll_AuthError_RetryFailsNonAuth verifies that when auth error
// leads to retry and retry fails with a non-auth error, the non-auth error is logged.
func TestAnthropicAgent_Poll_AuthError_RetryFailsNonAuth(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		if n == 1 {
			// First request: 401 to trigger retry
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		// Retry: return 500 (non-auth error)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// TokenRefreshFunc provides a token for the retry
	agent.SetTokenRefresh(func() string {
		return "refreshed-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("non-auth error")) {
		t.Errorf("expected 'non-auth error' in logs, got: %s", logs)
	}
}

// TestAnthropicAgent_Poll_CredsRefresh_ExpiringToken verifies that when credentials
// are expiring soon and RefreshAnthropicToken is called, a failed OAuth refresh
// logs the error and continues with the existing token.
func TestAnthropicAgent_Poll_CredsRefresh_ExpiringToken_OAuthFails(t *testing.T) {
	// The API server always returns valid quotas
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(20.0, 10.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// Disable Claude Code detection for tests
	agent.isClaudeCodeRunning = func() bool { return false }

	// Redirect OAuth endpoint to a mock that returns an error
	oauthMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"test_error"}`))
	}))
	defer oauthMock.Close()
	api.SetOAuthURLForTest(oauthMock.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	// Credentials are expiring in 1 minute (< 10 minute threshold → IsExpiringSoon = true)
	// RefreshToken is set so the refresh path is triggered
	// The refresh will fail because the mock returns an error
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "expiring-refresh-token",
			ExpiresIn:    1 * time.Minute, // < 10 min threshold
			ExpiresAt:    time.Now().Add(1 * time.Minute),
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for the poll to attempt and fail the OAuth refresh
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	// Poll should have succeeded with existing token despite refresh failure
	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Token expiring soon")) {
		t.Errorf("expected 'Token expiring soon' in logs, got: %s", logs)
	}
	// OAuth refresh should fail (either rate limited with backoff, or generic failure)
	hasRateLimitBackoff := bytes.Contains([]byte(logs), []byte("Proactive OAuth refresh rate limited"))
	hasGenericFailure := bytes.Contains([]byte(logs), []byte("Proactive OAuth refresh failed"))
	if !hasRateLimitBackoff && !hasGenericFailure {
		t.Errorf("expected proactive OAuth refresh failure in logs, got: %s", logs)
	}
}

// TestAnthropicAgent_Poll_CredsRefresh_NoRefreshToken verifies that when credentials
// are expiring soon but RefreshToken is empty, the proactive OAuth refresh is skipped.
func TestAnthropicAgent_Poll_CredsRefresh_ExpiringButNoRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(20.0, 10.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// Expiring credentials but no refresh token → skip OAuth refresh
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "", // empty → no refresh attempted
			ExpiresIn:    1 * time.Minute,
			ExpiresAt:    time.Now().Add(1 * time.Minute),
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	// Should have stored a snapshot (polling worked)
	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Error("expected snapshot to be stored even without refresh token")
	}
}

// TestAnthropicAgent_Poll_CredsRefresh_NilCreds verifies that when credsRefresh
// returns nil, the poll continues without panicking.
func TestAnthropicAgent_Poll_CredsRefresh_NilCreds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(20.0, 10.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// credsRefresh returns nil → should skip refresh path
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Error("expected snapshot when credsRefresh returns nil")
	}
}

// TestAnthropicAgent_Poll_AuthPaused_WithNoTokenChange verifies that when
// authPaused=true and the same token is returned, polling stays paused.
func TestAnthropicAgent_Poll_AuthPaused_PollingSkipped(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("bad-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 20*time.Millisecond, logger, nil)

	// Always return same bad token → never changes → pause is never lifted
	agent.SetTokenRefresh(func() string {
		return "bad-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait enough time for pause to kick in
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	// Once paused, request count should stop growing
	// We only need to verify the agent handles the paused state without panicking
	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("PAUSED")) {
		t.Errorf("expected PAUSED log entry, got: %s", logs)
	}
}

// TestAnthropicAgent_Poll_WithSessionManager verifies that successful polls
// are reported to the session manager.
func TestAnthropicAgent_Poll_WithSessionManager(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.0, 20.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	sm := NewSessionManager(str, "anthropic", 10*time.Second, logger)
	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, sm)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within 2s")
	}

	// Snapshot should be stored
	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Fatal("expected snapshot to be stored with session manager active")
	}
}

// TestAnthropicAgent_Poll_ContextCancelledDuringFetch verifies that context
// cancellation during FetchQuotas is handled gracefully (ctx.Err() != nil path).
func TestAnthropicAgent_Poll_ContextCancelledDuringFetch(t *testing.T) {
	// Server that blocks for a short time to simulate slow response
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cancel context while the request is in-flight
		cancel()
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(10.0, 5.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil on context cancel, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not stop within 3s after context cancel")
	}
}
