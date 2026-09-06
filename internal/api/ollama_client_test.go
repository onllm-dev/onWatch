package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const ollamaUsageFixture = `{"activity":{"cost":"1.25000","period":{"type":"last_4_weeks","starting_at":"2026-08-10T00:00:00Z","ending_at":"2026-09-06T22:11:30.695625284Z"},"models":[{"name":"gpt-oss:120b","cost":"1.25"}]},"limits":{"monthly":{"usage":7.5,"models":[{"name":"gpt-oss:120b","request_count":3},{"name":"gpt-oss:20b","request_count":1}]}}}`

const ollamaMeFixture = `{"ID":"c19e043f-0000-0000-0000-000000000000","CreatedAt":"2026-01-15T20:53:06.720639Z","Email":"user@example.com","Name":"user","Bio":"","AvatarURL":"","Links":[],"Plan":"pro"}`

func newOllamaTestServer(t *testing.T, usageStatus int, usageBody string, meStatus int, meBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.URL.Path == "/api/usage" && r.Method == http.MethodGet:
			w.WriteHeader(usageStatus)
			_, _ = w.Write([]byte(usageBody))
		case r.URL.Path == "/api/me" && r.Method == http.MethodPost:
			w.WriteHeader(meStatus)
			_, _ = w.Write([]byte(meBody))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func fixedOllamaNow() time.Time { return time.Date(2026, 9, 6, 22, 0, 0, 0, time.UTC) }

func TestOllamaClient_FetchSnapshot(t *testing.T) {
	srv := newOllamaTestServer(t, 200, ollamaUsageFixture, 200, ollamaMeFixture)
	defer srv.Close()

	c := NewOllamaClient("test-key", nil, WithOllamaBaseURL(srv.URL), withOllamaNow(fixedOllamaNow))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if snap.Plan != "pro" || snap.AccountName != "user" || snap.AccountEmail != "user@example.com" {
		t.Errorf("account = %+v", snap)
	}
	if snap.MonthlyUsedUSD != 7.5 || snap.MonthlyLimitUSD != 60 {
		t.Errorf("used/limit = %v/%v", snap.MonthlyUsedUSD, snap.MonthlyLimitUSD)
	}
	if snap.ExtraCostUSD != 1.25 {
		t.Errorf("extra cost = %v", snap.ExtraCostUSD)
	}
	if len(snap.Models) != 2 || snap.Models[0].Name != "gpt-oss:120b" || snap.Models[0].RequestCount != 3 || snap.Models[0].Cost != 1.25 {
		t.Errorf("models = %+v", snap.Models)
	}
	if len(snap.Quotas) != 1 {
		t.Fatalf("quotas = %d", len(snap.Quotas))
	}
	q := snap.Quotas[0]
	if q.Name != "monthly" || q.Format != OllamaQuotaFormatCurrency || q.Used != 7.5 || q.Limit != 60 || q.Utilization != 12.5 {
		t.Errorf("quota = %+v", q)
	}
	// Account created on the 15th at 20:53:06 -> next reset 2026-09-15T20:53:06Z.
	want := time.Date(2026, 9, 15, 20, 53, 6, 0, time.UTC)
	if q.ResetsAt == nil || !q.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v, want %v", q.ResetsAt, want)
	}
	if snap.RawJSON != ollamaUsageFixture {
		t.Errorf("raw json not preserved")
	}
}

func TestOllamaClient_Overrides(t *testing.T) {
	srv := newOllamaTestServer(t, 200, ollamaUsageFixture, 200, ollamaMeFixture)
	defer srv.Close()

	c := NewOllamaClient("test-key", nil, WithOllamaBaseURL(srv.URL), withOllamaNow(fixedOllamaNow), WithOllamaMonthlyLimit(30), WithOllamaResetDay(1))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	q := snap.Quotas[0]
	if q.Limit != 30 || q.Utilization != 25 {
		t.Errorf("override limit not applied: %+v", q)
	}
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if q.ResetsAt == nil || !q.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v, want %v", q.ResetsAt, want)
	}
}

func TestOllamaClient_MeFailureIsNonFatal(t *testing.T) {
	srv := newOllamaTestServer(t, 200, ollamaUsageFixture, 500, `boom`)
	defer srv.Close()

	c := NewOllamaClient("test-key", nil, WithOllamaBaseURL(srv.URL), withOllamaNow(fixedOllamaNow))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if snap.Plan != "" || snap.MonthlyLimitUSD != 0 || snap.Quotas[0].ResetsAt != nil {
		t.Errorf("expected unknown plan/limit/reset, got %+v", snap)
	}
	if snap.MonthlyUsedUSD != 7.5 || snap.Quotas[0].Utilization != 0 {
		t.Errorf("usage should still be recorded with zero utilization: %+v", snap.Quotas[0])
	}
}

func TestOllamaClient_Errors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", 401, `{"error":"unauthorized"}`, ErrOllamaUnauthorized},
		{"forbidden", 403, ``, ErrOllamaUnauthorized},
		{"rate limited", 429, ``, ErrOllamaRateLimited},
		{"server error", 503, ``, ErrOllamaServerError},
		{"bad json", 200, `not json`, ErrOllamaInvalidResponse},
		{"unexpected status", 418, `teapot`, ErrOllamaInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newOllamaTestServer(t, tc.status, tc.body, 200, ollamaMeFixture)
			defer srv.Close()
			c := NewOllamaClient("test-key", nil, WithOllamaBaseURL(srv.URL))
			_, err := c.FetchSnapshot(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestOllamaClient_MissingKey(t *testing.T) {
	c := NewOllamaClient("   ", nil)
	if _, err := c.FetchSnapshot(context.Background()); !errors.Is(err, ErrOllamaMissingAPIKey) {
		t.Fatalf("err = %v", err)
	}
	if !IsOllamaAuthError(ErrOllamaUnauthorized) || IsOllamaAuthError(ErrOllamaRateLimited) {
		t.Error("IsOllamaAuthError misclassified")
	}
}

func TestOllamaClient_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewOllamaClient("test-key", nil, WithOllamaBaseURL(srv.URL))
	if _, err := c.FetchSnapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
