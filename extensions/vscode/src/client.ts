// HTTP client for the daemon's menubar API. No "vscode" import.
import { isLoopbackUrl } from "./discovery";
import type { Preferences, ProviderCard, QuotaMeter, Severity, Snapshot } from "./model";

export type DaemonErrorKind = "unreachable" | "timeout" | "auth" | "remoteUnsupported" | "notFound" | "http" | "invalidResponse";

export class DaemonError extends Error {
  constructor(
    public readonly kind: DaemonErrorKind,
    message: string,
    public readonly url: string,
    public readonly status?: number,
  ) {
    super(message);
    this.name = "DaemonError";
  }
}

export interface Credentials {
  username: string;
  password: string;
}

export interface ClientOptions {
  baseUrl: string;
  credentials?: Credentials;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

export const DEFAULT_TIMEOUT_MS = 3000;

export function basicAuthHeader(credentials: Credentials | undefined): string | undefined {
  if (!credentials || credentials.username === "") {
    return undefined;
  }
  return `Basic ${Buffer.from(`${credentials.username}:${credentials.password}`, "utf8").toString("base64")}`;
}

export function classifyError(err: unknown, url: string): DaemonError {
  if (err instanceof DaemonError) {
    return err;
  }
  const e = err as { name?: string; message?: string; cause?: { code?: string; message?: string } };
  if (e?.name === "AbortError" || e?.name === "TimeoutError") {
    return new DaemonError("timeout", `Timed out contacting ${url}`, url);
  }
  const detail = e?.cause?.code ?? e?.cause?.message ?? e?.message ?? String(err);
  return new DaemonError("unreachable", `Could not reach ${url} (${detail})`, url);
}

const SEVERITIES: ReadonlySet<string> = new Set(["healthy", "warning", "danger", "critical"]);

function asSeverity(v: unknown): Severity {
  return typeof v === "string" && SEVERITIES.has(v) ? (v as Severity) : "healthy";
}

function asNumber(v: unknown): number {
  const n = typeof v === "string" ? Number(v) : v;
  return typeof n === "number" && Number.isFinite(n) ? n : Number.NaN;
}

function asOptString(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

function normalizeQuota(raw: unknown): QuotaMeter | undefined {
  if (!raw || typeof raw !== "object") {
    return undefined;
  }
  const q = raw as Record<string, unknown>;
  const key = asOptString(q.key) ?? asOptString(q.label);
  if (!key) {
    return undefined;
  }
  return {
    key,
    label: asOptString(q.label) ?? key,
    display_value: asOptString(q.display_value) ?? "",
    percent: asNumber(q.percent),
    status: asSeverity(q.status),
    used: typeof q.used === "number" ? q.used : undefined,
    limit: typeof q.limit === "number" ? q.limit : undefined,
    format: asOptString(q.format),
    reset_at: asOptString(q.reset_at),
    time_until_reset: asOptString(q.time_until_reset),
    source: asOptString(q.source),
    age_seconds: typeof q.age_seconds === "number" ? q.age_seconds : undefined,
    is_stale: typeof q.is_stale === "boolean" ? q.is_stale : undefined,
  };
}

function normalizeProvider(raw: unknown): ProviderCard | undefined {
  if (!raw || typeof raw !== "object") {
    return undefined;
  }
  const p = raw as Record<string, unknown>;
  const id = asOptString(p.id);
  if (!id) {
    return undefined;
  }
  const quotas = Array.isArray(p.quotas) ? p.quotas.map(normalizeQuota).filter((q): q is QuotaMeter => q !== undefined) : [];
  return {
    id,
    base_provider: asOptString(p.base_provider) ?? id.split(":")[0],
    label: asOptString(p.label) ?? id,
    subtitle: asOptString(p.subtitle),
    status: asSeverity(p.status),
    highest_percent: asNumber(p.highest_percent),
    updated_at: asOptString(p.updated_at),
    quotas,
  };
}

export function normalizeSnapshot(raw: unknown, url: string): Snapshot {
  if (!raw || typeof raw !== "object" || !Array.isArray((raw as Record<string, unknown>).providers)) {
    throw new DaemonError("invalidResponse", `Unexpected summary payload from ${url}`, url);
  }
  const s = raw as Record<string, unknown>;
  const providers = (s.providers as unknown[]).map(normalizeProvider).filter((p): p is ProviderCard => p !== undefined);
  const agg = (s.aggregate ?? {}) as Record<string, unknown>;
  return {
    generated_at: asOptString(s.generated_at) ?? "",
    updated_ago: asOptString(s.updated_ago) ?? "",
    aggregate: {
      provider_count: typeof agg.provider_count === "number" ? agg.provider_count : providers.length,
      warning_count: typeof agg.warning_count === "number" ? agg.warning_count : 0,
      critical_count: typeof agg.critical_count === "number" ? agg.critical_count : 0,
      highest_percent: asNumber(agg.highest_percent),
      status: asSeverity(agg.status),
      label: asOptString(agg.label) ?? "",
    },
    providers,
  };
}

export class DaemonClient {
  private readonly baseUrl: string;
  private readonly authHeader: string | undefined;
  private readonly timeoutMs: number;
  private readonly fetchImpl: typeof fetch;

  constructor(options: ClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.authHeader = basicAuthHeader(options.credentials);
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.fetchImpl = options.fetchImpl ?? globalThis.fetch;
  }

  get url(): string {
    return this.baseUrl;
  }

  async fetchSummary(): Promise<Snapshot> {
    const raw = await this.getJson("/api/menubar/summary");
    return normalizeSnapshot(raw, this.baseUrl);
  }

  async fetchPreferences(): Promise<Preferences> {
    const raw = await this.getJson("/api/menubar/preferences");
    if (!raw || typeof raw !== "object") {
      throw new DaemonError("invalidResponse", `Unexpected preferences payload from ${this.baseUrl}`, this.baseUrl);
    }
    return raw as Preferences;
  }

  private async getJson(path: string): Promise<unknown> {
    const url = `${this.baseUrl}${path}`;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    const headers: Record<string, string> = { Accept: "application/json" };
    if (this.authHeader) {
      headers.Authorization = this.authHeader;
    }
    let response: Response;
    try {
      response = await this.fetchImpl(url, { method: "GET", headers, signal: controller.signal, redirect: "manual" });
    } catch (err) {
      throw classifyError(err, this.baseUrl);
    } finally {
      clearTimeout(timer);
    }

    if (response.status === 401 || response.status === 403) {
      throw new DaemonError("auth", `Daemon at ${this.baseUrl} requires authentication (HTTP ${response.status})`, this.baseUrl, response.status);
    }
    if (response.status === 404) {
      if (!isLoopbackUrl(this.baseUrl)) {
        throw new DaemonError("remoteUnsupported", `Daemon at ${this.baseUrl} only serves ${path} to localhost (HTTP 404)`, this.baseUrl, 404);
      }
      throw new DaemonError("notFound", `HTTP 404 for ${url} - check the base path and daemon version`, this.baseUrl, 404);
    }
    if (!response.ok) {
      throw new DaemonError("http", `HTTP ${response.status} from ${url}`, this.baseUrl, response.status);
    }
    try {
      return await response.json();
    } catch {
      throw new DaemonError("invalidResponse", `Non-JSON response from ${url}`, this.baseUrl, response.status);
    }
  }
}
