package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// breakProviderStorage makes every subsequent read of one provider's tables
// fail, without touching any other provider. It opens a second handle on the
// same shared-cache in-memory database and drops the tables the menubar builder
// reads. A dropped table turns the builder's read into a hard SQLite error,
// which is exactly the storage failure the last-good cache has to survive.
func breakProviderStorage(t *testing.T, dsn string, tables ...string) {
	t.Helper()

	// The store's own pool hides its *sql.DB, so reach the same database
	// through a second connection on the shared-cache DSN.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	for _, table := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

// execSQLOnDSN runs one statement on a second handle to the shared-cache
// in-memory database. Tests use it to change storage under the handler's feet:
// delete rows for a successful empty read, or rename a table to make one read
// fail while an adjacent read still succeeds.
func execSQLOnDSN(t *testing.T, dsn string, statements ...string) {
	t.Helper()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// newSharedMemoryMenubarHandler returns a handler backed by an in-memory
// SQLite database that other connections can reach, so a test can break one
// provider's tables while the handler keeps serving the others.
func newSharedMemoryMenubarHandler(t *testing.T, cfg *config.Config) (*Handler, *store.Store, string) {
	t.Helper()

	dsn := "file:menubar-lastgood-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	s, err := store.New(dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	h := NewHandler(s, nil, nil, nil, cfg)
	return h, s, dsn
}

func menubarCardByID(snapshot *menubar.Snapshot, id string) (menubar.ProviderCard, bool) {
	for _, card := range snapshot.Providers {
		if card.ID == id {
			return card, true
		}
	}
	return menubar.ProviderCard{}, false
}

// A storage read failure for one quota-array provider must not drop its card.
// The daemon keeps serving the last good card, with the original stored
// timestamp, so the client's age label keeps counting up.
func TestMenubarLastGoodServesPreviousCardOnQuotaArrayReadFailure(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	reset := capturedAt.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent, ResetsAt: &reset},
	})

	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	before, ok := menubarCardByID(first, "cursor")
	if !ok {
		t.Fatalf("expected cursor card in first snapshot, got %+v", first.Providers)
	}
	if before.UpdatedAtTime == "" {
		t.Fatal("expected cursor card to carry updated_at_time")
	}

	breakProviderStorage(t, dsn, "cursor_quota_values", "cursor_snapshots")

	after, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(after, "cursor")
	if !ok {
		t.Fatalf("cursor card vanished after a storage read failure: %+v", after.Providers)
	}
	if card.UpdatedAtTime != before.UpdatedAtTime {
		t.Fatalf("cursor updated_at_time = %q, want the original %q", card.UpdatedAtTime, before.UpdatedAtTime)
	}
	if len(card.Quotas) != len(before.Quotas) {
		t.Fatalf("cursor quotas = %d, want the cached %d", len(card.Quotas), len(before.Quotas))
	}
	for _, quota := range card.Quotas {
		if quota.Percent == 0 && before.Quotas[0].Percent != 0 {
			t.Fatalf("cached cursor card was zeroed: %+v", card.Quotas)
		}
	}
	if after.LatestAt.IsZero() {
		t.Fatal("LatestAt went zero after a storage failure; it must keep the last real data time")
	}
	if !after.LatestAt.Equal(capturedAt) {
		t.Fatalf("LatestAt = %s, want %s (the cached provider's real capturedAt)", after.LatestAt, capturedAt)
	}
}

