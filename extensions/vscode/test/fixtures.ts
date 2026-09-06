import type { Preferences, ProviderCard, QuotaMeter, Snapshot } from "../src/model";

export const NOW = Date.parse("2026-09-06T10:00:00Z");

export function quota(overrides: Partial<QuotaMeter> = {}): QuotaMeter {
  return {
    key: "five_hour",
    label: "5h window",
    display_value: "62%",
    percent: 62,
    status: "healthy",
    reset_at: new Date(NOW + (2 * 60 + 14) * 60_000).toISOString(),
    time_until_reset: "2h 14m",
    ...overrides,
  };
}

export function provider(overrides: Partial<ProviderCard> = {}): ProviderCard {
  return {
    id: "anthropic",
    base_provider: "anthropic",
    label: "Claude",
    status: "healthy",
    highest_percent: 62,
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

/** Three providers: Claude 62% healthy, Codex 82% warning, Copilot 95% critical. */
export function threeProviders(): ProviderCard[] {
  return [
    provider(),
    provider({
      id: "codex",
      base_provider: "codex",
      label: "Codex",
      status: "warning",
      highest_percent: 82,
      quotas: [
        quota({ key: "weekly", label: "Weekly", display_value: "82%", percent: 82, status: "warning", time_until_reset: "3d 4h" }),
        quota({ key: "five_hour", label: "5h", display_value: "20%", percent: 20, status: "healthy", time_until_reset: "1h 2m" }),
      ],
    }),
    provider({
      id: "copilot",
      base_provider: "copilot",
      label: "GitHub Copilot",
      status: "critical",
      highest_percent: 95,
      quotas: [quota({ key: "premium", label: "Premium requests", display_value: "95%", percent: 95, status: "critical", time_until_reset: "2h 14m" })],
    }),
  ];
}
