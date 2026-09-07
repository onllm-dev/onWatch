import { randomBytes } from "node:crypto";
import * as vscode from "vscode";
import type { DaemonState } from "./model";
import { quickViewContent, renderQuickViewHtml, type QuickViewContent } from "./quickViewHtml";

/** View id from package.json; `${QUICK_VIEW_ID}.focus` is the command VS Code generates to reveal it. */
export const QUICK_VIEW_ID = "onwatch.quickView";

/**
 * Sidebar view hosting the daemon's own quick view page, so the panel the
 * macOS menubar shows sits next to the file explorer. Only the daemon state
 * decides what is rendered; the framed page keeps polling on its own, and the
 * HTML is only rewritten when the state kind or the URL changes, so the frame
 * is not reloaded on every extension poll.
 */
export class QuickViewProvider implements vscode.WebviewViewProvider, vscode.Disposable {
  private view: vscode.WebviewView | undefined;
  private state: DaemonState = { kind: "loading" };
  private url = "";
  private renderedKey = "";
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly log: (message: string) => void) {}

  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
    this.renderedKey = "";
    // Give the webview a document with a CSP before touching its options: an options
    // change reloads whatever HTML is current, and the initial empty document would
    // trip VS Code's missing-CSP check.
    view.webview.html = renderQuickViewHtml(quickViewContent({ kind: "loading" }, this.url), this.renderOptions(view));
    view.webview.options = { enableScripts: true, localResourceRoots: [] };
    this.disposables.push(
      view.webview.onDidReceiveMessage((message: unknown) => {
        const command = (message as { command?: unknown } | undefined)?.command;
        if (typeof command === "string" && command.startsWith("onwatch.")) {
          void vscode.commands.executeCommand(command);
        }
      }),
      view.onDidDispose(() => {
        if (this.view === view) {
          this.view = undefined;
          this.renderedKey = "";
        }
      }),
    );
    void this.render();
  }

  /** Called after every poll; cheap when nothing that affects the view changed. */
  update(state: DaemonState, quickViewUrl: string): void {
    this.state = state;
    this.url = quickViewUrl;
    void this.render();
  }

  private async render(): Promise<void> {
    const view = this.view;
    if (!view) {
      return;
    }
    let content: QuickViewContent = quickViewContent(this.state, this.url);
    const key = JSON.stringify(content);
    if (key === this.renderedKey) {
      return;
    }
    this.renderedKey = key;
    if (content.kind === "frame") {
      try {
        // Maps the daemon URL through VS Code's port forwarding in remote setups; a no-op locally.
        const external = await vscode.env.asExternalUri(vscode.Uri.parse(content.url, true));
        content = { kind: "frame", url: external.toString(true) };
      } catch (err) {
        this.log(`asExternalUri failed for ${content.url}: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
    if (this.view !== view || this.renderedKey !== key) {
      return; // superseded while awaiting
    }
    view.webview.html = renderQuickViewHtml(content, this.renderOptions(view));
  }

  private renderOptions(view: vscode.WebviewView): { cspSource: string; nonce: string } {
    return { cspSource: view.webview.cspSource, nonce: randomBytes(16).toString("hex") };
  }

  dispose(): void {
    for (const d of this.disposables) {
      d.dispose();
    }
    this.disposables.length = 0;
    this.view = undefined;
  }
}
