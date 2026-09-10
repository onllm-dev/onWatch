package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// codexPathServer serves the two Codex usage paths with per-path counters so a
// test can assert which endpoint a poll actually reached.
type codexPathServer struct {
	server        *httptest.Server
	primaryCalls  atomic.Int32
	fallbackCalls atomic.Int32
}

const codexPrimaryPath = "/backend-api/wham/usage"
const codexFallbackPath = "/api/codex/usage"

const codexUsageJSON = `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`

// newCodexPathServer wires a handler per path; each handler sees the 1-based
// call number for that path so it can change behaviour over time.
func newCodexPathServer(t *testing.T, primary, fallback func(w http.ResponseWriter, call int32)) *codexPathServer {
	t.Helper()
	ps := &codexPathServer{}
	ps.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case codexPrimaryPath:
			primary(w, ps.primaryCalls.Add(1))
		case codexFallbackPath:
			fallback(w, ps.fallbackCalls.Add(1))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ps.server.Close)
	return ps
}

func writeCodexUsage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, codexUsageJSON)
}

func (ps *codexPathServer) client(t *testing.T) *CodexClient {
	t.Helper()
	return NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(ps.server.URL+codexPrimaryPath))
}

// A fallback that never served a usable response must not be pinned: once the
// primary recovers, polling has to find it again without a restart (issue #127).
func TestCodexClient_FetchUsage_FailedFallbackIsNotCached(t *testing.T) {
	ps := newCodexPathServer(t,
		func(w http.ResponseWriter, call int32) {
			if call == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeCodexUsage(w)
		},
		func(w http.ResponseWriter, _ int32) {
			w.WriteHeader(http.StatusForbidden)
		})
	client := ps.client(t)

	if _, err := client.FetchUsage(context.Background()); !errors.Is(err, ErrCodexForbidden) {
		t.Fatalf("first FetchUsage error = %v, want ErrCodexForbidden", err)
	}

	resp, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("second FetchUsage: %v", err)
	}
	if resp.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", resp.PlanType)
	}
	if got := ps.primaryCalls.Load(); got != 2 {
		t.Fatalf("primary calls = %d, want 2 (the recovered primary must be retried)", got)
	}
	if got := ps.fallbackCalls.Load(); got != 1 {
		t.Fatalf("fallback calls = %d, want 1 (a failed fallback must not be cached)", got)
	}
}

// A fallback that worked and later breaks must be released too, otherwise a
// transient server-side failure pins polling to a dead endpoint forever.
func TestCodexClient_FetchUsage_CachedFallbackClearedOnServerError(t *testing.T) {
	ps := newCodexPathServer(t,
		func(w http.ResponseWriter, call int32) {
			if call == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeCodexUsage(w)
		},
		func(w http.ResponseWriter, call int32) {
			if call == 1 {
				writeCodexUsage(w)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		})
	client := ps.client(t)

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("first FetchUsage: %v", err)
	}
	if got := client.getFallbackBaseURL(); got == "" {
		t.Fatal("expected a successful fallback to be cached")
	}

	if _, err := client.FetchUsage(context.Background()); !errors.Is(err, ErrCodexServerError) {
		t.Fatalf("second FetchUsage error = %v, want ErrCodexServerError", err)
	}
	if got := client.getFallbackBaseURL(); got != "" {
		t.Fatalf("fallback = %q, want cleared after a failure", got)
	}

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("third FetchUsage: %v", err)
	}
	if got := ps.primaryCalls.Load(); got != 2 {
		t.Fatalf("primary calls = %d, want 2", got)
	}
}

// A transport failure on the cached endpoint releases it as well.
func TestCodexClient_FetchUsage_CachedFallbackClearedOnNetworkError(t *testing.T) {
	ps := newCodexPathServer(t,
		func(w http.ResponseWriter, _ int32) { w.WriteHeader(http.StatusNotFound) },
		func(w http.ResponseWriter, _ int32) { writeCodexUsage(w) })
	client := ps.client(t)

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("first FetchUsage: %v", err)
	}
	if client.getFallbackBaseURL() == "" {
		t.Fatal("expected a successful fallback to be cached")
	}

	ps.server.Close()
	if _, err := client.FetchUsage(context.Background()); err == nil {
		t.Fatal("expected a network error once the server is gone")
	}
	if got := client.getFallbackBaseURL(); got != "" {
		t.Fatalf("fallback = %q, want cleared after a network error", got)
	}
}

