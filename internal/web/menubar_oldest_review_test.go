package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

// Review item 1: the remembered oldest quota time must move FORWARD once the
// quota refreshes. The bug kept the first-seen time forever, so every card
// went stale permanently after one window.
func TestMenubarOldestQuotaTimeMovesForward(t *testing.T) {
	h, s, _, done := staleTestHandler(t, anthropicStaleConfig())
	defer done()

	oldest := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	newest := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	insertAnthropicQuotaSnapshots(t, s, newest, oldest)

	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	card, ok := menubarCardByID(first, "anthropic")
	if !ok {
		t.Fatalf("anthropic card missing")
	}
	if card.UpdatedAtTime != oldest.Format(time.RFC3339) {
		t.Fatalf("first build updated_at_time = %q, want the oldest quota time %q", card.UpdatedAtTime, oldest.Format(time.RFC3339))
	}

	// Both quotas refresh (new snapshot carrying both). The card age must
	// advance to the new oldest quota time, not stay pinned 48h back.
	refreshed := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		CapturedAt: refreshed,
		Quotas: []api.AnthropicQuota{
			{Name: "seven_day", Utilization: 41},
			{Name: "five_hour", Utilization: 11},
		},
	}); err != nil {
		t.Fatalf("insert refreshed snapshot: %v", err)
	}
	h.invalidateMenubarSnapshot()

	second, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	card, ok = menubarCardByID(second, "anthropic")
	if !ok {
		t.Fatalf("anthropic card missing after refresh")
	}
	if card.UpdatedAtTime != refreshed.Format(time.RFC3339) {
		t.Fatalf("updated_at_time after refresh = %q, want %q (the remembered oldest must move forward)", card.UpdatedAtTime, refreshed.Format(time.RFC3339))
	}
	if card.Stale {
		t.Fatalf("refreshed card still stale: %+v", card)
	}
}

// A quota that disappears from the payload is remembered, and once every quota
// is back (with fresh times) the remembered value is cleared.
func TestMenubarDroppedQuotaRememberedThenCleared(t *testing.T) {
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

	// seven_day drops out of the window: its time must be remembered.
	dropped := map[string]interface{}{
		"capturedAt": newest.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": newest.Format(time.RFC3339)},
		},
	}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", dropped); !got.Equal(oldest) {
		t.Fatalf("dropped quota pass = %v, want the remembered %v", got, oldest)
	}

	// seven_day comes back with a fresh time: the remembered stale value must
	// be cleared, not kept forever.
	fresh := newest.Add(5 * time.Minute)
	back := map[string]interface{}{
		"capturedAt": fresh.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": fresh.Format(time.RFC3339)},
			map[string]interface{}{"name": "seven_day", "lastUpdatedAt": fresh.Format(time.RFC3339)},
		},
	}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", back); !got.Equal(fresh) {
		t.Fatalf("restored quotas pass = %v, want the fresh %v", got, fresh)
	}

	// The remembered value is cleared: dropping seven_day again after the
	// fresh reading must remember the FRESH time, not resurrect the old one.
	dropAgain := map[string]interface{}{
		"capturedAt": fresh.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": fresh.Format(time.RFC3339)},
		},
	}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", dropAgain); !got.Equal(fresh) {
		t.Fatalf("drop after restore = %v, want the fresh %v (the old remembered value must be gone)", got, fresh)
	}
}

