import type { Preferences, ProviderCard, QuotaMeter, Snapshot } from "../src/model";

export const NOW = Date.parse("2026-09-06T10:00:00Z");

/** Default quota mirrors the daemon's Anthropic 5-hour window at 85% (warning). */
export function quota(overrides: Partial<QuotaMeter> = {}): QuotaMeter {
  return {
    key: "five_hour",
    label: "5-Hour Limit",
    display_value: "85%",
    percent: 85,
    status: "warning",
    reset_at: new Date(NOW + (2 * 60 + 27) * 60_000).toISOString(),
    time_until_reset: "2h 27m",
    ...overrides,
  };
}

export function provider(overrides: Partial<ProviderCard> = {}): ProviderCard {
  return {
    id: "anthropic",
    base_provider: "anthropic",
    label: "Anthropic",
    status: "warning",
    highest_percent: 85,
    quotas: [quota()],
    ...overrides,
  };
}

export function snapshot(providers: ProviderCard[], overrides: Partial<Snapshot> = {}): Snapshot {
  const highest = Math.max(0, ...providers.map((p) => p.highest_percent));
  return {
    generated_at: new Date(NOW).toISOString(),
    updated_ago: "12s ago",
    aggregate: {
      provider_count: providers.length,
      warning_count: providers.filter((p) => p.status === "warning" || p.status === "danger").length,
      critical_count: providers.filter((p) => p.status === "critical").length,
      highest_percent: highest,
      status: "healthy",
      label: "All good",
    },
    providers,
    ...overrides,
  };
}

export function preferences(overrides: Partial<Preferences> = {}): Preferences {
  return {
    enabled: true,
    default_view: "standard",
    refresh_seconds: 30,
    providers_order: ["codex", "anthropic", "copilot"],
    visible_providers: ["anthropic", "codex"],
    warning_percent: 75,
    critical_percent: 92,
    ...overrides,
  };
}

/** Anthropic (5h 85% warning, two weekly quotas). */
export function anthropic(): ProviderCard {
  return provider({
    quotas: [
      quota(),
      quota({ key: "weekly_all", label: "Weekly All-Model", display_value: "20%", percent: 20, status: "healthy", time_until_reset: "5d 3h" }),
      quota({ key: "weekly_fable", label: "Weekly Fable", display_value: "13%", percent: 13, status: "healthy", time_until_reset: "5d 3h" }),
    ],
  });
}

/** Codex profile with a subtitle, everything healthy. */
export function codex(): ProviderCard {
  return provider({
    id: "codex:prakersh7",
    base_provider: "codex",
    label: "Codex - prakersh7",
    subtitle: "ChatGPT account",
    status: "healthy",
    highest_percent: 0,
    quotas: [quota({ key: "weekly", label: "Weekly All-Model", display_value: "0%", percent: 0, status: "healthy", time_until_reset: "13d 11h" })],
  });
}

/** Copilot premium requests at 95% critical with used/limit numbers. */
export function copilot(): ProviderCard {
  return provider({
    id: "copilot",
    base_provider: "copilot",
    label: "GitHub Copilot",
    status: "critical",
    highest_percent: 95,
    quotas: [
      quota({
        key: "premium",
        label: "Premium Requests",
        display_value: "95%",
        percent: 95,
        status: "critical",
        used: 285,
        limit: 300,
        time_until_reset: "2h 14m",
      }),
    ],
  });
}

/** Anthropic 85% warning, Codex 0% healthy, Copilot 95% critical. */
export function threeProviders(): ProviderCard[] {
  return [anthropic(), codex(), copilot()];
}
