import { describe, expect, it } from "vitest";
import {
  applyThresholds,
  buildStatusBarItems,
  colorTier,
  compactDuration,
  compactQuotaLabel,
  durationUntil,
  formatCombinedLabel,
  formatPercent,
  formatProviderLabel,
  formatProviderSection,
  formatTooltip,
  resetIn,
  selectProviders,
  severityFromPercent,
  severityRank,
  statusIcon,
  tightestQuota,
  type DaemonState,
} from "../src/model";
import { NOW, anthropic, codex, copilot, provider, quota, threeProviders } from "./fixtures";

const JUNK = /NaN|undefined|null|0\/0|—/;

describe("severity helpers", () => {
  it("ranks severities in order", () => {
    expect(severityRank("healthy")).toBeLessThan(severityRank("warning"));
    expect(severityRank("warning")).toBeLessThan(severityRank("danger"));
    expect(severityRank("danger")).toBeLessThan(severityRank("critical"));
  });

  it("treats unknown statuses as healthy", () => {
    expect(severityRank("bogus" as never)).toBe(0);
    expect(colorTier(undefined as never)).toBe("none");
  });

  it("maps danger to the warning color tier", () => {
    expect(colorTier("healthy")).toBe("none");
    expect(colorTier("warning")).toBe("warning");
    expect(colorTier("danger")).toBe("warning");
    expect(colorTier("critical")).toBe("critical");
  });

  it("derives severity from percent using thresholds", () => {
    expect(severityFromPercent(10, 70, 90)).toBe("healthy");
    expect(severityFromPercent(70, 70, 90)).toBe("warning");
    expect(severityFromPercent(89.9, 70, 90)).toBe("warning");
    expect(severityFromPercent(90, 70, 90)).toBe("critical");
    expect(severityFromPercent(150, 70, 90)).toBe("critical");
    expect(severityFromPercent(Number.NaN, 70, 90)).toBe("healthy");
  });

  it("picks the status icon per severity", () => {
    expect(statusIcon("healthy")).toBe("$(pass)");
    expect(statusIcon("warning")).toBe("$(warning)");
    expect(statusIcon("danger")).toBe("$(warning)");
    expect(statusIcon("critical")).toBe("$(error)");
  });
});

describe("applyThresholds", () => {
  it("recomputes quota and provider statuses from percent", () => {
    const [a, c, g] = applyThresholds(threeProviders(), 60, 90);
    expect(a.quotas[0].status).toBe("warning");
    expect(a.quotas[1].status).toBe("healthy");
    expect(a.status).toBe("warning");
    expect(c.status).toBe("healthy");
    expect(g.status).toBe("critical");
  });

  it("does not mutate the input", () => {
    const input = threeProviders();
    applyThresholds(input, 10, 20);
    expect(input[1].status).toBe("healthy");
    expect(input[1].quotas[0].status).toBe("healthy");
  });

  it("marks providers without quotas healthy", () => {
    const [p] = applyThresholds([provider({ quotas: [], status: "critical" })], 70, 90);
    expect(p.status).toBe("healthy");
  });
});

describe("selectProviders", () => {
  it("returns everything in original order when no order or filter is given", () => {
    expect(selectProviders(threeProviders(), {}).map((p) => p.id)).toEqual(["anthropic", "codex:prakersh7", "copilot"]);
  });

  it("orders by the given order and appends the rest", () => {
    const out = selectProviders(threeProviders(), { order: ["copilot", "anthropic"] });
    expect(out.map((p) => p.id)).toEqual(["copilot", "anthropic", "codex:prakersh7"]);
  });

  it("filters to visible providers", () => {
    expect(selectProviders(threeProviders(), { visible: ["copilot"] }).map((p) => p.id)).toEqual(["copilot"]);
  });

  it("treats an empty visible list as all", () => {
    expect(selectProviders(threeProviders(), { visible: [] })).toHaveLength(3);
  });

  it("matches profile-scoped ids by base provider", () => {
    const providers = [
      provider({ id: "codex:work", base_provider: "codex" }),
      provider({ id: "codex:home", base_provider: "codex" }),
      provider({ id: "anthropic" }),
    ];
    const out = selectProviders(providers, { visible: ["codex"], order: ["codex"] });
    expect(out.map((p) => p.id)).toEqual(["codex:work", "codex:home"]);
  });

  it("prefers exact id matches when ordering", () => {
    const providers = [provider({ id: "codex:home", base_provider: "codex" }), provider({ id: "codex:work", base_provider: "codex" })];
    expect(selectProviders(providers, { order: ["codex:work"] }).map((p) => p.id)).toEqual(["codex:work", "codex:home"]);
  });
});

