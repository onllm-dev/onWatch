package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// usageSummaryFallbackServer returns a server whose primary endpoints yield no
// usable quotas (so the fallback fires) and whose /api/usage-summary responds
// with the given status and body.
func usageSummaryFallbackServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"enabled": true, "billingCycleStart": "1", "billingCycleEnd": "2", "displayThreshold": 100}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"planInfo": {"planName": "Enterprise"}}`))
		case "/api/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"gpt-4": {"numRequests": 0, "maxRequestUsage": null}}`))
		case "/api/usage-summary":
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newFallbackClient(t *testing.T, server *httptest.Server) *CursorClient {
	t.Helper()
	return NewCursorClient(createTestJWT("auth0|user_test", 1735689600), discardLoggerClient(),
		WithCursorBaseURL(server.URL),
		WithCursorWebBaseURL(server.URL),
	)
}

// H3: a fallback 401/403 must never be classified as an auth failure. The
// primary endpoint accepted the token, so the poll must succeed with the
// primary snapshot and the agent must not count an auth failure or refresh.
func TestCursorClient_FetchQuotas_UsageSummaryAuthStatusIsNotAuthError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := usageSummaryFallbackServer(t, status, `{"error":"nope"}`)
			defer server.Close()

			snapshot, err := newFallbackClient(t, server).FetchQuotas(context.Background())
			if err != nil {
				t.Fatalf("FetchQuotas returned an error for a fallback %d: %v", status, err)
			}
			if snapshot == nil {
				t.Fatal("FetchQuotas returned a nil snapshot")
			}
			if IsCursorAuthError(err) {
				t.Fatalf("fallback %d produced an auth-classified error", status)
			}
		})
	}
}

// H3: a fallback 404 is normal for accounts with no quotas. The base stored the
// (empty) primary snapshot; that behaviour must be preserved.
func TestCursorClient_FetchQuotas_UsageSummary404ReturnsPrimarySnapshot(t *testing.T) {
	server := usageSummaryFallbackServer(t, http.StatusNotFound, `not found`)
	defer server.Close()

	snapshot, err := newFallbackClient(t, server).FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas: %v", err)
	}
	if snapshot == nil {
		t.Fatal("FetchQuotas returned a nil snapshot")
	}
	if IsCursorAuthError(err) || IsCursorSessionExpired(err) {
		t.Fatal("fallback 404 was classified as an auth failure")
	}
	if snapshot.AccountType != CursorAccountEnterprise {
		t.Fatalf("AccountType = %q, want the primary Enterprise snapshot", snapshot.AccountType)
	}
}

// H3: an unparseable fallback body is also non-fatal: keep the primary snapshot.
func TestCursorClient_FetchQuotas_UsageSummaryUnparseableBodyKeepsPrimary(t *testing.T) {
	server := usageSummaryFallbackServer(t, http.StatusOK, `{"used": [}`)
	defer server.Close()

	snapshot, err := newFallbackClient(t, server).FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas: %v", err)
	}
	if snapshot == nil {
		t.Fatal("FetchQuotas returned a nil snapshot")
	}
}

// H3: transient fallback failures still fail the poll, so the agent keeps the
// last-known values, but the error must never satisfy the auth sentinels.
func TestCursorClient_FetchQuotas_UsageSummaryTransientReturnsNonAuthError(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := usageSummaryFallbackServer(t, status, `{}`)
			defer server.Close()

			snapshot, err := newFallbackClient(t, server).FetchQuotas(context.Background())
			if err == nil {
				t.Fatalf("FetchQuotas returned no error for a fallback %d", status)
			}
			if snapshot != nil {
				t.Fatalf("FetchQuotas returned a snapshot alongside the error: %+v", snapshot)
			}
			if IsCursorAuthError(err) || IsCursorSessionExpired(err) {
				t.Fatalf("transient fallback error was classified as auth: %v", err)
			}
		})
	}
}

