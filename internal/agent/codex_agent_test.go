package agent

import (
	"context"
	"fmt"
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

func setupCodexTest(t *testing.T) (*CodexAgent, *store.Store, *httptest.Server) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if r.Header.Get("Authorization") != "Bearer oauth_token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	sm := NewSessionManager(st, "codex", 600*time.Second, logger)
	ag := NewCodexAgent(client, st, tr, 100*time.Millisecond, logger, sm)
	return ag, st, server
}

func TestCodexAgent_SinglePoll(t *testing.T) {
	t.Parallel()
	ag, st, _ := setupCodexTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(250 * time.Millisecond)
	cancel()

	latest, err := st.QueryLatestCodex(store.DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after poll")
	}
	if latest.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", latest.PlanType)
	}
	if len(latest.Quotas) == 0 {
		t.Fatal("expected at least one quota")
	}
}

func TestCodexAgent_PollingCheck(t *testing.T) {
	t.Parallel()
	ag, st, _ := setupCodexTest(t)
	ag.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()

	latest, err := st.QueryLatestCodex(store.DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot when polling disabled")
	}
}

func TestCodexAgent_ContextCancellation(t *testing.T) {
	t.Parallel()
	ag, _, _ := setupCodexTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ag.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within timeout")
	}
}

// TestCodexAgent_ProactiveRefreshFailures_PausesAfterMax verifies that consecutive
// proactive OAuth refresh failures (non-reused-token, e.g. HTTP 401) pause the agent
// after maxCodexAuthFailures attempts, preventing infinite retry storms.
func TestCodexAgent_ProactiveRefreshFailures_PausesAfterMax(t *testing.T) {
	t.Parallel()
	// API server: succeeds normally (the issue is the proactive refresh, not FetchUsage)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Hour, logger, nil)

	// Simulate: proactive refresh has already failed maxCodexAuthFailures-1 times.
	// One more failure should trigger pause.
	ag.proactiveRefreshFailures = maxCodexAuthFailures - 1

	// Set creds to return expired tokens (triggers proactive refresh path).
	// Use "oauth_token" as access token - matches what tokenRefresh returns,
	// so the resume-on-credential-change check won't clear the pause.
	ag.SetCredentialsRefresh(func() *api.CodexCredentials {
		return &api.CodexCredentials{
			AccessToken:  "oauth_token",
			RefreshToken: "dead-refresh-token",
			ExpiresIn:    -1 * time.Hour, // already expired
			ExpiresAt:    time.Now().Add(-1 * time.Hour),
		}
	})
	ag.lastToken = "oauth_token"
	ag.SetTokenRefresh(func() string { return "oauth_token" })

	// Run one poll - the proactive refresh will fail (can't reach real OpenAI endpoint)
	// and increment proactiveRefreshFailures to maxCodexAuthFailures
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	if ag.proactiveRefreshFailures < maxCodexAuthFailures {
		t.Fatalf("proactiveRefreshFailures = %d, want >= %d", ag.proactiveRefreshFailures, maxCodexAuthFailures)
	}
	if !ag.authPaused {
		t.Fatal("expected authPaused=true after max proactive refresh failures")
	}
}

// TestCodexAgent_ProactiveRefreshFailures_NotPausedBeforeMax verifies that a single
// proactive refresh failure does not immediately pause the agent.
func TestCodexAgent_ProactiveRefreshFailures_NotPausedBeforeMax(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Hour, logger, nil)

	ag.SetCredentialsRefresh(func() *api.CodexCredentials {
		return &api.CodexCredentials{
			AccessToken:  "oauth_token",
			RefreshToken: "dead-refresh-token",
			ExpiresIn:    -1 * time.Hour,
			ExpiresAt:    time.Now().Add(-1 * time.Hour),
		}
	})
	ag.lastToken = "oauth_token"
	ag.SetTokenRefresh(func() string { return "oauth_token" })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ag.poll(ctx)

	// One failure should increment but NOT pause
	if ag.proactiveRefreshFailures != 1 {
		t.Fatalf("proactiveRefreshFailures = %d, want 1", ag.proactiveRefreshFailures)
	}
	if ag.authPaused {
		t.Fatal("expected authPaused=false after single proactive refresh failure")
	}
}

func TestCodexAgent_AuthFailuresPauseUntilTokenChanges(t *testing.T) {
	t.Parallel()
	var currentToken atomic.Value
	currentToken.Store("bad")

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.Default()
	client := api.NewCodexClient("bad", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	sm := NewSessionManager(st, "codex", 600*time.Second, logger)
	ag := NewCodexAgent(client, st, tr, 50*time.Millisecond, logger, sm)
	ag.SetTokenRefresh(func() string {
		v, _ := currentToken.Load().(string)
		return v
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)

	// Wait for agent to hit max auth failures and pause.
	deadline := time.After(2 * time.Second)
	for {
		if calls.Load() >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("expected codex agent to hit repeated auth failures")
		case <-time.After(20 * time.Millisecond):
		}
	}

	pausedCalls := calls.Load()
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != pausedCalls {
		t.Fatalf("expected no fetch calls while paused, got %d -> %d", pausedCalls, calls.Load())
	}

	// Change token and ensure polling resumes.
	currentToken.Store("good")
	deadline = time.After(2 * time.Second)
	for {
		if calls.Load() > pausedCalls {
			break
		}
		select {
		case <-deadline:
			t.Fatal("expected codex polling to resume after token change")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
