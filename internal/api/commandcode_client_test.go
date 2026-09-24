package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixedCommandCodeNow() time.Time {
	return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
}

// newCommandCodeTestServer serves the four alpha endpoints. Each handler gets
// the request so tests can assert on the Authorization header and query params.
func newCommandCodeTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want the bearer key", got)
		}
		handler, ok := handlers[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
}

func commandCodeHappyHandlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(commandCodeWhoamiFixture))
		},
		"/alpha/billing/credits": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(commandCodeCreditsFixture))
		},
		"/alpha/billing/subscriptions": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(commandCodeSubscriptionFixture))
		},
		"/alpha/usage/summary": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(commandCodeSummaryFixture))
		},
	}
}

func TestCommandCodeClientFetchSnapshot(t *testing.T) {
	srv := newCommandCodeTestServer(t, commandCodeHappyHandlers())
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil,
		WithCommandCodeBaseURL(srv.URL), withCommandCodeNow(fixedCommandCodeNow))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	if snap.AccountName != "prakersh" || snap.Plan != "individual-goat" {
		t.Errorf("account/plan = %q/%q", snap.AccountName, snap.Plan)
	}
	if len(snap.Quotas) != 3 {
		t.Fatalf("quotas = %d", len(snap.Quotas))
	}
	if !snap.CapturedAt.Equal(fixedCommandCodeNow()) {
		t.Errorf("capturedAt = %v, want the injected clock", snap.CapturedAt)
	}
	if snap.RawJSON == "" || !strings.Contains(snap.RawJSON, "whoami") {
		t.Errorf("raw json not captured: %q", snap.RawJSON)
	}
}

func TestCommandCodeClientRequiresAPIKey(t *testing.T) {
	c := NewCommandCodeClient("", nil)
	if _, err := c.FetchSnapshot(context.Background()); !errors.Is(err, ErrCommandCodeMissingAPIKey) {
		t.Fatalf("err = %v, want ErrCommandCodeMissingAPIKey", err)
	}
}

func TestCommandCodeClientSendsUserAgent(t *testing.T) {
	// The API sits behind Cloudflare, which answers a client signature it
	// dislikes with HTTP 403 error 1010. Go's default UA was observed to be
	// rejected, so the product token must be sent on every request.
	var seen string
	handlers := commandCodeHappyHandlers()
	handlers["/alpha/whoami"] = func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(commandCodeWhoamiFixture))
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if seen != commandCodeUserAgent {
		t.Fatalf("User-Agent = %q, want %q", seen, commandCodeUserAgent)
	}
}

func TestCommandCodeClientUsesOrgIDAndPeriodSince(t *testing.T) {
	var creditsQuery, subscriptionsQuery, summaryQuery string
	handlers := commandCodeHappyHandlers()
	handlers["/alpha/whoami"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"user":{"userName":"pk"},"org":{"id":"org_42","login":"acme"}}`))
	}
	handlers["/alpha/billing/credits"] = func(w http.ResponseWriter, r *http.Request) {
		creditsQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(commandCodeCreditsFixture))
	}
	handlers["/alpha/billing/subscriptions"] = func(w http.ResponseWriter, r *http.Request) {
		subscriptionsQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(commandCodeSubscriptionFixture))
	}
	handlers["/alpha/usage/summary"] = func(w http.ResponseWriter, r *http.Request) {
		summaryQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(commandCodeSummaryFixture))
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if snap.OrgID != "org_42" {
		t.Fatalf("orgID = %q", snap.OrgID)
	}
	for name, got := range map[string]string{
		"credits":       creditsQuery,
		"subscriptions": subscriptionsQuery,
		"summary":       summaryQuery,
	} {
		if !strings.Contains(got, "orgId=org_42") {
			t.Errorf("%s query = %q, want orgId", name, got)
		}
	}
	// The summary must be scoped to the billing period so the totals are the
	// current period's, not the account's lifetime.
	if !strings.Contains(summaryQuery, "since=") {
		t.Errorf("summary query = %q, want a since parameter", summaryQuery)
	}
	if !strings.Contains(summaryQuery, "2026-09-19") {
		t.Errorf("summary query = %q, want the period start", summaryQuery)
	}
	// Only the summary takes `since`; the billing lookups take only orgId.
	if strings.Contains(creditsQuery, "since=") {
		t.Errorf("credits query = %q, must not carry since", creditsQuery)
	}
}

func TestCommandCodeClientOmitsOrgIDWhenAbsent(t *testing.T) {
	var creditsQuery string
	handlers := commandCodeHappyHandlers()
	handlers["/alpha/billing/credits"] = func(w http.ResponseWriter, r *http.Request) {
		creditsQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(commandCodeCreditsFixture))
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if creditsQuery != "" {
		t.Fatalf("credits query = %q, want empty for a personal account", creditsQuery)
	}
}

func TestCommandCodeClientAuthFailureIsBlocking(t *testing.T) {
	// whoami failing is fatal: without an account there is nothing to record.
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid 'Authorization' header"}}`))
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeUnauthorized) {
		t.Fatalf("err = %v, want ErrCommandCodeUnauthorized", err)
	}
	if !IsCommandCodeAuthError(err) {
		t.Fatal("IsCommandCodeAuthError must report true")
	}
}

func TestCommandCodeClientBillingAuthFailureIsBlocking(t *testing.T) {
	handlers := commandCodeHappyHandlers()
	for _, path := range []string{"/alpha/billing/credits", "/alpha/billing/subscriptions", "/alpha/usage/summary"} {
		handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeUnauthorized) {
		t.Fatalf("err = %v, want ErrCommandCodeUnauthorized", err)
	}
}

