package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestCodexClient_ReadOnly_NoConsumeRequests is the behavioural counterpart to
// the source-string guard: every Codex client method that makes a request is
// pointed at one server, which fails the test on any non-GET request (except
// the quota-starter ping's own Responses POST) and on any path that mentions
// consuming a reset credit. URL building cannot route around it.
func TestCodexClient_ReadOnly_NoConsumeRequests(t *testing.T) {
	const starterPath = "/backend-api/codex/responses"

	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		if strings.Contains(strings.ToLower(r.URL.Path), "consume") {
			t.Errorf("codex client called a consume path: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
		case http.MethodPost:
			if r.URL.Path != starterPath {
				t.Errorf("codex client POSTed outside the starter endpoint: %s %s", r.Method, r.URL.Path)
			}
		default:
			t.Errorf("codex client used a non-GET method: %s %s", r.Method, r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			io.WriteString(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":10,"reset_at":1766000000,"limit_window_seconds":18000}},"rate_limit_reset_credits":{"available_count":1}}`)
		case "/backend-api/wham/rate-limit-reset-credits":
			io.WriteString(w, `{"credits":[{"id":"credit-1","status":"available"}],"available_count":1}`)
		case starterPath:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
		default:
			// A stray path is a failure: this client must only read the usage
			// and reset-credit GETs, plus its own starter ping.
			t.Errorf("codex client requested an unexpected path: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"),
		WithCodexStarterURL(srv.URL+starterPath),
	)
	ctx := context.Background()

	if _, err := client.FetchUsage(ctx); err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if _, err := client.FetchResetCredits(ctx); err != nil {
		t.Fatalf("FetchResetCredits: %v", err)
	}
	if err := client.SendStarterPing(ctx, "acct-1"); err != nil {
		t.Fatalf("SendStarterPing: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("no requests were observed; the guard did not exercise the client")
	}
	for _, req := range requests {
		if strings.Contains(strings.ToLower(req), "consume") {
			t.Errorf("observed a consume request: %s", req)
		}
	}
}

// The real endpoints are never contacted by the guard: a request to any other
// host is impossible because every request method reads the configured base.
func TestCodexClient_ReadOnly_GuardUsesConfiguredBase(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":10,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(), WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
	if _, err := client.FetchUsage(context.Background()); err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/backend-api/wham/usage" {
		t.Fatalf("paths = %v, want only the configured usage path", paths)
	}
}
