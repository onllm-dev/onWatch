package web

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// peakNodeScript loads the shared peak helper under node and runs the given
// assertions. The helper is a plain IIFE with a CommonJS export, so it does
// not need the page's DOM.
func peakNodeScript(t *testing.T, assertions string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for peak-hours tests")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	scriptPath := filepath.Join(filepath.Dir(file), "static", "peak-hours.js")

	script := `
const assert = require('node:assert/strict');
const peak = require(` + jsStringLiteral(t, scriptPath) + `);
` + assertions
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestPeakHoursScheduleBoundaries(t *testing.T) {
	peakNodeScript(t, `
const at = (iso) => peak.peakStateAt(new Date(iso));

// 2026-10-22 is a Thursday.
let state = at("2026-10-22T00:00:00Z");
assert.equal(state.peak, false);
assert.equal(state.until.toISOString(), "2026-10-22T01:00:00.000Z");

state = at("2026-10-22T01:00:00Z");
assert.equal(state.peak, true, "exactly 01:00 starts peak");
assert.equal(state.until.toISOString(), "2026-10-22T04:00:00.000Z");
assert.equal(state.remainingMs, 3 * 60 * 60 * 1000);

state = at("2026-10-22T03:59:59Z");
assert.equal(state.peak, true);
assert.equal(state.remainingMs, 1000);

state = at("2026-10-22T04:00:00Z");
assert.equal(state.peak, false, "exactly 04:00 is the 04-06 gap");
assert.equal(state.until.toISOString(), "2026-10-22T06:00:00.000Z");
assert.equal(state.remainingMs, 2 * 60 * 60 * 1000);

state = at("2026-10-22T05:59:00Z");
assert.equal(state.peak, false);
assert.equal(state.remainingMs, 60 * 1000);

state = at("2026-10-22T06:00:00Z");
assert.equal(state.peak, true, "exactly 06:00 starts peak");
assert.equal(state.until.toISOString(), "2026-10-22T10:00:00.000Z");

state = at("2026-10-22T10:00:00Z");
assert.equal(state.peak, false, "exactly 10:00 ends peak");
assert.equal(state.until.toISOString(), "2026-10-23T01:00:00.000Z");
assert.equal(state.remainingMs, 15 * 60 * 60 * 1000);
`)
}

func TestPeakHoursWeekendWrap(t *testing.T) {
	peakNodeScript(t, `
// Friday 2026-10-23 10:00 to Monday 2026-10-26 01:00 is 63 hours.
let state = peak.peakStateAt(new Date("2026-10-23T10:00:00Z"));
assert.equal(state.peak, false);
assert.equal(state.until.toISOString(), "2026-10-26T01:00:00.000Z");
assert.equal(state.remainingMs, 63 * 60 * 60 * 1000);

// Saturday and Sunday never peak, and their next transition is Monday 01:00.
state = peak.peakStateAt(new Date("2026-10-24T12:00:00Z"));
assert.equal(state.peak, false);
assert.equal(state.remainingMs, 37 * 60 * 60 * 1000);

state = peak.peakStateAt(new Date("2026-10-25T23:59:00Z"));
assert.equal(state.peak, false);
assert.equal(state.until.toISOString(), "2026-10-26T01:00:00.000Z");
assert.equal(state.remainingMs, 61 * 60 * 1000);

// Sunday inside what would be a peak window on a weekday is still off-peak.
state = peak.peakStateAt(new Date("2026-10-25T07:00:00Z"));
assert.equal(state.peak, false);
assert.equal(state.until.toISOString(), "2026-10-26T01:00:00.000Z");

// Monday 01:00 starts peak again.
state = peak.peakStateAt(new Date("2026-10-26T01:00:00Z"));
assert.equal(state.peak, true);
`)
}

func TestPeakHoursCountdownFormatting(t *testing.T) {
	peakNodeScript(t, `
assert.equal(peak.peakCountdownText(0), "");
assert.equal(peak.peakCountdownText(-1000), "");
assert.equal(peak.peakCountdownText(39 * 1000), "39s");
assert.equal(peak.peakCountdownText(60 * 1000), "1m");
assert.equal(peak.peakCountdownText(45 * 60 * 1000), "45m");
assert.equal(peak.peakCountdownText(2 * 60 * 60 * 1000 + 10 * 60 * 1000), "2h 10m");
assert.equal(peak.peakCountdownText(15 * 60 * 60 * 1000), "15h");
assert.equal(peak.peakCountdownText(17 * 60 * 60 * 1000 + 50 * 60 * 1000), "17h 50m");

// The chip text matches the user-facing wording.
assert.equal(peak.peakLabel(new Date("2026-10-22T07:50:00Z")).text, "Peak till 2h 10m");
assert.equal(peak.peakLabel(new Date("2026-10-24T12:00:00Z")).text, "Off-peak till 37h");
`)
}

func TestPeakHoursTooltipAndChip(t *testing.T) {
	peakNodeScript(t, `
const meta = {
  provider: "commandcode",
  model_note: "Command Code DeepSeek models",
  pricing: "peak $0.30 / $1.20"
};
const tooltip = peak.peakTooltip(meta);
assert.equal(tooltip, "DeepSeek peak 01:00-04:00 and 06:00-10:00 UTC, Mon-Fri; off-peak all other hours; Command Code DeepSeek models; peak $0.30 / $1.20");
assert.equal(
  peak.peakTooltip({ provider: "opencode", model_note: "DeepSeek V4.1 Flash / V4 Pro" }),
  "DeepSeek peak 01:00-04:00 and 06:00-10:00 UTC, Mon-Fri; off-peak all other hours; DeepSeek V4.1 Flash / V4 Pro"
);

const RealDate = Date;
const freeze = (iso) => {
  const fixed = new RealDate(iso).getTime();
  global.Date = class extends RealDate {
    constructor(...args) { return args.length ? new RealDate(...args) : new RealDate(fixed); }
    static now() { return fixed; }
  };
};
freeze("2026-10-22T07:50:00Z");
let chip = peak.peakChipMarkup(meta);
assert.ok(chip.includes('class="peak-chip promo-peak"'), chip);
assert.ok(chip.includes("Peak till 2h 10m"), chip);
assert.ok(chip.includes("peak $0.30 / $1.20"), chip);
assert.ok(chip.includes("data-peak-provider=\"commandcode\""), chip);

freeze("2026-10-24T12:00:00Z");
chip = peak.peakChipMarkup(meta);
assert.ok(chip.includes('class="peak-chip promo-offpeak"'), chip);
assert.ok(chip.includes("Off-peak till 37h"), chip);

// No metadata renders no chip.
assert.equal(peak.peakChipMarkup(null), "");

// Tooltip text is escaped into the attribute.
const escaped = peak.peakChipMarkup({ provider: "x", pricing: '"quoted"' });
assert.ok(escaped.includes("&quot;quoted&quot;"), escaped);
global.Date = RealDate;
`)
}

// The dashboard and the popover must load the shared helper and use its chip
// and ticker instead of the removed Anthropic promo code.
func TestPeakHoursSharedChipWiring(t *testing.T) {
	appJS := readStaticFile(t, "static/app.js")
	menubarHTML := readStaticFile(t, "static/menubar.html")
	layout := readStaticFile(t, "templates/layout.html")

	for name, source := range map[string]string{"app.js": appJS, "menubar.html": menubarHTML} {
		if strings.Contains(source, "promo-tag") || strings.Contains(source, "isAnthropicPeakHours") || strings.Contains(source, "activeAnthropicPromo") {
			t.Errorf("%s still contains the removed Anthropic promo code", name)
		}
		if !strings.Contains(source, "OnWatchPeak") {
			t.Errorf("%s must use the shared OnWatchPeak helper", name)
		}
		if !strings.Contains(source, "refreshPeakChips") {
			t.Errorf("%s must refresh peak chips on the 30s ticker", name)
		}
	}
	if !strings.Contains(layout, "/static/peak-hours.js?v=") {
		t.Error("layout.html must load peak-hours.js")
	}
	if !strings.Contains(menubarHTML, "/static/peak-hours.js?v=") {
		t.Error("menubar.html must load peak-hours.js")
	}
	for _, provider := range []string{"commandcode", "opencode", "deepseek"} {
		if !strings.Contains(appJS, "peakProviderMeta") || !strings.Contains(appJS, provider+":") {
			t.Errorf("app.js must define peak metadata for %s", provider)
		}
	}
	if !strings.Contains(appJS, "peakChipHTML('commandcode')") {
		t.Error("app.js Command Code card must render the peak chip")
	}
	if !strings.Contains(appJS, "peakChipHTML('opencode')") {
		t.Error("app.js OpenCode card must render the peak chip")
	}
	if !strings.Contains(appJS, "field.primary ? peakChipHTML(provider) : ''") {
		t.Error("app.js DeepSeek balance card must render the peak chip")
	}
	if !strings.Contains(appJS, "window.OnWatchPeak.refreshPeakChips()") {
		t.Error("app.js must refresh chips every 30s")
	}
}

// Dashboard card renderers must place the peak chip on the Command Code,
// OpenCode and DeepSeek cards. The node run slices the page's own renderers
// and supplies the real shared helper.
func TestDashboardPeakChipRendering(t *testing.T) {
	appJS := readStaticFile(t, "static/app.js")
	peakSlice := func(start, end string) string {
		t.Helper()
		from := strings.Index(appJS, start)
		if from < 0 {
			t.Fatalf("app.js is missing %q", start)
		}
		to := strings.Index(appJS[from:], end)
		if to < 0 {
			t.Fatalf("app.js is missing %q after %q", end, start)
		}
		return appJS[from : from+to]
	}
	metaSlice := peakSlice("const peakProviderMeta = {", "function renderAnthropicQuotaCards(")
	kpiSlice := peakSlice("function renderProviderKPIHTML(", "function sortItemsByPreference(")
	balanceSlice := peakSlice("function renderBalanceCards(", "function updateBalanceCards(")

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for dashboard chip test")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	helperPath := filepath.Join(filepath.Dir(file), "static", "peak-hours.js")

	script := `
const assert = require('node:assert/strict');
const peak = require(` + jsStringLiteral(t, helperPath) + `);
global.window = { OnWatchPeak: peak };
function escapeHTML(value) { return String(value == null ? "" : value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch])); }
function formatNumber(value) { return String(value); }
function formatResetTime() { return "1h"; }
function formatDuration(seconds) { return seconds + "s"; }
function minimaxSharedSubtitle() { return ""; }
function codexAutoStartBadge() { return ""; }
const statusConfig = { healthy: { icon: "M0 0", label: "Healthy" }, warning: { icon: "M0 0", label: "Warning" }, critical: { icon: "M0 0", label: "Critical" } };
const quotaIcons = {};
const anthropicQuotaIcons = {};
function commandCodeCardLabel(q) { return "1 / 10"; }
function getQuotaStatus(percent) { return percent >= 90 ? "critical" : (percent >= 75 ? "warning" : "healthy"); }
function openCodeCardLabel(q) { return "1 / 10"; }
function openCodeStatusNote(q) { return ""; }
const opencodeDisplayNames = { five_hour: "5-Hour", weekly: "Weekly", monthly: "Monthly" };
const balanceProviderFields = {
  deepseek: [
    { key: "total", historyKey: "total_balance", label: "Total Balance", primary: true },
    { key: "granted", historyKey: "granted_balance", label: "Granted" },
    { key: "toppedUp", historyKey: "topped_up_balance", label: "Topped Up" }
  ]
};

const RealDate = Date;
const freeze = (iso) => {
  const fixed = new RealDate(iso).getTime();
  global.Date = class extends RealDate {
    constructor(...args) { return args.length ? new RealDate(...args) : new RealDate(fixed); }
    static now() { return fixed; }
  };
};
` + metaSlice + `
` + kpiSlice + `
` + balanceSlice + `
freeze("2026-10-22T07:50:00Z");

// Both view KPI cards.
let kpi = renderProviderKPIHTML([{ name: "five_hour", cardPercent: 20, status: "healthy" }], "commandcode");
assert.ok(kpi.includes("Peak till 2h 10m"), kpi);
assert.ok(kpi.includes("peak-chip-wrap"), kpi);
assert.ok(kpi.includes("peak $0.30 / $1.20"), kpi);

kpi = renderProviderKPIHTML([{ name: "five_hour", cardPercent: 20, status: "healthy" }], "opencode");
assert.ok(kpi.includes("DeepSeek V4.1 Flash / V4 Pro"), kpi);

kpi = renderProviderKPIHTML([{ name: "balance", cardPercent: 20, status: "healthy" }], "deepseek");
assert.ok(kpi.includes("peak-chip-wrap"), kpi);

// Anthropic gets no chip on the both view.
kpi = renderProviderKPIHTML([{ name: "five_hour", cardPercent: 20, status: "healthy" }], "anthropic");
assert.ok(!kpi.includes("peak-chip"), kpi);

// DeepSeek balance card (single tab).
const container = { innerHTML: "", querySelectorAll: () => [] };
global.document = { getElementById: () => container };
const State = {};
function updateBalanceCards() {}
renderBalanceCards("deepseek", { status: "healthy", currency: "USD", total: 4.2 }, "quota-grid-deepseek");
assert.ok(container.innerHTML.includes("peak-chip-wrap"), container.innerHTML);
assert.ok(container.innerHTML.includes("Total Balance"), container.innerHTML);

freeze("2026-10-24T12:00:00Z");
kpi = renderProviderKPIHTML([{ name: "five_hour", cardPercent: 20, status: "healthy" }], "opencode");
assert.ok(kpi.includes("Off-peak till 37h"), kpi);
global.Date = RealDate;
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// The popover header renders the chip next to the provider name and never the
// Anthropic promo. The node run uses the page's own slice and the real shared
// helper, so a wiring drift fails here.
func TestMenubarPeakChipRendering(t *testing.T) {
	html := readMenubarHTML(t)
	slice := menubarSlice(t, html, "function peakChipHTML(", "function renderStateCard(")
	if strings.Contains(html, "promo-tag") {
		t.Fatal("menubar.html must not render the removed Anthropic promo tag")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for popover chip test")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	helperPath := filepath.Join(filepath.Dir(file), "static", "peak-hours.js")

	script := `
const assert = require('node:assert/strict');
const peak = require(` + jsStringLiteral(t, helperPath) + `);
global.window = { OnWatchPeak: peak };
function escapeHTML(value) { return String(value == null ? "" : value); }
function providerIconClass() { return "provider-icon-commandcode"; }
function providerAgeLabel() { return "5m"; }
function clampPercent(value) { return Math.max(0, Math.min(100, Number(value || 0))); }
function providerQuotas(provider) { return Array.isArray(provider && provider.quotas) ? provider.quotas : []; }
function usageSummary() { return ""; }
function quotaStatusNote() { return ""; }
function fullQuotaLabel(quota) { return String(quota.label || ""); }
function compactResetLabel() { return "1h"; }
function ringColumns() { return 2; }
const mistralRecovery = { markup: () => "" };
function providerStaleNow() { return false; }
function providerStaleFromAttributes() { return false; }
function staleTooltipText() { return ""; }
function ageLabelFromTimestamp() { return "5m"; }
function providerStatusMarkup(provider) {
  return '<span class="provider-status"><span class="provider-status-dot status-' + (provider.status || "healthy") + '"></span></span>';
}
function providerAgeMarkup() { return '<span class="provider-updated">5m</span>'; }
` + slice + `
const RealDate = Date;
const freeze = (iso) => {
  const fixed = new RealDate(iso).getTime();
  global.Date = class extends RealDate {
    constructor(...args) { return args.length ? new RealDate(...args) : new RealDate(fixed); }
    static now() { return fixed; }
  };
};
const peakMeta = {
  provider: "commandcode",
  schedule_text: peak.DEEPSEEK_SCHEDULE_TEXT,
  model_note: "Command Code DeepSeek models",
  pricing: "peak $0.30 / $1.20"
};
const provider = {
  label: "Command Code", id: "commandcode", base_provider: "commandcode",
  status: "healthy", updated_at_time: new Date().toISOString(),
  peak: peakMeta,
  quotas: [{ key: "five_hour", label: "5-Hour", percent: 20, status: "healthy" }]
};

freeze("2026-10-22T07:50:00Z");
let header = standardHeaderMarkup(provider);
assert.ok(header.includes("peak-chip-wrap"), header);
assert.ok(header.includes("Peak till 2h 10m"), header);
assert.ok(header.includes("promo-peak"), header);
assert.ok(header.includes("DeepSeek peak 01:00-04:00 and 06:00-10:00 UTC, Mon-Fri; off-peak all other hours"), header);
assert.ok(header.includes("peak $0.30 / $1.20"), header);

freeze("2026-10-24T12:00:00Z");
header = standardHeaderMarkup(provider);
assert.ok(header.includes("Off-peak till 37h"), header);
assert.ok(header.includes("promo-offpeak"), header);

// No peak metadata: no chip at all.
const plain = Object.assign({}, provider);
delete plain.peak;
assert.ok(!standardHeaderMarkup(plain).includes("peak-chip"), "a card without peak metadata must not render a chip");

// Anthropic never renders a peak chip or a promo tag.
const anthropic = Object.assign({}, provider, { label: "Anthropic", id: "anthropic", base_provider: "anthropic" });
delete anthropic.peak;
const anthropicHeader = standardHeaderMarkup(anthropic);
assert.ok(!anthropicHeader.includes("peak-chip"));
assert.ok(!anthropicHeader.includes("promo"));

// The chip keeps the age label and status dot in place.
assert.ok(header.includes("provider-updated"));
assert.ok(header.includes("provider-status-dot"));
global.Date = RealDate;
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
