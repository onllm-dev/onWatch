package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenCodeClient_FetchSnapshot_CookieModeReadsGoStatus(t *testing.T) {
	var gotPath, gotCookie, gotOrg, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCookie, gotOrg = r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("x-org-id")
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = w.Write([]byte(goStatusBody))
	}))
	defer srv.Close()

	snap, err := newTestOpenCodeClient(t, srv).FetchSnapshot(context.Background(), " wrk_123 ", " sess-value ")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if gotPath != "/console/api/go/status" {
		t.Fatalf("path = %q, want /console/api/go/status", gotPath)
	}
	if gotCookie != "__Host-console_session=sess-value" || gotOrg != "wrk_123" {
		t.Fatalf("cookie=%q x-org-id=%q", gotCookie, gotOrg)
	}
	if gotAuth != "" || gotAccept != "application/json" {
		t.Fatalf("authorization=%q accept=%q, want no bearer and JSON", gotAuth, gotAccept)
	}
	if snap.PlanName != "OpenCode Go" || len(snap.Quotas) != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	weekly := quotaByName(t, snap.Quotas, "weekly")
	if weekly.Format != OpenCodeQuotaFormatCurrency || weekly.Limit != 30 || weekly.Utilization != 12.8 {
		t.Fatalf("weekly = %+v, want $30 currency quota at 12.8%%", weekly)
	}
	wantReset(t, quotaByName(t, snap.Quotas, "monthly"), "2026-10-24T15:00:25Z")
}

func TestOpenCodeClient_FetchSnapshot_CookieHeader(t *testing.T) {
	for _, tt := range []struct {
		name, value, want string
	}{
		{"bare value", "abc", "__Host-console_session=abc"},
		{"bare value with base64 padding", "token==", "__Host-console_session=token=="},
		{"named cookie", "__Host-console_session=abc", "__Host-console_session=abc"},
		{"full cookie header", "theme=dark; __Host-console_session=abc", "theme=dark; __Host-console_session=abc"},
		{"copied header line", "Cookie: __Host-console_session=abc", "__Host-console_session=abc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := openCodeConsoleCookieHeader(tt.value); got != tt.want {
				t.Fatalf("cookie header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenCodeClient_FetchSnapshot_RejectedCookieExplainsWhichCookie(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusFound} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == http.StatusFound {
				http.Redirect(w, r, "/auth/authorize", status)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"_tag":"Unauthorized","secret":"BODY-MARKER"}`))
		}))
		_, err := newTestOpenCodeClient(t, srv).FetchSnapshot(context.Background(), "ws", "auth=Fe26.2**old")
		srv.Close()
		if !errors.Is(err, ErrOpenCodeUnauthorized) {
			t.Fatalf("status %d: err = %v, want ErrOpenCodeUnauthorized", status, err)
		}
		if !strings.Contains(err.Error(), "__Host-console_session") {
			t.Fatalf("status %d: error %q does not name the cookie to paste", status, err)
		}
		if strings.Contains(err.Error(), "BODY-MARKER") || strings.Contains(err.Error(), "Fe26") {
			t.Fatalf("status %d: error leaks the body or cookie: %v", status, err)
		}
	}
}

func TestOpenCodeClient_FetchSnapshot_RedirectIsNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(goStatusBody))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/login", http.StatusFound)
	}))
	defer srv.Close()

	if _, err := newTestOpenCodeClient(t, srv).FetchSnapshot(context.Background(), "ws", "cookie"); !errors.Is(err, ErrOpenCodeUnauthorized) {
		t.Fatalf("err = %v, want ErrOpenCodeUnauthorized", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d request(s), want 0", targetHits.Load())
	}
}

func TestOpenCodeClient_FetchSnapshot_Malformed(t *testing.T) {
	srv := goStatusServer(t, `<html>no usage data</html>`)
	if _, err := newTestOpenCodeClient(t, srv).FetchSnapshot(context.Background(), "ws", "cookie"); !errors.Is(err, ErrOpenCodeParseFailed) {
		t.Fatalf("err = %v, want ErrOpenCodeParseFailed", err)
	}
}

func TestOpenCodeClient_FetchSnapshot_MissingConfigMakesNoRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	client := newTestOpenCodeClient(t, srv)
	if _, err := client.FetchSnapshot(context.Background(), "", "cookie"); !errors.Is(err, ErrOpenCodeMissingConfig) {
		t.Fatalf("empty workspace err = %v", err)
	}
	if _, err := client.FetchSnapshot(context.Background(), "ws", " "); !errors.Is(err, ErrOpenCodeMissingConfig) {
		t.Fatalf("empty cookie err = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("requests = %d, want 0", calls.Load())
	}
}

// The reuse window is per credential: a cookie never serves a cached API-key
// status (or another workspace's), and vice versa.
func TestOpenCodeClient_StatusReuseIsPerCredential(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(goStatusBody))
	}))
	defer srv.Close()
	c := newTestOpenCodeClient(t, srv)
	ctx := context.Background()
	steps := []func() error{
		func() error { _, err := c.FetchUsageSnapshot(ctx, "k"); return err },
		func() error { _, err := c.FetchSnapshot(ctx, "ws", "k"); return err },
		func() error { _, err := c.FetchSnapshot(ctx, "ws", "k"); return err },
		func() error { _, err := c.FetchSnapshot(ctx, "ws2", "k"); return err },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("status requests = %d, want 3 (key, cookie ws, cookie ws2)", calls.Load())
	}
}

func TestIsOpenCodeAuthError(t *testing.T) {
	if !IsOpenCodeAuthError(ErrOpenCodeUnauthorized) {
		t.Error("expected unauthorized")
	}
	if !IsOpenCodeAuthError(ErrOpenCodeForbidden) {
		t.Error("expected forbidden")
	}
	if IsOpenCodeAuthError(ErrOpenCodeParseFailed) {
		t.Error("parse failed should not be auth error")
	}
}

func newTestOpenCodeClient(t *testing.T, srv *httptest.Server) *OpenCodeClient {
	t.Helper()
	return NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL))
}
