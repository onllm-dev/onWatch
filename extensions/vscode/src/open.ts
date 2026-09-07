import * as vscode from "vscode";
import { QUICK_VIEW_ID } from "./quickView";
import type { OpenIn } from "./settings";

export type Logger = (message: string) => void;

/** Open a URL in Simple Browser (when requested and available) or the external browser. */
export async function openUrl(url: string, mode: OpenIn, log: Logger): Promise<void> {
  if (mode !== "externalBrowser") {
    try {
      await vscode.commands.executeCommand("simpleBrowser.show", url);
      return;
    } catch (err) {
      log(`Simple Browser unavailable (${err instanceof Error ? err.message : String(err)}), opening externally: ${url}`);
    }
  }
  await openExternal(url);
}

/** Reveal the sidebar quick view, or open the quick view page like any other URL when the user prefers a browser. */
export async function openQuickView(url: string, mode: OpenIn, log: Logger): Promise<void> {
  if (mode === "sidebar") {
    await vscode.commands.executeCommand(`${QUICK_VIEW_ID}.focus`);
    return;
  }
  await openUrl(url, mode, log);
}

export async function openExternal(url: string): Promise<void> {
  await vscode.env.openExternal(vscode.Uri.parse(url, true));
}
