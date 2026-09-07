// Settings model and daemon-preference merge logic. No "vscode" import.
import type { Preferences, StatusBarMode, Visibility } from "./model";

/** Where pages open: the quick view in the onWatch sidebar view, or any page in Simple Browser or the system browser. */
export type OpenIn = "sidebar" | "simpleBrowser" | "externalBrowser";
export type NotifyMode = "off" | "critical" | "warningAndCritical";

export interface ExtensionSettings {
  daemonUrl: string;
  statusBarMode: StatusBarMode;
  visibility: Visibility;
  openIn: OpenIn;
  followDaemonSettings: boolean;
  providers: string[];
  pollIntervalSeconds: number;
  warningPercent: number;
  criticalPercent: number;
  notify: NotifyMode;
  username: string;
}

/** Settings that may override daemon preferences when set explicitly by the user. */
export type OverridableKey = "providers" | "pollIntervalSeconds" | "warningPercent" | "criticalPercent";

export const DEFAULT_SETTINGS: ExtensionSettings = {
  daemonUrl: "",
  statusBarMode: "combined",
  visibility: "always",
  openIn: "sidebar",
  followDaemonSettings: true,
  providers: [],
  pollIntervalSeconds: 60,
  warningPercent: 70,
  criticalPercent: 90,
  notify: "critical",
  username: "",
};

export const MIN_POLL_SECONDS = 10;

export interface EffectiveConfig {
  providersOrder: string[];
  /** undefined means "all providers". */
  visibleProviders: string[] | undefined;
  warningPercent: number;
  criticalPercent: number;
  pollIntervalSeconds: number;
  /** true: compute quota severity locally from percent; false: trust the daemon's status fields. */
  recomputeStatus: boolean;
  source: "daemon" | "extension";
}

function clampPoll(seconds: number | undefined, fallback: number): number {
  const n = Number(seconds);
  if (!Number.isFinite(n) || n <= 0) {
    return Math.max(MIN_POLL_SECONDS, fallback);
  }
  return Math.max(MIN_POLL_SECONDS, Math.round(n));
}

function validPercent(n: number | undefined): number | undefined {
  return typeof n === "number" && Number.isFinite(n) && n >= 1 && n <= 100 ? n : undefined;
}

function saneThresholds(warning: number, critical: number): { warningPercent: number; criticalPercent: number } {
  const criticalPercent = validPercent(critical) ?? DEFAULT_SETTINGS.criticalPercent;
  let warningPercent = validPercent(warning) ?? DEFAULT_SETTINGS.warningPercent;
  if (warningPercent > criticalPercent) {
    warningPercent = criticalPercent;
  }
  return { warningPercent, criticalPercent };
}

function cleanIds(ids: string[] | undefined): string[] {
  return (ids ?? []).map((s) => String(s).trim()).filter((s) => s.length > 0);
}

export function resolveEffectiveConfig(
  settings: ExtensionSettings,
  explicit: ReadonlySet<OverridableKey>,
  prefs: Preferences | undefined,
): EffectiveConfig {
  const localProviders = cleanIds(settings.providers);
  const follow = settings.followDaemonSettings;

  if (!follow || !prefs) {
    return {
      providersOrder: localProviders,
      visibleProviders: localProviders.length > 0 ? localProviders : undefined,
      ...saneThresholds(settings.warningPercent, settings.criticalPercent),
      pollIntervalSeconds: clampPoll(settings.pollIntervalSeconds, DEFAULT_SETTINGS.pollIntervalSeconds),
      recomputeStatus: !follow,
      source: "extension",
    };
  }

  const daemonVisible = cleanIds(prefs.visible_providers);
  let providersOrder = cleanIds(prefs.providers_order);
  let visibleProviders: string[] | undefined = daemonVisible.length > 0 ? daemonVisible : undefined;
  if (explicit.has("providers") && localProviders.length > 0) {
    providersOrder = localProviders;
    visibleProviders = localProviders;
  }

  const warning = explicit.has("warningPercent") ? settings.warningPercent : (validPercent(prefs.warning_percent) ?? DEFAULT_SETTINGS.warningPercent);
  const critical = explicit.has("criticalPercent")
    ? settings.criticalPercent
    : (validPercent(prefs.critical_percent) ?? DEFAULT_SETTINGS.criticalPercent);
  const recomputeStatus = explicit.has("warningPercent") || explicit.has("criticalPercent");

  const pollIntervalSeconds = explicit.has("pollIntervalSeconds")
    ? clampPoll(settings.pollIntervalSeconds, DEFAULT_SETTINGS.pollIntervalSeconds)
    : clampPoll(prefs.refresh_seconds, settings.pollIntervalSeconds);

  return {
    providersOrder,
    visibleProviders,
    ...saneThresholds(warning, critical),
    pollIntervalSeconds,
    recomputeStatus,
    source: "daemon",
  };
}