// Mistral's builder returns a connection-only payload after its 3s read
// timeout. That payload must not replace the previous quota card.
func TestMenubarLastGoodServesPreviousCardOnMistralTimeout(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"}
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-45 * time.Minute).Truncate(time.Second)
	snap := &api.MistralSnapshot{
		Identity:   "test",
		CapturedAt: capturedAt,
		Status:     "ok",
		Quotas: []api.MistralQuota{
			{Name: "api_included", Used: 30, Limit: 100, Currency: "EUR", Utilization: 30, CapturedAt: capturedAt},
			{Name: "vibe_included", Used: 50, Limit: 100, Currency: "EUR", Utilization: 50, CapturedAt: capturedAt},
		},
	}
	if err := s.SaveMistral(t.Context(), snap); err != nil {
		t.Fatalf("SaveMistral: %v", err)
	}

	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	before, ok := menubarCardByID(first, "mistral")
	if !ok {
		t.Fatalf("expected mistral card in first snapshot, got %+v", first.Providers)
	}
	if len(before.Quotas) != 2 {
		t.Fatalf("mistral quotas = %d, want 2", len(before.Quotas))
	}

	breakProviderStorage(t, dsn, "mistral_quota_values", "mistral_snapshots")

	after, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(after, "mistral")
	if !ok {
		t.Fatalf("mistral card vanished after its read failed: %+v", after.Providers)
	}
	if len(card.Quotas) != 2 {
		t.Fatalf("mistral card dropped to a connection-only placeholder: %+v", card)
	}
	if card.UpdatedAtTime != before.UpdatedAtTime {
		t.Fatalf("mistral updated_at_time = %q, want the original %q", card.UpdatedAtTime, before.UpdatedAtTime)
	}
}

// Synthetic/Z.ai/OpenRouter build initialized quota maps stamped time.Now() on
// a read error. Those must never reach a card: the previous card is served
// unchanged instead, and the timestamp never resets to "now".
func TestMenubarLastGoodServesPreviousCardOnMapShapedProviderReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provider  string
		cfg       *config.Config
		table     string
		snapshot  func(capturedAt time.Time) any
		buildCard func(*Handler) (map[string]interface{}, bool, bool)
	}{
		{
			name:     "synthetic",
			provider: "synthetic",
			cfg:      createTestConfigWithSynthetic(),
			table:    "quota_snapshots",
			snapshot: func(capturedAt time.Time) any {
				return &api.Snapshot{
					CapturedAt: capturedAt,
					Sub:        api.QuotaInfo{Limit: 100, Requests: 40, RenewsAt: capturedAt.Add(time.Hour)},
					Search:     api.QuotaInfo{Limit: 100, Requests: 20, RenewsAt: capturedAt.Add(time.Hour)},
					ToolCall:   api.QuotaInfo{Limit: 100, Requests: 10, RenewsAt: capturedAt.Add(time.Hour)},
				}
			},
			buildCard: func(h *Handler) (map[string]interface{}, bool, bool) { return h.buildSyntheticCurrentOK() },
		},
		{
			name:     "zai",
			provider: "zai",
			cfg:      createTestConfigWithZai(),
			table:    "zai_snapshots",
			snapshot: func(capturedAt time.Time) any {
				return &api.ZaiSnapshot{
					CapturedAt:         capturedAt,
					TokensUsage:        1000,
					TokensCurrentValue: 400,
					TokensPercentage:   40,
					TimeUsage:          100,
					TimeCurrentValue:   30,
					TimePercentage:     30,
				}
			},
			buildCard: func(h *Handler) (map[string]interface{}, bool, bool) { return h.buildZaiCurrentOK() },
		},
		{
			name:     "openrouter",
			provider: "openrouter",
			cfg:      &config.Config{OpenRouterAPIKey: "or_test_key", PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"},
			table:    "openrouter_snapshots",
			snapshot: func(capturedAt time.Time) any {
				return &api.OpenRouterSnapshot{CapturedAt: capturedAt, Usage: 12.5, UsageDaily: 1.5}
			},
			buildCard: func(h *Handler) (map[string]interface{}, bool, bool) { return h.buildOpenRouterCurrentOK() },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s, dsn := newSharedMemoryMenubarHandler(t, tc.cfg)
			// Synthetic's builder only reads the store when a tracker is set.
			h.tracker = tracker.New(s, nil)

			capturedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
			switch snap := tc.snapshot(capturedAt).(type) {
			case *api.Snapshot:
				if _, err := s.InsertSnapshot(snap); err != nil {
					t.Fatalf("InsertSnapshot: %v", err)
				}
			case *api.ZaiSnapshot:
				if _, err := s.InsertZaiSnapshot(snap); err != nil {
					t.Fatalf("InsertZaiSnapshot: %v", err)
				}
			case *api.OpenRouterSnapshot:
				if _, err := s.InsertOpenRouterSnapshot(snap); err != nil {
					t.Fatalf("InsertOpenRouterSnapshot: %v", err)
				}
			}

			first, err := h.BuildMenubarSnapshot()
			if err != nil {
				t.Fatalf("first BuildMenubarSnapshot: %v", err)
			}
			before, ok := menubarCardByID(first, tc.provider)
			if !ok {
				t.Fatalf("expected %s card in first snapshot, got %+v", tc.provider, first.Providers)
			}

			breakProviderStorage(t, dsn, tc.table)

			// The builder's ok flag must report the failure, so the menubar
			// knows to keep the cached card rather than this fresh fallback.
			payload, readOK, _ := tc.buildCard(h)
			if readOK {
				t.Fatalf("%s builder reported success after a storage read failure: %v", tc.provider, payload)
			}

			after, err := h.BuildMenubarSnapshot()
			if err != nil {
				t.Fatalf("second BuildMenubarSnapshot: %v", err)
			}
			card, ok := menubarCardByID(after, tc.provider)
			if !ok {
				t.Fatalf("%s card vanished after a storage read failure: %+v", tc.provider, after.Providers)
			}
			if card.UpdatedAtTime != before.UpdatedAtTime {
				t.Fatalf("%s updated_at_time = %q, want the original %q", tc.provider, card.UpdatedAtTime, before.UpdatedAtTime)
			}
			if card.UpdatedAtTime == "" {
				t.Fatalf("%s card has no timestamp, so the client cannot show an age", tc.provider)
			}
		})
	}
}

