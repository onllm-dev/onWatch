package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewMiniMaxClient(t *testing.T) {
	client := NewMiniMaxClient("sk_test", slog.Default())
	if client == nil {
		t.Fatal("NewMiniMaxClient returned nil")
	}
	if client.baseURL != "https://api.minimax.io/v1/api/openplatform/coding_plan/remains" {
		t.Fatalf("baseURL=%q", client.baseURL)
	}
}

func TestNewMiniMaxClient_WithOptions(t *testing.T) {
	client := NewMiniMaxClient("sk_test", slog.Default(),
		WithMiniMaxBaseURL("http://localhost:9999"),
		WithMiniMaxTimeout(5*time.Second),
	)
	if client.baseURL != "http://localhost:9999" {
		t.Fatalf("baseURL=%q", client.baseURL)
	}
	if client.httpClient.Timeout != 5*time.Second {
		t.Fatalf("timeout=%v", client.httpClient.Timeout)
	}
}

func TestMiniMaxClient_FetchRemains_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk_testtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"unauthorized"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"base_resp": {"status_code": 0, "status_msg": "success"},
			"model_remains": [
				{
					"model_name": "MiniMax-M2",
					"start_time": 1771218000000,
					"end_time": 1771236000000,
					"remains_time": 205310,
					"current_interval_total_count": 15000,
					"current_interval_usage_count": 14077
				}
			]
		}`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("sk_testtoken", slog.Default(), WithMiniMaxBaseURL(server.URL))
	resp, err := client.FetchRemains(context.Background())
	if err != nil {
		t.Fatalf("FetchRemains: %v", err)
	}
	if resp.BaseResp.StatusCode != 0 {
		t.Fatalf("status_code=%d", resp.BaseResp.StatusCode)
	}
	if len(resp.ModelRemains) != 1 {
		t.Fatalf("model_remains=%d", len(resp.ModelRemains))
	}
	if resp.ModelRemains[0].ModelName != "MiniMax-M2" {
		t.Fatalf("model=%q", resp.ModelRemains[0].ModelName)
	}
}

func TestMiniMaxClient_FetchRemains_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"unauthorized"}`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("bad", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxUnauthorized) {
		t.Fatalf("expected ErrMiniMaxUnauthorized, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_AccessBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<!DOCTYPE html><html><title>Attention Required!</title><body>Please enable cookies. Sorry, you have been blocked</body></html>`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("blocked", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxAccessBlocked) {
		t.Fatalf("expected ErrMiniMaxAccessBlocked, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_UnauthorizedInBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"invalid token"},"model_remains":[]}`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("bad", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxUnauthorized) {
		t.Fatalf("expected ErrMiniMaxUnauthorized, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewMiniMaxClient("sk_test", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxServerError) {
		t.Fatalf("expected ErrMiniMaxServerError, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_EmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewMiniMaxClient("sk_test", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxInvalidResponse) {
		t.Fatalf("expected ErrMiniMaxInvalidResponse, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{invalid`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("sk_test", slog.Default(), WithMiniMaxBaseURL(server.URL))
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxInvalidResponse) {
		t.Fatalf("expected ErrMiniMaxInvalidResponse, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_NetworkError(t *testing.T) {
	client := NewMiniMaxClient("sk_test", slog.Default(),
		WithMiniMaxBaseURL("http://127.0.0.1:1"),
		WithMiniMaxTimeout(1*time.Second),
	)
	_, err := client.FetchRemains(context.Background())
	if !errors.Is(err, ErrMiniMaxNetworkError) {
		t.Fatalf("expected ErrMiniMaxNetworkError, got %v", err)
	}
}

func TestMiniMaxClient_FetchRemains_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"base_resp":{"status_code":0,"status_msg":"success"},"model_remains":[]}`)
	}))
	defer server.Close()

	client := NewMiniMaxClient("sk_test", slog.Default(), WithMiniMaxBaseURL(server.URL), WithMiniMaxTimeout(3*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.FetchRemains(ctx)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
