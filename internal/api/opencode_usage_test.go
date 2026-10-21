package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// goStatusBody is the retired console go/status shape, still used by the
// browser-session mode. IDs replaced.
const goStatusBody = `{"subscriberUserId":"user_TESTSUBSCRIBER","product":"go","renewalProduct":"go",
"paymentMethodId":"payment_method_TESTPM","renewalCurrency":"usd","useBalance":false,
"cancelAtPeriodEnd":false,"renewalPending":false,"upgradePrice":1000,
"access":{"startsAt":"2026-09-24T15:00:25.000Z","endsAt":"2026-10-24T15:00:25.000Z","cancelAtPeriodEnd":false,
"meters":{
"fiveHour":{"startsAt":null,"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"0"},
"week":{"startsAt":"2026-09-21T00:00:00.000Z","resetsAt":"2026-09-28T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"384204992"},
"month":{"limitMicroCents":"6000000000","usedMicroCents":"384204992"}}}}`

// zenUsageBody is the live /zen/go/v1/usage shape (2026-10-17). Each window is
// an integer percentage with a reset time.
const zenUsageBody = `{"usage":{"rolling":{"status":"ok","percent":5,"resetsAt":"2026-10-16T23:13:00.000Z"},"weekly":{"status":"ok","percent":2,"resetsAt":"2026-10-19T00:00:00.000Z"},"monthly":{"status":"ok","percent":1,"resetsAt":"2026-11-16T18:01:43.000Z"}}}`

func quotaByName(t *testing.T, quotas []OpenCodeQuota, name string) OpenCodeQuota {
	t.Helper()
	for _, q := range quotas {
		if q.Name == name {
			return q
		}
	}
	t.Fatalf("quota %q missing from %+v", name, quotas)
	return OpenCodeQuota{}
}

func goStatusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchZenUsageBody(t *testing.T, body string) (*OpenCodeSnapshot, error) {
	t.Helper()
	srv := goStatusServer(t, body)
	return NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "k")
}

func wantReset(t *testing.T, q OpenCodeQuota, want string) {
	t.Helper()
	if want == "" {
		if q.ResetsAt != nil {
			t.Fatalf("%s ResetsAt = %v, want none", q.Name, q.ResetsAt)
		}
		return
	}
	w, _ := time.Parse(time.RFC3339, want)
	if q.ResetsAt == nil || !q.ResetsAt.Equal(w) {
		t.Fatalf("%s ResetsAt = %v, want %v", q.Name, q.ResetsAt, w)
	}
}

func TestFetchUsageSnapshot_SendsHeadersAndMapsWindows(t *testing.T) {
	var gotMethod, gotPath, gotUA, gotAccept, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotUA, gotAccept, gotAuth = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(zenUsageBody))
	}))
	defer srv.Close()

	snap, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL+"/")).FetchUsageSnapshot(context.Background(), " oc_sk_test ")
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/zen/go/v1/usage" {
		t.Fatalf("request = %s %s, want GET /zen/go/v1/usage", gotMethod, gotPath)
	}
	if gotAuth != "Bearer oc_sk_test" || gotAccept != "application/json" || gotUA != "onwatch-opencode-usage/1" {
		t.Fatalf("headers: auth=%q accept=%q ua=%q", gotAuth, gotAccept, gotUA)
	}
	if snap.PlanName != "OpenCode Go" || snap.AccountType != OpenCodeAccountTypePro || snap.CapturedAt.IsZero() {
		t.Fatalf("snapshot = %+v", snap)
	}
	names := []string{}
	for _, q := range snap.Quotas {
		names = append(names, q.Name)
		if q.Format != OpenCodeQuotaFormatPercent {
			t.Fatalf("quota %+v, want percent format", q)
		}
	}
	if strings.Join(names, ",") != "five_hour,weekly,monthly" {
		t.Fatalf("quota names = %v", names)
	}
	for name, want := range map[string]float64{"five_hour": 5, "weekly": 2, "monthly": 1} {
		q := quotaByName(t, snap.Quotas, name)
		if q.Utilization != want || q.Used != want || q.Limit != 100 {
			t.Fatalf("%s = util %v used %v limit %v, want %v/100", name, q.Utilization, q.Used, q.Limit, want)
		}
	}
	wantReset(t, quotaByName(t, snap.Quotas, "five_hour"), "2026-10-16T23:13:00Z")
	wantReset(t, quotaByName(t, snap.Quotas, "weekly"), "2026-10-19T00:00:00Z")
	wantReset(t, quotaByName(t, snap.Quotas, "monthly"), "2026-11-16T18:01:43Z")
	if dump := fmt.Sprintf("%+v", *snap); strings.Contains(dump, "TESTSUBSCRIBER") || strings.Contains(dump, "TESTPM") {
		t.Fatal("snapshot carries account IDs")
	}
}