// With no cached card there is nothing honest to show, so the provider must be
// omitted rather than filled with a zero-value card stamped now.
func TestMenubarLastGoodOmitsProviderOnFirstEverReadFailure(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, _, dsn := newSharedMemoryMenubarHandler(t, cfg)

	breakProviderStorage(t, dsn, "cursor_quota_values", "cursor_snapshots")

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(snapshot, "cursor"); ok {
		t.Fatalf("cursor card fabricated on a first-ever read failure: %+v", card)
	}
	if !snapshot.LatestAt.IsZero() {
		t.Fatalf("LatestAt = %s, want zero: no provider supplied real data", snapshot.LatestAt)
	}
}

// Eviction: a provider that is no longer configured must not be served from
// the cache, and its entry must be dropped.
func TestMenubarLastGoodEvictsRemovedProvider(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, _ := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	if _, ok := h.menubarLastGood["cursor"]; !ok {
		t.Fatalf("expected cursor entry in the last-good cache, got %v", h.menubarLastGood)
	}

	h.config.CursorToken = ""

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(snapshot, "cursor"); ok {
		t.Fatalf("disabled provider still served from the cache: %+v", card)
	}
	if _, ok := h.menubarLastGood["cursor"]; ok {
		t.Fatalf("disabled provider entry was not evicted: %v", h.menubarLastGood)
	}
}

