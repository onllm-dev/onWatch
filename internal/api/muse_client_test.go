package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func museTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %s, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(got, "Bearer ")) == "" {
			t.Errorf("missing Bearer [REDACTED]")
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Errorf("Accept = %q, want event-stream", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestMuseClientFetchSnapshot(t *testing.T) {
	srv := museTestServer(t, "data: "+museTestSSESubscription+"\n\ndata: [DONE]\n", http.StatusOK)
	defer srv.Close()

	c := NewMuseClient("test-key", "muse-spark-1.3", nil, WithMuseBaseURL(srv.URL))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) != 2 {
		t.Fatalf("quotas = %d, want 2", len(snap.Quotas))
	}
	if snap.Model != "muse-spark-1.3" {
		t.Fatalf("model = %q", snap.Model)
	}
	if snap.RawJSON == "" {
		t.Fatal("RawJSON empty")
	}
}

func TestMuseClientSendsProbeBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + museTestSSESubscription + "\n"))
	}))
	defer srv.Close()

	c := NewMuseClient("k", "muse-spark-1.3", nil, WithMuseBaseURL(srv.URL))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	for _, want := range []string{`"stream":true`, `"max_output_tokens":16`, `"muse-spark-1.3"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("probe body missing %s: %s", want, gotBody)
		}
	}
}

func TestMuseClientAuthErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := museTestServer(t, `{"error":{"message":"bad key"}}`, status)
		c := NewMuseClient("bad", "m", nil, WithMuseBaseURL(srv.URL))
		_, err := c.FetchSnapshot(context.Background())
		srv.Close()
		if err == nil || !IsMuseAuthError(err) {
			t.Fatalf("status %d: err = %v, want auth error", status, err)
		}
	}
}

func TestMuseClientRateLimited(t *testing.T) {
	srv := museTestServer(t, `{}`, http.StatusTooManyRequests)
	defer srv.Close()
	c := NewMuseClient("k", "m", nil, WithMuseBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil || !IsMuseRateLimited(err) {
		t.Fatalf("err = %v, want rate limited", err)
	}
}

func TestMuseClientMissingKey(t *testing.T) {
	c := NewMuseClient("  ", "m", nil)
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected missing-key error")
	}
}

func TestMuseClientNoSubscription(t *testing.T) {
	srv := museTestServer(t, "data: {\"type\":\"pong\"}\n\ndata: [DONE]\n", http.StatusOK)
	defer srv.Close()
	c := NewMuseClient("k", "m", nil, WithMuseBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected error when stream has no subscription")
	}
}

func TestMuseClientStopsAfterSubscription(t *testing.T) {
	// The live Meta stream keeps generating after the subscription event.
	// The client must return as soon as it has usage, not wait for [DONE],
	// otherwise it holds a competing /v1/responses call against the Muse CLI.
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter is not a Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+museTestSSESubscription+"\n\n")
		flusher.Flush()
		select {
		case <-r.Context().Done():
			close(released)
		case <-time.After(3 * time.Second):
			t.Error("client kept the probe stream open after the subscription event")
			close(released)
		}
	}))
	defer srv.Close()

	c := NewMuseClient("k", "muse-spark-1.3", nil, WithMuseBaseURL(srv.URL), WithMuseTimeout(4*time.Second))
	start := time.Now()
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if snap == nil || snap.WindowUsedPct != 34 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("FetchSnapshot took %s; expected to return at the subscription event", elapsed)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe client disconnect")
	}
}

func TestMuseClientNeverLogsKey(t *testing.T) {
	// The client must not embed the key in returned errors.
	srv := museTestServer(t, `{"error":{"message":"boom"}}`, http.StatusBadGateway)
	defer srv.Close()
	secret := "muse-secret-key-12345"
	c := NewMuseClient(secret, "m", nil, WithMuseBaseURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("error leaks API key: %v", err)
	}
}
