package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZenmuxUsageContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/management/usage" || r.Header.Get("Authorization") != "Bearer synthetic-management-key" {
			t.Errorf("wrong request")
		}
		q := r.URL.Query()
		if q.Get("type") != "usage" || q.Get("query_dimension") != "BIZ_DT" || q.Get("query_time") != "20260903" || q.Get("bill_types") != "subscription" {
			t.Errorf("wrong query: %v", q)
		}
		fmt.Fprint(w, `{"success":true,"data":{"tokensTotal":"0","tokensPrompt":null,"tokensCompletion":"12","requestCounts":"2","tokensByModel":[{"bizTime":"2026090312","modelSlug":"sample/model","tokens":"0","requestCounts":"2"}],"ignored":"synthetic-management-key"}}`)
	}))
	defer server.Close()
	result, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_DT", Time: "20260903", BillType: "subscription"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.TokensTotal == nil || *result.Data.TokensTotal != "0" || result.Data.TokensPrompt != nil || len(result.Data.TokensByModel) != 1 || *result.Data.TokensByModel[0].RequestCounts != "2" {
		t.Fatalf("unknown or zero lost: %+v", result)
	}
	if result.FetchedAt.IsZero() {
		t.Fatal("missing fetch timestamp")
	}
}

func TestZenmuxUsageProviderContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "provider" || r.URL.Query().Get("model_slugs") != "sample/model" || r.URL.Query().Get("bill_types") != "fallbackMetered" {
			t.Errorf("incorrect provider query: %v", r.URL.Query())
		}
		fmt.Fprint(w, `{"success":true,"data":{"uptime":"0.75","endpointUsages":[{"endpointSlug":"provider-a","requestCounts":"0","tokens":null,"medianLatency":"320"}]}}`)
	}))
	defer server.Close()
	result, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609", BillType: "fallbackMetered", Model: "sample/model"})
	if err != nil || len(result.Data.EndpointUsages) != 1 || result.Data.EndpointUsages[0].Tokens != nil || *result.Data.EndpointUsages[0].RequestCounts != "0" {
		t.Fatalf("provider fields lost: %+v %v", result, err)
	}
}

func TestZenmuxUsageProviderMetricsDecoded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "provider" || r.URL.Query().Get("model_slugs") != "sample/model" {
			t.Errorf("wrong provider query: %v", r.URL.Query())
		}
		fmt.Fprint(w, `{"success":true,"data":{"uptime":"0.875","mainEndpointSlug":"provider/main","fastestEndpointSlug":"provider/fast","endpointUsages":[{"endpointSlug":"provider/main","requestRatio":"0.4375","tokens":"0"},{"endpointSlug":"provider/fast","requestRatio":"0","tokens":null},{"endpointSlug":"provider/unknown","requestRatio":null,"tokens":"0"}]}}`)
	}))
	defer server.Close()
	result, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609", Model: "sample/model"})
	if err != nil {
		t.Fatal(err)
	}
	wantString := func(name string, value *string, want string) {
		t.Helper()
		if value == nil || *value != want {
			t.Errorf("%s = %v, want %q", name, value, want)
		}
	}
	wantString("uptime", result.Data.Uptime, "0.875")
	wantString("main endpoint", result.Data.MainEndpointSlug, "provider/main")
	wantString("fastest endpoint", result.Data.FastestEndpointSlug, "provider/fast")
	if len(result.Data.EndpointUsages) != 3 {
		t.Fatalf("got %d endpoints, want 3", len(result.Data.EndpointUsages))
	}
	main, fast, unknown := result.Data.EndpointUsages[0], result.Data.EndpointUsages[1], result.Data.EndpointUsages[2]
	if main.EndpointSlug != "provider/main" || fast.EndpointSlug != "provider/fast" || unknown.EndpointSlug != "provider/unknown" {
		t.Fatalf("endpoint slugs: %+v", result.Data.EndpointUsages)
	}
	wantString("main request ratio", main.RequestRatio, "0.4375")
	wantString("fast request ratio", fast.RequestRatio, "0")
	wantString("main tokens", main.Tokens, "0")
	wantString("unknown endpoint tokens", unknown.Tokens, "0")
	if unknown.RequestRatio != nil || fast.Tokens != nil {
		t.Fatalf("null converted to a value: fast=%+v unknown=%+v", fast, unknown)
	}
}