func TestFetchUsageSnapshot_FractionalPercentAndMissingMonthly(t *testing.T) {
	// A plan without a monthly window: rolling and weekly still map.
	body := `{"usage":{"rolling":{"status":"ok","percent":31.6,"resetsAt":"2026-10-16T23:13:00.000Z"},"weekly":{"status":"rate-limited","percent":100,"resetsAt":null}}}`
	snap, err := fetchZenUsageBody(t, body)
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	if len(snap.Quotas) != 2 {
		t.Fatalf("quotas = %+v, want 2 (monthly optional)", snap.Quotas)
	}
	if got := quotaByName(t, snap.Quotas, "five_hour").Utilization; got != 31.6 {
		t.Fatalf("five_hour = %v, want 31.6", got)
	}
	if got := quotaByName(t, snap.Quotas, "weekly").Utilization; got != 100 {
		t.Fatalf("weekly = %v, want 100", got)
	}
	wantReset(t, quotaByName(t, snap.Quotas, "weekly"), "")
}

// The usage API reports a per-window status alongside the percent. It must be
// carried through so a window that is not "ok" can render critical whatever
// its percent, and an unknown value must survive verbatim.
func TestFetchUsageSnapshot_CarriesWindowStatus(t *testing.T) {
	body := `{"usage":{"rolling":{"status":"exhausted","percent":10,"resetsAt":null},"weekly":{"status":"ok","percent":4,"resetsAt":null},"monthly":{"status":"rate-limited","percent":2,"resetsAt":null}}}`
	snap, err := fetchZenUsageBody(t, body)
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	want := map[string]string{"five_hour": "exhausted", "weekly": "ok", "monthly": "rate-limited"}
	for name, status := range want {
		if got := quotaByName(t, snap.Quotas, name).Status; got != status {
			t.Fatalf("%s status = %q, want %q", name, got, status)
		}
	}
}

