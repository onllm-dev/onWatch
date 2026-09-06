import { describe, expect, it } from "vitest";
import { NotificationTracker } from "../src/notifications";
import { NOW, provider, quota } from "./fixtures";

const RESET_A = new Date(NOW + 3_600_000).toISOString();
const RESET_B = new Date(NOW + 7_200_000).toISOString();

function claude(percent: number, status: "healthy" | "warning" | "danger" | "critical", reset_at = RESET_A) {
  return provider({
    quotas: [quota({ percent, status, reset_at, time_until_reset: "59m", display_value: `${percent}%` })],
    status,
    highest_percent: percent,
  });
}

describe("NotificationTracker", () => {
  it("does nothing when off", () => {
    const t = new NotificationTracker();
    expect(t.evaluate([claude(10, "healthy")], "off")).toEqual([]);
    expect(t.evaluate([claude(95, "critical")], "off")).toEqual([]);
  });

  it("does not notify on the first observation, even if already critical", () => {
    const t = new NotificationTracker();
    expect(t.evaluate([claude(95, "critical")], "critical")).toEqual([]);
  });

  it("notifies once on the transition into critical", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "critical");
    const fired = t.evaluate([claude(95, "critical")], "critical");
    expect(fired).toHaveLength(1);
    expect(fired[0]).toMatchObject({ providerId: "anthropic", quotaKey: "five_hour", tier: "critical", percent: 95, resetAt: RESET_A });
    expect(fired[0].message).toBe("onWatch: Anthropic 5-Hour Limit is at 95% (critical) - resets in 59m");
    expect(fired[0].message).not.toMatch(/\u2014/);
    // stays critical: silent
    expect(t.evaluate([claude(97, "critical")], "critical")).toEqual([]);
    expect(t.evaluate([claude(99, "critical")], "critical")).toEqual([]);
  });

  it("does not repeat within the same reset window after dipping and re-entering", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "critical");
    expect(t.evaluate([claude(95, "critical")], "critical")).toHaveLength(1);
    t.evaluate([claude(85, "warning")], "critical");
    expect(t.evaluate([claude(96, "critical")], "critical")).toEqual([]);
  });

  it("notifies again in a new reset window", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "critical");
    expect(t.evaluate([claude(95, "critical")], "critical")).toHaveLength(1);
    t.evaluate([claude(5, "healthy", RESET_B)], "critical");
    expect(t.evaluate([claude(95, "critical", RESET_B)], "critical")).toHaveLength(1);
  });

  it("ignores warning transitions in critical mode", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "critical");
    expect(t.evaluate([claude(75, "warning")], "critical")).toEqual([]);
    expect(t.evaluate([claude(85, "danger")], "critical")).toEqual([]);
    expect(t.evaluate([claude(95, "critical")], "critical")).toHaveLength(1);
  });

  it("notifies on warning and then on critical in warningAndCritical mode", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "warningAndCritical");
    const w = t.evaluate([claude(75, "warning")], "warningAndCritical");
    expect(w).toHaveLength(1);
    expect(w[0].tier).toBe("warning");
    expect(w[0].message).toBe("onWatch: Anthropic 5-Hour Limit is at 75% (warning) - resets in 59m");
    // danger is still the warning tier: silent
    expect(t.evaluate([claude(85, "danger")], "warningAndCritical")).toEqual([]);
    const c = t.evaluate([claude(95, "critical")], "warningAndCritical");
    expect(c).toHaveLength(1);
    expect(c[0].tier).toBe("critical");
  });

  it("fires only the critical notification when jumping straight from healthy to critical", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "warningAndCritical");
    const fired = t.evaluate([claude(95, "critical")], "warningAndCritical");
    expect(fired).toHaveLength(1);
    expect(fired[0].tier).toBe("critical");
  });

  it("tracks quotas independently per provider and quota key", () => {
    const t = new NotificationTracker();
    const two = (a: number, aStatus: "healthy" | "critical", b: number, bStatus: "healthy" | "critical") => [
      provider({ id: "anthropic", quotas: [quota({ key: "five_hour", percent: a, status: aStatus }), quota({ key: "weekly", percent: b, status: bStatus })] }),
      provider({ id: "codex", label: "Codex", quotas: [quota({ key: "weekly", percent: b, status: bStatus })] }),
    ];
    t.evaluate(two(10, "healthy", 10, "healthy"), "critical");
    const fired = t.evaluate(two(10, "healthy", 95, "critical"), "critical");
    expect(fired.map((f) => `${f.providerId}/${f.quotaKey}`)).toEqual(["anthropic/weekly", "codex/weekly"]);
  });

  it("baselines newly appearing quotas without notifying", () => {
    const t = new NotificationTracker();
    t.evaluate([], "critical");
    expect(t.evaluate([claude(95, "critical")], "critical")).toEqual([]);
  });

  it("handles a missing reset_at by keying on an empty window", () => {
    const t = new NotificationTracker();
    const p = (percent: number, status: "healthy" | "critical") =>
      provider({ quotas: [quota({ percent, status, reset_at: undefined, time_until_reset: undefined })] });
    t.evaluate([p(10, "healthy")], "critical");
    const fired = t.evaluate([p(95, "critical")], "critical");
    expect(fired).toHaveLength(1);
    expect(fired[0].resetAt).toBeUndefined();
    expect(fired[0].message).toBe("onWatch: Anthropic 5-Hour Limit is at 95% (critical)");
    t.evaluate([p(10, "healthy")], "critical");
    expect(t.evaluate([p(95, "critical")], "critical")).toEqual([]);
  });

  it("reset() clears all state", () => {
    const t = new NotificationTracker();
    t.evaluate([claude(10, "healthy")], "critical");
    t.evaluate([claude(95, "critical")], "critical");
    t.reset();
    expect(t.evaluate([claude(95, "critical")], "critical")).toEqual([]);
  });
});
