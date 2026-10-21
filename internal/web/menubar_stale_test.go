package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// staleTestHandler builds a handler backed by an in-memory store with the
// given poll interval and a registry installed.
func staleTestHandler(t *testing.T, cfg *config.Config) (*Handler, *store.Store, *agent.PollStatusRegistry, func()) {
	t.Helper()
	h, s, _ := newSharedMemoryMenubarHandler(t, cfg)
	registry := agent.NewPollStatusRegistry()
	h.SetPollStatusRegistry(registry)
	prev := agent.GetPollStatusRegistry()
	agent.SetPollStatusRegistry(registry)
	return h, s, registry, func() { agent.SetPollStatusRegistry(prev) }
}

func staleCursorCard(h *Handler, t *testing.T) menubar.ProviderCard {
	t.Helper()
	snap, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(snap, "cursor")
	if !ok {
		t.Fatalf("cursor card missing: %+v", snap.Providers)
	}
	return card
}

func TestStaleAfterThresholds(t *testing.T) {
	interval := 120 * time.Second
	cases := []struct {
		name string
		ctx  staleContext
		want time.Duration
	}{
		{"generic default is 3 intervals", staleContext{Base: "cursor", Polling: true, PollInterval: interval}, 6 * time.Minute},
		{"floor wins for a short interval", staleContext{Base: "cursor", Polling: true, PollInterval: 30 * time.Second}, 5 * time.Minute},
		{"statusline cadence", staleContext{Base: "anthropic", Polling: true, PollInterval: interval}, 15 * time.Minute},
		{"antigravity probe cadence", staleContext{Base: "antigravity", Polling: true, PollInterval: interval}, 15 * time.Minute},
		{"mistral normal", staleContext{Base: "mistral", Polling: true, PollInterval: interval}, 6 * time.Minute},
		{"mistral backed off", staleContext{Base: "mistral", Polling: true, PollInterval: interval, Status: "stale"}, 6 * time.Hour},
		{"mistral reconnect", staleContext{Base: "mistral", Polling: true, PollInterval: interval, Status: "reconnect"}, 6 * time.Hour},
		{"codex paused", staleContext{Base: "codex", Polling: true, PollInterval: interval, Outcome: agent.PollOutcomePaused}, 6 * time.Hour},
		{"codex normal", staleContext{Base: "codex", Polling: true, PollInterval: interval}, 6 * time.Minute},
		{"manual provider never goes stale", staleContext{Base: "cursor", Polling: false, PollInterval: interval}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := staleAfterFor(tc.ctx); got != tc.want {
				t.Fatalf("staleAfterFor(%+v) = %s, want %s", tc.ctx, got, tc.want)
			}
		})
	}
}

func TestMenubarCardMarksStaleFromAgeAndRegistry(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, registry, done := staleTestHandler(t, cfg)
	defer done()

	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	registry.Report("cursor", "", agent.PollOutcomeNetwork)

	card := staleCursorCard(h, t)
	if !card.Stale {
		t.Fatal("a 20m-old card with a 60s poll interval must be stale")
	}
	if card.StaleReason != "network error" {
		t.Fatalf("StaleReason = %q, want network error", card.StaleReason)
	}
	// 3 x 60s is below the 5m floor.
	if card.StaleAfterSeconds != int64((5 * time.Minute).Seconds()) {
		t.Fatalf("StaleAfterSeconds = %d, want the 300s floor", card.StaleAfterSeconds)
	}
	if card.Status != "healthy" {
		t.Fatalf("Status = %q, staleness must not change severity", card.Status)
	}

	// Success clears the registry entry; a stale card then carries the generic
	// reason computed from its age.
	registry.Clear("cursor", "")
	card = staleCursorCard(h, t)
	if !card.Stale || card.StaleReason != "no update in 20m" {
		t.Fatalf("stale card after clear = %+v, want the generic 20m reason", card)
	}
}

func TestMenubarCardFreshHasNoStaleFlag(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, registry, done := staleTestHandler(t, cfg)
	defer done()

	insertTestCursorSnapshot(t, s, time.Now().UTC(), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	// A stale registry entry alone must not mark a fresh card stale: the age
	// is what matters, and a successful poll clears the entry anyway.
	registry.Report("cursor", "", agent.PollOutcomeNetwork)
	card := staleCursorCard(h, t)
	if card.Stale {
		t.Fatalf("fresh card marked stale: %+v", card)
	}
}

func TestMenubarCardPausedWinsOnWording(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, registry, done := staleTestHandler(t, cfg)
	defer done()

	capturedAt := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	registry.Report("cursor", "", agent.PollOutcomePaused)

	card := staleCursorCard(h, t)
	if !card.Stale {
		t.Fatal("a paused 30m-old card must be stale")
	}
	if card.StaleReason != "polling paused" {
		t.Fatalf("StaleReason = %q, want the paused wording", card.StaleReason)
	}
}

func TestStaleManualPollingProviderNeverFlagged(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, _, done := staleTestHandler(t, cfg)
	defer done()
	if err := h.saveProviderVisibility(map[string]map[string]bool{
		"cursor": {"polling": false, "dashboard": true},
	}); err != nil {
		t.Fatalf("saveProviderVisibility: %v", err)
	}

	capturedAt := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	card := staleCursorCard(h, t)
	if card.Stale {
		t.Fatalf("a polling:false provider must not be flagged stale: %+v", card)
	}
	if card.StaleAfterSeconds != 0 {
		t.Fatalf("StaleAfterSeconds = %d for manual provider, want 0", card.StaleAfterSeconds)
	}
	if card.UpdatedAtTime == "" {
		t.Fatal("manual provider must still carry its real age label")
	}
}

// Menubar JSON must never be cacheable: a day-old summary served as fresh is
// indistinguishable from live data.
func TestMenubarSummaryResponseIsNoStore(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
	rr := httptest.NewRecorder()
	h.MenubarSummary(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := body["providers"]; !ok {
		t.Fatalf("summary missing providers: %v", body)
	}
}

func TestAllJSONResponsesAreNoStore(t *testing.T) {
	rr := httptest.NewRecorder()
	respondJSON(rr, http.StatusOK, map[string]string{"ok": "yes"})
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("respondJSON Cache-Control = %q, want no-store", got)
	}
	if !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("Content-Type = %q", rr.Header().Get("Content-Type"))
	}
}

