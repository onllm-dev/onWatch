// Pure data model and formatting for the onWatch status bar.
// This module must not import "vscode" so it can be unit-tested under Node.
import { providerIcon } from "./icons";

export type Severity = "healthy" | "warning" | "danger" | "critical";
export type ColorTier = "none" | "warning" | "critical";
export type StatusBarMode = "combined" | "perProvider";
export type Visibility = "always" | "whenAnyProviderNearLimit" | "never";

export interface QuotaMeter {
  key: string;
  label: string;
  display_value: string;
  percent: number;
  status: Severity;
  used?: number;
  limit?: number;
  /** "currency" when used/limit are US dollars. */
  format?: string;
  reset_at?: string;
  time_until_reset?: string;
  source?: string;
  age_seconds?: number;
  is_stale?: boolean;
}

export interface ProviderCard {
  id: string;
  base_provider: string;
  label: string;
  subtitle?: string;
  status: Severity;
  highest_percent: number;
  updated_at?: string;
  quotas: QuotaMeter[];
}

export interface Aggregate {
  provider_count: number;
  warning_count: number;
  critical_count: number;
  highest_percent: number;
  status: Severity;
  label: string;
}

export interface Snapshot {
  generated_at: string;
  updated_ago: string;
  aggregate: Aggregate;
  providers: ProviderCard[];
}

export interface PreferenceProvider {
  id: string;
  base_provider?: string;
  label?: string;
  subtitle?: string;
  visible?: boolean;
  quotas?: { key: string; label: string }[];
}

export interface Preferences {
  enabled?: boolean;
  default_view?: string;
  refresh_seconds?: number;
  providers_order?: string[];
  visible_providers?: string[];
  warning_percent?: number;
  critical_percent?: number;
  status_display?: { mode?: string; selected_quotas?: { provider_id: string; quota_key: string }[] };
  theme?: string;
  providers?: PreferenceProvider[];
}

export interface TightestQuota {
  provider: ProviderCard;
  quota: QuotaMeter;
}

export const REPO_URL = "https://github.com/onllm-dev/onWatch";

const SEVERITY_RANK: Record<Severity, number> = { healthy: 0, warning: 1, danger: 2, critical: 3 };

export function severityRank(status: Severity | undefined): number {
  return status !== undefined && status in SEVERITY_RANK ? SEVERITY_RANK[status] : 0;
}

export function colorTier(status: Severity | undefined): ColorTier {
  switch (status) {
    case "critical":
      return "critical";
    case "warning":
    case "danger":
      return "warning";
    default:
      return "none";
  }
}

const TIER_RANK: Record<ColorTier, number> = { none: 0, warning: 1, critical: 2 };

export function tierRank(tier: ColorTier): number {
  return TIER_RANK[tier];
}

export function severityFromPercent(percent: number, warningPercent: number, criticalPercent: number): Severity {
  if (!Number.isFinite(percent)) {
    return "healthy";
  }
  if (percent >= criticalPercent) {
    return "critical";
  }
  if (percent >= warningPercent) {
    return "warning";
  }
  return "healthy";
}

export function statusIcon(status: Severity | undefined): string {
  switch (colorTier(status)) {
    case "critical":
      return "$(error)";
    case "warning":
      return "$(warning)";
    default:
      return "$(pass)";
  }
}

function maxSeverity(statuses: Severity[]): Severity {
  let best: Severity = "healthy";
  for (const s of statuses) {
    if (severityRank(s) > severityRank(best)) {
      best = s;
    }
  }
  return best;
}

/** Recompute quota and provider statuses locally from percent. Returns new objects. */
export function applyThresholds(providers: ProviderCard[], warningPercent: number, criticalPercent: number): ProviderCard[] {
  return providers.map((p) => {
    const quotas = p.quotas.map((q) => ({ ...q, status: severityFromPercent(q.percent, warningPercent, criticalPercent) }));
    return { ...p, quotas, status: maxSeverity(quotas.map((q) => q.status)) };
  });
}

function matchesId(p: ProviderCard, id: string): boolean {
  return p.id === id || p.base_provider === id;
}

/** Filter to `visible` (empty or undefined means all) and order by `order` (exact ids first, then base ids). */
export function selectProviders(providers: ProviderCard[], opts: { order?: string[]; visible?: string[] }): ProviderCard[] {
  const visible = opts.visible && opts.visible.length > 0 ? opts.visible : undefined;
  const filtered = visible ? providers.filter((p) => visible.some((id) => matchesId(p, id))) : providers.slice();
  const order = opts.order ?? [];
  if (order.length === 0) {
    return filtered;
  }
  const position = (p: ProviderCard): number => {
    const exact = order.indexOf(p.id);
    if (exact !== -1) {
      return exact;
    }
    const base = order.indexOf(p.base_provider);
    return base !== -1 ? base + 0.5 : Number.POSITIVE_INFINITY;
  };
  return filtered
    .map((p, index) => ({ p, index, pos: position(p) }))
    .sort((a, b) => a.pos - b.pos || a.index - b.index)
    .map((x) => x.p);
}

