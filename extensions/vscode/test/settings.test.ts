import { describe, expect, it } from "vitest";
import { DEFAULT_SETTINGS, resolveEffectiveConfig, type ExtensionSettings } from "../src/settings";
import { preferences } from "./fixtures";

const settings = (overrides: Partial<ExtensionSettings> = {}): ExtensionSettings => ({ ...DEFAULT_SETTINGS, ...overrides });

describe("resolveEffectiveConfig - following daemon settings", () => {
  it("takes everything from the daemon preferences by default", () => {
    const cfg = resolveEffectiveConfig(settings(), new Set(), preferences());
    expect(cfg).toEqual({
      providersOrder: ["codex", "anthropic", "copilot"],
      visibleProviders: ["anthropic", "codex"],
      warningPercent: 75,
      criticalPercent: 92,
      pollIntervalSeconds: 30,
      recomputeStatus: false,
      source: "daemon",
    });
  });

  it("treats an empty daemon visible list as all", () => {
    const cfg = resolveEffectiveConfig(settings(), new Set(), preferences({ visible_providers: [] }));
    expect(cfg.visibleProviders).toBeUndefined();
  });

  it("lets an explicit providers setting override order and visibility", () => {
    const cfg = resolveEffectiveConfig(settings({ providers: ["copilot", "anthropic"] }), new Set(["providers"]), preferences());
    expect(cfg.providersOrder).toEqual(["copilot", "anthropic"]);
    expect(cfg.visibleProviders).toEqual(["copilot", "anthropic"]);
    expect(cfg.recomputeStatus).toBe(false);
  });

  it("ignores an explicit but empty providers setting", () => {
    const cfg = resolveEffectiveConfig(settings({ providers: [] }), new Set(["providers"]), preferences());
    expect(cfg.visibleProviders).toEqual(["anthropic", "codex"]);
  });

  it("does not let a non-explicit providers value override the daemon", () => {
    const cfg = resolveEffectiveConfig(settings({ providers: ["copilot"] }), new Set(), preferences());
    expect(cfg.visibleProviders).toEqual(["anthropic", "codex"]);
  });

  it("lets an explicit poll interval override refresh_seconds", () => {
    const cfg = resolveEffectiveConfig(settings({ pollIntervalSeconds: 120 }), new Set(["pollIntervalSeconds"]), preferences());
    expect(cfg.pollIntervalSeconds).toBe(120);
  });

  it("uses explicit thresholds and switches to local status computation", () => {
    const cfg = resolveEffectiveConfig(settings({ warningPercent: 50 }), new Set(["warningPercent"]), preferences());
    expect(cfg.warningPercent).toBe(50);
    expect(cfg.criticalPercent).toBe(92);
    expect(cfg.recomputeStatus).toBe(true);
  });

  it("falls back to extension settings when preferences are unavailable but keeps trusting daemon status", () => {
    const cfg = resolveEffectiveConfig(settings({ providers: ["codex"], pollIntervalSeconds: 45 }), new Set(), undefined);
    expect(cfg).toEqual({
      providersOrder: ["codex"],
      visibleProviders: ["codex"],
      warningPercent: 70,
      criticalPercent: 90,
      pollIntervalSeconds: 45,
      recomputeStatus: false,
      source: "extension",
    });
  });

  it("clamps the daemon refresh interval to the minimum", () => {
    const cfg = resolveEffectiveConfig(settings(), new Set(), preferences({ refresh_seconds: 2 }));
    expect(cfg.pollIntervalSeconds).toBe(10);
  });

  it("ignores nonsense daemon thresholds", () => {
    const cfg = resolveEffectiveConfig(settings(), new Set(), preferences({ warning_percent: 0, critical_percent: 400 }));
    expect(cfg.warningPercent).toBe(70);
    expect(cfg.criticalPercent).toBe(90);
  });
});

describe("resolveEffectiveConfig - not following daemon settings", () => {
  it("uses only the extension settings", () => {
    const cfg = resolveEffectiveConfig(
      settings({ followDaemonSettings: false, providers: ["anthropic"], warningPercent: 60, criticalPercent: 80, pollIntervalSeconds: 15 }),
      new Set(),
      preferences(),
    );
    expect(cfg).toEqual({
      providersOrder: ["anthropic"],
      visibleProviders: ["anthropic"],
      warningPercent: 60,
      criticalPercent: 80,
      pollIntervalSeconds: 15,
      recomputeStatus: true,
      source: "extension",
    });
  });

  it("shows all providers when the list is empty", () => {
    const cfg = resolveEffectiveConfig(settings({ followDaemonSettings: false }), new Set(), undefined);
    expect(cfg.providersOrder).toEqual([]);
    expect(cfg.visibleProviders).toBeUndefined();
  });

  it("enforces the minimum poll interval and sane thresholds", () => {
    const cfg = resolveEffectiveConfig(
      settings({ followDaemonSettings: false, pollIntervalSeconds: 1, warningPercent: 95, criticalPercent: 90 }),
      new Set(),
      undefined,
    );
    expect(cfg.pollIntervalSeconds).toBe(10);
    expect(cfg.warningPercent).toBe(90);
    expect(cfg.criticalPercent).toBe(90);
  });
});
