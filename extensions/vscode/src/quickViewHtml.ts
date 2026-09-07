// HTML for the sidebar quick view webview. No "vscode" import so it runs under vitest.
import type { DaemonState } from "./model";

export const REPO_URL = "https://github.com/onllm-dev/onWatch";

export interface QuickViewAction {
  /** An onwatch.* command the webview asks the extension to run. */
  command: string;
  label: string;
}

export type QuickViewContent =
  | { kind: "frame"; url: string }
  | { kind: "notice"; title: string; body: string; actions: QuickViewAction[] };

/**
 * Decide what the sidebar shows for a daemon state. The daemon's own quick view
 * page is framed as soon as the daemon answers; every other state gets a short
 * notice with the same actions the status bar tooltip offers.
 */
export function quickViewContent(state: DaemonState, quickViewUrl: string): QuickViewContent {
  switch (state.kind) {
    case "ok":
      return { kind: "frame", url: quickViewUrl };
    case "loading":
      return { kind: "notice", title: "Contacting the onWatch daemon...", body: quickViewUrl, actions: [] };
    case "noDaemon":
      return {
        kind: "notice",
        title: "onWatch daemon not reachable",
        body: `Nothing answered at ${state.url}${state.message ? ` (${state.message})` : ""}. Start it by running onwatch in a terminal, or set onwatch.daemonUrl if it listens somewhere else.`,
        actions: [
          { command: "onwatch.refresh", label: "Retry" },
          { command: "onwatch.showLogs", label: "Show logs" },
        ],
      };
    case "auth":
      return {
        kind: "notice",
        title: "Sign in to the onWatch daemon",
        body: `The daemon at ${state.url} requires authentication. Enter the dashboard username and password; the password is kept in VS Code secret storage.`,
        actions: [
          { command: "onwatch.setCredentials", label: "Set credentials" },
          { command: "onwatch.refresh", label: "Retry" },
        ],
      };
    case "remoteUnsupported":
      return {
        kind: "notice",
        title: "Remote daemon not supported",
        body: `The daemon at ${state.url} only serves the compact quota API to localhost. Run VS Code on the daemon's machine or forward its port. The full dashboard still opens.`,
        actions: [{ command: "onwatch.openDashboard", label: "Open dashboard" }],
      };
    case "error":
      return {
        kind: "notice",
        title: "onWatch could not read quotas",
        body: `${state.url}: ${state.message}`,
        actions: [
          { command: "onwatch.showLogs", label: "Show logs" },
          { command: "onwatch.refresh", label: "Retry" },
        ],
      };
  }
}

export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

/** scheme://host[:port] of a URL, the value a CSP frame-src directive needs. */
export function originOf(url: string): string {
  const parsed = new URL(url);
  return `${parsed.protocol}//${parsed.host}`;
}

export interface RenderOptions {
  /** webview.cspSource: the origin VS Code serves local webview resources from. */
  cspSource: string;
  /** Per-render random token that whitelists the inline style and script. */
  nonce: string;
}

const BASE_STYLE = `
  html, body { margin: 0; padding: 0; height: 100%; overflow: hidden; background: transparent; color: var(--vscode-foreground); font-family: var(--vscode-font-family); font-size: var(--vscode-font-size); }
  iframe { border: 0; width: 100%; height: 100%; display: block; }
  .notice { padding: 16px 20px; line-height: 1.5; }
  .notice h2 { margin: 0 0 8px; font-size: 1.05em; font-weight: 600; }
  .notice p { margin: 0 0 12px; opacity: 0.9; word-break: break-word; }
  .notice .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .notice button { border: 1px solid var(--vscode-button-border, transparent); border-radius: 2px; padding: 4px 12px; cursor: pointer; font: inherit; color: var(--vscode-button-foreground); background: var(--vscode-button-background); }
  .notice button:hover { background: var(--vscode-button-hoverBackground); }
  .notice button.secondary { color: var(--vscode-button-secondaryForeground); background: var(--vscode-button-secondaryBackground); }
  .notice button.secondary:hover { background: var(--vscode-button-secondaryHoverBackground); }
  .notice a { color: var(--vscode-textLink-foreground); }
`;

/** Full HTML document for the webview. Scripts and styles are nonce-pinned; only the daemon origin may be framed. */
export function renderQuickViewHtml(content: QuickViewContent, opts: RenderOptions): string {
  const frameSrc = content.kind === "frame" ? originOf(content.url) : "'none'";
  const csp = [
    "default-src 'none'",
    `style-src ${opts.cspSource} 'nonce-${opts.nonce}'`,
    `script-src 'nonce-${opts.nonce}'`,
    `frame-src ${frameSrc}`,
  ].join("; ");
  const body =
    content.kind === "frame"
      ? `<iframe src="${escapeHtml(content.url)}" title="onWatch quick view" allow="" referrerpolicy="no-referrer"></iframe>`
      : renderNotice(content);
  const script =
    content.kind === "notice" && content.actions.length > 0
      ? `<script nonce="${opts.nonce}">
  const vscode = acquireVsCodeApi();
  for (const button of document.querySelectorAll("button[data-command]")) {
    button.addEventListener("click", () => vscode.postMessage({ command: button.dataset.command }));
  }
</script>`
      : "";
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>onWatch</title>
<style nonce="${opts.nonce}">${BASE_STYLE}</style>
</head>
<body>
${body}
${script}
</body>
</html>`;
}

function renderNotice(content: Extract<QuickViewContent, { kind: "notice" }>): string {
  const buttons = content.actions
    .map((a, i) => `<button type="button" data-command="${escapeHtml(a.command)}"${i > 0 ? ' class="secondary"' : ""}>${escapeHtml(a.label)}</button>`)
    .join("");
  const actions = buttons !== "" ? `<div class="actions">${buttons}</div>` : "";
  return `<div class="notice"><h2>${escapeHtml(content.title)}</h2><p>${escapeHtml(content.body)}</p>${actions}<p><a href="${REPO_URL}">Install and docs</a></p></div>`;
}
