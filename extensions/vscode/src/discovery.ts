// Daemon URL discovery. No "vscode" import so it can be unit-tested under Node.
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export const DEFAULT_PORT = 9211;
export const LOOPBACK_HOST = "127.0.0.1";

export type UrlSource = "setting" | "portFile" | "env" | "default";

export interface ResolvedUrl {
  url: string;
  source: UrlSource;
  /** Set when `daemonUrl` was provided but could not be used. */
  warning?: string;
}

export function parsePort(text: string | undefined | null): number | undefined {
  if (text === undefined || text === null) {
    return undefined;
  }
  const trimmed = text.trim();
  if (!/^\d{1,5}$/.test(trimmed)) {
    return undefined;
  }
  const port = Number(trimmed);
  return port >= 1 && port <= 65535 ? port : undefined;
}

/** Validate an http(s) URL, drop query/fragment, strip trailing slashes, keep any base path. */
export function normalizeDaemonUrl(input: string | undefined): string | undefined {
  const trimmed = (input ?? "").trim();
  if (trimmed === "") {
    return undefined;
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return undefined;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return undefined;
  }
  const path = parsed.pathname.replace(/\/+$/, "");
  return `${parsed.protocol}//${parsed.host}${path}`;
}

export function resolveBaseUrl(opts: { daemonUrl?: string; portFileContents?: string | null; envPort?: string }): ResolvedUrl {
  let warning: string | undefined;
  const raw = (opts.daemonUrl ?? "").trim();
  if (raw !== "") {
    const normalized = normalizeDaemonUrl(raw);
    if (normalized) {
      return { url: normalized, source: "setting" };
    }
    warning = `Ignoring invalid onwatch.daemonUrl "${raw}" - expected an http(s) URL. Falling back to auto-discovery.`;
  }
  const withWarning = (r: ResolvedUrl): ResolvedUrl => (warning ? { ...r, warning } : r);

  const filePort = parsePort(opts.portFileContents);
  if (filePort !== undefined) {
    return withWarning({ url: `http://${LOOPBACK_HOST}:${filePort}`, source: "portFile" });
  }
  const envPort = parsePort(opts.envPort);
  if (envPort !== undefined) {
    return withWarning({ url: `http://${LOOPBACK_HOST}:${envPort}`, source: "env" });
  }
  return withWarning({ url: `http://${LOOPBACK_HOST}:${DEFAULT_PORT}`, source: "default" });
}

export function isLoopbackUrl(url: string): boolean {
  let host: string;
  try {
    host = new URL(url).hostname.toLowerCase();
  } catch {
    return false;
  }
  if (host === "localhost" || host === "[::1]" || host === "::1") {
    return true;
  }
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host);
}

/**
 * Where the daemon writes its port file. It sits next to the daemon's PID file:
 * `%LOCALAPPDATA%\\onwatch\\port` on Windows, `~/.onwatch/port` elsewhere. The
 * home-directory path is still tried on Windows for daemons started with an
 * empty LOCALAPPDATA.
 */
export function portFileCandidates(platform: string = process.platform, env: NodeJS.ProcessEnv = process.env, home: string = homedir()): string[] {
  const paths: string[] = [];
  if (platform === "win32") {
    const local = (env.LOCALAPPDATA ?? "").trim();
    if (local !== "") {
      paths.push(join(local, "onwatch", "port"));
    }
  }
  paths.push(join(home, ".onwatch", "port"));
  return paths;
}

export function portFilePath(home: string = homedir()): string {
  return join(home, ".onwatch", "port");
}

/** Contents of the first readable port file, or undefined when none exists. */
export function readPortFile(home: string = homedir(), env: NodeJS.ProcessEnv = process.env, platform: string = process.platform): string | undefined {
  for (const path of portFileCandidates(platform, env, home)) {
    try {
      return readFileSync(path, "utf8");
    } catch {
      // try the next location
    }
  }
  return undefined;
}

export function discoverBaseUrl(
  daemonUrl: string,
  env: NodeJS.ProcessEnv = process.env,
  home: string = homedir(),
  platform: string = process.platform,
): ResolvedUrl {
  return resolveBaseUrl({ daemonUrl, portFileContents: readPortFile(home, env, platform), envPort: env.ONWATCH_PORT });
}
