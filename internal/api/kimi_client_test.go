package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// Auto-detected kimi-code tokens must use disk+refresh, not a frozen static
// access token. When usages returns 401 for an unexpired access token, force
// OAuth refresh on the kimi-code store and retry.
func TestKimiClientFetchSnapshot_ForceRefreshOn401UnexpiredAccess(t *testing.T) {
	var usagesHits atomic.Int32
	var refreshHits atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/usages", func(w http.ResponseWriter, r *http.Request) {
		usagesHits.Add(1)
		auth := r.Header.Get("Authorization")
		switch {
		case auth == "Bearer stale-access":
			w.WriteHeader(http.StatusUnauthorized)
		case auth == "Bearer fresh-access":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"usage": map[string]string{"limit": "100", "used": "42", "remaining": "58", "resetTime": "2026-07-15T00:00:00Z"},
				"user":  map[string]interface{}{"id": "u1", "membership": map[string]string{"level": "LEVEL_INTERMEDIATE"}},
			})
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	mux.HandleFunc("/api/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		if vals.Get("refresh_token") != "refresh-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "fresh-access",
			"refresh_token": "refresh-2",
			"token_type":    "Bearer",
			"scope":         "kimi-code",
			"expires_in":    900,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("KIMI_CODE_CREDENTIALS", "")
	t.Setenv("KIMI_CREDENTIALS", "")
	codeHome := filepath.Join(home, ".kimi-code")
	credDir := filepath.Join(codeHome, "credentials")
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Access is NOT expired by wall clock, but API rejects it — classic frozen/stale token.
	credPath := filepath.Join(credDir, "kimi-code.json")
	raw := map[string]interface{}{
		"access_token":  "stale-access",
		"refresh_token": "refresh-1",
		"token_type":    "Bearer",
		"scope":         "kimi-code",
		"expires_at":    float64(time.Now().Unix() + 600),
		"expires_in":    900,
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(credPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Ensure legacy kimi-cli is ignored even if present.
	cliHome := filepath.Join(home, ".kimi")
	_ = os.MkdirAll(filepath.Join(cliHome, "credentials"), 0o700)
	_ = os.WriteFile(filepath.Join(cliHome, "credentials", "kimi.json"), []byte(`{"access_token":"cli-only","refresh_token":"cli-r"}`), 0o600)

	InvalidateKimiCredentialsCache()
	// Empty static token → disk path (what auto-detect should use after the fix).
	c := NewKimiClient("", nil,
		WithKimiBaseURL(srv.URL),
		WithKimiOAuthHost(srv.URL),
	)
	snap, err := c.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("refresh hits = %d, want 1", refreshHits.Load())
	}
	if usagesHits.Load() < 2 {
		t.Fatalf("usages hits = %d, want >= 2 (stale then fresh)", usagesHits.Load())
	}
	if len(snap.Quotas) == 0 || snap.Quotas[0].Utilization != 42 {
		t.Fatalf("snapshot quotas = %+v", snap.Quotas)
	}
	// Persisted back to kimi-code only.
	saved, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "fresh-access") || !strings.Contains(string(saved), "refresh-2") {
		t.Fatalf("credentials not updated on disk: %s", saved)
	}
}
