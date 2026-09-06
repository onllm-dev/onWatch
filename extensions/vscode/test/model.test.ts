import { describe, expect, it } from "vitest";
import {
  applyThresholds,
  buildStatusBarItems,
  colorTier,
  compactDuration,
  durationUntil,
  formatCombinedLabel,
  formatProviderLabel,
  formatProviderTooltipLine,
  formatTooltip,
  formatPercent,
  resetIn,
  selectProviders,
  severityFromPercent,
  severityRank,
  shortProviderLabel,
  statusIcon,
  tightestQuota,
  type DaemonState,
} from "../src/model";
import { NOW, provider, quota, threeProviders } from "./fixtures";

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
  });

  it("picks the status icon per severity", () => {
    expect(statusIcon("healthy")).toBe("$(pass)");
    expect(statusIcon("warning")).toBe("$(warning)");
    expect(statusIcon("danger")).toBe("$(warning)");
    expect(statusIcon("critical")).toBe("$(error)");
  });
});

describe("applyThresholds", () => {
  it("recomputes quota, provider statuses from percent", () => {
    const [claude, codex, copilot] = applyThresholds(threeProviders(), 60, 90);
    expect(claude.quotas[0].status).toBe("warning");
    expect(claude.status).toBe("warning");
    expect(codex.status).toBe("warning");
    expect(codex.quotas[1].status).toBe("healthy");
    expect(copilot.status).toBe("critical");
  });

  it("does not mutate the input", () => {
    const input = threeProviders();
    applyThresholds(input, 10, 20);
    expect(input[0].status).toBe("healthy");
    expect(input[0].quotas[0].status).toBe("healthy");
  });

  it("marks providers without quotas healthy", () => {
    const [p] = applyThresholds([provider({ quotas: [], status: "critical" })], 70, 90);
    expect(p.status).toBe("healthy");
  });
});

describe("selectProviders", () => {
  it("returns everything in original order when no order or filter is given", () => {
    const out = selectProviders(threeProviders(), {});
    expect(out.map((p) => p.id)).toEqual(["anthropic", "codex", "copilot"]);
  });

  it("orders by the given order and appends the rest", () => {
    const out = selectProviders(threeProviders(), { order: ["copilot", "anthropic"] });
    expect(out.map((p) => p.id)).toEqual(["copilot", "anthropic", "codex"]);
  });

  it("filters to visible providers", () => {
    const out = selectProviders(threeProviders(), { visible: ["codex"] });
    expect(out.map((p) => p.id)).toEqual(["codex"]);
  });

  it("treats an empty visible list as all", () => {
    expect(selectProviders(threeProviders(), { visible: [] })).toHaveLength(3);
  });

  it("matches profile-scoped ids by base provider", () => {
    const providers = [
      provider({ id: "codex:work", base_provider: "codex", label: "Codex (work)" }),
      provider({ id: "codex:home", base_provider: "codex", label: "Codex (home)" }),
      provider({ id: "anthropic" }),
    ];
    const out = selectProviders(providers, { visible: ["codex"], order: ["codex"] });
    expect(out.map((p) => p.id)).toEqual(["codex:work", "codex:home"]);
  });

  it("prefers exact id matches when ordering", () => {
    const providers = [provider({ id: "codex:home", base_provider: "codex" }), provider({ id: "codex:work", base_provider: "codex" })];
    const out = selectProviders(providers, { order: ["codex:work"] });
    expect(out.map((p) => p.id)).toEqual(["codex:work", "codex:home"]);
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
    const t = tightestQuota(providers);
    expect(t?.provider.id).toBe("b");
  });

  it("ignores non-numeric percents", () => {
    const providers = [provider({ quotas: [quota({ percent: Number.NaN }), quota({ key: "ok", percent: 5 })] })];
    expect(tightestQuota(providers)?.quota.key).toBe("ok");
  });
});

