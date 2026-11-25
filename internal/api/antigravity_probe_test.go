package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	unleashRPC    = "/exa.language_server_pb.LanguageServerService/GetUnleashData"
	userStatusRPC = "/exa.language_server_pb.LanguageServerService/GetUserStatus"
	quotaRPC      = "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary"
)

type probeRecorder struct {
	mu    sync.Mutex
	paths []string
	csrf  []string
	body  []string
}

func (p *probeRecorder) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, r.URL.Path)
	p.csrf = append(p.csrf, r.Header.Get("X-Codeium-Csrf-Token"))
	p.body = append(p.body, string(b))
}

func (p *probeRecorder) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

func testServerPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("port of %q: %v", rawURL, err)
	}
	return port
}

func quietAntigravityClient() *AntigravityClient {
	return NewAntigravityClient(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

// newAgy15Server mimics the agy 1.5.x plain-HTTP listener from issue #168:
// GetUnleashData is not routed (a plain mux "404 page not found"), while the
// quota RPCs answer 200.
func newAgy15Server(t *testing.T, rec *probeRecorder, served ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for _, path := range served {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbePort_FallsBackWhenGetUnleashDataIs404(t *testing.T) {
	rec := &probeRecorder{}
	srv := newAgy15Server(t, rec, userStatusRPC, quotaRPC)

	conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", "csrf-abc")
	if conn == nil {
		t.Fatal("probePort returned nil for an agy 1.5.x server that serves GetUserStatus")
	}
	if conn.Protocol != "http" || conn.CSRFToken != "csrf-abc" {
		t.Fatalf("conn = %+v, want http with the CSRF token", conn)
	}
	got := rec.snapshot()
	want := []string{unleashRPC, userStatusRPC}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("probed paths = %v, want %v (stop at the first route that answers)", got, want)
	}
	for i, c := range rec.csrf {
		if c != "csrf-abc" {
			t.Fatalf("probe %d sent CSRF %q, want csrf-abc", i, c)
		}
	}
	if !strings.Contains(rec.body[1], `"ideName":"antigravity"`) {
		t.Fatalf("GetUserStatus probe body = %q, want the metadata body", rec.body[1])
	}
}

func TestProbePort_FallsBackToQuotaSummary(t *testing.T) {
	rec := &probeRecorder{}
	srv := newAgy15Server(t, rec, quotaRPC)

	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
		t.Fatal("probePort returned nil for a server that serves RetrieveUserQuotaSummary")
	}
	got := rec.snapshot()
	if len(got) != 3 || got[2] != quotaRPC {
		t.Fatalf("probed paths = %v, want all three routes ending with the quota summary", got)
	}
}

func TestProbePort_GetUnleashDataStillWorksWithOneRequest(t *testing.T) {
	rec := &probeRecorder{}
	srv := newAgy15Server(t, rec, unleashRPC, userStatusRPC, quotaRPC)

	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
		t.Fatal("probePort returned nil for a server that serves GetUnleashData")
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != unleashRPC {
		t.Fatalf("probed paths = %v, want only GetUnleashData", got)
	}
}

// A non-404 failure (such as 400 from a plain HTTP request to a TLS port) is
// not a missing route: the port is rejected without trying more routes.
func TestProbePort_NonNotFoundErrorStopsProbing(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn != nil {
		t.Fatal("probePort accepted a port that answers 400")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestProbePort_AllRoutesMissing(t *testing.T) {
	rec := &probeRecorder{}
	srv := newAgy15Server(t, rec)

	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn != nil {
		t.Fatal("probePort accepted a port where every route is 404")
	}
	if got := rec.snapshot(); len(got) != len(antigravityProbeRoutes) {
		t.Fatalf("probed paths = %v, want every route tried once", got)
	}
}

// The issue #168 layout: agy listens on a TLS gRPC port that does not serve
// the Connect routes, and on a plain-HTTP Connect port without GetUnleashData.
// probeForConnectAPI must skip the first and settle on the second.
func TestProbeForConnectAPI_Agy15TwoListeners(t *testing.T) {
	grpc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer grpc.Close()
	rec := &probeRecorder{}
	plain := newAgy15Server(t, rec, userStatusRPC, quotaRPC)

	client := quietAntigravityClient()
	conn, err := client.probeForConnectAPI(context.Background(),
		[]int{testServerPort(t, grpc.URL), testServerPort(t, plain.URL)}, "csrf")
	if err != nil {
		t.Fatalf("probeForConnectAPI: %v", err)
	}
	if conn.Port != testServerPort(t, plain.URL) || conn.Protocol != "http" {
		t.Fatalf("conn = %+v, want the plain-HTTP listener", conn)
	}
}

// withProbeTimeouts shortens the probe timeouts so the timeout cases run fast.
func withProbeTimeouts(t *testing.T, first, fallback time.Duration) {
	t.Helper()
	orig := antigravityProbeRoutes
	routes := make([]antigravityProbeRoute, len(orig))
	copy(routes, orig)
	for i := range routes {
		if i == 0 {
			routes[i].timeout = first
		} else {
			routes[i].timeout = fallback
		}
	}
	antigravityProbeRoutes = routes
	t.Cleanup(func() { antigravityProbeRoutes = orig })
}

// probeServer answers each RPC path with the handler given for it; any other
// path gets a 404 like agy 1.5.x does for GetUnleashData.
func probeServer(t *testing.T, rec *probeRecorder, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if h, ok := handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProbePort_FallbackStatusHandling(t *testing.T) {
	const connectJSON = "application/json"
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantConn    bool
		wantProbes  int
	}{
		{"401 without a valid CSRF token still proves the API", http.StatusUnauthorized, "", "", true, 2},
		{"500 page rejects the port", http.StatusInternalServerError, "", "", false, 2},
		{"403 page rejects the port", http.StatusForbidden, "text/html", "<html>forbidden</html>", false, 2},
		{"405 rejects the port", http.StatusMethodNotAllowed, "", "", false, 2},
		{"404 moves on to the quota summary", http.StatusNotFound, "", "", false, 3},
		{"Connect permission_denied proves the API", http.StatusForbidden, connectJSON, `{"code":"permission_denied","message":"signed out"}`, true, 2},
		{"Connect failed_precondition proves the API", http.StatusBadRequest, connectJSON, `{"code":"failed_precondition"}`, true, 2},
		{"Connect unavailable proves the API", http.StatusServiceUnavailable, connectJSON + "; charset=utf-8", `{"code":"unavailable"}`, true, 2},
		{"Connect unauthenticated proves the API", http.StatusUnauthorized, connectJSON, `{"code":"unauthenticated"}`, true, 2},
		{"Connect resource_exhausted proves the API", http.StatusTooManyRequests, connectJSON, `{"code":"resource_exhausted"}`, true, 2},
		{"Connect internal moves on to the quota summary", http.StatusInternalServerError, connectJSON, `{"code":"internal","message":"boom"}`, false, 3},
		{"Connect invalid_argument moves on to the quota summary", http.StatusBadRequest, connectJSON, `{"code":"invalid_argument"}`, false, 3},
		{"JSON without a Connect code rejects the port", http.StatusForbidden, connectJSON, `{"error":"forbidden"}`, false, 2},
		{"unknown code word rejects the port", http.StatusBadRequest, connectJSON, `{"code":"nope"}`, false, 2},
		{"Connect code as plain text rejects the port", http.StatusForbidden, "text/plain", `{"code":"permission_denied"}`, false, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &probeRecorder{}
			srv := probeServer(t, rec, map[string]http.HandlerFunc{
				userStatusRPC: func(w http.ResponseWriter, r *http.Request) {
					if tt.contentType != "" {
						w.Header().Set("Content-Type", tt.contentType)
					}
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				},
			})
			conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", "")
			if (conn != nil) != tt.wantConn {
				t.Fatalf("conn = %+v, want found=%v", conn, tt.wantConn)
			}
			if got := rec.snapshot(); len(got) != tt.wantProbes {
				t.Fatalf("probed paths = %v, want %d probes", got, tt.wantProbes)
			}
		})
	}
}

// A real RPC can be slower than the liveness probe's 500ms. Once a port has
// answered 404, a slow fallback answer must still be accepted.
func TestProbePort_SlowFallbackAnswerIsAccepted(t *testing.T) {
	withProbeTimeouts(t, 100*time.Millisecond, 2*time.Second)
	rec := &probeRecorder{}
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		userStatusRPC: func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond) // slower than the first probe's timeout
			_, _ = w.Write([]byte(`{}`))
		},
	})
	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
		t.Fatal("probePort rejected a port whose GetUserStatus answered slowly but successfully")
	}
}