// A cached card must not be served for an account the user deleted, and the
// cache is keyed per account for multi-account providers.
func TestMenubarLastGoodEvictsDeletedAccountAndKeepsOthers(t *testing.T) {
	cfg := createTestConfigWithCodex()
	h, s, _ := newSharedMemoryMenubarHandler(t, cfg)

	work, err := s.GetOrCreateProviderAccount("codex", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(work): %v", err)
	}
	personal, err := s.GetOrCreateProviderAccount("codex", "personal")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(personal): %v", err)
	}
	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	for _, accountID := range []int64{work.ID, personal.ID} {
		if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
			CapturedAt: capturedAt,
			AccountID:  accountID,
			PlanType:   "plus",
			Quotas:     []api.CodexQuota{{Name: "seven_day", Utilization: 30}},
		}); err != nil {
			t.Fatalf("InsertCodexSnapshot(%d): %v", accountID, err)
		}
	}

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	workKey := fmt.Sprintf("codex:%d", work.ID)
	personalKey := fmt.Sprintf("codex:%d", personal.ID)
	if _, ok := menubarCardByID(snapshot, workKey); !ok {
		t.Fatalf("expected %s card, got %+v", workKey, snapshot.Providers)
	}
	if _, ok := menubarCardByID(snapshot, personalKey); !ok {
		t.Fatalf("expected %s card, got %+v", personalKey, snapshot.Providers)
	}

	// Soft-delete the personal account; its cached card must go with it.
	if err := s.MarkProviderAccountDeletedByID(personal.ID); err != nil {
		t.Fatalf("MarkProviderAccountDeletedByID: %v", err)
	}

	after, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(after, personalKey); ok {
		t.Fatalf("deleted account still served from the cache: %+v", card)
	}
	if _, ok := menubarCardByID(after, workKey); !ok {
		t.Fatalf("remaining account was evicted too: %+v", after.Providers)
	}
	if _, ok := h.menubarLastGood[personalKey]; ok {
		t.Fatalf("deleted account entry was not evicted: %v", h.menubarLastGood)
	}
	if _, ok := h.menubarLastGood[workKey]; !ok {
		t.Fatalf("remaining account entry was dropped: %v", h.menubarLastGood)
	}
}

// The cache is bounded: one entry per provider card, and repeated failures do
// not accumulate entries.
func TestMenubarLastGoodCacheIsBounded(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}

	breakProviderStorage(t, dsn, "cursor_quota_values", "cursor_snapshots")
	for i := 0; i < 5; i++ {
		if _, err := h.BuildMenubarSnapshot(); err != nil {
			t.Fatalf("BuildMenubarSnapshot %d: %v", i, err)
		}
	}
	if len(h.menubarLastGood) != 1 {
		t.Fatalf("cache holds %d entries after repeated failures, want 1: %v", len(h.menubarLastGood), h.menubarLastGood)
	}
}

