import * as vscode from "vscode";
import type { ColorTier, StatusBarItemView } from "./model";

const BASE_PRIORITY = 100;

function backgroundFor(tier: ColorTier): vscode.ThemeColor | undefined {
  switch (tier) {
    case "critical":
      return new vscode.ThemeColor("statusBarItem.errorBackground");
    case "warning":
      return new vscode.ThemeColor("statusBarItem.warningBackground");
    default:
      return undefined;
  }
}

/** Owns the set of status bar items and reconciles them against a rendered view. */
export class StatusBarController implements vscode.Disposable {
  private readonly items = new Map<string, vscode.StatusBarItem>();

  render(views: StatusBarItemView[]): void {
    const wanted = new Set(views.map((v) => v.id));
    for (const [id, item] of this.items) {
      if (!wanted.has(id)) {
        item.dispose();
        this.items.delete(id);
      }
    }
    views.forEach((view, index) => {
      let item = this.items.get(view.id);
      if (!item) {
        item = vscode.window.createStatusBarItem(`onwatch.${view.id}`, vscode.StatusBarAlignment.Right, BASE_PRIORITY - index);
        item.name = "onWatch";
        this.items.set(view.id, item);
      }
      item.text = view.text;
      const tooltip = new vscode.MarkdownString(view.tooltip, true);
      tooltip.isTrusted = true;
      tooltip.supportThemeIcons = true;
      item.tooltip = tooltip;
      item.command = view.command;
      item.backgroundColor = backgroundFor(view.tier);
      item.accessibilityInformation = { label: `onWatch: ${view.text.replace(/\$\([a-z-]+\)\s*/g, "")}` };
      item.show();
    });
  }

  dispose(): void {
    for (const item of this.items.values()) {
      item.dispose();
    }
    this.items.clear();
  }
}