// A fallback RPC that times out is inconclusive: the next route is tried.
func TestProbePort_FallbackTimeoutTriesNextRoute(t *testing.T) {
	withProbeTimeouts(t, 200*time.Millisecond, 100*time.Millisecond)
	rec := &probeRecorder{}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		userStatusRPC: func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		},
		quotaRPC: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) },
	})
	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
		t.Fatal("probePort gave up after a GetUserStatus timeout instead of trying the quota summary")
	}
	if got := rec.snapshot(); len(got) != 3 || got[2] != quotaRPC {
		t.Fatalf("probed paths = %v, want all three routes", got)
	}
}

// A port that never answers the first probe is ruled out after one short
// timeout, without trying the slower fallback routes.
func TestProbePort_HangingPortIsRuledOutQuickly(t *testing.T) {
	withProbeTimeouts(t, 100*time.Millisecond, 5*time.Second)
	rec := &probeRecorder{}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", "")
	if conn != nil {
		t.Fatal("probePort accepted a port that never answered")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe took %v, want about one first-probe timeout", elapsed)
	}
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("probed paths = %v, want only the first route", got)
	}
}

// A closed port fails fast with one attempt.
func TestProbePort_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := testServerPort(t, srv.URL)
	srv.Close()

	if conn := quietAntigravityClient().probePort(context.Background(), port, "http", ""); conn != nil {
		t.Fatal("probePort accepted a closed port")
	}
}

