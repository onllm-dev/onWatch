package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

const codexResetCreditsJSON = `{
  "credits": [
    {
      "id": "credit-1",
      "reset_type": "codex_rate_limits",
      "status": "available",
      "granted_at": "2026-06-17T00:00:00Z",
      "expires_at": "2026-07-17T00:00:00Z"
    },
    {
      "id": "credit-2",
      "reset_type": "codex_rate_limits",
      "status": "available",
      "granted_at": "2026-06-18T00:00:00Z",
      "expires_at": null
    }
  ],
  "available_count": 2,
  "total_earned_count": 4
}`

func TestBuildCodexResetCreditsURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{
			in:   "https://chatgpt.com/backend-api/wham/usage",
			want: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
			ok:   true,
		},
		{
			in:   "https://chatgpt.com/api/codex/usage",
			want: "https://chatgpt.com/api/codex/rate-limit-reset-credits",
			ok:   true,
		},
		{
			in:   "https://chatgpt.com/backend-api/wham/usage?x=1",
			want: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
			ok:   true,
		},
		{in: "https://example.test/unknown", ok: false},
	}
	for _, tc := range cases {
		got, ok := buildCodexResetCreditsURL(tc.in)
		if ok != tc.ok {
			t.Fatalf("buildCodexResetCreditsURL(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("buildCodexResetCreditsURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCodexClient_FetchResetCredits_DefaultBaseAndHeaders(t *testing.T) {
	var gotPath, gotAuth, gotAccount string
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-Id")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, codexResetCreditsJSON)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
	client.SetAccountID("acct-123")

	resp, err := client.FetchResetCredits(context.Background())
	if err != nil {
		t.Fatalf("FetchResetCredits: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if gotPath != "/backend-api/wham/rate-limit-reset-credits" {
		t.Fatalf("path = %q, want the wham detail path", gotPath)
	}
	if gotAuth != "Bearer oauth_token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccount != "acct-123" {
		t.Fatalf("ChatGPT-Account-Id = %q", gotAccount)
	}
	if resp.AvailableCount == nil || *resp.AvailableCount != 2 || len(resp.Credits) != 2 {
		t.Fatalf("response = %#v", resp)
	}
}

func TestCodexClient_FetchResetCredits_AlternateBase(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, codexResetCreditsJSON)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/api/codex/usage"))

	if _, err := client.FetchResetCredits(context.Background()); err != nil {
		t.Fatalf("FetchResetCredits: %v", err)
	}
	if gotPath != "/api/codex/rate-limit-reset-credits" {
		t.Fatalf("path = %q, want the codex-api detail path", gotPath)
	}
}

func TestCodexClient_FetchResetCredits_UsesCachedWorkingUsageBase(t *testing.T) {
	var gotPath string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, codexResetCreditsJSON)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
	// The usage poll learned that this account is served from the alternate base.
	client.setFallbackBaseURL(srv.URL + "/api/codex/usage")

	if _, err := client.FetchResetCredits(context.Background()); err != nil {
		t.Fatalf("FetchResetCredits: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/codex/rate-limit-reset-credits" {
		t.Fatalf("path = %q, want the cached alternate base", gotPath)
	}
}

func TestCodexClient_FetchResetCredits_404FallsBackOnceThenUnavailable(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/rate-limit-reset-credits":
			primaryCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		case "/api/codex/rate-limit-reset-credits":
			fallbackCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))

	_, err := client.FetchResetCredits(context.Background())
	if !errors.Is(err, ErrCodexResetCreditsUnavailable) {
		t.Fatalf("err = %v, want ErrCodexResetCreditsUnavailable", err)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("calls = %d/%d, want exactly one probe each", primaryCalls.Load(), fallbackCalls.Load())
	}
}

func TestCodexClient_FetchResetCredits_UnauthorizedUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		status := status
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			client := NewCodexClient("api_key_like_token", discardLoggerClient(),
				WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
			_, err := client.FetchResetCredits(context.Background())
			if !errors.Is(err, ErrCodexResetCreditsUnavailable) {
				t.Fatalf("err = %v, want ErrCodexResetCreditsUnavailable", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1 (never retried)", calls.Load())
			}
		})
	}
}

func TestCodexClient_FetchResetCredits_MissingCreditsFieldUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"available_count": 1}`)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
	_, err := client.FetchResetCredits(context.Background())
	if !errors.Is(err, ErrCodexResetCreditsUnavailable) {
		t.Fatalf("err = %v, want ErrCodexResetCreditsUnavailable", err)
	}
}

func TestCodexClient_FetchResetCredits_ServerErrorDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewCodexClient("oauth_token", discardLoggerClient(),
		WithCodexBaseURL(srv.URL+"/backend-api/wham/usage"))
	if _, err := client.FetchResetCredits(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry loop)", calls.Load())
	}
}