func TestCommandCodeClientAuthErrorBodyIsNotEchoed(t *testing.T) {
	// Vendors commonly echo the rejected credential back in an auth failure
	// body and the agent logs the error verbatim, so the body must be dropped.
	const secret = "user_SUPERSECRETKEYMATERIAL"
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid token ` + secret + `"}}`))
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("auth error leaked the response body: %v", err)
	}
}

func TestCommandCodeClientRateLimited(t *testing.T) {
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeRateLimited) {
		t.Fatalf("err = %v, want ErrCommandCodeRateLimited", err)
	}
	if !IsCommandCodeRateLimited(err) {
		t.Fatal("IsCommandCodeRateLimited must report true")
	}
}

func TestCommandCodeClientServerError(t *testing.T) {
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeServerError) {
		t.Fatalf("err = %v, want ErrCommandCodeServerError", err)
	}
}

func TestCommandCodeClientPartialBillingFailureKeepsTheRest(t *testing.T) {
	// A failing credits lookup costs only the credit windows; usage and plan
	// must still be recorded rather than the whole poll being thrown away.
	handlers := commandCodeHappyHandlers()
	handlers["/alpha/billing/credits"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) != 0 {
		t.Errorf("expected no quotas without credits, got %+v", snap.Quotas)
	}
	if snap.Plan != "individual-goat" {
		t.Errorf("plan = %q, want the subscription to survive", snap.Plan)
	}
	if snap.PeriodReqs != 6197 {
		t.Errorf("requests = %d, want the summary to survive", snap.PeriodReqs)
	}
}

func TestCommandCodeClientAllSectionsFailed(t *testing.T) {
	handlers := commandCodeHappyHandlers()
	for path := range handlers {
		if path == "/alpha/whoami" {
			continue
		}
		handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeInvalidResponse) {
		t.Fatalf("err = %v, want ErrCommandCodeInvalidResponse", err)
	}
}

func TestCommandCodeClientUnrecognizedWhoami(t *testing.T) {
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`"just a string"`))
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrCommandCodeInvalidResponse) {
		t.Fatalf("err = %v, want ErrCommandCodeInvalidResponse", err)
	}
}

func TestCommandCodeClientContextCancellation(t *testing.T) {
	srv := newCommandCodeTestServer(t, map[string]http.HandlerFunc{
		"/alpha/whoami": func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte(commandCodeWhoamiFixture))
		},
	})
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchSnapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCommandCodeErrorDetailTruncatesRunewise(t *testing.T) {
	// A byte slice would split a multi-byte rune and leave an invalid UTF-8
	// fragment in the log line.
	long := strings.Repeat("é", commandCodeDetailMaxRunes+50)
	got := truncateCommandCodeDetail(long)
	if len([]rune(got)) != commandCodeDetailMaxRunes {
		t.Fatalf("rune length = %d, want %d", len([]rune(got)), commandCodeDetailMaxRunes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncation produced invalid UTF-8")
	}
}

func TestCommandCodeErrorDetailFromEnvelope(t *testing.T) {
	got := commandCodeErrorDetail([]byte(`{"error":{"message":"  too   many requests  "}}`))
	if got != "too many requests" {
		t.Fatalf("detail = %q", got)
	}
	got = commandCodeErrorDetail([]byte(`{"detail":"plain detail"}`))
	if got != "plain detail" {
		t.Fatalf("detail = %q", got)
	}
	if got := commandCodeErrorDetail(nil); got != "unknown" {
		t.Fatalf("empty body = %q, want unknown", got)
	}
}

func TestCommandCodeOrgAndSummaryPathHelpers(t *testing.T) {
	if got := commandCodeOrgPath("/alpha/billing/credits", ""); got != "/alpha/billing/credits" {
		t.Errorf("no org = %q", got)
	}
	if got := commandCodeOrgPath("/alpha/billing/credits", "org_1"); got != "/alpha/billing/credits?orgId=org_1" {
		t.Errorf("org = %q", got)
	}
	if got := commandCodeSummaryPath("", ""); got != "/alpha/usage/summary" {
		t.Errorf("bare summary = %q", got)
	}
	if got := commandCodeSummaryPath("org_1", ""); got != "/alpha/usage/summary?orgId=org_1" {
		t.Errorf("org summary = %q", got)
	}
}

func TestCommandCodeRawJSONSkipsMissingBodies(t *testing.T) {
	got := commandCodeRawJSON([]byte(`{"a":1}`), nil, []byte(`{"b":2}`), nil)
	if !strings.Contains(got, `"whoami":{"a":1}`) {
		t.Errorf("raw = %q", got)
	}
	if !strings.Contains(got, `"subscriptions":{"b":2}`) {
		t.Errorf("raw = %q", got)
	}
	if strings.Contains(got, `,,`) || strings.Contains(got, `,}`) {
		t.Errorf("raw JSON has a dangling comma: %q", got)
	}
	if got := commandCodeRawJSON(nil, nil, nil, nil); got != "{}" {
		t.Errorf("all-empty raw = %q, want {}", got)
	}
}

func TestCommandCodeClientQueryEscape(t *testing.T) {
	// An org id with a character that needs escaping must not break the URL.
	var query string
	handlers := commandCodeHappyHandlers()
	handlers["/alpha/whoami"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"user":{"userName":"pk"},"org":{"id":"org/42 +x"}}`))
	}
	handlers["/alpha/billing/credits"] = func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("orgId")
		_, _ = w.Write([]byte(commandCodeCreditsFixture))
	}
	srv := newCommandCodeTestServer(t, handlers)
	defer srv.Close()

	c := NewCommandCodeClient("test-key", nil, WithCommandCodeBaseURL(srv.URL))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if query != "org/42 +x" {
		t.Fatalf("orgId = %q, want the decoded original", query)
	}
}
