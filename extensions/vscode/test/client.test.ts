import { describe, expect, it, vi } from "vitest";
import { DaemonClient, DaemonError, basicAuthHeader, classifyError } from "../src/client";
import { snapshot, threeProviders, preferences } from "./fixtures";

type FetchImpl = typeof fetch;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function fakeFetch(handler: (url: string, init: RequestInit | undefined) => Response | Promise<Response>): FetchImpl {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => handler(String(input), init)) as unknown as FetchImpl;
}

describe("basicAuthHeader", () => {
  it("encodes user:pass", () => {
    expect(basicAuthHeader({ username: "admin", password: "s3cret" })).toBe("Basic YWRtaW46czNjcmV0");
  });

  it("returns undefined for empty credentials", () => {
    expect(basicAuthHeader(undefined)).toBeUndefined();
    expect(basicAuthHeader({ username: "", password: "x" })).toBeUndefined();
  });
});

describe("DaemonClient.fetchSummary", () => {
  it("fetches and parses the snapshot from the summary endpoint", async () => {
    const f = fakeFetch((url) => {
      expect(url).toBe("http://127.0.0.1:9211/api/menubar/summary");
      return jsonResponse(snapshot(threeProviders()));
    });
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: f });
    const s = await c.fetchSummary();
    expect(s.providers).toHaveLength(3);
    expect(s.providers[2].quotas[0].percent).toBe(95);
  });

  it("honours a base path", async () => {
    const f = fakeFetch((url) => {
      expect(url).toBe("http://host:9211/onwatch/api/menubar/preferences");
      return jsonResponse(preferences());
    });
    const c = new DaemonClient({ baseUrl: "http://host:9211/onwatch", fetchImpl: f });
    const p = await c.fetchPreferences();
    expect(p.refresh_seconds).toBe(30);
  });

  it("sends basic auth on every request when credentials are set", async () => {
    const f = fakeFetch((_url, init) => {
      const headers = init?.headers as Record<string, string>;
      expect(headers.Authorization).toBe("Basic YWRtaW46cHc=");
      return jsonResponse(snapshot([]));
    });
    const c = new DaemonClient({ baseUrl: "http://host:9211", fetchImpl: f, credentials: { username: "admin", password: "pw" } });
    await c.fetchSummary();
    expect(f).toHaveBeenCalledTimes(1);
  });

  it("does not send an Authorization header without credentials", async () => {
    const f = fakeFetch((_url, init) => {
      const headers = init?.headers as Record<string, string>;
      expect(headers.Authorization).toBeUndefined();
      return jsonResponse(snapshot([]));
    });
    await new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: f }).fetchSummary();
  });

  it("maps 401 and 403 to an auth error", async () => {
    for (const status of [401, 403]) {
      const c = new DaemonClient({ baseUrl: "http://host:9211", fetchImpl: fakeFetch(() => new Response("nope", { status })) });
      await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "auth", status });
    }
  });

  it("maps 404 on a remote URL to remoteUnsupported", async () => {
    const c = new DaemonClient({ baseUrl: "http://192.168.1.10:9211", fetchImpl: fakeFetch(() => new Response("", { status: 404 })) });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "remoteUnsupported", status: 404 });
  });

  it("maps 404 on a loopback URL to notFound", async () => {
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: fakeFetch(() => new Response("", { status: 404 })) });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "notFound", status: 404 });
  });

  it("maps other HTTP errors to http", async () => {
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: fakeFetch(() => new Response("boom", { status: 500 })) });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "http", status: 500 });
  });

  it("maps network failures to unreachable", async () => {
    const f = fakeFetch(() => {
      throw new TypeError("fetch failed");
    });
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: f });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "unreachable" });
  });

  it("maps aborts to timeout", async () => {
    const f = fakeFetch((_url, init) => {
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          const err = new Error("aborted");
          err.name = "AbortError";
          reject(err);
        });
      });
    });
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: f, timeoutMs: 5 });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "timeout" });
  });

  it("rejects malformed payloads", async () => {
    const c = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: fakeFetch(() => jsonResponse({ nope: true })) });
    await expect(c.fetchSummary()).rejects.toMatchObject({ kind: "invalidResponse" });
    const c2 = new DaemonClient({ baseUrl: "http://127.0.0.1:9211", fetchImpl: fakeFetch(() => new Response("<html>", { status: 200 })) });
    await expect(c2.fetchSummary()).rejects.toMatchObject({ kind: "invalidResponse" });
  });

  it("fills in missing optional arrays", async () => {
    const c = new DaemonClient({
      baseUrl: "http://127.0.0.1:9211",
      fetchImpl: fakeFetch(() => jsonResponse({ providers: [{ id: "x", label: "X", status: "healthy", highest_percent: 0 }] })),
    });
    const s = await c.fetchSummary();
    expect(s.providers[0].quotas).toEqual([]);
    expect(s.providers[0].base_provider).toBe("x");
  });

  it("never includes credentials in error messages", async () => {
    const c = new DaemonClient({
      baseUrl: "http://host:9211",
      fetchImpl: fakeFetch(() => new Response("", { status: 500 })),
      credentials: { username: "admin", password: "hunter2" },
    });
    try {
      await c.fetchSummary();
    } catch (e) {
      expect(String((e as Error).message)).not.toContain("hunter2");
      expect(String((e as Error).message)).not.toContain("admin");
    }
  });
});

describe("classifyError", () => {
  it("passes DaemonError through", () => {
    const e = new DaemonError("unreachable", "x", "http://u");
    expect(classifyError(e, "http://u")).toBe(e);
  });

  it("wraps unknown errors as unreachable", () => {
    const e = classifyError(new Error("ECONNREFUSED"), "http://u");
    expect(e.kind).toBe("unreachable");
    expect(e.message).toContain("ECONNREFUSED");
  });
});
