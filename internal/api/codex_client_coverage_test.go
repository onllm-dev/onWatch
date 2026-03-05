package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	var gotXAccount, gotChatClaudeAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXAccount = r.Header.Get("X-Account-Id")
		gotChatClaudeAccount = r.Header.Get("ChatClaude-Account-Id")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":10,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	defer server.Close()

	client := NewCodexClient("token", discardLoggerClient(), WithCodexBaseURL(server.URL))
	client.SetAccountID("acct_test")
	_, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if gotXAccount != "acct_test" {
		t.Errorf("X-Account-Id = %q, want acct_test", gotXAccount)
	}
	if gotChatClaudeAccount != "acct_test" {
		t.Errorf("ChatClaude-Account-Id = %q, want acct_test", gotChatClaudeAccount)
	}
}