// When the caller cancels, probing stops instead of treating the cancel as a
// fallback timeout and trying more routes.
func TestProbePort_CancelledContextStops(t *testing.T) {
	rec := &probeRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		userStatusRPC: func(w http.ResponseWriter, r *http.Request) {
			cancel()
			<-r.Context().Done()
		},
	})
	if conn := quietAntigravityClient().probePort(ctx, testServerPort(t, srv.URL), "http", ""); conn != nil {
		t.Fatal("probePort returned a connection after the context was cancelled")
	}
	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("probed paths = %v, want to stop after the cancelled GetUserStatus", got)
	}
}

// Several listeners: a port that is some other HTTP server (404 everywhere)
// is skipped, and an older language server on a later port is still found.
func TestProbeForConnectAPI_SkipsUnrelatedServer(t *testing.T) {
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	rec := &probeRecorder{}
	old := newAgy15Server(t, rec, unleashRPC)

	conn, err := quietAntigravityClient().probeForConnectAPI(context.Background(),
		[]int{testServerPort(t, other.URL), testServerPort(t, old.URL)}, "")
	if err != nil {
		t.Fatalf("probeForConnectAPI: %v", err)
	}
	if conn.Port != testServerPort(t, old.URL) {
		t.Fatalf("conn = %+v, want the older language server's port", conn)
	}
}

func TestProbeForConnectAPI_NothingFound(t *testing.T) {
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	_, err := quietAntigravityClient().probeForConnectAPI(context.Background(), []int{testServerPort(t, other.URL)}, "")
	if !errors.Is(err, ErrAntigravityConnectionFailed) {
		t.Fatalf("err = %v, want ErrAntigravityConnectionFailed", err)
	}
	if _, err := quietAntigravityClient().probeForConnectAPI(context.Background(), nil, ""); !errors.Is(err, ErrAntigravityConnectionFailed) {
		t.Fatalf("no ports: err = %v, want ErrAntigravityConnectionFailed", err)
	}
}

func TestIsTimeoutError(t *testing.T) {
	if !isTimeoutError(context.DeadlineExceeded) {
		t.Fatal("DeadlineExceeded should count as a timeout")
	}
	if !isTimeoutError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)) {
		t.Fatal("wrapped DeadlineExceeded should count as a timeout")
	}
	if isTimeoutError(context.Canceled) {
		t.Fatal("Canceled is not a timeout")
	}
	if isTimeoutError(errors.New("connection refused")) {
		t.Fatal("a plain error is not a timeout")
	}
}