// A cached card must survive a storage failure that happens before any client
// ever saw it: the second request is the one that has to fall back.
func TestMenubarLastGoodServedCardHasNoFabricatedTimestamp(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)
	h.tracker = tracker.New(s, nil)

	capturedAt := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	if _, err := s.InsertSnapshot(&api.Snapshot{
		CapturedAt: capturedAt,
		Sub:        api.QuotaInfo{Limit: 100, Requests: 40, RenewsAt: capturedAt.Add(time.Hour)},
		Search:     api.QuotaInfo{Limit: 100, Requests: 20, RenewsAt: capturedAt.Add(time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 100, Requests: 10, RenewsAt: capturedAt.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}

	breakProviderStorage(t, dsn, "quota_snapshots")

	rr := httptest.NewRecorder()
	h.MenubarSummary(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("summary returned %d: %s", rr.Code, rr.Body.String())
	}
	var snapshot menubar.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	card, ok := menubarCardByID(&snapshot, "synthetic")
	if !ok {
		t.Fatalf("synthetic card missing after failure: %+v", snapshot.Providers)
	}
	want := capturedAt.UTC().Format(time.RFC3339)
	if card.UpdatedAtTime != want {
		t.Fatalf("synthetic updated_at_time = %q, want %q", card.UpdatedAtTime, want)
	}
	if snapshot.GeneratedAt.Before(snapshot.LatestAt) {
		t.Fatalf("GeneratedAt %s is before LatestAt %s", snapshot.GeneratedAt, snapshot.LatestAt)
	}
}

// A successful read that simply has no rows yet must not seed the cache with a
// fabricated card. Synthetic's builder stamps the empty fallback with now, so
// caching it would resurrect that zero card after a later real read failure.
func TestMenubarLastGoodDoesNotCacheFabricatedEmptyCard(t *testing.T) {
	cfg := createTestConfigWithSynthetic()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)
	h.tracker = tracker.New(s, nil)

	// No snapshot inserted: the read succeeds and returns nothing.
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	if _, ok := h.menubarLastGood["synthetic"]; ok {
		t.Fatalf("a no-data read seeded the cache: %+v", h.menubarLastGood)
	}

	breakProviderStorage(t, dsn, "quota_snapshots")
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(snapshot, "synthetic"); ok {
		t.Fatalf("synthetic card fabricated after a read failure with no history: %+v", card)
	}
}

// Mistral with no stored history must not seed the cache with its
// connection-only placeholder, so a later read failure cannot resurrect it as
// if it were real quota data.
func TestMenubarLastGoodDoesNotCacheMistralPlaceholder(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"}
	h, _, dsn := newSharedMemoryMenubarHandler(t, cfg)

	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	if _, ok := h.menubarLastGood["mistral"]; ok {
		t.Fatalf("a no-history Mistral read seeded the cache: %+v", h.menubarLastGood)
	}

	breakProviderStorage(t, dsn, "mistral_quota_values", "mistral_snapshots")
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(snapshot, "mistral"); ok {
		t.Fatalf("mistral placeholder served as cached data after a read failure: %+v", card)
	}
}

// insertCodexTestSnapshot stores one Codex snapshot for an account.
func insertCodexTestSnapshot(t *testing.T, s *store.Store, accountID int64, capturedAt time.Time, utilization float64) {
	t.Helper()
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: capturedAt,
		AccountID:  accountID,
		PlanType:   "plus",
		Quotas:     []api.CodexQuota{{Name: "seven_day", Utilization: utilization}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot(%d): %v", accountID, err)
	}
}

// Finding 1: when the account list cannot be read, the known Codex cards must
// keep being served and must not be pruned. Recovery must bring back the real
// accounts, and only a successful enumeration may prune a genuinely deleted
// one. Before the fix, codexUsageAccounts turned the error into an empty list,
// nothing entered attempted, and deferred pruning deleted every cached card.
func TestMenubarLastGoodKeepsCodexAccountsWhenEnumerationFails(t *testing.T) {
	cfg := createTestConfigWithCodex()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	work, err := s.GetOrCreateProviderAccount("codex", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(work): %v", err)
	}
	personal, err := s.GetOrCreateProviderAccount("codex", "personal")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(personal): %v", err)
	}
	capturedAt := time.Now().UTC().Add(-25 * time.Minute).Truncate(time.Second)
	insertCodexTestSnapshot(t, s, work.ID, capturedAt, 30)
	insertCodexTestSnapshot(t, s, personal.ID, capturedAt, 55)

	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	workKey := fmt.Sprintf("codex:%d", work.ID)
	personalKey := fmt.Sprintf("codex:%d", personal.ID)
	workBefore, ok := menubarCardByID(first, workKey)
	if !ok {
		t.Fatalf("expected %s card, got %+v", workKey, first.Providers)
	}
	personalBefore, ok := menubarCardByID(first, personalKey)
	if !ok {
		t.Fatalf("expected %s card, got %+v", personalKey, first.Providers)
	}

	// Enumeration fails; the snapshot tables stay readable.
	execSQLOnDSN(t, dsn, "ALTER TABLE provider_accounts RENAME TO provider_accounts_broken")

	failed, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	workCard, ok := menubarCardByID(failed, workKey)
	if !ok {
		t.Fatalf("%s vanished when account enumeration failed: %+v", workKey, failed.Providers)
	}
	if workCard.UpdatedAtTime != workBefore.UpdatedAtTime {
		t.Fatalf("%s updated_at_time = %q, want the cached %q", workKey, workCard.UpdatedAtTime, workBefore.UpdatedAtTime)
	}
	personalCard, ok := menubarCardByID(failed, personalKey)
	if !ok {
		t.Fatalf("%s vanished when account enumeration failed: %+v", personalKey, failed.Providers)
	}
	if personalCard.UpdatedAtTime != personalBefore.UpdatedAtTime {
		t.Fatalf("%s updated_at_time = %q, want the cached %q", personalKey, personalCard.UpdatedAtTime, personalBefore.UpdatedAtTime)
	}
	if _, ok := h.menubarLastGood[workKey]; !ok {
		t.Fatalf("enumeration failure pruned %s: %v", workKey, h.menubarLastGood)
	}

	// Recovery: the list reads again.
	execSQLOnDSN(t, dsn, "ALTER TABLE provider_accounts_broken RENAME TO provider_accounts")
	recovered, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("third BuildMenubarSnapshot: %v", err)
	}
	if _, ok := menubarCardByID(recovered, workKey); !ok {
		t.Fatalf("%s missing after recovery: %+v", workKey, recovered.Providers)
	}
	if _, ok := menubarCardByID(recovered, personalKey); !ok {
		t.Fatalf("%s missing after recovery: %+v", personalKey, recovered.Providers)
	}

	// A genuine deletion after a successful enumeration is still pruned.
	if err := s.MarkProviderAccountDeletedByID(personal.ID); err != nil {
		t.Fatalf("MarkProviderAccountDeletedByID: %v", err)
	}
	deleted, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("fourth BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(deleted, personalKey); ok {
		t.Fatalf("deleted account still served: %+v", card)
	}
	if _, ok := h.menubarLastGood[personalKey]; ok {
		t.Fatalf("deleted account was not pruned after a successful enumeration: %v", h.menubarLastGood)
	}
	if _, ok := menubarCardByID(deleted, workKey); !ok {
		t.Fatalf("%s was pruned along with the deleted account: %+v", workKey, deleted.Providers)
	}
}

