package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type oauthRedirectTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t oauthRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "console.anthropic.com" && req.URL.Path == "/v1/oauth/token" {
		cloned := req.Clone(req.Context())
		newURL := *cloned.URL
		newURL.Scheme = t.target.Scheme
		newURL.Host = t.target.Host
		cloned.URL = &newURL
		return t.base.RoundTrip(cloned)
	}
	return t.base.RoundTrip(req)
}

func withAnthropicOAuthRedirect(t *testing.T, oauthServerURL string) {
	t.Helper()
	target, err := url.Parse(oauthServerURL)
	if err != nil {
		t.Fatalf("parse oauth server url: %v", err)
	}

	base := http.DefaultTransport
	http.DefaultTransport = oauthRedirectTransport{
		base:   base,
		target: target,
	}
	t.Cleanup(func() {
		http.DefaultTransport = base
	})
}

type blockingRunner struct {
	started chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil
}

func TestAgentManager_NewAndStartBranches(t *testing.T) {
	m := NewAgentManager(nil)
	if m.logger == nil {
		t.Fatal("expected default logger")
	}
	if m.factories == nil || m.running == nil {
		t.Fatal("expected internal maps to be initialized")
	}

	m.RegisterFactory("nil-runner", func() (AgentRunner, error) { return nil, nil })
	if err := m.Start("nil-runner"); err == nil || !strings.Contains(err.Error(), "nil runner") {
		t.Fatalf("Start(nil-runner) error = %v", err)
	}

	var calls atomic.Int32
	runner := &blockingRunner{started: make(chan struct{}, 1)}
	m.RegisterFactory("synthetic", func() (AgentRunner, error) {
		calls.Add(1)
		return runner, nil
	})

	if err := m.Start("synthetic"); err != nil {
		t.Fatalf("Start(synthetic): %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	// Already running branch should be a no-op and not call factory again.
	if err := m.Start("synthetic"); err != nil {
		t.Fatalf("Start(synthetic second call): %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("factory calls = %d, want 1", got)
	}
	m.Stop("synthetic")
}

func TestMiniMaxAgent_NewAndPollErrorBranches(t *testing.T) {
	a := NewMiniMaxAgent(nil, nil, nil, time.Second, nil, nil)
	if a.logger == nil {
		t.Fatal("expected default logger for nil input")
	}

	t.Run("fetch error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"unauthorized"}}`)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		client := api.NewMiniMaxClient("bad", slog.Default(), api.WithMiniMaxBaseURL(server.URL))
		tr := tracker.NewMiniMaxTracker(s, nil)
		ag := NewMiniMaxAgent(client, s, tr, time.Second, slog.Default(), nil)
		ag.poll(context.Background())

		latest, err := s.QueryLatestMiniMax()
		if err != nil {
			t.Fatalf("QueryLatestMiniMax: %v", err)
		}
		if latest != nil {
			t.Fatal("expected no snapshot on fetch error")
		}
	})

	t.Run("store/tracker errors are handled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"base_resp":{"status_code":0,"status_msg":"ok"},"model_remains":[{"model_name":"MiniMax-M2","start_time":1771218000000,"end_time":1771236000000,"remains_time":205310,"current_interval_total_count":15000,"current_interval_usage_count":14077}]}`)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		client := api.NewMiniMaxClient("ok", slog.Default(), api.WithMiniMaxBaseURL(server.URL))
		tr := tracker.NewMiniMaxTracker(s, nil)
		ag := NewMiniMaxAgent(client, s, tr, time.Second, slog.Default(), nil)

		_ = s.Close()
		ag.poll(context.Background()) // should not panic even when inserts/process fail
	})
}

func TestCodexAgent_NewWithAccountAndAuthRetryNoToken(t *testing.T) {
	agent := NewCodexAgentWithAccount(nil, nil, nil, time.Second, nil, nil, 0)
	if agent.accountID != store.DefaultCodexAccountID {
		t.Fatalf("accountID = %d, want default %d", agent.accountID, store.DefaultCodexAccountID)
	}
	if agent.logger == nil {
		t.Fatal("expected default logger")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
	}))
	defer server.Close()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	client := api.NewCodexClient("bad", slog.Default(), api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(s, nil)
	ag := NewCodexAgent(client, s, tr, time.Second, slog.Default(), nil)
	ag.SetTokenRefresh(func() string { return "" }) // exercise "no token after re-read"

	ag.poll(context.Background())

	latest, err := s.QueryLatestCodex(store.DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no codex snapshot when auth retry has no token")
	}
}