// H3: the previous first-pass test assumed every fallback failure failed the
// poll. Only transient failures do; a 500 still does, a 404 no longer does.
func TestCursorClient_FetchQuotas_UsageSummary500StillFailsPoll(t *testing.T) {
	server := usageSummaryFallbackServer(t, http.StatusInternalServerError, ``)
	defer server.Close()

	if _, err := newFallbackClient(t, server).FetchQuotas(context.Background()); err == nil {
		t.Fatal("a fallback 500 must still fail the poll")
	}
}

// L5: every status >= 500 is server-side and transient, including 504 and
// Cloudflare's non-standard 52x statuses. The poll must fail, not store the
// empty primary snapshot.
func TestCursorClient_FetchQuotas_UsageSummaryServerStatusFailsPoll(t *testing.T) {
	for _, status := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		520, // Cloudflare unknown error
		522, // Cloudflare connection timed out
		525, // Cloudflare SSL handshake failed
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := usageSummaryFallbackServer(t, status, `{}`)
			defer server.Close()

			snapshot, err := newFallbackClient(t, server).FetchQuotas(context.Background())
			if err == nil {
				t.Fatalf("FetchQuotas returned no error for a fallback %d", status)
			}
			if !errors.Is(err, ErrCursorServerError) {
				t.Fatalf("fallback %d error = %v, want ErrCursorServerError", status, err)
			}
			if snapshot != nil {
				t.Fatalf("FetchQuotas returned a snapshot alongside the error: %+v", snapshot)
			}
			if IsCursorAuthError(err) || IsCursorSessionExpired(err) {
				t.Fatalf("fallback %d was classified as auth: %v", status, err)
			}
		})
	}
}

// L5: a cancelled context during the fallback means shutdown. The primary
// snapshot has no quotas, so it must not be stored as a successful poll.
func TestCursorClient_FetchQuotas_CancelledContextFailsPoll(t *testing.T) {
	server := usageSummaryFallbackServer(t, http.StatusServiceUnavailable, `{}`)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snapshot, err := newFallbackClient(t, server).FetchQuotas(ctx)
	if err == nil {
		t.Fatal("FetchQuotas returned no error for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a cancelled-context error", err)
	}
	if snapshot != nil {
		t.Fatalf("FetchQuotas returned a snapshot alongside the error: %+v", snapshot)
	}
}

// E5: a token echoed in the refresh response body must not appear in the
// returned error, which gets logged.
func TestRefreshCursorToken_HTTPErrorDoesNotLeakBody(t *testing.T) {
	const secret = "super-secret-token-material"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","echo":"` + secret + `","access_token":"` + secret + `"}`))
	}))
	defer server.Close()

	prev := cursorOAuthURL
	cursorOAuthURL = server.URL
	t.Cleanup(func() { cursorOAuthURL = prev })

	_, err := RefreshCursorToken(context.Background(), "bad_token")
	if err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("refresh error leaked response body token material: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("refresh error should still report the status code: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("refresh error should keep the sanitized OAuth error code: %v", err)
	}
}

// E5: a body that is not JSON contributes nothing but the status.
func TestRefreshCursorToken_HTTPErrorNonJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`token=abcdef<script>`))
	}))
	defer server.Close()

	prev := cursorOAuthURL
	cursorOAuthURL = server.URL
	t.Cleanup(func() { cursorOAuthURL = prev })

	_, err := RefreshCursorToken(context.Background(), "bad_token")
	if err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
	if strings.Contains(err.Error(), "abcdef") {
		t.Fatalf("refresh error leaked non-JSON body content: %v", err)
	}
}

// E5: the sanitizer accepts only short, code-shaped values, so a body that
// echoes a token in its error field cannot leak it either.
func TestCursorOAuthErrorCode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"recognized code", `{"error":"invalid_grant"}`, "invalid_grant"},
		{"error_code field", `{"error_code":"token-expired"}`, "token-expired"},
		{"token-like value rejected", `{"error":"eyJhbGciOiJSUzI1NiJ9.abc.def"}`, ""},
		{"long value rejected", `{"error":"` + strings.Repeat("a", 41) + `"}`, ""},
		{"non-json", `error=invalid_grant`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cursorOAuthErrorCode([]byte(tt.body)); got != tt.want {
				t.Fatalf("cursorOAuthErrorCode(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}
