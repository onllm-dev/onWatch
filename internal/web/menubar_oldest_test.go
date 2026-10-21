package web

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// The menubar age must come from the OLDEST quota a card shows, not the
// newest: a fresh quota otherwise hides a days-old one and the card reads as
// live. The dashboard API keeps reporting the newest time.
func TestMenubarUsesOldestQuotaCaptureAnthropic(t *testing.T) {
	h, s, _, done := staleTestHandler(t, anthropicStaleConfig())
	defer done()

	newest := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	oldest := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	insertAnthropicQuotaSnapshots(t, s, newest, oldest)

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(snapshot, "anthropic")
	if !ok {
		t.Fatalf("anthropic card missing: %+v", snapshot.Providers)
	}
	if card.UpdatedAtTime != oldest.Format(time.RFC3339) {
		t.Fatalf("menubar updated_at_time = %q, want the oldest quota time %q", card.UpdatedAtTime, oldest.Format(time.RFC3339))
	}
	if !card.Stale {
		t.Fatal("a card with a days-old quota must be stale")
	}

	// Dashboard endpoint: unchanged, still the newest quota time.
	response, _, _ := h.buildAnthropicCurrentOK()
	if got, _ := response["capturedAt"].(string); got != newest.Format(time.RFC3339) {
		t.Fatalf("dashboard capturedAt = %q, want the newest quota time %q (dashboard contract unchanged)", got, newest.Format(time.RFC3339))
	}
}

func anthropicStaleConfig() *config.Config {
	return &config.Config{AnthropicToken: "token", AnthropicSource: "api", PollInterval: 120 * time.Second}
}

// insertAnthropicQuotaSnapshots writes one snapshot for five_hour at newest
// and one for seven_day at oldest, so the latest-per-quota merge spans them.
func insertAnthropicQuotaSnapshots(t *testing.T, s *store.Store, newest, oldest time.Time) {
	t.Helper()
	for _, snap := range []*api.AnthropicSnapshot{
		{CapturedAt: oldest, Quotas: []api.AnthropicQuota{{Name: "seven_day", Utilization: 40}}},
		{CapturedAt: newest, Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 10}}},
	} {
		if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
			t.Fatalf("InsertAnthropicSnapshot: %v", err)
		}
	}
}

func TestMenubarUsesOldestQuotaCaptureCursor(t *testing.T) {
	h, s, _, done := staleTestHandler(t, createTestConfigWithCursor())
	defer done()

	newest := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	oldest := time.Now().UTC().Add(-26 * time.Hour).Truncate(time.Second)
	// Two snapshots; the per-quota read only keeps the latest snapshot, so use
	// one snapshot whose parent time is the newest and a quota row is old only
	// through a second older snapshot carrying the other quota.
	insertTestCursorSnapshot(t, s, oldest, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})
	insertTestCursorSnapshot(t, s, newest, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "api_usage", Used: 5, Limit: 100, Utilization: 5, Format: api.CursorFormatPercent},
	})

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(snapshot, "cursor")
	if !ok {
		t.Fatalf("cursor card missing")
	}
	// Cursor's builder reports the latest snapshot (newest) and the per-quota
	// read only spans that snapshot, so no per-quota split exists; the card
	// keeps the builder value. This test documents that limitation.
	if card.UpdatedAtTime != newest.Format(time.RFC3339) {
		t.Fatalf("cursor updated_at_time = %q, want the builder value %q", card.UpdatedAtTime, newest.Format(time.RFC3339))
	}
}

func TestOldestQuotaCapturedAtParsing(t *testing.T) {
	h := &Handler{}
	oldest := "2026-10-17T10:00:00Z"
	newest := "2026-10-18T10:00:00Z"
	payload := map[string]interface{}{
		"capturedAt": newest,
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": newest},
			map[string]interface{}{"name": "seven_day", "lastUpdatedAt": oldest},
			map[string]interface{}{"name": "no_time"},
		},
	}
	got, ok := oldestQuotaCapturedAt(payload)
	if !ok || got.Format(time.RFC3339) != oldest {
		t.Fatalf("oldestQuotaCapturedAt = %v, %v; want %s", got, ok, oldest)
	}
	if _, ok := oldestQuotaCapturedAt(map[string]interface{}{}); ok {
		t.Fatal("a payload without quota times must report no oldest time")
	}

	card := &menubar.ProviderCard{UpdatedAtTime: newest}
	viaCard := h.applyMenubarOldestCapturedAt(card, "anthropic", payload)
	if viaCard.Format(time.RFC3339) != oldest || card.UpdatedAtTime != oldest {
		t.Fatalf("applyMenubarOldestCapturedAt = %v, card %q; want %s", viaCard, card.UpdatedAtTime, oldest)
	}

	// Providers not in the list keep the builder value.
	unchanged := &menubar.ProviderCard{UpdatedAtTime: newest}
	got2 := h.applyMenubarOldestCapturedAt(unchanged, "zai", payload)
	if got2.Format(time.RFC3339) != newest || unchanged.UpdatedAtTime != newest {
		t.Fatalf("zai was rewritten to %v", got2)
	}
}
