package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiClientFetchSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"usage": map[string]string{"limit": "100", "used": "10", "remaining": "90", "resetTime": "2026-07-15T00:00:00Z"},
		})
	}))
	defer srv.Close()

	c := NewKimiClient("test-token", nil, WithKimiBaseURL(srv.URL), WithKimiStaticToken("test-token"))
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Quotas) == 0 {
		t.Fatal("no quotas")
	}
	if snap.Quotas[0].Name != KimiQuotaSevenDay {
		t.Fatalf("name %s", snap.Quotas[0].Name)
	}
}

func TestKimiClientUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewKimiClient("bad", nil, WithKimiBaseURL(srv.URL), WithKimiStaticToken("bad"))
	_, err := c.FetchSnapshot(context.Background())
	if err != ErrKimiUnauthorized {
		t.Fatalf("want unauthorized, got %v", err)
	}
}
