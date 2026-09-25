package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// goStatusBody is the live go/status shape (2026-09-25), IDs replaced.
const goStatusBody = `{"subscriberUserId":"user_TESTSUBSCRIBER","product":"go","renewalProduct":"go",
"paymentMethodId":"payment_method_TESTPM","renewalCurrency":"usd","useBalance":false,
"cancelAtPeriodEnd":false,"renewalPending":false,"upgradePrice":1000,
"access":{"startsAt":"2026-09-24T15:00:25.000Z","endsAt":"2026-10-24T15:00:25.000Z","cancelAtPeriodEnd":false,
"meters":{
"fiveHour":{"startsAt":null,"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"0"},
"week":{"startsAt":"2026-09-21T00:00:00.000Z","resetsAt":"2026-09-28T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"384204992"},
"month":{"limitMicroCents":"6000000000","usedMicroCents":"384204992"}}}}`

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

func fetchGoStatusBody(t *testing.T, body string) (*OpenCodeSnapshot, error) {
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

func TestFetchUsageSnapshot_SendsHeadersAndMapsMeters(t *testing.T) {
	var gotMethod, gotPath, gotUA, gotAccept, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotUA, gotAccept, gotAuth = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(goStatusBody))
	}))
	defer srv.Close()

	snap, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL+"/")).FetchUsageSnapshot(context.Background(), " oc_sk_test ")
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/console/api/go/status" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
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
		if q.Limit != 100 || q.Used != q.Utilization || q.Format != OpenCodeQuotaFormatPercent {
			t.Fatalf("quota %+v, want percent of 100 with Used == Utilization", q)
		}
	}
	if strings.Join(names, ",") != "five_hour,weekly,monthly" {
		t.Fatalf("quota names = %v", names)
	}
	// 384204992 / 3000000000 = 12.807% and / 6000000000 = 6.403% (console: 12.81%, 6.40%).
	for name, want := range map[string]float64{"five_hour": 0, "weekly": 12.8, "monthly": 6.4} {
		if got := quotaByName(t, snap.Quotas, name).Utilization; got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	wantReset(t, quotaByName(t, snap.Quotas, "five_hour"), "") // no open 5-hour session
	wantReset(t, quotaByName(t, snap.Quotas, "weekly"), "2026-09-28T00:00:00Z")
	wantReset(t, quotaByName(t, snap.Quotas, "monthly"), "2026-10-24T15:00:25Z") // access.endsAt
	if dump := fmt.Sprintf("%+v", *snap); strings.Contains(dump, "TESTSUBSCRIBER") || strings.Contains(dump, "TESTPM") {
		t.Fatal("snapshot carries account IDs")
	}
}

func TestFetchUsageSnapshot_OpenFiveHourSessionAndMonthResetsAt(t *testing.T) {
	body := strings.Replace(goStatusBody,
		`"fiveHour":{"startsAt":null,"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"0"}`,
		`"fiveHour":{"startsAt":"2026-09-25T09:00:00.000Z","resetsAt":"2026-09-25T14:00:00.000Z","limitMicroCents":"1200000000","usedMicroCents":"400000000"}`, 1)
	body = strings.Replace(body, `"month":{`, `"month":{"resetsAt":"2026-10-20T00:00:00.000Z",`, 1)
	snap, err := fetchGoStatusBody(t, body)
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	five := quotaByName(t, snap.Quotas, "five_hour")
	if five.Utilization != 33.3 {
		t.Fatalf("five_hour = %v, want 33.3 (one decimal)", five.Utilization)
	}
	wantReset(t, five, "2026-09-25T14:00:00Z")
	// A month meter that carries its own resetsAt wins over access.endsAt.
	wantReset(t, quotaByName(t, snap.Quotas, "monthly"), "2026-10-20T00:00:00Z")
}

func TestFetchUsageSnapshot_AcceptsBareNumberAmounts(t *testing.T) {
	body := strings.Replace(goStatusBody, `"limitMicroCents":"3000000000","usedMicroCents":"384204992"`,
		`"limitMicroCents":3000000000,"usedMicroCents":1500000000`, 1)
	snap, err := fetchGoStatusBody(t, body)
	if err != nil {
		t.Fatalf("FetchUsageSnapshot: %v", err)
	}
	if got := quotaByName(t, snap.Quotas, "weekly").Utilization; got != 50 {
		t.Fatalf("weekly = %v, want 50", got)
	}
}

