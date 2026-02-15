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

func TestNewCopilotClient(t *testing.T) {
	logger := slog.Default()
	client := NewCopilotClient("ghp_test123", logger)
	if client == nil {
		t.Fatal("NewCopilotClient returned nil")
	}
	if client.baseURL != "https://api.github.com/copilot_internal/user" {
		t.Errorf("baseURL = %q, want default", client.baseURL)
	}
}

func TestNewCopilotClient_WithOptions(t *testing.T) {
	logger := slog.Default()
	client := NewCopilotClient("ghp_test123", logger,
		WithCopilotBaseURL("http://localhost:1234"),
		WithCopilotTimeout(5*time.Second),
	)
	if client.baseURL != "http://localhost:1234" {
		t.Errorf("baseURL = %q, want custom", client.baseURL)
	}
}

func TestCopilotClient_FetchQuotas_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_testtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept header = %q, want application/json", r.Header.Get("Accept"))
		}
		if r.Header.Get("User-Agent") != "onwatch/1.0" {
			t.Errorf("User-Agent = %q, want onwatch/1.0", r.Header.Get("User-Agent"))
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"login": "testuser",
			"copilot_plan": "individual_pro",
			"quota_reset_date_utc": "2026-03-01T00:00:00.000Z",
			"quota_snapshots": {
				"premium_interactions": {
					"entitlement": 1500, "remaining": 473,
					"percent_remaining": 31.578, "unlimited": false,
					"overage_count": 0, "overage_permitted": false
				},
				"chat": {
					"entitlement": 0, "remaining": 0,
					"percent_remaining": 100.0, "unlimited": true,
					"overage_count": 0, "overage_permitted": false
				}
			}
		}`)
	}))
	defer server.Close()

	logger := slog.Default()
	client := NewCopilotClient("ghp_testtoken", logger, WithCopilotBaseURL(server.URL))

	resp, err := client.FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas: %v", err)
	}

	if resp.Login != "testuser" {
		t.Errorf("Login = %q, want %q", resp.Login, "testuser")
	}
	if resp.CopilotPlan != "individual_pro" {
		t.Errorf("CopilotPlan = %q, want %q", resp.CopilotPlan, "individual_pro")
	}
	if len(resp.QuotaSnapshots) != 2 {
		t.Fatalf("QuotaSnapshots len = %d, want 2", len(resp.QuotaSnapshots))
	}
	if resp.QuotaSnapshots["premium_interactions"].Remaining != 473 {
		t.Errorf("premium remaining = %d, want 473", resp.QuotaSnapshots["premium_interactions"].Remaining)
	}
}

func TestCopilotClient_FetchQuotas_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message": "Bad credentials"}`)
	}))
	defer server.Close()

	client := NewCopilotClient("bad_token", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotUnauthorized) {
		t.Errorf("Expected ErrCopilotUnauthorized, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "Resource not accessible"}`)
	}))
	defer server.Close()

	client := NewCopilotClient("bad_scope", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotForbidden) {
		t.Errorf("Expected ErrCopilotForbidden, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotServerError) {
		t.Errorf("Expected ErrCopilotServerError, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_BadGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotServerError) {
		t.Errorf("Expected ErrCopilotServerError, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_EmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotInvalidResponse) {
		t.Errorf("Expected ErrCopilotInvalidResponse, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{invalid json`)
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotInvalidResponse) {
		t.Errorf("Expected ErrCopilotInvalidResponse, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_NetworkError(t *testing.T) {
	client := NewCopilotClient("ghp_test", slog.Default(),
		WithCopilotBaseURL("http://127.0.0.1:1"),
		WithCopilotTimeout(1*time.Second),
	)
	_, err := client.FetchQuotas(context.Background())
	if !errors.Is(err, ErrCopilotNetworkError) {
		t.Errorf("Expected ErrCopilotNetworkError, got %v", err)
	}
}

func TestCopilotClient_FetchQuotas_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Slow server
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.FetchQuotas(ctx)
	if err == nil {
		t.Fatal("Expected error for cancelled context")
	}
}

func TestCopilotClient_FetchQuotas_UnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	client := NewCopilotClient("ghp_test", slog.Default(), WithCopilotBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Fatal("Expected error for unexpected status code")
	}
	if errors.Is(err, ErrCopilotUnauthorized) || errors.Is(err, ErrCopilotServerError) {
		t.Errorf("Should not be a sentinel error, got: %v", err)
	}
}

func TestRedactCopilotToken(t *testing.T) {
	tests := []struct {
		token    string
		expected string
	}{
		{"", "(empty)"},
		{"short", "***...***"},
		{"ghp_abcdefghijklmnop", "ghp_***...***nop"},
	}

	for _, tt := range tests {
		got := redactCopilotToken(tt.token)
		if got != tt.expected {
			t.Errorf("redactCopilotToken(%q) = %q, want %q", tt.token, got, tt.expected)
		}
	}
}
