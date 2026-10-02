package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexClient_SetToken(t *testing.T) {
	client := NewCodexClient("original", discardLoggerClient())
	if got := client.getToken(); got != "original" {
		t.Fatalf("initial token = %q, want original", got)
	}
	client.SetToken("new-token")
	if got := client.getToken(); got != "new-token" {
		t.Fatalf("after SetToken = %q, want new-token", got)
	}
}

func TestCodexClient_SetAccountID(t *testing.T) {
	client := NewCodexClient("token", discardLoggerClient())
	if got := client.getAccountID(); got != "" {
		t.Fatalf("initial accountID = %q, want empty", got)
	}
	client.SetAccountID("acct_456")
	if got := client.getAccountID(); got != "acct_456" {
		t.Fatalf("after SetAccountID = %q, want acct_456", got)
	}
}

func TestBuildCodexFallbackBaseURL_CodexToWham(t *testing.T) {
	url, ok := buildCodexFallbackBaseURL("https://chatgpt.com/api/codex/usage")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if url != "https://chatgpt.com/backend-api/wham/usage" {
		t.Fatalf("fallback = %q, want https://chatgpt.com/backend-api/wham/usage", url)
	}
}

func TestBuildCodexFallbackBaseURL_WhamToCodex(t *testing.T) {
	url, ok := buildCodexFallbackBaseURL("https://chatgpt.com/backend-api/wham/usage")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if url != "https://chatgpt.com/api/codex/usage" {
		t.Fatalf("fallback = %q, want https://chatgpt.com/api/codex/usage", url)
	}
}

func TestBuildCodexFallbackBaseURL_UnknownPath(t *testing.T) {
	_, ok := buildCodexFallbackBaseURL("https://chatgpt.com/other/path")
	if ok {
		t.Fatal("expected ok=false for unknown path")
	}
}

func TestBuildCodexFallbackBaseURL_InvalidURL(t *testing.T) {
	_, ok := buildCodexFallbackBaseURL("://invalid-url")
	if ok {
		t.Fatal("expected ok=false for invalid URL")
	}
}

func TestCodexClient_FetchUsage_EmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write nothing
	}))
	defer server.Close()

	client := NewCodexClient("token", discardLoggerClient(), WithCodexBaseURL(server.URL))
	_, err := client.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestCodexClient_FetchUsage_UnexpectedStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418
	}))
	defer server.Close()

	client := NewCodexClient("token", discardLoggerClient(), WithCodexBaseURL(server.URL))
	_, err := client.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error for 418")
	}
}

func TestCodexClient_WithCodexTimeout(t *testing.T) {
	client := NewCodexClient("token", discardLoggerClient(), WithCodexTimeout(42*1e9))
	if client.httpClient.Timeout != 42*1e9 {
		t.Fatalf("timeout = %v, want 42s", client.httpClient.Timeout)
	}
}

func TestCodexClient_FetchUsage_FallbacksTo404BothPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both paths return 404
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewCodexClient("token", discardLoggerClient(), WithCodexBaseURL(server.URL+"/api/codex/usage"))
	_, err := client.FetchUsage(context.Background())
	if err == nil {
		t.Fatal("expected error when both paths return 404")
	}
}

func TestCodexClient_FetchUsage_AccountIDHeaders(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "primary"
		if fallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			client := NewCodexClient("token", discardLoggerClient(), WithCodexBaseURL("https://example.invalid/backend-api/wham/usage"))
			var requests []*http.Request
			client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req)
				status := http.StatusOK
				if fallback && req.URL.Path == "/backend-api/wham/usage" {
					status = http.StatusNotFound
				}
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader(`{"plan_type":"pro"}`)),
					Header:     make(http.Header),
				}, nil
			})

			for _, accountID := range []string{"", "acct_test", ""} {
				client.SetAccountID(accountID)
				// Re-probe both paths for every account state while retaining the
				// same client so clearing the ID also detects stale headers.
				client.clearFallbackBaseURL()
				requests = nil
				if _, err := client.FetchUsage(context.Background()); err != nil {
					t.Fatalf("FetchUsage: %v", err)
				}
				paths := []string{"/backend-api/wham/usage"}
				if fallback {
					paths = append(paths, "/api/codex/usage")
				}
				if len(requests) != len(paths) {
					t.Fatalf("requests = %d, want %d", len(requests), len(paths))
				}
				for i, req := range requests {
					if req.URL.Path != paths[i] {
						t.Errorf("request path = %q, want %q", req.URL.Path, paths[i])
					}
					if req.Header.Get("Authorization") != "Bearer token" {
						t.Error("Authorization did not preserve bearer authentication")
					}
					if got := req.Header.Get("ChatGPT-Account-Id"); got != accountID {
						t.Errorf("%s ChatGPT-Account-Id = %q, want %q", req.URL.Path, got, accountID)
					}
					if accountID == "" {
						if _, exists := req.Header[http.CanonicalHeaderKey("ChatGPT-Account-Id")]; exists {
							t.Error("ChatGPT-Account-Id should be absent without an account ID")
						}
					}
					for _, obsolete := range []string{"X-Account-Id", "ChatClaude-Account-Id"} {
						if _, exists := req.Header[http.CanonicalHeaderKey(obsolete)]; exists {
							t.Errorf("%s obsolete %s header should be absent", req.URL.Path, obsolete)
						}
					}
				}
			}
		})
	}
}