describe("tightestQuota", () => {
  it("returns undefined when there is nothing to show", () => {
    expect(tightestQuota([])).toBeUndefined();
    expect(tightestQuota([provider({ quotas: [] })])).toBeUndefined();
  });

  it("picks the highest percent quota across providers", () => {
    const t = tightestQuota(threeProviders());
    expect(t?.provider.id).toBe("copilot");
    expect(t?.quota.key).toBe("premium");
  });

  it("breaks percent ties by severity, then by order", () => {
    const providers = [
      provider({ id: "a", quotas: [quota({ percent: 80, status: "healthy", key: "x" })] }),
      provider({ id: "b", quotas: [quota({ percent: 80, status: "warning", key: "y" })] }),
      provider({ id: "c", quotas: [quota({ percent: 80, status: "warning", key: "z" })] }),
    ];
    expect(tightestQuota(providers)?.provider.id).toBe("b");
  });

  it("ignores non-numeric percents", () => {
    const providers = [provider({ quotas: [quota({ percent: Number.NaN }), quota({ key: "ok", percent: 5 })] })];
    expect(tightestQuota(providers)?.quota.key).toBe("ok");
  });
});

describe("compactQuotaLabel", () => {
  it.each([
    ["5-Hour Limit", "5h"],
    ["Weekly All-Model", "Weekly"],
    ["Weekly Fable", "Fable weekly"],
    ["Wkly general", "Weekly"],
    ["general", "General"],
    ["Credits", "Credits"],
    ["Gemini 5h", "Gemini 5h"],
    ["Claude + GPT 5h", "Claude+GPT 5h"],
    ["Claude + GPT Weekly", "Claude+GPT wk"],
    ["Premium Requests", "Premium"],
  ])("%s -> %s", (input, expected) => {
    expect(compactQuotaLabel(input, "key")).toBe(expected);
  });

  it("handles other hour spellings", () => {
    expect(compactQuotaLabel("24 hour limit", "k")).toBe("24h");
    expect(compactQuotaLabel("1 Hours", "k")).toBe("1h");
    expect(compactQuotaLabel("Weekly All Models", "k")).toBe("Weekly");
  });

  it("falls back to the key and never exceeds 14 characters", () => {
    expect(compactQuotaLabel("", "five_hour")).toBe("five_hour");
    expect(compactQuotaLabel("", "")).toBe("");
    expect(compactQuotaLabel("An Extremely Long Quota Name Here", "k")).toHaveLength(14);
    expect(compactQuotaLabel("Something Long Weekly", "k")).toBe("Something Long");
  });
});

describe("durations", () => {
  it("compacts daemon durations", () => {
    expect(compactDuration("2h 14m")).toBe("2h14m");
    expect(compactDuration("  1h  5m ")).toBe("1h5m");
    expect(compactDuration("")).toBeUndefined();
    expect(compactDuration(undefined)).toBeUndefined();
    expect(compactDuration(null as never)).toBeUndefined();
  });

  it("computes durations until a timestamp in the daemon's spaced style", () => {
    expect(durationUntil(new Date(NOW + (2 * 60 + 14) * 60_000).toISOString(), NOW)).toBe("2h 14m");
    expect(durationUntil(new Date(NOW + 45 * 60_000).toISOString(), NOW)).toBe("45m");
    expect(durationUntil(new Date(NOW + (3 * 24 + 4) * 3_600_000 + 30_000).toISOString(), NOW)).toBe("3d 4h");
    expect(durationUntil(new Date(NOW + 3 * 3_600_000).toISOString(), NOW)).toBe("3h");
    expect(durationUntil(new Date(NOW + 20_000).toISOString(), NOW)).toBe("<1m");
    expect(durationUntil(new Date(NOW - 1000).toISOString(), NOW)).toBeUndefined();
    expect(durationUntil("not a date", NOW)).toBeUndefined();
    expect(durationUntil(undefined, NOW)).toBeUndefined();
    expect(durationUntil("", NOW)).toBeUndefined();
  });

  it("passes time_until_reset through verbatim and falls back to reset_at", () => {
    expect(resetIn(quota({ time_until_reset: "Resetting..." }), NOW)).toBe("Resetting...");
    expect(resetIn(quota({ time_until_reset: " 1h 1m " }), NOW)).toBe("1h 1m");
    expect(resetIn(quota({ time_until_reset: "" }), NOW)).toBe("2h 27m");
    expect(resetIn(quota({ time_until_reset: undefined, reset_at: undefined }), NOW)).toBeUndefined();
    expect(resetIn(quota({ time_until_reset: "", reset_at: "" }), NOW)).toBeUndefined();
  });
});