// Finding 1 (MiniMax half of finding 1): the same rule applies to MiniMax.
func TestMenubarLastGoodKeepsMiniMaxAccountsWhenEnumerationFails(t *testing.T) {
	cfg := &config.Config{MiniMaxAPIKey: "minimax_test_key", PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"}
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	account, err := s.GetOrCreateProviderAccount("minimax", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	capturedAt := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Second)
	if _, err := s.InsertMiniMaxSnapshot(&api.MiniMaxSnapshot{
		CapturedAt: capturedAt,
		Models: []api.MiniMaxModelQuota{
			{ModelName: "general", Used: 10, Total: 100, Remain: 90, UsedPercent: 10},
		},
	}, account.ID); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	key := fmt.Sprintf("minimax:%d", account.ID)
	before, ok := menubarCardByID(first, key)
	if !ok {
		t.Fatalf("expected %s card, got %+v", key, first.Providers)
	}

	execSQLOnDSN(t, dsn, "ALTER TABLE provider_accounts RENAME TO provider_accounts_broken")

	after, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(after, key)
	if !ok {
		t.Fatalf("%s vanished when account enumeration failed: %+v", key, after.Providers)
	}
	if card.UpdatedAtTime != before.UpdatedAtTime {
		t.Fatalf("%s updated_at_time = %q, want the cached %q", key, card.UpdatedAtTime, before.UpdatedAtTime)
	}
}

// Finding 2: the outcome must come from the read that builds the payload, not
// from a separate probe. A broken payload read must report failure, and a
// successful read backed by stored rows must report success. The old probe ran
// QueryLatestCursor and the builder then re-read the same data inside
// buildCursorCurrent, so the two could disagree.
func TestMenubarLastGoodUsesBuilderReadOutcome(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	payload, readOK, hasData := h.buildCursorCurrentOK()
	if !readOK || !hasData {
		t.Fatalf("builder outcome = (readOK=%v, hasData=%v), want both true for stored data: %v", readOK, hasData, payload)
	}

	// Break the table the builder's payload read depends on. The outcome must
	// follow that actual read.
	execSQLOnDSN(t, dsn, "ALTER TABLE cursor_snapshots RENAME TO cursor_snapshots_broken")

	payload, readOK, _ = h.buildCursorCurrentOK()
	if readOK {
		t.Fatalf("builder reported success after its payload read failed: %v", payload)
	}
}

// Finding 2 for a range-fallback provider: when the latest snapshot has no
// quotas and the 7-day range read fails, the builder must report failure
// instead of treating the empty result as a successful no-data read.
func TestMenubarLastGoodSeesGrokFallbackReadFailure(t *testing.T) {
	cfg := &config.Config{GrokToken: "grok_test_token", PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"}
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
		CapturedAt: capturedAt,
		Quotas:     []api.GrokQuota{{Name: "credits", Utilization: 20, Status: "healthy"}},
	}); err != nil {
		t.Fatalf("InsertGrokSnapshot: %v", err)
	}
	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	before, ok := menubarCardByID(first, "grok")
	if !ok {
		t.Fatalf("expected grok card, got %+v", first.Providers)
	}

	// An empty newest snapshot forces the range fallback; breaking the quota
	// value table makes that fallback read fail.
	insertEmptyGrokSnapshot(t, s)
	execSQLOnDSN(t, dsn, "ALTER TABLE grok_quota_values RENAME TO grok_quota_values_broken")

	after, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(after, "grok")
	if !ok {
		t.Fatalf("grok card vanished after its fallback read failed: %+v", after.Providers)
	}
	if card.UpdatedAtTime != before.UpdatedAtTime {
		t.Fatalf("grok updated_at_time = %q, want the cached %q", card.UpdatedAtTime, before.UpdatedAtTime)
	}
}

