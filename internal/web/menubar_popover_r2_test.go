package web

import (
	"strings"
	"testing"
)

// A version-1 cache entry can be valid JSON and still be structurally corrupt:
// providers:[null] passes the shallow "providers is an array" check, then the
// first sort dereferences item.id and throws before either fetch starts. The
// reader must validate the nested card/quota shapes the renderer needs and
// clear any entry that fails.
func TestMenubarCorruptCacheValidation(t *testing.T) {
	html := readMenubarHTML(t)

	slice := menubarSlice(t, html, "function isRenderableProviderCard(", "function resolveThemeMode(")

	script := `
const assert = require('node:assert/strict');
const SNAPSHOT_CACHE_KEY = "menubar_snapshot_cache";
const SNAPSHOT_CACHE_VERSION = 1;
const backing = new Map();
const localStorage = {
  getItem: (key) => (backing.has(key) ? backing.get(key) : null),
  setItem: (key, value) => { backing.set(key, String(value)); },
  removeItem: (key) => { backing.delete(key); }
};
const state = { snapshot: null, loading: true, error: "" };
` + slice + `
function put(snapshot) {
  backing.set(SNAPSHOT_CACHE_KEY, JSON.stringify({ version: SNAPSHOT_CACHE_VERSION, snapshot }));
}
function rejects(snapshot, label) {
  put(snapshot);
  assert.equal(readCachedSnapshot(), null, label + " must be rejected");
  assert.ok(!backing.has(SNAPSHOT_CACHE_KEY), label + " must be cleared");
}

// Valid-JSON entries with the wrong nested shape.
rejects({ providers: [null] }, "providers:[null]");
rejects({ providers: [{ id: "" , quotas: [] }] }, "an empty provider id");
rejects({ providers: [{ id: "codex" }] }, "a provider without quotas");
rejects({ providers: [{ id: "codex", quotas: null }] }, "null quotas");
rejects({ providers: [{ id: "codex", quotas: [null] }] }, "a null quota");
rejects({ providers: ["codex"] }, "a string provider");
rejects({ providers: "codex" }, "a non-array providers field");

// A provider with no quotas is legitimate and must still restore.
put({ providers: [{ id: "codex", quotas: [] }] });
const empty = readCachedSnapshot();
assert.ok(empty, "a provider with an empty quota list must restore");
assert.equal(empty.providers[0].id, "codex");

// Quota objects are accepted.
put({ providers: [{ id: "codex", quotas: [{ key: "weekly", label: "Weekly", percent: 40 }] }] });
const full = readCachedSnapshot();
assert.ok(full, "a provider with quota objects must restore");
assert.equal(full.providers[0].quotas[0].percent, 40);
`
	runMenubarNode(t, script)
}