describe("formatting", () => {
  it("formats percent as a rounded integer", () => {
    expect(formatPercent(62.4)).toBe("62%");
    expect(formatPercent(81.5)).toBe("82%");
    expect(formatPercent(-3)).toBe("0%");
    expect(formatPercent(Number.NaN)).toBe("--");
  });

  it("compacts daemon durations", () => {
    expect(compactDuration("2h 14m")).toBe("2h14m");
    expect(compactDuration("45m")).toBe("45m");
    expect(compactDuration("3d 4h")).toBe("3d4h");
    expect(compactDuration("  1h  5m ")).toBe("1h5m");
    expect(compactDuration("")).toBeUndefined();
    expect(compactDuration(undefined)).toBeUndefined();
  });

  it("computes compact durations until a timestamp", () => {
    expect(durationUntil(new Date(NOW + (2 * 60 + 14) * 60_000).toISOString(), NOW)).toBe("2h14m");
    expect(durationUntil(new Date(NOW + 45 * 60_000).toISOString(), NOW)).toBe("45m");
    expect(durationUntil(new Date(NOW + (3 * 24 + 4) * 3_600_000 + 30_000).toISOString(), NOW)).toBe("3d4h");
    expect(durationUntil(new Date(NOW + 20_000).toISOString(), NOW)).toBe("<1m");
    expect(durationUntil(new Date(NOW - 1000).toISOString(), NOW)).toBeUndefined();
    expect(durationUntil("not a date", NOW)).toBeUndefined();
    expect(durationUntil(undefined, NOW)).toBeUndefined();
  });

  it("prefers time_until_reset over reset_at", () => {
    expect(resetIn(quota({ time_until_reset: "1h 1m" }), NOW)).toBe("1h1m");
    expect(resetIn(quota({ time_until_reset: undefined }), NOW)).toBe("2h14m");
    expect(resetIn(quota({ time_until_reset: undefined, reset_at: undefined }), NOW)).toBeUndefined();
  });

  it("shortens provider labels", () => {
    expect(shortProviderLabel(provider({ label: "Claude" }))).toBe("Claude");
    expect(shortProviderLabel(provider({ label: "GitHub Copilot" }))).toBe("Copilot");
    expect(shortProviderLabel(provider({ label: "OpenCode Go" }))).toBe("OpenCode Go");
    expect(shortProviderLabel(provider({ label: "Antigravity Extended" }))).toBe("Antigravity");
    expect(shortProviderLabel(provider({ label: "SuperLongProviderNameHere" }))).toBe("SuperLongPr");
    expect(shortProviderLabel(provider({ label: "", base_provider: "kimi" }))).toBe("kimi");
  });

  it("formats the combined label without reset time below critical", () => {
    const t = { provider: provider(), quota: quota({ percent: 82, status: "warning" }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(pulse) 82%");
  });

  it("formats the combined label with a middle dot and reset time at critical", () => {
    const t = { provider: provider(), quota: quota({ percent: 95, status: "critical", time_until_reset: "2h 14m" }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(pulse) 95% · 2h14m");
  });

  it("omits the reset suffix at critical when no reset info exists", () => {
    const t = { provider: provider(), quota: quota({ percent: 95, status: "critical", time_until_reset: undefined, reset_at: undefined }) };
    expect(formatCombinedLabel(t, NOW)).toBe("$(pulse) 95%");
  });

  it("formats the placeholder combined label", () => {
    expect(formatCombinedLabel(undefined, NOW)).toBe("$(pulse) --");
  });

  it("formats per-provider labels", () => {
    expect(formatProviderLabel(threeProviders()[2])).toBe("Copilot 95%");
    expect(formatProviderLabel(provider({ quotas: [] }))).toBe("Claude --");
  });

  it("formats provider tooltip lines", () => {
    const [claude, , copilot] = threeProviders();
    expect(formatProviderTooltipLine(claude, NOW)).toBe("$(pass) **Claude** 62% - 5h window - resets in 2h14m");
    expect(formatProviderTooltipLine(copilot, NOW)).toBe("$(error) **GitHub Copilot** 95% - Premium requests - resets in 2h14m");
    expect(formatProviderTooltipLine(provider({ quotas: [] }), NOW)).toBe("$(pass) **Claude** - no quota data");
    expect(
      formatProviderTooltipLine(provider({ quotas: [quota({ time_until_reset: undefined, reset_at: undefined })] }), NOW),
    ).toBe("$(pass) **Claude** 62% - 5h window");
  });

  it("escapes markdown characters in provider labels", () => {
    const line = formatProviderTooltipLine(provider({ label: "Weird*Name_" }), NOW);
    expect(line).toContain("**Weird\\*Name\\_**");
  });

  it("builds the full tooltip with a footer", () => {
    const md = formatTooltip(threeProviders(), { fetchedAt: NOW - 12_000, nowMs: NOW, daemonUpdatedAgo: "3m ago" });
    const lines = md.split("\n\n");
    expect(lines[0]).toBe("$(pass) **Claude** 62% - 5h window - resets in 2h14m");
    expect(lines[1]).toMatch(/^\$\(warning\) \*\*Codex\*\* 82% - Weekly/);
    expect(lines[2]).toMatch(/^\$\(error\) \*\*GitHub Copilot\*\*/);
    expect(lines[3]).toBe(
      "Updated 12s ago (daemon data 3m ago) - [Open dashboard](command:onwatch.openDashboard) - [Quick view](command:onwatch.openQuickView) - [Refresh](command:onwatch.refresh)",
    );
    expect(md).not.toContain("\u2014");
  });

  it("builds a tooltip when there are no providers", () => {
    const md = formatTooltip([], { fetchedAt: NOW, nowMs: NOW });
    expect(md.split("\n\n")[0]).toBe("No providers to show.");
    expect(md).toContain("Updated just now");
  });
});

describe("buildStatusBarItems", () => {
  const ok: DaemonState = { kind: "ok", providers: threeProviders(), fetchedAt: NOW - 12_000, daemonUpdatedAgo: "12s ago" };
  const base = { visibility: "always" as const, nowMs: NOW, url: "http://127.0.0.1:9211" };

  it("returns nothing when visibility is never", () => {
    expect(buildStatusBarItems(ok, { ...base, mode: "combined", visibility: "never" })).toEqual([]);
  });

  it("builds one combined item showing the tightest quota", () => {
    const items = buildStatusBarItems(ok, { ...base, mode: "combined" });
    expect(items).toHaveLength(1);
    expect(items[0].id).toBe("combined");
    expect(items[0].text).toBe("$(pulse) 95% · 2h14m");
    expect(items[0].tier).toBe("critical");
    expect(items[0].command).toBe("onwatch.openQuickView");
    expect(items[0].tooltip).toContain("**Claude**");
    expect(items[0].tooltip).toContain("command:onwatch.refresh");
  });

  it("colors the combined item by the tightest quota's tier", () => {
    const state: DaemonState = { kind: "ok", providers: threeProviders().slice(0, 2), fetchedAt: NOW };
    const items = buildStatusBarItems(state, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(pulse) 82%");
    expect(items[0].tier).toBe("warning");
  });

  it("builds one item per provider", () => {
    const items = buildStatusBarItems(ok, { ...base, mode: "perProvider" });
    expect(items.map((i) => i.id)).toEqual(["provider:anthropic", "provider:codex", "provider:copilot"]);
    expect(items.map((i) => i.text)).toEqual(["Claude 62%", "Codex 82%", "Copilot 95%"]);
    expect(items.map((i) => i.tier)).toEqual(["none", "warning", "critical"]);
    expect(items[1].tooltip).toContain("**Codex** 82% - Weekly");
    expect(items[1].tooltip).not.toContain("**Claude**");
  });

  it("hides healthy state under whenAnyProviderNearLimit", () => {
    const healthy: DaemonState = { kind: "ok", providers: [provider()], fetchedAt: NOW };
    expect(buildStatusBarItems(healthy, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
    expect(buildStatusBarItems(healthy, { ...base, mode: "perProvider", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
  });

  it("shows under whenAnyProviderNearLimit when any provider is warning or worse", () => {
    const items = buildStatusBarItems(ok, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" });
    expect(items).toHaveLength(1);
    const per = buildStatusBarItems(ok, { ...base, mode: "perProvider", visibility: "whenAnyProviderNearLimit" });
    expect(per).toHaveLength(3);
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
    const items = buildStatusBarItems({ kind: "loading" }, { ...base, mode: "combined" });
    expect(items[0].text).toBe("$(pulse) ...");
    expect(buildStatusBarItems({ kind: "loading" }, { ...base, mode: "combined", visibility: "whenAnyProviderNearLimit" })).toEqual([]);
  });

  it("never uses an em dash anywhere in rendered text", () => {
    const states: DaemonState[] = [
      ok,
      { kind: "noDaemon", url: "u" },
      { kind: "auth", url: "u" },
      { kind: "remoteUnsupported", url: "u" },
      { kind: "error", url: "u", message: "m" },
      { kind: "loading" },
    ];
    for (const s of states) {
      for (const mode of ["combined", "perProvider"] as const) {
        for (const item of buildStatusBarItems(s, { ...base, mode })) {
          expect(item.text + item.tooltip).not.toMatch(/\u2014/);
        }
      }
    }
  });
});