func TestSyntheticAgent_PollErrorBranches(t *testing.T) {
	t.Run("fetch error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		client := api.NewClient("bad", slog.Default(), api.WithBaseURL(server.URL))
		tr := tracker.New(s, nil)
		ag := New(client, s, tr, time.Second, slog.Default(), nil)
		ag.poll(context.Background())

		latest, err := s.QueryLatest()
		if err != nil {
			t.Fatalf("QueryLatest: %v", err)
		}
		if latest != nil {
			t.Fatal("expected no snapshot on fetch error")
		}
	})

	t.Run("store/tracker errors are handled", func(t *testing.T) {
		now := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"subscription":{"limit":1000,"requests":100,"renewsAt":"%s"},"search":{"hourly":{"limit":100,"requests":10,"renewsAt":"%s"}},"toolCallDiscounts":{"limit":100,"requests":5,"renewsAt":"%s"}}`, now, now, now)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		client := api.NewClient("ok", slog.Default(), api.WithBaseURL(server.URL))
		tr := tracker.New(s, nil)
		ag := New(client, s, tr, time.Second, slog.Default(), nil)

		_ = s.Close()
		ag.poll(context.Background()) // should not panic even when persistence fails
	})
}

func TestAnthropicAgent_PollRateLimitAndNoRetryToken(t *testing.T) {
	t.Run("rate limit without creds refresh", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate_limited"}`)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		client := api.NewAnthropicClient("tok", slog.Default(), api.WithAnthropicBaseURL(server.URL))
		tr := tracker.NewAnthropicTracker(s, nil)
		ag := NewAnthropicAgent(client, s, tr, time.Second, slog.Default(), nil)

		ag.poll(context.Background())
		latest, err := s.QueryLatestAnthropic()
		if err != nil {
			t.Fatalf("QueryLatestAnthropic: %v", err)
		}
		if latest != nil {
			t.Fatal("expected no snapshot on rate limit error without bypass")
		}
	})

	t.Run("auth error without retry token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
		}))
		defer server.Close()

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		client := api.NewAnthropicClient("tok", slog.Default(), api.WithAnthropicBaseURL(server.URL))
		tr := tracker.NewAnthropicTracker(s, nil)
		ag := NewAnthropicAgent(client, s, tr, time.Second, slog.Default(), nil)
		ag.SetTokenRefresh(func() string { return "" })

		ag.poll(context.Background())
		if ag.authFailCount != 0 || ag.authPaused {
			t.Fatalf("expected no auth pause without retry token, got failCount=%d paused=%v", ag.authFailCount, ag.authPaused)
		}
	})
}