// Review item 1, hybrid mode: statusline-fed primary quotas use the statusline
// cadence; API-only supplementary quotas (Sonnet, extra usage) refresh only
// every 10 intervals (about 20m) and must not mark the card stale at 15m.
func TestMenubarAnthropicHybridPrimaryQuotasOnly(t *testing.T) {
	cfg := &config.Config{AnthropicToken: "token", AnthropicSource: "auto", PollInterval: 120 * time.Second}
	h, s, _, done := staleTestHandler(t, cfg)
	defer done()

	now := time.Now().UTC().Truncate(time.Second)
	// Statusline primaries are 20m old; API-only Sonnet is 25m old. With a
	// 15m statusline threshold the card must be stale (the primaries are old).
	statuslineAt := now.Add(-20 * time.Minute)
	apiOnlyAt := now.Add(-25 * time.Minute)
	for _, snap := range []*api.AnthropicSnapshot{
		{CapturedAt: statuslineAt, Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 20},
			{Name: "seven_day", Utilization: 40},
		}},
		{CapturedAt: apiOnlyAt, Quotas: []api.AnthropicQuota{
			{Name: "seven_day_sonnet", Utilization: 10},
		}},
	} {
		if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
			t.Fatalf("InsertAnthropicSnapshot: %v", err)
		}
	}
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	card, ok := menubarCardByID(snapshot, "anthropic")
	if !ok {
		t.Fatalf("anthropic card missing")
	}
	if !card.Stale {
		t.Fatalf("20m-old primary quotas with a 15m threshold must be stale: %+v", card)
	}

	// Fresh primaries, old supplementary quota: the card must NOT be stale.
	h2, s2, _, done2 := staleTestHandler(t, cfg)
	defer done2()
	for _, snap := range []*api.AnthropicSnapshot{
		{CapturedAt: now.Add(-time.Minute), Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 20},
			{Name: "seven_day", Utilization: 40},
		}},
		{CapturedAt: now.Add(-3 * time.Hour), Quotas: []api.AnthropicQuota{
			{Name: "seven_day_sonnet", Utilization: 10},
		}},
	} {
		if _, err := s2.InsertAnthropicSnapshot(snap); err != nil {
			t.Fatalf("InsertAnthropicSnapshot: %v", err)
		}
	}
	snapshot, err = h2.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	card, ok = menubarCardByID(snapshot, "anthropic")
	if !ok {
		t.Fatalf("anthropic card missing")
	}
	if card.Stale {
		t.Fatalf("fresh primary quotas must keep the card fresh even when an API-only quota is hours old: %+v", card)
	}
}

