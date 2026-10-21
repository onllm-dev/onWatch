package web

import (
	"testing"
)

// The stale indicator: a card whose data is older than its cadence gets an
// amber age label and a hollow dot, with the reason on the existing custom
// tooltip. It must coexist with the attention ring, and the local 30s ticker
// must be able to turn a card stale with no new response.
func TestMenubarStaleCardRender(t *testing.T) {
	html := readMenubarHTML(t)
	slice := menubarSlice(t, html, "function renderStandardCard(", "function renderStateCard(")

	script := `
const assert = require('node:assert/strict');
` + slice + `
function escapeHTML(value) { return String(value == null ? "" : value); }
function providerIconClass() { return "provider-icon-codex"; }
function providerAgeLabel(provider) {
  const then = Date.parse(provider.updated_at_time || "");
  const seconds = Math.max(0, (Date.now() - then) / 1000);
  if (seconds < 60) return "now";
  if (seconds < 3600) return Math.floor(seconds / 60) + "m";
  return Math.floor(seconds / 3600) + "h";
}
function clampPercent(value) { return Math.max(0, Math.min(100, Number(value || 0))); }
function providerQuotas(provider) { return Array.isArray(provider && provider.quotas) ? provider.quotas : []; }
function usageSummary() { return ""; }
function quotaStatusNote() { return ""; }
function fullQuotaLabel(quota) { return String(quota.label || ""); }
function compactResetLabel() { return "1h"; }
function ringColumns() { return 2; }
const mistralRecovery = { markup: () => "" };

const stale = {
  label: "Anthropic",
  id: "anthropic",
  base_provider: "anthropic",
  status: "healthy",
  highest_percent: 40,
  updated_at_time: new Date(Date.now() - 14 * 60 * 1000).toISOString(),
  stale: true,
  stale_reason: "network error",
  stale_after_seconds: 900,
  quotas: [{ key: "five_hour", label: "5-Hour", percent: 40, status: "healthy" }]
};
const header = standardHeaderMarkup(stale);
assert.ok(header.includes("is-stale"), "stale card must mark the age label");
assert.ok(/class="provider-status-dot [^"]*is-stale/.test(header), "stale card must mark the dot");
assert.ok(header.includes("provider-age"), "the age label gets the tooltip wrapper");
assert.ok(header.includes("Last updated 14m ago - network error"), "tooltip must pair age and reason: " + header);

// The dot keeps its severity class and becomes a ring via is-stale; the CSS
// makes the fill transparent.
const dot = header.match(/class="provider-status-dot [^"]*"/)[0];
assert.ok(dot.includes("status-healthy"), "stale must not drop severity");

// Generic reason when the registry has no entry.
const generic = Object.assign({}, stale, { stale_reason: "no update in 14m" });
const genericHeader = standardHeaderMarkup(generic);
assert.ok(genericHeader.includes("no update in 14m"), "generic reason must be shown as-is");

// Stale with an attention state: both treatments show, attention owns wording.
const paused = Object.assign({}, stale, { connection_status: "paused" });
const pausedHeader = standardHeaderMarkup(paused);
assert.ok(pausedHeader.includes("status-attention"), "attention ring must remain");
assert.ok(/class="provider-status-dot [^"]*status-attention[^"]*is-stale/.test(pausedHeader), "both classes coexist");
assert.ok(pausedHeader.includes("auth failed"), "attention wording wins on the dot tooltip");

// Fresh cards get no stale classes and no stale tooltip text.
const fresh = Object.assign({}, stale, { stale: false, updated_at_time: new Date().toISOString(), stale_after_seconds: 900 });
const freshHeader = standardHeaderMarkup(fresh);
assert.ok(!freshHeader.includes("is-stale"));
assert.ok(!freshHeader.includes("Last updated"));

// Numbers stay visible on a stale card.
const card = renderStandardCard(stale, 2);
assert.ok(card.includes("quota-ring-pct"));
assert.ok(card.includes("5-Hour"));
`
	runMenubarNode(t, script)
}

// The ticker decides staleness from data attributes alone, so a card turns
// stale while the daemon is unreachable (no new snapshot, no re-render input).
func TestMenubarStaleTicker(t *testing.T) {
	html := readMenubarHTML(t)
	slice := menubarSlice(t, html, "function updateAgeLabels(", "function resetRefreshTimer(")

	script := `
const assert = require('node:assert/strict');
` + slice + `
const now = Date.now();
const min = 60 * 1000;

// providerStaleFromAttributes mirrors providerStaleNow with raw attributes.
assert.equal(providerStaleFromAttributes(new Date(now - 2 * min).toISOString(), "300"), false, "inside the window");
assert.equal(providerStaleFromAttributes(new Date(now - 10 * min).toISOString(), "300"), true, "past the window");
assert.equal(providerStaleFromAttributes(new Date(now - 10 * min).toISOString(), "0"), false, "manual cards never age out");
assert.equal(providerStaleFromAttributes(new Date(now - 10 * min).toISOString(), ""), false);
assert.equal(providerStaleFromAttributes("", "300"), false);

// staleTooltipText keeps the reason while advancing the age phrase.
assert.equal(
  staleTooltipText(new Date(now - 20 * min).toISOString(), "20m", "Last updated 14m ago - network error"),
  "Last updated 20m ago - network error"
);
assert.equal(
  staleTooltipText(new Date(now - 20 * min).toISOString(), "20m", "no update in 14m"),
  "no update in 20m"
);
assert.equal(
  staleTooltipText(new Date(now - 20 * min).toISOString(), "now", "Last updated 14m ago - auth"),
  "Last updated just now - auth"
);
`
	runMenubarNode(t, script)
}

// The ticker callback itself must flip a stale class on the status dot (via the
// render path) once the recorded time crosses stale_after_seconds.
func TestMenubarStaleTickerFlipsClass(t *testing.T) {
	html := readMenubarHTML(t)
	slice := menubarSlice(t, html, "function updateAgeLabels(", "function resetRefreshTimer(")

	script := `
const assert = require('node:assert/strict');
let rendered = 0;
function render() { rendered += 1; }
const dataset = {
  ".provider-status[data-updated-at]": [{
    getAttribute: (name) => ({
      "data-updated-at": new Date(Date.now() - 15 * 60 * 1000).toISOString(),
      "data-stale-after": "300"
    })[name] || "",
    querySelector: () => null
  }]
};
global.document = {
  querySelectorAll: (selector) => dataset[selector] || []
};
const headerUpdated = { textContent: "" };
const state = { snapshot: null };
` + slice + `
updateAgeLabels();
assert.equal(rendered, 1, "crossing the threshold must re-render once");
`
	runMenubarNode(t, script)
}
