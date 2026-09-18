package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestMetricsEndpoint_NotServedWithoutTokenOrOptIn asserts /metrics is closed
// by default.
//
// It reports per-account quota usage. It used to be served unauthenticated
// whenever ONWATCH_METRICS_TOKEN was unset, which combined with the old
// 0.0.0.0 bind default meant any machine on the network could read it.
func TestMetricsEndpoint_NotServedWithoutTokenOrOptIn(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	srv := NewServer(0, h, slog.Default(), "", "", "127.0.0.1", "", "", nil)

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code == http.StatusOK {
		t.Errorf("/metrics answered %d with no token and no opt-in; it must not be registered", rec.Code)
	}
}

// TestMetricsEndpoint_ServedWithToken asserts the documented setup still works.
func TestMetricsEndpoint_ServedWithToken(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, nil, cfg)
	srv := NewServer(0, h, slog.Default(), "", "", "127.0.0.1", "", "sekrit", nil)

	// Without the token.
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /metrics = %d, want 401", rec.Code)
	}

	// With it.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated /metrics = %d, want 200", rec.Code)
	}
}

// TestMetricsEndpoint_ServedWhenExplicitlyPublic asserts the opt-out exists for
// an operator who genuinely wants an open endpoint on a trusted network.
func TestMetricsEndpoint_ServedWhenExplicitlyPublic(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	cfg := createTestConfigWithSynthetic()
	cfg.MetricsPublic = true
	h := NewHandler(s, nil, nil, nil, cfg)
	srv := NewServer(0, h, slog.Default(), "", "", "127.0.0.1", "", "", nil)

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics = %d with ONWATCH_METRICS_PUBLIC set, want 200", rec.Code)
	}
}