// Mistral quotas can come from different snapshots and carry their own
// capture times, so the card age must be the oldest of them.
func TestMenubarUsesOldestQuotaCaptureMistral(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, PollInterval: 120 * time.Second}
	h, s, _, done := staleTestHandler(t, cfg)
	defer done()

	newest := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	oldest := time.Now().UTC().Add(-30 * time.Hour).Truncate(time.Second)
	for _, snap := range []*api.MistralSnapshot{
		{Identity: "test", CapturedAt: oldest, Status: "ok", Quotas: []api.MistralQuota{
			{Name: "vibe_included", Used: 10, Limit: 100, Currency: "EUR", Utilization: 10, CapturedAt: oldest},
		}},
		{Identity: "test", CapturedAt: newest, Status: "ok", Quotas: []api.MistralQuota{
			{Name: "api_included", Used: 20, Limit: 100, Currency: "EUR", Utilization: 20, CapturedAt: newest},
		}},
	} {
		if err := s.SaveMistral(t.Context(), snap); err != nil {
			t.Fatalf("SaveMistral: %v", err)
		}
	}

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(snapshot, "mistral")
	if !ok {
		t.Fatalf("mistral card missing")
	}
	if card.UpdatedAtTime != oldest.Format(time.RFC3339) {
		t.Fatalf("mistral updated_at_time = %q, want the oldest quota time %q", card.UpdatedAtTime, oldest.Format(time.RFC3339))
	}
}

// Once a quota has aged out of the builder's query window, the remembered
// oldest time keeps the card honest instead of snapping back to the newest.
func TestMenubarRemembersOldestQuotaTime(t *testing.T) {
	h := &Handler{}
	newest := time.Now().UTC().Truncate(time.Second)
	oldest := newest.Add(-50 * time.Hour)
	split := map[string]interface{}{
		"capturedAt": newest.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": newest.Format(time.RFC3339)},
			map[string]interface{}{"name": "seven_day", "lastUpdatedAt": oldest.Format(time.RFC3339)},
		},
	}
	card := &menubar.ProviderCard{}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", split); !got.Equal(oldest) {
		t.Fatalf("first pass = %v, want %v", got, oldest)
	}
	// seven_day aged out of the window while five_hour stayed: the missing
	// quota's last-seen time is remembered.
	shrunk := map[string]interface{}{
		"capturedAt": newest.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": newest.Format(time.RFC3339)},
		},
	}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", shrunk); !got.Equal(oldest) {
		t.Fatalf("after the old quota aged out = %v, want the remembered %v", got, oldest)
	}
}

// The tray-title tooltip lists stale providers, and the segments carry the
// stale flag so GNOME can dim just those slots locally.
func TestMenubarTrayTitleCarriesStaleState(t *testing.T) {
	h, s, _, done := staleTestHandler(t, createTestConfigWithCursor())
	defer done()

	capturedAt := time.Now().UTC().Add(-25 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	registry := agent.GetPollStatusRegistry()
	registry.Report("cursor", "", agent.PollOutcomeNetwork)
	settings := menubar.DefaultSettings()
	settings.StatusDisplay = menubar.StatusDisplay{
		Mode:           menubar.StatusDisplayMultiProvider,
		SelectedQuotas: []menubar.StatusDisplaySelection{{ProviderID: "cursor"}},
	}
	if err := s.SetMenubarSettings(settings); err != nil {
		t.Fatalf("SetMenubarSettings: %v", err)
	}
	h.invalidateMenubarSnapshot()

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/tray-title", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	h.MenubarTrayTitle(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	tooltip, _ := body["tooltip"].(string)
	if !strings.Contains(tooltip, "Cursor: 25m ago (network error)") {
		t.Fatalf("tooltip = %q, want the stale cursor line", tooltip)
	}
	segments, ok := body["segments"].([]interface{})
	if !ok || len(segments) == 0 {
		t.Fatalf("segments missing: %v", body["segments"])
	}
	first, _ := segments[0].(map[string]interface{})
	if stale, _ := first["stale"].(bool); !stale {
		t.Fatalf("segment stale = %v, want true", first["stale"])
	}
	if reason, _ := first["stale_reason"].(string); reason != "network error" {
		t.Fatalf("segment stale_reason = %q", reason)
	}
}

// A cached (last-good) card flows through the same staleness pass, so a
// storage failure cannot make an old card look fresh.
func TestMenubarCachedCardStillMarkedStale(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)
	registry := agent.NewPollStatusRegistry()
	h.SetPollStatusRegistry(registry)

	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("first build: %v", err)
	}
	registry.Report("cursor", "", agent.PollOutcomeUpstream)

	breakProviderStorage(t, dsn, "cursor_snapshots", "cursor_quota_values")
	card := staleCursorCard(h, t)
	if !card.Stale || card.StaleReason != "upstream error" {
		t.Fatalf("cached card = stale %v reason %q, want stale upstream error", card.Stale, card.StaleReason)
	}
}
