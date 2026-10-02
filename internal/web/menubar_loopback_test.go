package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackRequestRejectsProxiedRequests(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		header string
		value  string
		want   bool
	}{
		{"direct ipv4", "127.0.0.1:5000", "", "", true},
		{"direct ipv6", "[::1]:5000", "", "", true},
		{"remote", "192.168.1.50:5000", "", "", false},
		{"x-forwarded-for", "127.0.0.1:5000", "X-Forwarded-For", "203.0.113.9", false},
		{"x-forwarded-for loopback", "127.0.0.1:5000", "X-Forwarded-For", "127.0.0.1", false},
		{"x-real-ip", "127.0.0.1:5000", "X-Real-IP", "203.0.113.9", false},
		{"forwarded", "127.0.0.1:5000", "Forwarded", "for=203.0.113.9", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
			r.RemoteAddr = tc.remote
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			if got := isLoopbackRequest(r); got != tc.want {
				t.Fatalf("isLoopbackRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// A reverse proxy on the same host connects from 127.0.0.1, so the public
// tray paths must still require auth when the request was forwarded. Base-path
// deployments sit behind a proxy by design and never expose them.
func TestMenubarPublicPathsRequireAuthBehindLocalProxy(t *testing.T) {
	sessions := NewSessionStore("admin", "unused", nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, basePath := range []string{"", "/onwatch"} {
		wantDirect := http.StatusOK
		if basePath != "" {
			wantDirect = http.StatusUnauthorized
		}
		handler := sessionAuthMiddlewareWithBasePath(sessions, basePath)(next)
		for _, path := range []string{"/api/menubar/summary", "/api/menubar/preferences", "/api/menubar/refresh", "/api/menubar/mistral/retry", "/api/menubar/tray-title"} {
			proxied := httptest.NewRequest(http.MethodGet, basePath+path, nil)
			proxied.RemoteAddr = "127.0.0.1:5000"
			proxied.Header.Set("X-Forwarded-For", "203.0.113.9")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, proxied)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("%s%s via local proxy: expected 401, got %d", basePath, path, rr.Code)
			}

			direct := httptest.NewRequest(http.MethodGet, basePath+path, nil)
			direct.RemoteAddr = "127.0.0.1:5000"
			rr = httptest.NewRecorder()
			handler.ServeHTTP(rr, direct)
			if rr.Code != wantDirect {
				t.Fatalf("%s%s direct loopback: expected %d, got %d", basePath, path, wantDirect, rr.Code)
			}
		}
	}
}
