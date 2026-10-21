package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// With one stored provider snapshot the tray-title response must carry
// `latest_at` as RFC3339 so the GNOME extension (and any other tray
// companion) can keep counting up even after the daemon stops responding.
// The header age cannot be derived from a build time when the daemon is gone.
func TestMenubarTrayTitleIncludesLatestAtWhenProviderDataPresent(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	capturedAt := time.Now().UTC().Add(-25 * time.Minute).Truncate(time.Second)
	reset := capturedAt.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent, ResetsAt: &reset},
	})

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/tray-title", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	h.MenubarTrayTitle(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	raw, ok := body["latest_at"]
	if !ok {
		t.Fatalf("latest_at missing from tray-title response: %s", rr.Body.String())
	}
	got, ok := raw.(string)
	if !ok {
		t.Fatalf("latest_at = %T (%v), want string", raw, raw)
	}
	want := capturedAt.UTC().Format(time.RFC3339)
	if got != want {
		t.Fatalf("latest_at = %q, want %q", got, want)
	}
}

// With no providers configured and no stored data, the tray-title response
// must omit `latest_at` entirely: the zero time would render as
// "0001-01-01T00:00:00Z" on clients that treat it as a real timestamp.
func TestMenubarTrayTitleOmitsLatestAtWhenNoProviderData(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, &config.Config{
		PollInterval: 60 * time.Second,
		Port:         9211,
		AdminUser:    "admin",
		AdminPass:    "test",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/tray-title", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	h.MenubarTrayTitle(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	raw := rr.Body.String()
	if _, ok := decodeTrayTitleHasKey(raw, "latest_at"); ok {
		t.Fatalf("latest_at must be omitted when no provider has data, got body: %s", raw)
	}
	if strings.Contains(raw, "0001-01-01") {
		t.Fatalf("zero time leaked into JSON: %s", raw)
	}
}

// decodeTrayTitleHasKey reports whether `key` appears in the JSON object's
// own keys (not inside any nested object). It does not parse the value.
func decodeTrayTitleHasKey(raw, key string) (interface{}, bool) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return nil, false
	}
	value, ok := body[key]
	if !ok {
		return nil, false
	}
	var out interface{}
	if err := json.Unmarshal(value, &out); err != nil {
		return raw, true
	}
	return out, true
}