// New credentials may carry different endpoint access, so a token change
// re-probes the default endpoint instead of inheriting the old selection.
func TestCodexClient_SetToken_ResetsEndpointSelection(t *testing.T) {
	ps := newCodexPathServer(t,
		func(w http.ResponseWriter, call int32) {
			if call == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeCodexUsage(w)
		},
		func(w http.ResponseWriter, _ int32) { writeCodexUsage(w) })
	client := ps.client(t)

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("first FetchUsage: %v", err)
	}
	if client.getFallbackBaseURL() == "" {
		t.Fatal("expected a successful fallback to be cached")
	}

	client.SetToken("rotated_token")
	if got := client.getFallbackBaseURL(); got != "" {
		t.Fatalf("fallback = %q, want cleared after a token change", got)
	}

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("second FetchUsage: %v", err)
	}
	if got := ps.primaryCalls.Load(); got != 2 {
		t.Fatalf("primary calls = %d, want 2 (a rotated token re-probes the default)", got)
	}
}

// Re-reading the same token from disk every poll must not throw away a working
// endpoint selection - that would cost a wasted 404 on every single poll.
func TestCodexClient_SetToken_SameTokenKeepsEndpointSelection(t *testing.T) {
	ps := newCodexPathServer(t,
		func(w http.ResponseWriter, call int32) {
			if call == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeCodexUsage(w)
		},
		func(w http.ResponseWriter, _ int32) { writeCodexUsage(w) })
	client := ps.client(t)

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("first FetchUsage: %v", err)
	}

	client.SetToken("oauth_token")
	if client.getFallbackBaseURL() == "" {
		t.Fatal("expected the endpoint selection to survive an unchanged token")
	}

	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("second FetchUsage: %v", err)
	}
	if got := ps.primaryCalls.Load(); got != 1 {
		t.Fatalf("primary calls = %d, want 1", got)
	}
	if got := ps.fallbackCalls.Load(); got != 2 {
		t.Fatalf("fallback calls = %d, want 2", got)
	}
}

// An HTML bot challenge is not an OAuth failure, and must not be reported as
// one - the agent pauses polling on repeated auth errors.
func TestCodexClient_FetchUsage_HTMLChallengeIsNotAuthError(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		server      string
		body        string
	}{
		{"html content type", "text/html; charset=utf-8", "", "<html><body>Just a moment...</body></html>"},
		{"cloudflare server", "", "cloudflare", "Attention Required! | Cloudflare"},
		{"blocked body", "text/plain", "", "You have been blocked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				if tt.server != "" {
					w.Header().Set("Server", tt.server)
				}
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			client := NewCodexClient("oauth_token", discardLoggerClient(), WithCodexBaseURL(server.URL))
			_, err := client.FetchUsage(context.Background())
			if !errors.Is(err, ErrCodexAccessBlocked) {
				t.Fatalf("error = %v, want ErrCodexAccessBlocked", err)
			}
			if errors.Is(err, ErrCodexForbidden) {
				t.Fatal("a challenge response must not be reported as an auth failure")
			}
		})
	}
}

// A genuine JSON 403 stays an auth error so the existing pause logic still fires.
func TestCodexClient_FetchUsage_JSONForbiddenIsAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"forbidden"}`)
	}))
	defer server.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(), WithCodexBaseURL(server.URL))
	_, err := client.FetchUsage(context.Background())
	if !errors.Is(err, ErrCodexForbidden) {
		t.Fatalf("error = %v, want ErrCodexForbidden", err)
	}
	if errors.Is(err, ErrCodexAccessBlocked) {
		t.Fatal("a JSON 403 must not be reported as a challenge")
	}
}