// Even a cache that passes validation must not be able to wedge the popover:
// the cached first paint is guarded, so a render exception clears the entry,
// resets to the empty state and lets initialization fetch normally.
func TestMenubarCorruptCacheRecovery(t *testing.T) {
	html := readMenubarHTML(t)

	initSlice := menubarSlice(t, html, "async function init()", "async function loadPreferences(")
	cacheSlice := menubarSlice(t, html, "function isRenderableProviderCard(", "function resolveThemeMode(")

	// init must paint through the guarded helper and still fetch.
	if !strings.Contains(initSlice, "restoreCachedSnapshotSafely()") {
		t.Fatal("init must render the cached snapshot through the guarded helper")
	}
	if !strings.Contains(initSlice, "loadSnapshot()") {
		t.Fatal("init must still fetch after a failed cached render")
	}
	if strings.Index(initSlice, "restoreCachedSnapshotSafely()") > strings.Index(initSlice, "loadSnapshot()") {
		t.Fatal("init must attempt the cached paint before fetching")
	}

	script := `
const assert = require('node:assert/strict');
const SNAPSHOT_CACHE_KEY = "menubar_snapshot_cache";
const SNAPSHOT_CACHE_VERSION = 1;
const backing = new Map();
const localStorage = {
  getItem: (key) => (backing.has(key) ? backing.get(key) : null),
  setItem: (key, value) => { backing.set(key, String(value)); },
  removeItem: (key) => { backing.delete(key); }
};
const state = { snapshot: null, loading: true, error: "" };
let renders = 0;
let renderThrows = 0;
function render() {
  renders += 1;
  if (renderThrows > 0) {
    renderThrows -= 1;
    throw new Error("render failed");
  }
}
function applyTheme() {}
let fetches = 0;
async function loadPreferences() {}
async function loadSnapshot() { fetches += 1; }
` + cacheSlice + initSlice + `
(async () => {
  // Case 1: structurally corrupt but valid JSON. The reader clears it and
  // init continues to fetch.
  backing.set(SNAPSHOT_CACHE_KEY, JSON.stringify({
    version: SNAPSHOT_CACHE_VERSION,
    snapshot: { providers: [null] }
  }));
  await init();
  assert.equal(fetches, 1, "init must fetch even when the cache is corrupt");
  assert.ok(!backing.has(SNAPSHOT_CACHE_KEY), "the corrupt entry must be cleared");
  assert.equal(state.snapshot, null, "a corrupt entry must not become the snapshot");

  // Case 2: a cache that passes validation but whose first render throws. The
  // entry is cleared, state returns to the empty loading view, and init still
  // fetches.
  fetches = 0;
  renders = 0;
  renderThrows = 1;
  backing.set(SNAPSHOT_CACHE_KEY, JSON.stringify({
    version: SNAPSHOT_CACHE_VERSION,
    snapshot: { providers: [{ id: "codex", quotas: [] }] }
  }));
  await init();
  assert.ok(!backing.has(SNAPSHOT_CACHE_KEY), "an unrenderable entry must be cleared");
  assert.equal(state.snapshot, null, "the failed cached render must be discarded");
  assert.equal(state.loading, true, "the popover must fall back to the loading state");
  assert.equal(fetches, 1, "init must fetch after a failed cached render");
  assert.ok(renders >= 2, "the empty state must be repainted after the failure");
})().catch((error) => { console.error(error); process.exit(1); });
`
	runMenubarNode(t, script)
}

// Manual, periodic, native-show and recovery refreshes overlap. A response
// that completes out of order must not replace newer live or persisted state.
func TestMenubarSnapshotResponseOrdering(t *testing.T) {
	html := readMenubarHTML(t)

	slice := menubarSlice(t, html, "let snapshotGeneration = 0;", "async function refreshData(")

	script := `
const assert = require('node:assert/strict');
const API_BASE = "";
const state = { snapshot: null, loading: true, error: "" };
const saved = [];
function writeCachedSnapshot(snapshot) { saved.push(snapshot); }
function render() {}
` + slice + `
const pending = [];
global.fetch = () => new Promise((resolve) => { pending.push(resolve); });
const respond = (index, snapshot) => pending[index]({ ok: true, json: async () => snapshot });

(async () => {
  const older = {
    generated_at: "2026-01-01T00:00:00Z",
    providers: [{ id: "codex", quotas: [], value: "old" }]
  };
  const newer = {
    generated_at: "2026-01-01T00:01:00Z",
    providers: [{ id: "codex", quotas: [], value: "new" }]
  };

  // Reversed completion: the second request finishes first with newer data,
  // then the first finishes with older data.
  const first = loadSnapshot();
  const second = loadSnapshot();
  assert.equal(pending.length, 2, "both refreshes must be in flight");
  respond(1, newer);
  await second;
  assert.equal(state.snapshot.providers[0].value, "new", "the newer response must apply");
  assert.equal(saved.length, 1, "the newer response must be persisted");
  respond(0, older);
  await first;
  assert.equal(state.snapshot.providers[0].value, "new", "an older response must not roll live state back");
  assert.equal(saved.length, 1, "an older response must not be persisted");
  assert.equal(saved[0].providers[0].value, "new");

  // A later request whose payload was built earlier must also be rejected.
  const stale = loadSnapshot();
  respond(2, {
    generated_at: "2026-01-01T00:00:30Z",
    providers: [{ id: "codex", quotas: [], value: "time-travel" }]
  });
  await stale;
  assert.equal(state.snapshot.providers[0].value, "new", "an older build must not replace a newer one");
  assert.equal(saved.length, 1, "an older build must not be persisted");

  // A genuinely newer build still applies and persists.
  const freshest = loadSnapshot();
  respond(3, {
    generated_at: "2026-01-01T00:02:00Z",
    providers: [{ id: "codex", quotas: [], value: "newest" }]
  });
  await freshest;
  assert.equal(state.snapshot.providers[0].value, "newest");
  assert.equal(saved.length, 2);
  assert.equal(saved[1].providers[0].value, "newest");

  // A late failure from a superseded request must not surface an error over
  // the newer data: the next two requests overlap, the newer one succeeds
  // first, then the older one fails.
  const failing = loadSnapshot();
  const succeeding = loadSnapshot();
  respond(5, {
    generated_at: "2026-01-01T00:03:00Z",
    providers: [{ id: "codex", quotas: [], value: "newest" }]
  });
  await succeeding;
  pending[4]({ ok: false, status: 503 });
  await failing;
  assert.equal(state.error, "", "a superseded failure must not set the error");
})().catch((error) => { console.error(error); process.exit(1); });
`
	runMenubarNode(t, script)
}

