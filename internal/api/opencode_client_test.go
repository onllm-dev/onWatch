package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const ssrFixtureHTML = `<!DOCTYPE html><html><body>
rollingUsage:$R[123]={usagePercent:4.5,resetInSec:6000}
weeklyUsage:$R[124]={resetInSec:1209600,usagePercent:12.3}
monthlyUsage:$R[125]={usagePercent:25.0,resetInSec:2592000}
</body></html>`

const dataSlotFixtureHTML = `<!DOCTYPE html><html><body>
<div data-slot="usage-item">
  <span data-slot="usage-label">Rolling Usage</span>
  <span data-slot="usage-value">15%</span>
  <span data-slot="reset-time">Resets in 1 hour 30 minutes</span>
</div>
<div data-slot="usage-item">
  <span data-slot="usage-label">Weekly Usage</span>
  <span data-slot="usage-value">22.5%</span>
  <span data-slot="reset-now">Reset now</span>
</div>
<div data-slot="usage-item">
  <span data-slot="usage-label">Monthly Usage</span>
  <span data-slot="usage-value">40%</span>
  <span data-slot="reset-time">Resets in 6 days 2 hours</span>
</div>
</body></html>`

func TestOpenCodeClient_FetchSnapshot_SSR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/ws-123/go" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if cookie := r.Header.Get("Cookie"); cookie != "auth=secret-cookie" {
			t.Errorf("unexpected cookie: %q", cookie)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssrFixtureHTML))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	snap, err := client.FetchSnapshot(context.Background(), "ws-123", "secret-cookie")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) != 3 {
		t.Fatalf("quotas = %d, want 3", len(snap.Quotas))
	}

	byName := mapQuotasByName(snap.Quotas)
	if byName["five_hour"].Utilization != 4.5 {
		t.Errorf("five_hour util = %v, want 4.5", byName["five_hour"].Utilization)
	}
	if byName["weekly"].Utilization != 12.3 {
		t.Errorf("weekly util = %v, want 12.3", byName["weekly"].Utilization)
	}
	if byName["monthly"].Utilization != 25.0 {
		t.Errorf("monthly util = %v, want 25.0", byName["monthly"].Utilization)
	}
	for _, q := range snap.Quotas {
		if q.Format != OpenCodeQuotaFormatPercent {
			t.Errorf("quota %s format = %q, want percent", q.Name, q.Format)
		}
		if q.ResetsAt == nil {
			t.Errorf("quota %s missing resetsAt", q.Name)
		}
	}
}

func TestOpenCodeClient_FetchSnapshot_DataSlotFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dataSlotFixtureHTML))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	snap, err := client.FetchSnapshot(context.Background(), "ws-abc", "tok")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) != 3 {
		t.Fatalf("quotas = %d, want 3", len(snap.Quotas))
	}
	byName := mapQuotasByName(snap.Quotas)
	if byName["five_hour"].Utilization != 15 {
		t.Errorf("five_hour util = %v, want 15", byName["five_hour"].Utilization)
	}
	if byName["weekly"].Utilization != 22.5 {
		t.Errorf("weekly util = %v, want 22.5", byName["weekly"].Utilization)
	}
	if byName["monthly"].Utilization != 40 {
		t.Errorf("monthly util = %v, want 40", byName["monthly"].Utilization)
	}

	rollingReset := byName["five_hour"].ResetsAt.Sub(snap.CapturedAt)
	if rollingReset < 89*time.Minute || rollingReset > 91*time.Minute {
		t.Errorf("five_hour reset offset = %v, want ~90m", rollingReset)
	}
}

func TestOpenCodeClient_FetchSnapshot_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("<html>login required secret</html>"))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	_, err := client.FetchSnapshot(context.Background(), "ws", "cookie")
	if !errors.Is(err, ErrOpenCodeUnauthorized) {
		t.Fatalf("err = %v, want ErrOpenCodeUnauthorized", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func TestOpenCodeClient_FetchSnapshot_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>no usage data</html>"))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	_, err := client.FetchSnapshot(context.Background(), "ws", "cookie")
	if !errors.Is(err, ErrOpenCodeParseFailed) {
		t.Fatalf("err = %v, want ErrOpenCodeParseFailed", err)
	}
}

func TestOpenCodeClient_FetchSnapshot_MissingConfig(t *testing.T) {
	client := NewOpenCodeClient(nil)
	_, err := client.FetchSnapshot(context.Background(), "", "cookie")
	if !errors.Is(err, ErrOpenCodeMissingConfig) {
		t.Fatalf("empty workspace err = %v", err)
	}
	_, err = client.FetchSnapshot(context.Background(), "ws", "")
	if !errors.Is(err, ErrOpenCodeMissingConfig) {
		t.Fatalf("empty cookie err = %v", err)
	}
}

func TestOpenCodeClient_FetchSnapshot_CookieWithEquals(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssrFixtureHTML))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	_, err := client.FetchSnapshot(context.Background(), "ws", "auth=already-set")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if gotCookie != "auth=already-set" {
		t.Errorf("cookie = %q, want auth=already-set", gotCookie)
	}
}

func TestOpenCodeClient_FetchSnapshot_WorkspaceURLEncoded(t *testing.T) {
	var gotRequestURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssrFixtureHTML))
	}))
	defer srv.Close()

	client := newTestOpenCodeClient(t, srv)
	_, err := client.FetchSnapshot(context.Background(), "ws/special id", "cookie")
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if !strings.Contains(gotRequestURI, "ws%2Fspecial%20id") {
		t.Errorf("request URI = %q, want encoded workspace id", gotRequestURI)
	}
}

func TestParseHumanReadableTime(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1 hour 56 minutes", 6960, true},
		{"6 days 2 hours", 525600, true},
		{"reset now", 0, true},
		{"not a duration", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseHumanReadableTime(tc.in)
		if ok != tc.ok {
			t.Errorf("parseHumanReadableTime(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseHumanReadableTime(%q) = %v, want %v", tc.in, got, tc.want)
		}
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

func mapQuotasByName(quotas []OpenCodeQuota) map[string]OpenCodeQuota {
	out := make(map[string]OpenCodeQuota, len(quotas))
	for _, q := range quotas {
		out[q.Name] = q
	}
	return out
}
