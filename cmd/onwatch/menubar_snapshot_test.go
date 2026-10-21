package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

func snapshotTestPort(t *testing.T, rawURL string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(rawURL[len("http://"):])
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}
	return n
}

// A transient failure (one bad response) must not surface as a refresh failure:
// the provider retries once before giving up.
func TestHTTPSnapshotProviderRetriesOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(menubar.Snapshot{Aggregate: menubar.Aggregate{ProviderCount: 1}})
	}))
	defer srv.Close()

	snapshot, err := httpSnapshotProvider(snapshotTestPort(t, srv.URL))()
	if err != nil {
		t.Fatalf("expected retry to recover, got error: %v", err)
	}
	if snapshot == nil || snapshot.Aggregate.ProviderCount != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", got)
	}
}

// When both attempts fail the provider reports an error so the caller can fall
// back to its cached snapshot.
func TestHTTPSnapshotProviderFailsAfterRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	snapshot, err := httpSnapshotProvider(snapshotTestPort(t, srv.URL))()
	if err == nil {
		t.Fatal("expected an error after both attempts failed")
	}
	if snapshot != nil {
		t.Fatalf("expected nil snapshot on failure, got %#v", snapshot)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", got)
	}
}
