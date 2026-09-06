import * as vscode from "vscode";
import type { OpenIn } from "./settings";

export type Logger = (message: string) => void;

/** Open a URL in Simple Browser (when requested and available) or the external browser. */
export async function openUrl(url: string, mode: OpenIn, log: Logger): Promise<void> {
  if (mode === "simpleBrowser") {
    try {
      await vscode.commands.executeCommand("simpleBrowser.show", url);
      return;
    } catch (err) {
      log(`Simple Browser unavailable (${err instanceof Error ? err.message : String(err)}), opening externally: ${url}`);
    }
  }
  await openExternal(url);
}

export async function openExternal(url: string): Promise<void> {
  await vscode.env.openExternal(vscode.Uri.parse(url, true));
}