function isBetter(candidate: QuotaMeter, current: QuotaMeter | undefined): boolean {
  if (!current) {
    return true;
  }
  if (candidate.percent !== current.percent) {
    return candidate.percent > current.percent;
  }
  return severityRank(candidate.status) > severityRank(current.status);
}

export function providerTightestQuota(provider: ProviderCard): QuotaMeter | undefined {
  let best: QuotaMeter | undefined;
  for (const q of provider.quotas) {
    if (!Number.isFinite(q.percent)) {
      continue;
    }
    if (isBetter(q, best)) {
      best = q;
    }
  }
  return best;
}

export function tightestQuota(providers: ProviderCard[]): TightestQuota | undefined {
  let best: TightestQuota | undefined;
  for (const provider of providers) {
    const quota = providerTightestQuota(provider);
    if (quota && isBetter(quota, best?.quota)) {
      best = { provider, quota };
    }
  }
  return best;
}

export function formatPercent(percent: number): string {
  if (typeof percent !== "number" || !Number.isFinite(percent)) {
    return "-";
  }
  return `${Math.round(Math.max(0, percent))}%`;
}

/** "2h 14m" -> "2h14m" (used in notification text). */
export function compactDuration(text: string | undefined | null): string | undefined {
  if (typeof text !== "string") {
    return undefined;
  }
  const compact = text.replace(/\s+/g, "");
  return compact.length > 0 ? compact : undefined;
}

/** Duration from now until an RFC3339 timestamp in the daemon's spaced style ("2h 14m"), or undefined when past or invalid. */
export function durationUntil(iso: string | undefined, nowMs: number): string | undefined {
  if (!iso) {
    return undefined;
  }
  const target = Date.parse(iso);
  if (!Number.isFinite(target)) {
    return undefined;
  }
  const deltaMs = target - nowMs;
  if (deltaMs <= 0) {
    return undefined;
  }
  const totalMinutes = Math.floor(deltaMs / 60_000);
  if (totalMinutes < 1) {
    return "<1m";
  }
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) {
    return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  }
  return `${minutes}m`;
}

/** The daemon's own countdown text verbatim ("Resetting..." included), else computed from reset_at. */
export function resetIn(quota: QuotaMeter, nowMs: number): string | undefined {
  const text = typeof quota.time_until_reset === "string" ? quota.time_until_reset.trim() : "";
  if (text !== "") {
    return text;
  }
  return durationUntil(quota.reset_at, nowMs);
}

const COMPACT_LABEL_MAX = 14;
const DROPPED_WORDS = new Set(["limit", "limits", "all-model", "all-models", "requests", "window", "usage"]);

/**
 * Compact quota name for the status bar: "5-Hour Limit" -> "5h", "Weekly All-Model" -> "Weekly",
 * "Weekly Fable" -> "Fable weekly", "Claude + GPT Weekly" -> "Claude+GPT wk". Never longer than 14 chars.
 */
export function compactQuotaLabel(label: string | undefined, key: string | undefined): string {
  let text = (label ?? "").trim();
  if (text === "") {
    return (key ?? "").trim();
  }
  text = text.replace(/(\d+)[- ]?hours?\b/gi, "$1h");
  text = text.replace(/\bwkly\b/gi, "Weekly");
  text = text.replace(/\ball\s+models?\b/gi, "all-model");
  text = text.replace(/\s*\+\s*/g, "+");
  let words = text.split(/\s+/).filter((w) => w !== "" && !DROPPED_WORDS.has(w.toLowerCase()));
  if (words.length > 1) {
    words = words.filter((w) => w.toLowerCase() !== "general");
  }
  if (words.length === 2 && words[0].toLowerCase() === "weekly") {
    words = [words[1], "weekly"];
  }
  if (words.length === 1 && /^[a-z]/.test(words[0])) {
    words[0] = words[0].charAt(0).toUpperCase() + words[0].slice(1);
  }
  let out = words.join(" ");
  if (out.length > COMPACT_LABEL_MAX) {
    out = out.replace(/\s+weekly$/i, " wk");
  }
  if (out.length > COMPACT_LABEL_MAX) {
    out = out.slice(0, COMPACT_LABEL_MAX);
  }
  return out;
}

function quotaLabel(quota: QuotaMeter): string {
  return (quota.label || quota.key || "").trim();
}

