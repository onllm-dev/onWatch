package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testOpenCodeGoLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestOpenCodeGoClient_NormalizeCookieValue(t *testing.T) {
	t.Parallel()

	got := normalizeCookieValue("foo=1; auth=abc; bar=2; __Host-auth=xyz")
	if got != "auth=abc; __Host-auth=xyz" {
		t.Fatalf("normalizeCookieValue = %q", got)
	}

	passthrough := normalizeCookieValue("raw-cookie-value")
	if passthrough != "raw-cookie-value" {
		t.Fatalf("passthrough normalizeCookieValue = %q", passthrough)
	}
}

func TestOpenCodeGoClient_DoGet_StatusHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, want: ErrOpenCodeGoUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, want: ErrOpenCodeGoUnauthorized},
		{name: "server_error", status: http.StatusInternalServerError, want: ErrOpenCodeGoServerError},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("nope"))
			}))
			defer server.Close()

			client := NewOpenCodeGoClient("auth=ok", testOpenCodeGoLogger(), WithOpenCodeGoBaseURL(server.URL))
			_, err := client.doGet(context.Background(), server.URL)
			if !errors.Is(err, tt.want) {
				t.Fatalf("doGet error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOpenCodeGoClient_ResolveWorkspaceID_UsesFallback(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("no workspace")), Header: make(http.Header)}, nil
		}
		if req.Method == http.MethodPost {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"workspace":"wrk_fallback123"}`)), Header: make(http.Header)}, nil
		}
		return nil, fmt.Errorf("unexpected method %s", req.Method)
	})

	client := NewOpenCodeGoClient("auth=ok", testOpenCodeGoLogger())
	client.httpClient = &http.Client{Transport: transport}

	id, err := client.resolveWorkspaceID(context.Background())
	if err != nil {
		t.Fatalf("resolveWorkspaceID: %v", err)
	}
	if id != "wrk_fallback123" {
		t.Fatalf("workspace id = %q", id)
	}
}

func TestOpenCodeGoClient_FetchQuotas_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/workspace/wrk_test/go") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if cookie := r.Header.Get("Cookie"); cookie != "auth=abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `window.__INITIAL_STATE__ = {rollingUsage:$R[30]={status:"ok",resetInSec:3600,usagePercent:25}};`)
	}))
	defer server.Close()

	client := NewOpenCodeGoClient("foo=1; auth=abc", testOpenCodeGoLogger(), WithOpenCodeGoBaseURL(server.URL), WithOpenCodeGoWorkspaceID("wrk_test"), WithOpenCodeGoTimeout(2*time.Second))
	client.httpClient = server.Client()

	snap, err := client.FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas: %v", err)
	}
	if snap == nil || len(snap.Windows) != 1 {
		t.Fatalf("snapshot windows = %d", len(snap.Windows))
	}
	if got := snap.Windows[0].UsagePercent; got != 25 {
		t.Fatalf("usage percent = %.1f", got)
	}
}

func TestExtractWorkspaceIDAndTrimQuotes(t *testing.T) {
	t.Parallel()

	if got := trimQuotes(`"'wrk_abc'"`); got != "wrk_abc" {
		t.Fatalf("trimQuotes = %q", got)
	}
	if got := extractWorkspaceID([]byte(`prefix "wrk_one" middle "wrk_two"`)); got != "wrk_one" {
		t.Fatalf("extractWorkspaceID = %q", got)
	}
}