func TestAnthropicAgent_PollAuthPauseAndResume(t *testing.T) {
	var requestCount atomic.Int32
	var allowSuccess atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if allowSuccess.Load() && r.Header.Get("Authorization") == "Bearer resumed-token" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, anthropicResponse(33.3, 44.4))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
	}))
	defer server.Close()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	client := api.NewAnthropicClient("initial-token", slog.Default(), api.WithAnthropicBaseURL(server.URL))
	tr := tracker.NewAnthropicTracker(s, nil)
	ag := NewAnthropicAgent(client, s, tr, time.Second, slog.Default(), nil)

	currentToken := &atomic.Value{}
	currentToken.Store("stale-token")
	ag.SetTokenRefresh(func() string {
		return currentToken.Load().(string)
	})

	// Hit max consecutive auth failures to trigger pause.
	for i := 0; i < maxAuthFailures; i++ {
		ag.poll(context.Background())
	}
	if !ag.authPaused {
		t.Fatalf("expected authPaused=true after %d failures", maxAuthFailures)
	}
	if ag.authFailCount != maxAuthFailures {
		t.Fatalf("authFailCount = %d, want %d", ag.authFailCount, maxAuthFailures)
	}
	if ag.lastFailedToken != "stale-token" {
		t.Fatalf("lastFailedToken = %q, want stale-token", ag.lastFailedToken)
	}

	// While paused and token unchanged, poll should short-circuit without fetch.
	before := requestCount.Load()
	ag.poll(context.Background())
	if after := requestCount.Load(); after != before {
		t.Fatalf("requestCount changed while paused: before=%d after=%d", before, after)
	}

	// New token should lift pause and allow a successful poll.
	currentToken.Store("resumed-token")
	allowSuccess.Store(true)
	ag.poll(context.Background())

	if ag.authPaused {
		t.Fatal("expected auth pause to be lifted after token change")
	}
	if ag.authFailCount != 0 {
		t.Fatalf("authFailCount = %d, want 0 after successful resume", ag.authFailCount)
	}

	latest, err := s.QueryLatestAnthropic()
	if err != nil {
		t.Fatalf("QueryLatestAnthropic: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after successful resume poll")
	}
}

func TestAnthropicAgent_PollRateLimitBypassWithOAuthRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token_type":"Bearer","access_token":"oauth-new-token","refresh_token":"oauth-new-refresh","expires_in":3600,"scope":"user:inference"}`)
	}))
	defer oauthServer.Close()
	withAnthropicOAuthRedirect(t, oauthServer.URL)

	var quotaCalls atomic.Int32
	quotaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := quotaCalls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate_limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, anthropicResponse(22.2, 55.5))
	}))
	defer quotaServer.Close()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	client := api.NewAnthropicClient("stale-token", slog.Default(), api.WithAnthropicBaseURL(quotaServer.URL))
	tr := tracker.NewAnthropicTracker(s, nil)
	ag := NewAnthropicAgent(client, s, tr, time.Second, slog.Default(), nil)
	ag.isClaudeCodeRunning = func() bool { return false }
	ag.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "stale-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    time.Now().Add(2 * time.Hour),
			ExpiresIn:    2 * time.Hour,
		}
	})

	ag.poll(context.Background())

	if ag.lastToken != "oauth-new-token" {
		t.Fatalf("lastToken = %q, want oauth-new-token", ag.lastToken)
	}
	if quotaCalls.Load() < 2 {
		t.Fatalf("expected retry after refresh, got %d quota calls", quotaCalls.Load())
	}
	latest, err := s.QueryLatestAnthropic()
	if err != nil {
		t.Fatalf("QueryLatestAnthropic: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after rate-limit bypass refresh")
	}
}

func TestAnthropicAgent_PollProactiveOAuthRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token_type":"Bearer","access_token":"proactive-token","refresh_token":"proactive-refresh","expires_in":3600,"scope":"user:inference"}`)
	}))
	defer oauthServer.Close()
	withAnthropicOAuthRedirect(t, oauthServer.URL)

	quotaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, anthropicResponse(11.1, 21.1))
	}))
	defer quotaServer.Close()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	client := api.NewAnthropicClient("old-token", slog.Default(), api.WithAnthropicBaseURL(quotaServer.URL))
	tr := tracker.NewAnthropicTracker(s, nil)
	ag := NewAnthropicAgent(client, s, tr, time.Second, slog.Default(), nil)

	// Disable Claude Code detection for tests
	ag.isClaudeCodeRunning = func() bool { return false }

	ag.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "old-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    time.Now().Add(2 * time.Minute),
			ExpiresIn:    2 * time.Minute,
		}
	})

	ag.poll(context.Background())

	if ag.lastToken != "proactive-token" {
		t.Fatalf("lastToken = %q, want proactive-token", ag.lastToken)
	}
	latest, err := s.QueryLatestAnthropic()
	if err != nil {
		t.Fatalf("QueryLatestAnthropic: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after proactive refresh poll")
	}
}