func TestFetchUsageSnapshot_MalformedStatusIsParseFailed(t *testing.T) {
	week := `"week":{"startsAt":"2026-09-21T00:00:00.000Z","resetsAt":"2026-09-28T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"384204992"},`
	month := ",\n" + `"month":{"limitMicroCents":"6000000000","usedMicroCents":"384204992"}`
	cases := map[string]string{
		"not json":         `<html>maintenance</html>`,
		"empty object":     `{}`,
		"null access":      `{"access":null}`,
		"no meters":        `{"access":{"endsAt":"2026-10-24T15:00:25.000Z"}}`,
		"no fiveHour":      strings.Replace(goStatusBody, `"fiveHour":{"startsAt":null,"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"0"},`, "", 1),
		"no week":          strings.Replace(goStatusBody, week, "", 1),
		"no month":         strings.Replace(goStatusBody, month, "", 1),
		"no limit":         strings.Replace(goStatusBody, `"limitMicroCents":"6000000000",`, "", 1),
		"no used":          strings.Replace(goStatusBody, `,"usedMicroCents":"0"`, "", 1),
		"null limit":       strings.Replace(goStatusBody, `"limitMicroCents":"6000000000"`, `"limitMicroCents":null`, 1),
		"zero limit":       strings.Replace(goStatusBody, `"limitMicroCents":"6000000000"`, `"limitMicroCents":"0"`, 1),
		"negative limit":   strings.Replace(goStatusBody, `"limitMicroCents":"3000000000"`, `"limitMicroCents":"-3000000000"`, 1),
		"negative used":    strings.Replace(goStatusBody, `"usedMicroCents":"0"`, `"usedMicroCents":"-1"`, 1),
		"non-numeric used": strings.Replace(goStatusBody, `"usedMicroCents":"0"`, `"usedMicroCents":"abc"`, 1),
		"empty limit":      strings.Replace(goStatusBody, `"limitMicroCents":"1200000000"`, `"limitMicroCents":""`, 1),
		"NaN limit":        strings.Replace(goStatusBody, `"limitMicroCents":"1200000000"`, `"limitMicroCents":"NaN"`, 1),
		"boolean used":     strings.Replace(goStatusBody, `"usedMicroCents":"0"`, `"usedMicroCents":true`, 1),
		"bad resetsAt":     strings.Replace(goStatusBody, `"resetsAt":"2026-09-28T00:00:00.000Z"`, `"resetsAt":"next monday"`, 1),
	}
	for name, body := range cases {
		if body == goStatusBody {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if _, err := fetchGoStatusBody(t, body); !errors.Is(err, ErrOpenCodeParseFailed) {
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
		{http.StatusFound, ErrOpenCodeInvalidResponse},
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
	srv := goStatusServer(t, goStatusBody+strings.Repeat(" ", openCodeUsageMaxBodyBytes))
	_, err := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL)).FetchUsageSnapshot(context.Background(), "k")
	if !errors.Is(err, ErrOpenCodeInvalidResponse) {
		t.Fatalf("oversized body: err=%v, want ErrOpenCodeInvalidResponse", err)
	}
}

func TestFetchUsageSnapshot_ReusesARecentStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(goStatusBody))
	}))
	defer srv.Close()
	c := NewOpenCodeClient(nil, WithOpenCodeBaseURL(srv.URL))
	for _, key := range []string{"k", "k", "other"} {
		snap, err := c.FetchUsageSnapshot(context.Background(), key)
		if err != nil {
			t.Fatalf("FetchUsageSnapshot: %v", err)
		}
		if got := quotaByName(t, snap.Quotas, "weekly").Utilization; got != 12.8 {
			t.Fatalf("reused snapshot weekly = %v, want 12.8", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("status requests = %d, want 2 (reuse within %v, refetch on key change)", calls.Load(), openCodeUsageMinInterval)
	}
}

func TestFetchUsageSnapshot_FailuresAreNotReused(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			_, _ = w.Write([]byte(`{"access":{}}`))
		default:
			_, _ = w.Write([]byte(goStatusBody))
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

func TestNewOpenCodeClient_StatusGetsItsOwnTimeout(t *testing.T) {
	c := NewOpenCodeClient(nil)
	if c.httpClient.Timeout != openCodeScrapeTimeout || c.usageHTTPClient.Timeout != openCodeUsageTimeout {
		t.Fatalf("timeouts: scrape=%v status=%v", c.httpClient.Timeout, c.usageHTTPClient.Timeout)
	}
	if tr, ok := c.usageHTTPClient.Transport.(*http.Transport); !ok || tr.ResponseHeaderTimeout != openCodeUsageTimeout {
		t.Fatalf("status transport = %+v", c.usageHTTPClient.Transport)
	}
	if tr := c.httpClient.Transport.(*http.Transport); tr.ResponseHeaderTimeout != openCodeScrapeTimeout {
		t.Fatalf("scrape transport header timeout changed to %v", tr.ResponseHeaderTimeout)
	}
	if long := NewOpenCodeClient(nil, WithOpenCodeTimeout(45*time.Second)); long.usageHTTPClient.Timeout != 45*time.Second {
		t.Fatalf("a longer configured timeout was shortened to %v", long.usageHTTPClient.Timeout)
	}
}