// The attention state must not replace the dot's severity fill. A paused
// account at 100% still reads red, with attention carried by a ring. A card
// that is merely healthy but stale gets no attention treatment at all.
func TestMenubarAttentionDotPreservesSeverity(t *testing.T) {
	html := readMenubarHTML(t)

	// Structurally, the attention rule must not set a background; severity
	// classes own the fill. It carries an amber ring plus halo.
	rule := menubarSlice(t, html, ".provider-status-dot.status-attention {", "}")
	if strings.Contains(rule, "background:") {
		t.Fatalf("the attention rule must not override the severity background:\n%s", rule)
	}
	if !strings.Contains(rule, "box-shadow") {
		t.Fatalf("the attention rule must add a ring:\n%s", rule)
	}
	if !strings.Contains(rule, "var(--attention)") || !strings.Contains(rule, "var(--attention-halo)") {
		t.Fatalf("the attention ring must use the themed attention variables:\n%s", rule)
	}

	slice := menubarSlice(t, html, "function renderStandardCard(", "function renderStateCard(")

	script := `
const assert = require('node:assert/strict');
` + slice + `
function escapeHTML(value) { return String(value == null ? "" : value); }
function providerIconClass() { return "provider-icon-codex"; }
function providerAgeLabel() { return "5m"; }
function clampPercent(value) { return Math.max(0, Math.min(100, Number(value || 0))); }
function providerQuotas(provider) { return Array.isArray(provider && provider.quotas) ? provider.quotas : []; }
function usageSummary() { return ""; }
function quotaStatusNote() { return ""; }
function fullQuotaLabel(quota) { return String(quota.label || ""); }
function compactResetLabel() { return "1h"; }
function ringColumns() { return 2; }
const mistralRecovery = { markup: () => "" };

const base = {
  label: "Codex",
  id: "codex",
  base_provider: "codex",
  status: "critical",
  highest_percent: 100,
  updated_at_time: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
  quotas: [{ key: "weekly", label: "Weekly", percent: 100, status: "critical" }]
};

// Critical plus paused: the dot keeps its critical class (red fill) and adds
// the attention ring.
const paused = Object.assign({}, base, { connection_status: "paused" });
const pausedHeader = standardHeaderMarkup(paused);
const pausedDot = pausedHeader.match(/class="provider-status-dot [^"]*"/)[0];
assert.ok(pausedDot.includes("status-critical"), "a paused critical card must keep its critical severity");
assert.ok(pausedDot.includes("status-attention"), "a paused card must carry the attention ring");
assert.ok(pausedHeader.includes("provider-status-tip"), "the tooltip stays");

// Critical without an action keeps the plain severity dot.
const hot = Object.assign({}, base, { connection_status: "" });
const hotHeader = standardHeaderMarkup(hot);
assert.ok(hotHeader.includes("status-critical"));
assert.ok(!hotHeader.includes("status-attention"));

// Healthy but stale: no attention treatment and no severity change.
const stale = Object.assign({}, base, {
  status: "healthy",
  highest_percent: 20,
  connection_status: "stale",
  quotas: [{ key: "weekly", label: "Weekly", percent: 20, status: "healthy" }]
});
const staleHeader = standardHeaderMarkup(stale);
assert.ok(staleHeader.includes("status-healthy"));
assert.ok(!staleHeader.includes("status-attention"), "staleness alone must not add the attention ring");
assert.ok(!staleHeader.includes("quota-source-tag"));
assert.ok(!staleHeader.includes("status-critical"), "staleness alone must not change severity");
`
	runMenubarNode(t, script)
}