func TestZenmuxUsageErrorsAndSanitization(t *testing.T) {
	cases := []struct {
		status int
		body   string
		target error
	}{
		{401, `{"error":"synthetic-management-key"}`, ErrZenmuxUnauthorized},
		{403, `{"error":"synthetic-management-key"}`, ErrZenmuxUnauthorized},
		{429, `{"error":"synthetic-management-key"}`, ErrZenmuxRateLimited},
		{502, `{"error":"synthetic-management-key"}`, nil},
		{200, `{"success":false,"data":{}}`, ErrZenmuxInvalidResponse},
		{200, `{"success":true,"data":null}`, ErrZenmuxInvalidResponse},
		{200, `{"success":true,"data":{"tokensByModel":[{"bizTime":"20260903","modelSlug":"synthetic-management-key"}]}}`, ErrZenmuxInvalidResponse},
		{200, `{"success":true,"data":{"tokensByModel":[{"bizTime":"20260903","modelSlug":"example","tokens":"synthetic-management-key"}]}}`, ErrZenmuxInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status, len(tc.body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			_, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
			if err == nil || (tc.target != nil && !errors.Is(err, tc.target)) || strings.Contains(err.Error(), "synthetic-management-key") {
				t.Fatalf("wrong or unsafe error: %v", err)
			}
		})
	}
}

func TestZenmuxUsageRejectsEscapedKeyInEndpointSlug(t *testing.T) {
	const key = "synthetic-management-key"
	const encodedKey = `synthetic\u002dmanagement\u002dkey`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"endpointUsages":[{"endpointSlug":"edge/`+encodedKey+`"}]}}`)
	}))
	defer server.Close()
	client := NewZenmuxClient(key, withZenmuxBaseURL(server.URL))
	defer client.CloseIdleConnections()

	result, err := client.FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609", Model: "sample/model"})
	if err == nil || !errors.Is(err, ErrZenmuxInvalidResponse) || result != nil {
		t.Fatal("escaped key in endpoint was not rejected safely")
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), encodedKey) {
		t.Fatal("response error exposed raw or encoded management key")
	}
}

func TestZenmuxUsageRejectsInvalidQueryWithoutHTTP(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL))
	for _, q := range []ZenmuxUsageQuery{{Dimension: "BIZ_DT", Time: "20260230"}, {Dimension: "BIZ_MTH", Time: "20260901"}, {Dimension: "BIZ_HOUR", Time: "2026090324"}, {Dimension: "BAD", Time: "202609"}, {Dimension: "BIZ_MTH", Time: "202609", BillType: "bad"}, {Dimension: "BIZ_MTH", Time: "202609", Model: "model,another"}, {Dimension: "BIZ_MTH", Time: "000001"}, {Dimension: "BIZ_MTH", Time: "202609", Model: "sk-mg-v1-secret"}} {
		if _, err := client.FetchUsage(context.Background(), q); err == nil {
			t.Errorf("accepted invalid query %+v", q)
		}
	}
	if calls != 0 {
		t.Fatal("invalid query reached HTTP")
	}
}

func TestZenmuxUsageEmptyAndBounds(t *testing.T) {
	cases := []struct {
		name, body string
		wantError  bool
	}{
		{"empty", `{"success":true,"data":{"tokensTotal":null,"requestCounts":"0","tokensByModel":[]}}`, false},
		{"missing array", `{"success":true,"data":{"requestCounts":"0"}}`, true},
		{"outside period", `{"success":true,"data":{"tokensByModel":[{"bizTime":"20260831","modelSlug":"sample/model"}]}}`, true},
		{"oversized", `{"success":true,"padding":"` + strings.Repeat("x", 1<<20) + `","data":{"tokensByModel":[]}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			result, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(server.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
			if (err != nil) != tc.wantError {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if !tc.wantError && (result.Data.TokensTotal != nil || result.Data.RequestCounts == nil || *result.Data.RequestCounts != "0" || len(result.Data.TokensByModel) != 0) {
				t.Fatal("empty/null/zero not preserved")
			}
		})
	}
}

func TestZenmuxUsageDoesNotFollowRedirect(t *testing.T) {
	got := false
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = true }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer source.Close()
	_, err := NewZenmuxClient("synthetic-management-key", withZenmuxBaseURL(source.URL)).FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
	if err == nil || got {
		t.Fatal("redirect followed or treated as success")
	}
}
