package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// readMenubarHTML loads the popover page and fails the test when it is missing.
func readMenubarHTML(t *testing.T) string {
	t.Helper()
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(page)
}

// menubarSlice returns the HTML between two anchors, failing when either is
// missing. The node-driven tests run the page's own JS, so the anchors must
// stay stable.
func menubarSlice(t *testing.T, html, start, end string) string {
	t.Helper()
	startIndex := strings.Index(html, start)
	if startIndex < 0 {
		t.Fatalf("menubar.html is missing %q", start)
	}
	endIndex := strings.Index(html[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("menubar.html is missing %q after %q", end, start)
	}
	return html[startIndex : startIndex+endIndex]
}

// runMenubarNode executes the given JS with node, skipping when node is absent.
func runMenubarNode(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for menubar hover/cache test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// A card that needs the user to act (re-auth, reconnect, browser access) must
// not print an inline tag next to the provider name. The reason and the action
// live on the status dot's custom tooltip, and the dot carries a distinct
// attention state. The age label alone shows staleness, so a stale card gets
// no tag and no attention state.
func TestMenubarProviderAttentionDotAndTooltip(t *testing.T) {
	html := readMenubarHTML(t)

	// The inline tags are gone from the source, including the stale one.
	for _, banned := range []string{
		"Updates unavailable",
		`quota-source-tag">Reconnect required`,
		`quota-source-tag">Re-auth required`,
		`quota-source-tag">Browser access required`,
	} {
		if strings.Contains(html, banned) {
			t.Fatalf("menubar.html still renders the inline tag %q", banned)
		}
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
  status: "warning",
  highest_percent: 42,
  updated_at_time: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
  quotas: [{ key: "weekly", label: "Weekly", percent: 42, status: "warning" }]
};

// Paused: attention dot, tooltip carries the reason and the action.
const paused = Object.assign({}, base, { connection_status: "paused" });
const pausedHeader = standardHeaderMarkup(paused);
assert.ok(pausedHeader.includes("status-attention"), "paused card must use the attention dot");
assert.ok(!pausedHeader.includes("quota-source-tag"), "paused card must not render an inline tag");
assert.ok(pausedHeader.includes("Codex auth failed"), "paused Codex tooltip must name the fix");
assert.ok(pausedHeader.includes("provider-status-tip"), "paused card must render the custom tooltip");
assert.ok(pausedHeader.includes("aria-label="), "the dot must keep an aria-label");

// Reconnect.
const reconnect = Object.assign({}, base, { connection_status: "reconnect" });
const reconnectHeader = standardHeaderMarkup(reconnect);
assert.ok(reconnectHeader.includes("status-attention"));
assert.ok(!reconnectHeader.includes("quota-source-tag"));
assert.ok(reconnectHeader.includes("Reconnect required"));

// Browser access denied.
const denied = Object.assign({}, base, { connection_status: "warning", connection: { reason: "browser_access_denied" } });
const deniedHeader = standardHeaderMarkup(denied);
assert.ok(deniedHeader.includes("status-attention"));
assert.ok(!deniedHeader.includes("quota-source-tag"));
assert.ok(deniedHeader.includes("Browser access required"));

// Healthy: no attention state, still a dot.
const healthy = Object.assign({}, base, { status: "healthy", connection_status: "" });
const healthyHeader = standardHeaderMarkup(healthy);
assert.ok(!healthyHeader.includes("status-attention"));
assert.ok(healthyHeader.includes("provider-status-dot"));

// Stale: the age label already covers it. No tag, no attention state.
const stale = Object.assign({}, base, { connection_status: "stale" });
const staleHeader = standardHeaderMarkup(stale);
assert.ok(!staleHeader.includes("status-attention"));
assert.ok(!staleHeader.includes("quota-source-tag"));
assert.ok(!staleHeader.includes("Updates unavailable"));

// The shared header (minimal and detailed views) shows the same dot and never
// an inline tag.
const sharedHeader = providerHeaderMarkup(paused);
assert.ok(sharedHeader.includes("provider-status-dot"));
assert.ok(sharedHeader.includes("status-attention"));
assert.ok(!sharedHeader.includes("quota-source-tag"));

// The attention class rides on top of the severity class rather than
// replacing it, so the dot keeps its usage-severity meaning.
const pausedDot = pausedHeader.match(/class="provider-status-dot [^"]*"/)[0];
assert.ok(pausedDot.includes("status-warning"), "attention must not drop the severity class");

// A high-usage card that needs no action keeps the plain severity dot and no
// action text in its tooltip.
const hot = Object.assign({}, base, { status: "critical", connection_status: "", highest_percent: 97 });
const hotHeader = standardHeaderMarkup(hot);
assert.ok(!hotHeader.includes("status-attention"));
assert.ok(hotHeader.includes("status-critical"));
assert.ok(!hotHeader.includes("auth failed"));

// Card numbers stay visible in every state.
const card = renderStandardCard(paused, 2);
assert.ok(card.includes("quota-tile"), "paused card must still render its quota numbers");
assert.ok(card.includes("quota-ring-pct"), "paused card must still render its percentages");
assert.ok(card.includes("Weekly"), "paused card must still render its quota labels");
assert.ok(!card.includes('class="hidden"'), "paused card must not hide its numbers");
assert.ok(!card.includes("display:none"), "paused card must not hide its numbers");
`
	runMenubarNode(t, script)
}

// The attention dot and the tooltip must be defined for both themes: the
// variables the rules read have to exist under the default (dark) root and
// under the light override, and the tooltip needs a light-surface shadow.
func TestMenubarAttentionDotThemeCoverage(t *testing.T) {
	html := readMenubarHTML(t)

	darkRoot := menubarSlice(t, html, ":root {", ":root[data-theme=\"light\"] {")
	lightRoot := menubarSlice(t, html, ":root[data-theme=\"light\"] {", "* {")

	for _, variable := range []string{"--attention:", "--attention-halo:"} {
		if !strings.Contains(darkRoot, variable) {
			t.Errorf("dark theme is missing %s", variable)
		}
		if !strings.Contains(lightRoot, variable) {
			t.Errorf("light theme is missing %s", variable)
		}
	}

	if !strings.Contains(html, ".provider-status-dot.status-attention") {
		t.Error("the attention dot rule is missing")
	}
	if !strings.Contains(html, ".provider-status-tip") {
		t.Error("the tooltip rule is missing")
	}
	if !strings.Contains(html, ":root[data-theme=\"light\"] .provider-status-tip") {
		t.Error("the tooltip has no light-theme rule")
	}
}

// The snapshot cache is one versioned localStorage entry: written on every
// successful fetch, read on load for an instant first paint, and cleared when
// corrupt or written by a different version.
func TestMenubarSnapshotCacheRoundTrip(t *testing.T) {
	html := readMenubarHTML(t)

	if !strings.Contains(html, "SNAPSHOT_CACHE_KEY") || !strings.Contains(html, "SNAPSHOT_CACHE_VERSION") {
		t.Fatal("menubar.html must define a versioned snapshot cache key")
	}

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
const state = { snapshot: null, loading: true };
` + slice + `
const snapshot = { generated_at: "2026-01-01T00:00:00Z", providers: [{ id: "codex", quotas: [] }] };

// Round trip.
writeCachedSnapshot(snapshot);
assert.deepEqual(readCachedSnapshot(), snapshot);
assert.equal(backing.size, 1, "the cache must be a single overwritten entry");

// Overwrite, never append.
writeCachedSnapshot({ providers: [{ id: "anthropic", quotas: [] }] });
assert.equal(backing.size, 1);
assert.equal(readCachedSnapshot().providers[0].id, "anthropic");

// Corrupt entry is ignored and cleared.
backing.set(SNAPSHOT_CACHE_KEY, "{not json");
assert.equal(readCachedSnapshot(), null);
assert.ok(!backing.has(SNAPSHOT_CACHE_KEY), "a corrupt entry must be cleared");

// Wrong version is ignored and cleared.
backing.set(SNAPSHOT_CACHE_KEY, JSON.stringify({ version: SNAPSHOT_CACHE_VERSION + 1, snapshot: snapshot }));
assert.equal(readCachedSnapshot(), null);
assert.ok(!backing.has(SNAPSHOT_CACHE_KEY), "a wrong-version entry must be cleared");

// A payload without a providers array is ignored and cleared.
backing.set(SNAPSHOT_CACHE_KEY, JSON.stringify({ version: SNAPSHOT_CACHE_VERSION, snapshot: { generated_at: "x" } }));
assert.equal(readCachedSnapshot(), null);
assert.ok(!backing.has(SNAPSHOT_CACHE_KEY));

// Nothing stored.
assert.equal(readCachedSnapshot(), null);

// A cached snapshot is adopted only when nothing newer is in memory.
writeCachedSnapshot(snapshot);
restoreCachedSnapshot();
assert.deepEqual(state.snapshot, snapshot);
assert.equal(state.loading, false, "a cached render must clear the loading state");

// Never render a cached snapshot over a newer in-memory one.
state.snapshot = { providers: [{ id: "fresh", quotas: [] }] };
restoreCachedSnapshot();
assert.equal(state.snapshot.providers[0].id, "fresh");

// Writing junk must not touch the cache.
const before = backing.get(SNAPSHOT_CACHE_KEY);
writeCachedSnapshot(null);
writeCachedSnapshot({});
assert.equal(backing.get(SNAPSHOT_CACHE_KEY), before);
`
	runMenubarNode(t, script)
}

// A successful fetch replaces the cache; a failed one leaves the last good
// snapshot in place so the next load still paints instantly.
func TestMenubarSnapshotCacheSavedOnlyOnSuccess(t *testing.T) {
	html := readMenubarHTML(t)

	// init restores the cache before the first fetch.
	initStart := strings.Index(html, "async function init()")
	if initStart < 0 {
		t.Fatal("menubar.html is missing init()")
	}
	initEnd := strings.Index(html[initStart:], "async function loadPreferences(")
	if initEnd < 0 {
		t.Fatal("menubar.html is missing loadPreferences()")
	}
	initBody := html[initStart : initStart+initEnd]
	// The guarded starter clears an entry whose first render throws, so init
	// always reaches the fetch. It calls restoreCachedSnapshot internally.
	if !strings.Contains(initBody, "restoreCachedSnapshotSafely()") {
		t.Fatal("init must restore the cached snapshot before fetching")
	}
	if strings.Index(initBody, "restoreCachedSnapshotSafely()") > strings.Index(initBody, "loadSnapshot()") {
		t.Fatal("init must restore the cached snapshot before loadSnapshot()")
	}

	slice := menubarSlice(t, html, "let snapshotGeneration = 0;", "async function refreshData(")

	script := `
const assert = require('node:assert/strict');
const API_BASE = "";
const state = { snapshot: null, loading: true, error: "" };
const saved = [];
function writeCachedSnapshot(snapshot) { saved.push(snapshot); }
function render() {}
` + slice + `
(async () => {
  // Success writes the cache.
  global.fetch = async () => ({ ok: true, json: async () => ({ providers: [{ id: "codex" }] }) });
  await loadSnapshot();
  assert.equal(saved.length, 1, "a successful fetch must write the cache");
  assert.equal(saved[0].providers[0].id, "codex");

  // Failure must not overwrite the last good snapshot.
  state.snapshot = null;
  global.fetch = async () => { throw new Error("offline"); };
  await loadSnapshot();
  assert.equal(saved.length, 1, "a failed fetch must not overwrite the cache");
  assert.ok(state.error, "a failed fetch must record the error");

  // A non-OK response is a failure too.
  global.fetch = async () => ({ ok: false, status: 503 });
  await loadSnapshot();
  assert.equal(saved.length, 1, "a 503 must not overwrite the cache");
})().catch((error) => { console.error(error); process.exit(1); });
`
	runMenubarNode(t, script)
}

// init() runs before the first fetch and its first render, so every binding
// the render and snapshot-restore paths read must already be initialized.
// Calling init() from the middle of the IIFE (above the snapshot generation
// counters) threw a temporal-dead-zone ReferenceError and left the popover
// stuck on "Loading" with no provider cards.
func TestMenubarInitRunsAfterTopLevelBindings(t *testing.T) {
	html := readMenubarHTML(t)

	initCall := strings.Index(html, "\n            init();")
	if initCall < 0 {
		t.Fatal("menubar.html is missing the top-level init() call")
	}
	for _, declaration := range []string{
		"let snapshotGeneration = 0;",
		"let appliedSnapshotGeneration = 0;",
		"let appliedSnapshotTime = null;",
		"let resizeReportDelivered = false;",
	} {
		declarationIndex := strings.Index(html, declaration)
		if declarationIndex < 0 {
			t.Fatalf("menubar.html is missing %q", declaration)
		}
		if declarationIndex > initCall {
			t.Fatalf("init() runs before the top-level binding %q; a render from init would throw a ReferenceError", declaration)
		}
	}
}