func insertEmptyGrokSnapshot(t *testing.T, s *store.Store) {
	t.Helper()
	if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas:     []api.GrokQuota{},
	}); err != nil {
		t.Fatalf("InsertGrokSnapshot(empty): %v", err)
	}
}

// Finding 3: real data, then a successful empty read, then a storage error.
// The successful empty read means the history is genuinely gone, so the old
// card must be evicted and must NOT come back on the later failure.
func TestMenubarLastGoodDoesNotResurrectAfterSuccessfulEmptyRead(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	if _, ok := menubarCardByID(first, "cursor"); !ok {
		t.Fatalf("expected cursor card, got %+v", first.Providers)
	}
	if _, ok := h.menubarLastGood["cursor"]; !ok {
		t.Fatalf("expected cursor in the last-good cache")
	}

	// Clear the history: the read now succeeds with no rows.
	execSQLOnDSN(t, dsn, "DELETE FROM cursor_quota_values", "DELETE FROM cursor_snapshots")
	empty, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(empty, "cursor"); ok {
		t.Fatalf("cursor card shown after a successful empty read: %+v", card)
	}
	if _, ok := h.menubarLastGood["cursor"]; ok {
		t.Fatalf("successful empty read did not evict the cached card: %v", h.menubarLastGood)
	}
	if !empty.LatestAt.IsZero() {
		t.Fatalf("LatestAt = %s after the only provider's data was cleared, want zero", empty.LatestAt)
	}

	// A later storage failure must not bring the old card back.
	execSQLOnDSN(t, dsn, "ALTER TABLE cursor_snapshots RENAME TO cursor_snapshots_broken")
	failed, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("third BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(failed, "cursor"); ok {
		t.Fatalf("cleared card came back after a storage failure: %+v", card)
	}
}

// Finding 3: the same eviction gap for Mistral's connection-only placeholder.
func TestMenubarLastGoodMistralPlaceholderDoesNotResurrect(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"}
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	if err := s.SaveMistral(t.Context(), &api.MistralSnapshot{
		Identity:   "test",
		CapturedAt: capturedAt,
		Status:     "ok",
		Quotas: []api.MistralQuota{
			{Name: "api_included", Used: 30, Limit: 100, Currency: "EUR", Utilization: 30, CapturedAt: capturedAt},
			{Name: "vibe_included", Used: 50, Limit: 100, Currency: "EUR", Utilization: 50, CapturedAt: capturedAt},
		},
	}); err != nil {
		t.Fatalf("SaveMistral: %v", err)
	}
	first, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("first BuildMenubarSnapshot: %v", err)
	}
	before, ok := menubarCardByID(first, "mistral")
	if !ok {
		t.Fatalf("expected mistral card, got %+v", first.Providers)
	}

	// Clear the history: the read succeeds, Mistral shows only its
	// connection-only placeholder, and the cached quota card must go.
	execSQLOnDSN(t, dsn, "DELETE FROM mistral_quota_values", "DELETE FROM mistral_snapshots")
	empty, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("second BuildMenubarSnapshot: %v", err)
	}
	if card, ok := menubarCardByID(empty, "mistral"); ok && card.UpdatedAtTime != "" {
		t.Fatalf("mistral placeholder fabricated a data time after a successful empty read: %+v", card)
	}
	if _, ok := h.menubarLastGood["mistral"]; ok {
		t.Fatalf("successful empty read did not evict the cached Mistral card: %v", h.menubarLastGood)
	}

	// The later storage failure must not resurrect the cleared quota card. With
	// nothing cached the provider may be omitted, or show Mistral's timestamp-
	// free connection placeholder; it must never come back with the old data.
	execSQLOnDSN(t, dsn, "ALTER TABLE mistral_snapshots RENAME TO mistral_snapshots_broken")
	failed, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("third BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(failed, "mistral")
	if !ok {
		if !failed.LatestAt.IsZero() {
			t.Fatalf("LatestAt = %s with no provider data", failed.LatestAt)
		}
		return
	}
	if len(card.Quotas) != 0 || card.UpdatedAtTime != "" {
		t.Fatalf("cleared Mistral card came back after a storage failure: %+v", card)
	}
	if card.UpdatedAtTime == before.UpdatedAtTime {
		t.Fatal("cleared Mistral card kept its old timestamp")
	}
}