/** `<mark> <compact quota> <percent>`, plus ` · <countdown>` when the quota is critical. */
function formatQuotaLabel(provider: ProviderCard, quota: QuotaMeter | undefined, nowMs: number): string {
  const icon = providerIcon(provider.base_provider, provider.id);
  if (!quota) {
    return `${icon} -`;
  }
  const parts = [icon];
  const compact = compactQuotaLabel(quota.label, quota.key);
  if (compact !== "") {
    parts.push(compact);
  }
  parts.push(formatPercent(quota.percent));
  let text = parts.join(" ");
  if (quota.status === "critical") {
    const reset = resetIn(quota, nowMs);
    if (reset) {
      // The middle dot is intentional here (requested by the issue author); everywhere else use "-".
      text += ` · ${reset}`;
    }
  }
  return text;
}

export function formatCombinedLabel(tightest: TightestQuota | undefined, nowMs: number): string {
  if (!tightest) {
    return `${providerIcon("all", "all")} -`;
  }
  return formatQuotaLabel(tightest.provider, tightest.quota, nowMs);
}

export function formatProviderLabel(provider: ProviderCard, nowMs: number): string {
  return formatQuotaLabel(provider, providerTightestQuota(provider), nowMs);
}

function escapeMarkdown(text: string): string {
  return text.replace(/([\\`*_{}[\]()#+!|<>~])/g, "\\$1");
}

function formatCount(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(1);
}

function formatUsd(value: number): string {
  return `$${value > 0 && value < 0.01 ? value.toFixed(3) : value.toFixed(2)}`;
}

function usedCell(quota: QuotaMeter): string {
  if (typeof quota.percent !== "number" || !Number.isFinite(quota.percent)) {
    return "-";
  }
  const { used, limit } = quota;
  const hasLimit = typeof used === "number" && typeof limit === "number" && Number.isFinite(used) && Number.isFinite(limit) && limit > 0;
  const displayValue = (quota.display_value ?? "").trim();
  let cell: string;
  if (!hasLimit && displayValue !== "" && !displayValue.endsWith("%")) {
    // The daemon already chose a non-percent reading (for example "$0.001 used"
    // when a currency quota has no known cap); a bare 0% would mislead.
    cell = escapeMarkdown(displayValue);
  } else {
    cell = formatPercent(quota.percent);
    if (hasLimit) {
      cell +=
        quota.format === "currency"
          ? ` (${formatUsd(used)}/${formatUsd(limit)})`
          : ` (${formatCount(used)}/${formatCount(limit)})`;
    }
  }
  return colorTier(quota.status) !== "none" ? `**${cell}**` : cell;
}

/** One provider: a heading with its mark, then a table with one row per limit. */
export function formatProviderSection(provider: ProviderCard, nowMs: number): string {
  const name = escapeMarkdown((provider.label || provider.id || "").trim());
  let heading = `### ${providerIcon(provider.base_provider, provider.id)} ${name}`;
  const subtitle = (provider.subtitle ?? "").trim();
  if (subtitle !== "") {
    heading += ` &nbsp; _${escapeMarkdown(subtitle)}_`;
  }
  if (provider.quotas.length === 0) {
    return `${heading}\n_No quota data_`;
  }
  const lines = [heading, "| Limit | Used | Resets in |", "|:--|--:|--:|"];
  for (const quota of provider.quotas) {
    let label = `${statusIcon(quota.status)} ${escapeMarkdown(quotaLabel(quota))}`;
    if (quota.is_stale) {
      label += " $(history)";
    }
    lines.push(`| ${label} | ${usedCell(quota)} | ${resetIn(quota, nowMs) ?? "-"} |`);
  }
  return lines.join("\n");
}

export function formatAgo(deltaMs: number): string {
  const seconds = Math.max(0, Math.floor(deltaMs / 1000));
  if (seconds < 5) {
    return "just now";
  }
  if (seconds < 60) {
    return `${seconds}s ago`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  return `${Math.floor(hours / 24)}d ago`;
}

export const TOOLTIP_LINKS =
  "[Quick view](command:onwatch.openQuickView) · [Dashboard](command:onwatch.openDashboard) · [Refresh](command:onwatch.refresh)";

export interface TooltipContext {
  fetchedAt: number;
  nowMs: number;
  daemonUpdatedAgo?: string;
}

function footer(ctx: TooltipContext): string {
  let updated = `Updated ${formatAgo(ctx.nowMs - ctx.fetchedAt)}`;
  const daemon = (ctx.daemonUpdatedAgo ?? "").trim();
  if (daemon !== "") {
    updated += ` (daemon: ${escapeMarkdown(daemon)})`;
  }
  return `${updated} · ${TOOLTIP_LINKS}`;
}

/** Provider sections in daemon order, a rule, then the freshness and action links. */
export function formatTooltip(providers: ProviderCard[], ctx: TooltipContext): string {
  const body = providers.length > 0 ? providers.map((p) => formatProviderSection(p, ctx.nowMs)).join("\n\n") : "No providers to show.";
  return `${body}\n\n---\n\n${footer(ctx)}`;
}

export type DaemonState =
  | { kind: "loading" }
  | { kind: "ok"; providers: ProviderCard[]; fetchedAt: number; daemonUpdatedAgo?: string }
  | { kind: "noDaemon"; url: string; message?: string }
  | { kind: "auth"; url: string }
  | { kind: "remoteUnsupported"; url: string }
  | { kind: "error"; url: string; message: string };

export interface StatusBarItemView {
  /** Stable id: "combined" or "provider:<id>". */
  id: string;
  text: string;
  tooltip: string;
  tier: ColorTier;
  command: string;
}

export interface StatusBarOptions {
  mode: StatusBarMode;
  visibility: Visibility;
  nowMs: number;
  url: string;
}

function anyNearLimit(providers: ProviderCard[]): boolean {
  return providers.some((p) => colorTier(p.status) !== "none" || p.quotas.some((q) => colorTier(q.status) !== "none"));
}

function problemItem(state: Exclude<DaemonState, { kind: "ok" | "loading" }>): StatusBarItemView {
  switch (state.kind) {
    case "noDaemon":
      return {
        id: "combined",
        text: "$(warning) onWatch: no daemon",
        tier: "none",
        command: "onwatch.refresh",
        tooltip: [
          `**onWatch daemon not reachable** at \`${state.url}\`${state.message ? ` (${escapeMarkdown(state.message)})` : ""}.`,
          "Start it by running `onwatch` in a terminal, or set `onwatch.daemonUrl` if it listens somewhere else.",
          `[Install and docs](${REPO_URL}) - [Retry](command:onwatch.refresh) - [Logs](command:onwatch.showLogs)`,
        ].join("\n\n"),
      };
    case "auth":
      return {
        id: "combined",
        text: "$(lock) onWatch: sign in",
        tier: "none",
        command: "onwatch.setCredentials",
        tooltip: [
          `**onWatch daemon at \`${state.url}\` requires authentication.**`,
          "Click to enter the dashboard username and password. The password is kept in VS Code secret storage.",
          "[Set credentials](command:onwatch.setCredentials) - [Retry](command:onwatch.refresh)",
        ].join("\n\n"),
      };
    case "remoteUnsupported":
      return {
        id: "combined",
        text: "$(warning) onWatch: remote unsupported",
        tier: "none",
        command: "onwatch.openDashboard",
        tooltip: [
          `**The daemon at \`${state.url}\` did not serve the compact quota API.**`,
          "This daemon version only serves the menubar API to localhost. Run VS Code on the same machine as the daemon, or point `onwatch.daemonUrl` at a local instance. The full dashboard still opens.",
          `[Open dashboard](command:onwatch.openDashboard) - [Docs](${REPO_URL})`,
        ].join("\n\n"),
      };
    case "error":
      return {
        id: "combined",
        text: "$(warning) onWatch: error",
        tier: "none",
        command: "onwatch.showLogs",
        tooltip: [
          `**onWatch could not read quotas from \`${state.url}\`.**`,
          escapeMarkdown(state.message),
          "[Show logs](command:onwatch.showLogs) - [Retry](command:onwatch.refresh)",
        ].join("\n\n"),
      };
  }
}

export function buildStatusBarItems(state: DaemonState, opts: StatusBarOptions): StatusBarItemView[] {
  if (opts.visibility === "never") {
    return [];
  }
  const nearOnly = opts.visibility === "whenAnyProviderNearLimit";

  if (state.kind === "loading") {
    return nearOnly ? [] : [{ id: "combined", text: `${providerIcon("all", "all")} ...`, tier: "none", command: "onwatch.openQuickView", tooltip: "onWatch: contacting daemon..." }];
  }
  if (state.kind !== "ok") {
    return [problemItem(state)];
  }
  if (nearOnly && !anyNearLimit(state.providers)) {
    return [];
  }

  const ctx: TooltipContext = { fetchedAt: state.fetchedAt, nowMs: opts.nowMs, daemonUpdatedAgo: state.daemonUpdatedAgo };
  if (opts.mode === "perProvider") {
    return state.providers.map((p) => {
      const quota = providerTightestQuota(p);
      return {
        id: `provider:${p.id}`,
        text: formatProviderLabel(p, opts.nowMs),
        tier: colorTier(quota?.status ?? p.status),
        command: "onwatch.openQuickView",
        tooltip: formatTooltip([p], ctx),
      };
    });
  }

  const tightest = tightestQuota(state.providers);
  return [
    {
      id: "combined",
      text: formatCombinedLabel(tightest, opts.nowMs),
      tier: colorTier(tightest?.quota.status),
      command: "onwatch.openQuickView",
      tooltip: formatTooltip(state.providers, ctx),
    },
  ];
}