// Review item 2: the summary must serialize stale_checked_at so the popover
// and the tray can re-evaluate an Anthropic hybrid card on the primary
// windows between server responses.
func TestMenubarSummarySerializesStaleCheckedAt(t *testing.T) {
	cfg := &config.Config{AnthropicToken: "token", AnthropicSource: "auto", PollInterval: 120 * time.Second}
	h, s, _, done := staleTestHandler(t, cfg)
	defer done()

	now := time.Now().UTC().Truncate(time.Second)
	for _, snap := range []*api.AnthropicSnapshot{
		{CapturedAt: now.Add(-time.Minute), Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 20},
			{Name: "seven_day", Utilization: 40},
		}},
		{CapturedAt: now.Add(-3 * time.Hour), Quotas: []api.AnthropicQuota{
			{Name: "seven_day_sonnet", Utilization: 10},
		}},
	} {
		if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
			t.Fatalf("InsertAnthropicSnapshot: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil)
	rr := httptest.NewRecorder()
	h.MenubarSummary(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Providers []menubar.ProviderCard `json:"providers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	var anthropic *menubar.ProviderCard
	for i := range body.Providers {
		if body.Providers[i].ID == "anthropic" {
			anthropic = &body.Providers[i]
		}
	}
	if anthropic == nil {
		t.Fatalf("anthropic card missing: %+v", body.Providers)
	}
	if anthropic.StaleCheckedAt == "" {
		t.Fatal("a hybrid Anthropic card must serialize stale_checked_at for the clients")
	}
	if anthropic.Stale {
		t.Fatalf("fresh primaries must keep the card fresh: %+v", anthropic)
	}
}

// A provider whose builder reads only the newest snapshot cannot have a quota
// that merely aged out of a query window: a quota missing from that snapshot
// is gone. Remembering it would pin the card stale for up to
// menubarQuotaMemoryBound, which is exactly the bug.
func TestMenubarLatestSnapshotProviderDropsMissingQuotaImmediately(t *testing.T) {
	h := &Handler{}
	newest := time.Now().UTC().Truncate(time.Second)
	oldest := newest.Add(-6 * time.Hour)
	withOld := map[string]interface{}{
		"capturedAt": newest.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "total_usage", "lastUpdatedAt": oldest.Format(time.RFC3339)},
			map[string]interface{}{"name": "api_usage", "lastUpdatedAt": newest.Format(time.RFC3339)},
		},
	}
	card := &menubar.ProviderCard{}
	if got := h.applyMenubarOldestCapturedAt(card, "cursor", withOld); !got.Equal(oldest) {
		t.Fatalf("first pass = %v, want %v", got, oldest)
	}

	// total_usage is absent from the newest snapshot. Cursor's builder reads
	// only that snapshot, so the quota is gone and must no longer contribute
	// its old time.
	dropped := map[string]interface{}{
		"capturedAt": newest.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "api_usage", "lastUpdatedAt": newest.Format(time.RFC3339)},
		},
	}
	if got := h.applyMenubarOldestCapturedAt(card, "cursor", dropped); !got.Equal(newest) {
		t.Fatalf("after the quota left the newest snapshot = %v, want %v (the missing quota must not be remembered)", got, newest)
	}
	if got := h.menubarQuotas["cursor"]; len(got) != 1 {
		t.Fatalf("remembered quotas = %v, want only api_usage", got)
	}

	// An empty cursor payload is equally authoritative: nothing present means
	// nothing may stay remembered.
	card = &menubar.ProviderCard{}
	if got := h.applyMenubarOldestCapturedAt(card, "cursor", map[string]interface{}{"quotas": []interface{}{}}); !got.IsZero() {
		t.Fatalf("empty payload = %v, want zero", got)
	}
	if got := h.menubarQuotas["cursor"]; len(got) != 0 {
		t.Fatalf("remembered quotas after empty payload = %v, want none", got)
	}
}

// Review item 3: a quota missing from the payload is remembered so the card
// cannot look fresher than its data, but a quota that is gone for good must
// stop pinning the card's age after a bound.
func TestMenubarRemovedQuotaUnpinsAfterBound(t *testing.T) {
	h := &Handler{}
	base := time.Now().UTC().Truncate(time.Second)
	now := base
	h.menubarNow = func() time.Time { return now }

	present := time.Now().UTC().Truncate(time.Second)
	oldest := present.Add(-60 * time.Hour)
	withOld := map[string]interface{}{
		"capturedAt": present.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": present.Format(time.RFC3339)},
			map[string]interface{}{"name": "extra_usage", "lastUpdatedAt": oldest.Format(time.RFC3339)},
		},
	}
	card := &menubar.ProviderCard{}
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", withOld); !got.Equal(oldest) {
		t.Fatalf("first pass = %v, want %v", got, oldest)
	}

	// The quota disappears from the payload. Inside the bound it still pins
	// the age, so old data is not silently relabelled as fresh.
	without := map[string]interface{}{
		"capturedAt": present.Format(time.RFC3339),
		"quotas": []interface{}{
			map[string]interface{}{"name": "five_hour", "lastUpdatedAt": present.Format(time.RFC3339)},
		},
	}
	now = base.Add(time.Hour)
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", without); !got.Equal(oldest) {
		t.Fatalf("inside the bound = %v, want the remembered %v", got, oldest)
	}

	// Past the bound the memory is dropped: the card follows the data that is
	// actually still there instead of staying pinned until restart.
	now = base.Add(time.Hour + menubarQuotaMemoryBound + time.Minute)
	if got := h.applyMenubarOldestCapturedAt(card, "anthropic", without); !got.Equal(present) {
		t.Fatalf("past the bound = %v, want the present quota time %v", got, present)
	}
	if got := h.menubarQuotas["anthropic"]; len(got) != 1 {
		t.Fatalf("remembered quotas = %v, want only the present one", got)
	}
}