describe("formatPercent", () => {
  it("formats a rounded integer and guards non-finite values", () => {
    expect(formatPercent(62.4)).toBe("62%");
    expect(formatPercent(81.5)).toBe("82%");
    expect(formatPercent(-3)).toBe("0%");
    expect(formatPercent(0)).toBe("0%");
    expect(formatPercent(Number.NaN)).toBe("-");
    expect(formatPercent(Number.POSITIVE_INFINITY)).toBe("-");
    expect(formatPercent(undefined as never)).toBe("-");
    expect(formatPercent(null as never)).toBe("-");
    expect(formatPercent("85" as never)).toBe("-");
  });
});

describe("status bar labels", () => {
  it("formats the combined label as icon, compact quota, percent", () => {
    const t = { provider: anthropic(), quota: quota() };
    expect(formatCombinedLabel(t, NOW)).toBe("$(onwatch-anthropic) 5h 85%");
  });

  it("appends the reset countdown after a middle dot at critical", () => {
    const t = { provider: anthropic(), quota: quota({ percent: 92, status: "critical" }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(onwatch-anthropic) 5h 92% · 2h 27m");
  });

  it("omits the countdown at critical when no reset info exists", () => {
    const t = { provider: copilot(), quota: quota({ label: "Premium Requests", percent: 95, status: "critical", time_until_reset: "", reset_at: undefined }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(onwatch-copilot) Premium 95%");
  });

  it("uses the OpenAI mark for Codex profiles", () => {
    const t = { provider: codex(), quota: codex().quotas[0] };
    expect(formatCombinedLabel(t, NOW)).toBe("$(onwatch-openai) Weekly 0%");
  });

  it("falls back cleanly when there is no quota or no label", () => {
    expect(formatCombinedLabel(undefined, NOW)).toBe("$(onwatch-all) -");
    const t = { provider: anthropic(), quota: quota({ label: "", key: "" }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(onwatch-anthropic) 85%");
  });

  it("formats per-provider labels from the provider's tightest quota", () => {
    expect(formatProviderLabel(anthropic(), NOW)).toBe("$(onwatch-anthropic) 5h 85%");
    expect(formatProviderLabel(copilot(), NOW)).toBe("$(onwatch-copilot) Premium 95% · 2h 14m");
    expect(formatProviderLabel(provider({ quotas: [] }), NOW)).toBe("$(onwatch-anthropic) -");
  });

  it("never renders NaN, undefined or null", () => {
    const bad = quota({ percent: Number.NaN, used: undefined, limit: undefined, time_until_reset: "", reset_at: undefined, status: "critical" });
    const p = provider({ quotas: [bad] });
    expect(formatCombinedLabel({ provider: p, quota: bad }, NOW)).toBe("$(onwatch-anthropic) 5h -");
    expect(formatCombinedLabel({ provider: p, quota: bad }, NOW)).not.toMatch(JUNK);
    expect(formatProviderLabel(p, NOW)).not.toMatch(JUNK);
    expect(compactDuration(undefined) ?? "").not.toMatch(JUNK);
  });
});

describe("tooltip sections", () => {
  it("renders a provider heading with the mark and a limits table", () => {
    const md = formatProviderSection(anthropic(), NOW);
    const lines = md.split("\n");
    expect(lines[0]).toBe("### $(onwatch-anthropic) Anthropic");
    expect(lines[1]).toBe("| Limit | Used | Resets in |");
    expect(lines[2]).toBe("|:--|--:|--:|");
    expect(lines[3]).toBe("| $(warning) 5-Hour Limit | **85%** | 2h 27m |");
    expect(lines[4]).toBe("| $(pass) Weekly All-Model | 20% | 5d 3h |");
    expect(lines[5]).toBe("| $(pass) Weekly Fable | 13% | 5d 3h |");
    expect(lines).toHaveLength(6);
  });

  it("renders the subtitle in italics after the name", () => {
    const md = formatProviderSection(codex(), NOW);
    expect(md.split("\n")[0]).toBe("### $(onwatch-openai) Codex - prakersh7 &nbsp; _ChatGPT account_");
    expect(md).toContain("| $(pass) Weekly All-Model | 0% | 13d 11h |");
  });

  it("omits the subtitle when empty", () => {
    const md = formatProviderSection(codex.call(null) && { ...codex(), subtitle: "" }, NOW);
    expect(md.split("\n")[0]).toBe("### $(onwatch-openai) Codex - prakersh7");
  });

  it("shows used/limit only when both are finite and limit is positive", () => {
    expect(formatProviderSection(copilot(), NOW)).toContain("| $(error) Premium Requests | **95% (285/300)** | 2h 14m |");
    const zeroLimit = provider({ quotas: [quota({ used: 0, limit: 0 })] });
    expect(formatProviderSection(zeroLimit, NOW)).toContain("| $(warning) 5-Hour Limit | **85%** | 2h 27m |");
    const partial = provider({ quotas: [quota({ used: 12, limit: undefined })] });
    expect(formatProviderSection(partial, NOW)).toContain("| **85%** |");
    const fractional = provider({ quotas: [quota({ used: 2.25, limit: 10, status: "healthy", percent: 22.5 })] });
    expect(formatProviderSection(fractional, NOW)).toContain("| 23% (2.3/10) |");
  });

  it("shows dollars for currency quotas and the daemon's reading when the cap is unknown", () => {
    const paid = provider({
      id: "ollama",
      base_provider: "ollama",
      label: "Ollama",
      status: "healthy",
      highest_percent: 13,
      quotas: [quota({ key: "monthly_included_usage", label: "Monthly Included Usage", display_value: "13%", percent: 12.5, status: "healthy", used: 7.5, limit: 60, format: "currency", time_until_reset: "12d 3h" })],
    });
    expect(formatProviderSection(paid, NOW)).toContain("| $(pass) Monthly Included Usage | 13% ($7.50/$60.00) | 12d 3h |");

    const free = provider({
      id: "ollama",
      base_provider: "ollama",
      label: "Ollama",
      status: "healthy",
      highest_percent: 0,
      quotas: [quota({ key: "monthly_included_usage", label: "Monthly Included Usage", display_value: "$0.001 used", percent: 0, status: "healthy", used: 0.001, limit: 0, format: "currency", time_until_reset: "29d 22h" })],
    });
    expect(formatProviderSection(free, NOW)).toContain("| $(pass) Monthly Included Usage | $0.001 used | 29d 22h |");
  });

  it("marks stale quotas and uses a hyphen for a missing countdown", () => {
    const p = provider({ quotas: [quota({ is_stale: true, time_until_reset: "", reset_at: undefined })] });
    expect(formatProviderSection(p, NOW)).toContain("| $(warning) 5-Hour Limit $(history) | **85%** | - |");
  });

  it("passes Resetting... through verbatim", () => {
    const p = provider({ quotas: [quota({ time_until_reset: "Resetting..." })] });
    expect(formatProviderSection(p, NOW)).toContain("| 2h 27m |".replace("2h 27m", "Resetting..."));
  });

  it("renders a placeholder for providers without quotas", () => {
    const md = formatProviderSection(provider({ quotas: [] }), NOW);
    expect(md).toBe("### $(onwatch-anthropic) Anthropic\n_No quota data_");
  });

  it("escapes markdown in labels and never repeats the provider name in rows", () => {
    const p = provider({ label: "Weird*Name_", quotas: [quota({ label: "Limit|Pipe" })] });
    const md = formatProviderSection(p, NOW);
    expect(md).toContain("### $(onwatch-anthropic) Weird\\*Name\\_");
    expect(md).toContain("| $(warning) Limit\\|Pipe |");
    expect(md.split("\n").slice(1).join("\n")).not.toContain("Weird");
    expect(md).not.toContain("five_hour");
  });

  it("never renders NaN, undefined, null or 0/0", () => {
    const bad = quota({ percent: Number.NaN, used: undefined, limit: undefined, time_until_reset: "", reset_at: undefined });
    const md = formatProviderSection(provider({ quotas: [bad, quota({ used: 0, limit: 0, percent: Number.NaN })] }), NOW);
    expect(md).toContain("| $(warning) 5-Hour Limit | - | - |");
    expect(md).not.toMatch(JUNK);
  });
});

describe("formatTooltip", () => {
  const ctx = { fetchedAt: NOW - 12_000, nowMs: NOW, daemonUpdatedAgo: "2m ago" };

  it("stacks provider sections in daemon order and ends with the footer", () => {
    const md = formatTooltip(threeProviders(), ctx);
    const headings = md.split("\n").filter((l) => l.startsWith("### "));
    expect(headings).toEqual([
      "### $(onwatch-anthropic) Anthropic",
      "### $(onwatch-openai) Codex - prakersh7 &nbsp; _ChatGPT account_",
      "### $(onwatch-copilot) GitHub Copilot",
    ]);
    expect(md).toContain("| Limit | Used | Resets in |");
    expect(md).toContain("| $(warning) 5-Hour Limit | **85%** | 2h 27m |");
    expect(md.indexOf("Anthropic")).toBeLessThan(md.indexOf("Codex"));
    expect(md.indexOf("Codex")).toBeLessThan(md.indexOf("Copilot"));
    const footer = md.split("\n---\n\n")[1];
    expect(footer).toBe(
      "Updated 12s ago (daemon: 2m ago) · [Quick view](command:onwatch.openQuickView) · [Dashboard](command:onwatch.openDashboard) · [Refresh](command:onwatch.refresh)",
    );
    expect(md).not.toMatch(JUNK);
  });

  it("separates sections with blank lines so multiple tables render", () => {
    const md = formatTooltip(threeProviders(), ctx);
    expect(md).toContain("| 5d 3h |\n\n### $(onwatch-openai)");
  });

  it("omits the daemon age when unknown", () => {
    const md = formatTooltip([anthropic()], { fetchedAt: NOW, nowMs: NOW });
    expect(md).toContain("Updated just now · [Quick view]");
    expect(md).not.toContain("daemon:");
  });

  it("renders a placeholder when there are no providers", () => {
    const md = formatTooltip([], ctx);
    expect(md.startsWith("No providers to show.")).toBe(true);
    expect(md).toContain("[Refresh](command:onwatch.refresh)");
  });
});

describe("buildStatusBarItems", () => {
  const ok: DaemonState = { kind: "ok", providers: threeProviders(), fetchedAt: NOW - 12_000, daemonUpdatedAgo: "12s ago" };
  const base = { visibility: "always" as const, nowMs: NOW, url: "http://127.0.0.1:9211" };

  it("returns nothing when visibility is never", () => {
    expect(buildStatusBarItems(ok, { ...base, mode: "combined", visibility: "never" })).toEqual([]);
  });

  it("builds one combined item showing the tightest quota with the provider mark", () => {
    const items = buildStatusBarItems(ok, { ...base, mode: "combined" });
    expect(items).toHaveLength(1);
    expect(items[0].id).toBe("combined");
    expect(items[0].text).toBe("$(onwatch-copilot) Premium 95% · 2h 14m");
    expect(items[0].tier).toBe("critical");
    expect(items[0].command).toBe("onwatch.openQuickView");
    expect(items[0].tooltip).toContain("### $(onwatch-anthropic) Anthropic");
    expect(items[0].tooltip).toContain("### $(onwatch-copilot) GitHub Copilot");
    expect(items[0].tooltip).toContain("command:onwatch.refresh");
  });

  it("colors the combined item by the tightest quota's tier", () => {
    const state: DaemonState = { kind: "ok", providers: [anthropic(), codex()], fetchedAt: NOW };
    const items = buildStatusBarItems(state, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(onwatch-anthropic) 5h 85%");
    expect(items[0].tier).toBe("warning");
  });

  it("builds one item per provider with only that provider's section in the tooltip", () => {
    const items = buildStatusBarItems(ok, { ...base, mode: "perProvider" });
    expect(items.map((i) => i.id)).toEqual(["provider:anthropic", "provider:codex:prakersh7", "provider:copilot"]);
    expect(items.map((i) => i.text)).toEqual([
      "$(onwatch-anthropic) 5h 85%",
      "$(onwatch-openai) Weekly 0%",
      "$(onwatch-copilot) Premium 95% · 2h 14m",
    ]);
    expect(items.map((i) => i.tier)).toEqual(["warning", "none", "critical"]);
    expect(items[1].tooltip).toContain("### $(onwatch-openai) Codex - prakersh7");
    expect(items[1].tooltip).not.toContain("Anthropic");
    expect(items[1].tooltip).toContain("[Refresh](command:onwatch.refresh)");
  });

  it("hides healthy state under whenAnyProviderNearLimit", () => {
    const healthy: DaemonState = { kind: "ok", providers: [codex()], fetchedAt: NOW };
    expect(buildStatusBarItems(healthy, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
    expect(buildStatusBarItems(healthy, { ...base, mode: "perProvider", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
  });

  it("shows under whenAnyProviderNearLimit when any provider is warning or worse", () => {
    expect(buildStatusBarItems(ok, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" })).toHaveLength(1);
    expect(buildStatusBarItems(ok, { ...base, mode: "perProvider", visibility: "whenAnyProviderNearLimit" })).toHaveLength(3);
  });

  it("shows the no-daemon item even under whenAnyProviderNearLimit", () => {
    const state: DaemonState = { kind: "noDaemon", url: "http://127.0.0.1:9211", message: "ECONNREFUSED" };
    const items = buildStatusBarItems(state, { ...base, mode: "perProvider", visibility: "whenAnyProviderNearLimit" });
    expect(items).toHaveLength(1);
    expect(items[0].text).toBe("$(warning) onWatch: no daemon");
    expect(items[0].tier).toBe("none");
    expect(items[0].tooltip).toContain("http://127.0.0.1:9211");
    expect(items[0].tooltip).toContain("`onwatch`");
    expect(items[0].tooltip).toContain("https://github.com/onllm-dev/onWatch");
    expect(items[0].command).toBe("onwatch.refresh");
  });

  it("shows the sign-in item on auth errors", () => {
    const items = buildStatusBarItems({ kind: "auth", url: "http://host:9211" }, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(lock) onWatch: sign in");
    expect(items[0].command).toBe("onwatch.setCredentials");
  });

  it("shows the remote unsupported item", () => {
    const items = buildStatusBarItems({ kind: "remoteUnsupported", url: "http://host:9211" }, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(warning) onWatch: remote unsupported");
    expect(items[0].tooltip).toMatch(/localhost/i);
  });

  it("shows a generic error item", () => {
    const items = buildStatusBarItems({ kind: "error", url: "http://127.0.0.1:9211", message: "HTTP 500" }, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(warning) onWatch: error");
    expect(items[0].tooltip).toContain("HTTP 500");
    expect(items[0].command).toBe("onwatch.showLogs");
  });

  it("shows a loading placeholder", () => {
    expect(buildStatusBarItems({ kind: "loading" }, { ...base, mode: "combined" })[0].text).toBe("$(onwatch-all) ...");
    expect(buildStatusBarItems({ kind: "loading" }, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
  });

  it("never renders junk in any state or mode", () => {
    const bad = quota({ percent: Number.NaN, used: undefined, limit: undefined, time_until_reset: "", reset_at: undefined, status: "critical" });
    const states: DaemonState[] = [
      ok,
      { kind: "ok", providers: [provider({ quotas: [bad] })], fetchedAt: NOW },
      { kind: "noDaemon", url: "u" },
      { kind: "auth", url: "u" },
      { kind: "remoteUnsupported", url: "u" },
      { kind: "error", url: "u", message: "m" },
      { kind: "loading" },
    ];
    for (const s of states) {
      for (const mode of ["combined", "perProvider"] as const) {
        for (const item of buildStatusBarItems(s, { ...base, mode })) {
          expect(item.text + item.tooltip).not.toMatch(JUNK);
        }
      }
    }
  });
});