func TestOpenCodeZenQuotas_MissingStatusIsEmpty(t *testing.T) {
	var u openCodeZenUsage
	if err := json.Unmarshal([]byte(`{"usage":{"rolling":{"percent":1},"weekly":{"percent":1}}}`), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	quotas, err := u.quotas()
	if err != nil {
		t.Fatalf("quotas: %v", err)
	}
	if got := quotaByName(t, quotas, "five_hour").Status; got != "" {
		t.Fatalf("status = %q, want empty for a window with no status", got)
	}
}

func TestFetchUsageSnapshot_MalformedStatusIsParseFailed(t *testing.T) {
	cases := map[string]string{
		"not json":        `<html>maintenance</html>`,
		"empty object":    `{}`,
		"null usage":      `{"usage":null}`,
		"no rolling":      `{"usage":{"weekly":{"percent":1}}}`,
		"no weekly":       `{"usage":{"rolling":{"percent":1}}}`,
		"null percent":    `{"usage":{"rolling":{"percent":null},"weekly":{"percent":1}}}`,
		"percent too low": `{"usage":{"rolling":{"percent":-1},"weekly":{"percent":1}}}`,
		"percent too big": `{"usage":{"rolling":{"percent":101},"weekly":{"percent":1}}}`,
		"bad resetsAt":    `{"usage":{"rolling":{"percent":1,"resetsAt":"next monday"},"weekly":{"percent":1}}}`,
	}
	for name, body := range cases {
		if _, err := fetchZenUsageBody(t, body); !errors.Is(err, ErrOpenCodeParseFailed) {
			t.Errorf("%s: err=%v, want ErrOpenCodeParseFailed", name, err)
		}
	}
}

func TestFetchUsageSnapshot_MapsHTTPErrorsWithoutEchoingBody(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrOpenCodeUnauthorized},
		{http.StatusForbidden, ErrOpenCodeForbidden},
		{http.StatusTooManyRequests, ErrOpenCodeRateLimited},
		{http.StatusInternalServerError, ErrOpenCodeServerError},
		{http.StatusBadGateway, ErrOpenCodeServerError},
		{http.StatusBadRequest, ErrOpenCodeInvalidResponse},
		{http.StatusNotFound, ErrOpenCodeInvalidResponse},
		{http.StatusFound, ErrOpenCodeUnauthorized}, // login redirect
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"_tag":"Forbidden","secret":"BODY-MARKER"}`))
		}))
		_, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "k")
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err=%v, want %v", tc.status, err, tc.want)
		}
		if strings.Contains(err.Error(), "BODY-MARKER") {
			t.Fatalf("status %d: error echoes the response body: %v", tc.status, err)
		}
	}
}

// A rejected key must say so plainly, not just "forbidden".
func TestFetchUsageSnapshot_AuthErrorsNameTheFix(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "oc_sk_bad")
		srv.Close()
		if !IsOpenCodeAuthError(err) {
			t.Fatalf("status %d: err = %v, want an auth error", status, err)
		}
		if !strings.Contains(err.Error(), "Usage API Key") {
			t.Fatalf("status %d: error %q does not point at the Usage API Key setting", status, err)
		}
	}
}

func TestFetchUsageSnapshot_MissingKeyMakesNoRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	_, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "  ")
	if !errors.Is(err, ErrOpenCodeMissingAPIKey) || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d, want ErrOpenCodeMissingAPIKey and no request", err, calls.Load())
	}
}

func TestFetchUsageSnapshot_OversizedBodyIsAnError(t *testing.T) {
	// Valid JSON followed by whitespace: only the size cap can reject it.
	srv := goStatusServer(t, zenUsageBody+strings.Repeat(" ", openCodeUsageMaxBodyBytes))
	_, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "k")
	if !errors.Is(err, ErrOpenCodeInvalidResponse) {
		t.Fatalf("oversized body: err=%v, want ErrOpenCodeInvalidResponse", err)
	}
}

func TestFetchUsageSnapshot_ReusesARecentStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(zenUsageBody))
	}))
	defer srv.Close()
	c := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL))
	for _, key := range []string{"k", "k", "other"} {
		snap, err := c.FetchUsageSnapshot(context.Background(), key)
		if err != nil {
			t.Fatalf("FetchUsageSnapshot: %v", err)
		}
		if got := quotaByName(t, snap.Quotas, "weekly").Utilization; got != 2 {
			t.Fatalf("reused snapshot weekly = %v, want 2", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("status requests = %d, want 2 (reuse within %v, refetch on key change)", calls.Load(), openCodeUsageMinInterval)
	}
}

// A status served from the 60s reuse window was fetched earlier. Stamping the
// snapshot with the serve time makes up to a minute-old data read as fresh.
func TestFetchUsageSnapshot_ReuseKeepsTheFillTime(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(zenUsageBody))
	}))
	defer srv.Close()
	c := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL))

	first, err := c.FetchUsageSnapshot(context.Background(), "k")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Simulate a poll one full interval later: the client cache is deliberately
	// aged just inside the window so it is reused.
	c.usageMu.Lock()
	servedAt := time.Now().Add(-openCodeUsageMinInterval + 5*time.Second)
	c.usageAt = servedAt
	c.usageMu.Unlock()

	second, err := c.FetchUsageSnapshot(context.Background(), "k")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("status requests = %d, want 1 (reused)", calls.Load())
	}
	if !second.CapturedAt.Truncate(time.Second).Equal(servedAt.Truncate(time.Second)) {
		t.Fatalf("reused CapturedAt = %v, want the cache fill time %v", second.CapturedAt, servedAt)
	}
	if second.CapturedAt.After(first.CapturedAt) {
		t.Fatalf("reused CapturedAt moved forward: %v after %v", second.CapturedAt, first.CapturedAt)
	}
}

func TestFetchUsageSnapshot_FailuresAreNotReused(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			_, _ = w.Write([]byte(`{"usage":{}}`))
		default:
			_, _ = w.Write([]byte(zenUsageBody))
		}
	}))
	defer srv.Close()
	c := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL))
	if _, err := c.FetchUsageSnapshot(context.Background(), "k"); !errors.Is(err, ErrOpenCodeRateLimited) {
		t.Fatalf("first: err=%v, want ErrOpenCodeRateLimited", err)
	}
	if _, err := c.FetchUsageSnapshot(context.Background(), "k"); !errors.Is(err, ErrOpenCodeParseFailed) {
		t.Fatalf("second: err=%v, want ErrOpenCodeParseFailed", err)
	}
	if _, err := c.FetchUsageSnapshot(context.Background(), "k"); err != nil {
		t.Fatalf("third: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("status requests = %d, want 3 (errors are retried on the next poll)", calls.Load())
	}
}

func TestNewOpenCodeClient_Timeout(t *testing.T) {
	c := NewOpenCodeClient(nil)
	if c.httpClient.Timeout != openCodeUsageTimeout {
		t.Fatalf("timeout = %v, want %v", c.httpClient.Timeout, openCodeUsageTimeout)
	}
	if tr, ok := c.httpClient.Transport.(*http.Transport); !ok || tr.ResponseHeaderTimeout != openCodeUsageTimeout {
		t.Fatalf("transport = %+v", c.httpClient.Transport)
	}
	if long := NewOpenCodeClient(nil, WithOpenCodeTimeout(45*time.Second)); long.httpClient.Timeout != 45*time.Second {
		t.Fatalf("configured timeout = %v, want 45s", long.httpClient.Timeout)
	}
}

// Rounding: percent is stored to one decimal so charts and cycle math are
// stable across the integer API values.
func TestOpenCodeZenQuotas_RoundsToOneDecimal(t *testing.T) {
	var u openCodeZenUsage
	if err := json.Unmarshal([]byte(`{"usage":{"rolling":{"percent":33.33333},"weekly":{"percent":0}}}`), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	quotas, err := u.quotas()
	if err != nil {
		t.Fatalf("quotas: %v", err)
	}
	if got := quotaByName(t, quotas, "five_hour").Utilization; math.Abs(got-33.3) > 1e-9 {
		t.Fatalf("five_hour = %v, want 33.3", got)
	}
}
