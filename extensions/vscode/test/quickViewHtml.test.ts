import { describe, expect, it } from "vitest";
import type { DaemonState } from "../src/model";
import { escapeHtml, originOf, quickViewContent, renderQuickViewHtml } from "../src/quickViewHtml";

const URL = "http://127.0.0.1:9211/menubar";
const OPTS = { cspSource: "https://file+.vscode-resource.vscode-cdn.net", nonce: "abc123" };

describe("quickViewContent", () => {
  it("frames the daemon page once the daemon answers", () => {
    const state: DaemonState = { kind: "ok", providers: [], fetchedAt: 0 };
    expect(quickViewContent(state, URL)).toEqual({ kind: "frame", url: URL });
  });

  it("shows a notice with retry for an unreachable daemon", () => {
    const c = quickViewContent({ kind: "noDaemon", url: "http://127.0.0.1:9211", message: "ECONNREFUSED" }, URL);
    expect(c.kind).toBe("notice");
    if (c.kind === "notice") {
      expect(c.body).toContain("ECONNREFUSED");
      expect(c.actions.map((a) => a.command)).toEqual(["onwatch.refresh", "onwatch.showLogs"]);
    }
  });

  it("offers credentials when the daemon wants a sign in", () => {
    const c = quickViewContent({ kind: "auth", url: "http://host:9211" }, URL);
    expect(c.kind === "notice" && c.actions[0].command).toBe("onwatch.setCredentials");
  });

  it("only offers the dashboard for a remote daemon and logs for errors", () => {
    const remote = quickViewContent({ kind: "remoteUnsupported", url: "http://host:9211" }, URL);
    expect(remote.kind === "notice" && remote.actions.map((a) => a.command)).toEqual(["onwatch.openDashboard"]);
    const error = quickViewContent({ kind: "error", url: "http://host:9211", message: "boom" }, URL);
    expect(error.kind === "notice" && error.actions[0].command).toBe("onwatch.showLogs");
    expect(error.kind === "notice" && error.body).toContain("boom");
  });

  it("shows a quiet notice with no buttons while loading", () => {
    const c = quickViewContent({ kind: "loading" }, URL);
    expect(c.kind === "notice" && c.actions).toEqual([]);
  });
});

describe("renderQuickViewHtml", () => {
  it("pins the frame to the daemon origin and everything else to the nonce", () => {
    const html = renderQuickViewHtml({ kind: "frame", url: URL }, OPTS);
    expect(html).toContain(`<iframe src="${URL}"`);
    expect(html).toContain("frame-src http://127.0.0.1:9211");
    expect(html).toContain("default-src 'none'");
    expect(html).toContain(`style-src ${OPTS.cspSource} 'nonce-abc123'`);
    expect(html).toContain("script-src 'nonce-abc123'");
    expect(html).not.toContain("<script"); // nothing to run for a plain frame
  });

  it("renders notice buttons that post their command, and blocks frames", () => {
    const html = renderQuickViewHtml(quickViewContent({ kind: "noDaemon", url: "http://127.0.0.1:9211" }, URL), OPTS);
    expect(html).toContain("frame-src 'none'");
    expect(html).not.toContain("<iframe");
    expect(html).toContain('data-command="onwatch.refresh">Retry</button>');
    expect(html).toContain('data-command="onwatch.showLogs"');
    expect(html).toContain('<script nonce="abc123">');
    expect(html).toContain("acquireVsCodeApi()");
    expect(html).toContain("onWatch daemon not reachable");
  });

  it("escapes daemon-provided text", () => {
    const html = renderQuickViewHtml(quickViewContent({ kind: "error", url: "http://h", message: "<img src=x onerror=alert(1)>" }, URL), OPTS);
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
  });
});

describe("helpers", () => {
  it("escapeHtml covers the five characters", () => {
    expect(escapeHtml(`<a href="x">'&'</a>`)).toBe("&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;");
  });

  it("originOf keeps scheme, host and port only", () => {
    expect(originOf("http://127.0.0.1:9211/onwatch/menubar?view=minimal")).toBe("http://127.0.0.1:9211");
    expect(originOf("https://quota.example.com/menubar")).toBe("https://quota.example.com");
  });
});