// A Connect "unimplemented" answer is the route missing, not the API proven:
// the next route is tried.
func TestProbePort_ConnectUnimplementedMovesOn(t *testing.T) {
	unimplemented := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"code":"unimplemented"}`))
	}
	t.Run("last route answers", func(t *testing.T) {
		rec := &probeRecorder{}
		srv := probeServer(t, rec, map[string]http.HandlerFunc{
			userStatusRPC: unimplemented,
			"/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary": func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{}`))
			},
		})
		if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
			t.Fatal("probePort stopped at an unimplemented fallback instead of trying the last route")
		}
		if got := rec.snapshot(); len(got) != 3 {
			t.Fatalf("probed paths = %v, want 3", got)
		}
	})
	t.Run("every route unimplemented", func(t *testing.T) {
		rec := &probeRecorder{}
		srv := probeServer(t, rec, map[string]http.HandlerFunc{
			userStatusRPC: unimplemented,
			"/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary": unimplemented,
		})
		if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn != nil {
			t.Fatalf("conn = %+v, want none", conn)
		}
		if got := rec.snapshot(); len(got) != 3 {
			t.Fatalf("probed paths = %v, want 3", got)
		}
	})
}

// The first probe keeps its rule: only 200 or 401 accept, so another server
// that happens to speak Connect on the liveness path is not taken.
func TestProbePort_FirstProbeIgnoresConnectErrors(t *testing.T) {
	rec := &probeRecorder{}
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		"/exa.language_server_pb.LanguageServerService/GetUnleashData": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"permission_denied"}`))
		},
	})
	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn != nil {
		t.Fatalf("conn = %+v, want none", conn)
	}
}

// A liveness route answered with Connect "unimplemented" is missing, like a
// 404, so the fallback routes are still tried.
func TestProbePort_FirstProbeUnimplementedMovesOn(t *testing.T) {
	rec := &probeRecorder{}
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		"/exa.language_server_pb.LanguageServerService/GetUnleashData": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(`{"code":"unimplemented"}`))
		},
		userStatusRPC: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		},
	})
	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn == nil {
		t.Fatal("probePort stopped at an unimplemented liveness route")
	}
	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("probed paths = %v, want 2", got)
	}
}

// A Connect server that is not the language server answers a fallback with
// invalid_argument; the port is only taken if a later route proves it.
func TestProbePort_OtherConnectErrorTriesTheLastRoute(t *testing.T) {
	invalid := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_argument"}`))
	}
	rec := &probeRecorder{}
	srv := probeServer(t, rec, map[string]http.HandlerFunc{
		userStatusRPC: invalid,
		"/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary": invalid,
	})
	if conn := quietAntigravityClient().probePort(context.Background(), testServerPort(t, srv.URL), "http", ""); conn != nil {
		t.Fatalf("conn = %+v, want none for a server that only answers invalid_argument", conn)
	}
	if got := rec.snapshot(); len(got) != 3 {
		t.Fatalf("probed paths = %v, want 3", got)
	}
}

// An agy 1.5.x server whose quota RPCs are only slow (404 on GetUnleashData,
// then timeouts) is used when no other port proves itself, and a port that
// does prove itself still wins over it.
func TestProbeForConnectAPI_SlowFallbacksAreALastResort(t *testing.T) {
	withProbeTimeouts(t, 100*time.Millisecond, 100*time.Millisecond)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/GetUnleashData") {
			http.NotFound(w, r)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	slowPort := testServerPort(t, slow.URL)

	conn, err := quietAntigravityClient().probeForConnectAPI(context.Background(), []int{slowPort}, "")
	if err != nil || conn == nil || conn.Port != slowPort {
		t.Fatalf("conn = %+v, err = %v; want the slow port as a last resort", conn, err)
	}

	rec := &probeRecorder{}
	good := newAgy15Server(t, rec, unleashRPC)
	conn, err = quietAntigravityClient().probeForConnectAPI(context.Background(), []int{slowPort, testServerPort(t, good.URL)}, "")
	if err != nil || conn.Port != testServerPort(t, good.URL) {
		t.Fatalf("conn = %+v, err = %v; want the port that answered", conn, err)
	}

	// A plain 404 server is still not taken.
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	if _, err := quietAntigravityClient().probeForConnectAPI(context.Background(), []int{testServerPort(t, other.URL)}, ""); !errors.Is(err, ErrAntigravityConnectionFailed) {
		t.Fatalf("err = %v, want ErrAntigravityConnectionFailed for a 404-only server", err)
	}
}