// Finding 3: a provider that is configured but has no stored history must not
// produce a fabricated card or a non-zero LatestAt. Synthetic stamped its empty
// fallback with time.Now(), which produced a fake-fresh zero card.
func TestMenubarLastGoodConfiguredNoHistoryHasNoFabricatedCard(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      *config.Config
		provider string
	}{
		{name: "synthetic", cfg: createTestConfigWithSynthetic(), provider: "synthetic"},
		{name: "zai", cfg: createTestConfigWithZai(), provider: "zai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newSharedMemoryMenubarHandler(t, tc.cfg)
			if tc.provider == "synthetic" {
				h.tracker = tracker.New(h.store, nil)
			}

			snapshot, err := h.BuildMenubarSnapshot()
			if err != nil {
				t.Fatalf("BuildMenubarSnapshot: %v", err)
			}
			if card, ok := menubarCardByID(snapshot, tc.provider); ok {
				t.Fatalf("%s card fabricated with no stored history: %+v", tc.provider, card)
			}
			if !snapshot.LatestAt.IsZero() {
				t.Fatalf("LatestAt = %s with no stored history, want zero", snapshot.LatestAt)
			}
		})
	}
}

// Finding 8: severity comes from usage alone. A healthy but very old card keeps
// its usage-based severity; the age label carries staleness. Connection states
// that need action (paused, reconnect) still floor at warning.
func TestMenubarStalenessDoesNotRaiseSeverity(t *testing.T) {
	cfg := createTestConfigWithCursor()
	h, s, _ := newSharedMemoryMenubarHandler(t, cfg)

	capturedAt := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 20, Limit: 100, Utilization: 20, Format: api.CursorFormatPercent},
	})

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card, ok := menubarCardByID(snapshot, "cursor")
	if !ok {
		t.Fatalf("expected cursor card, got %+v", snapshot.Providers)
	}
	if card.Status != "healthy" {
		t.Fatalf("stale 20%% card status = %q, want healthy (age alone must not raise severity)", card.Status)
	}
	if card.UpdatedAtTime == "" {
		t.Fatal("stale card carries no age label source; staleness must still be visible there")
	}

	// A stale quota flagged by the payload must likewise not raise severity.
	payload := map[string]interface{}{
		"capturedAt": capturedAt.Format(time.RFC3339),
		"quotas": []interface{}{map[string]interface{}{
			"name":        "total_usage",
			"utilization": 20.0,
			"isStale":     true,
		}},
	}
	normalized := normalizeProviderCard("cursor", "Cursor", "", payload, 70, 90)
	if normalized == nil {
		t.Fatal("normalizeProviderCard returned nil for a stale quota")
	}
	if normalized.Status != "healthy" {
		t.Fatalf("normalizeProviderCard status = %q for a stale 20%% card, want healthy", normalized.Status)
	}
}
