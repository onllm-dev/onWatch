import * as vscode from "vscode";
import { DaemonClient, DaemonError, type Credentials } from "./client";
import { discoverBaseUrl } from "./discovery";
import { applyThresholds, buildStatusBarItems, selectProviders, type DaemonState, type Preferences, type ProviderCard } from "./model";
import { NotificationTracker } from "./notifications";
import { openExternal, openQuickView, openUrl } from "./open";
import { QUICK_VIEW_ID, QuickViewProvider } from "./quickView";
import { DEFAULT_SETTINGS, resolveEffectiveConfig, type ExtensionSettings, type OverridableKey } from "./settings";
import { StatusBarController } from "./statusBar";

const CONFIG_SECTION = "onwatch";
const SECRET_PASSWORD_KEY = "onwatch.password";
const ERROR_POLL_SECONDS = 30;

interface LoadedSettings {
  settings: ExtensionSettings;
  explicit: Set<OverridableKey>;
}

function loadSettings(): LoadedSettings {
  const cfg = vscode.workspace.getConfiguration(CONFIG_SECTION);
  const get = <T>(key: string, fallback: T): T => cfg.get<T>(key, fallback);
  const settings: ExtensionSettings = {
    daemonUrl: get("daemonUrl", DEFAULT_SETTINGS.daemonUrl),
    statusBarMode: get("statusBar.mode", DEFAULT_SETTINGS.statusBarMode),
    visibility: get("statusBar.visibility", DEFAULT_SETTINGS.visibility),
    openIn: get("openIn", DEFAULT_SETTINGS.openIn),
    followDaemonSettings: get("followDaemonSettings", DEFAULT_SETTINGS.followDaemonSettings),
    providers: get("providers", DEFAULT_SETTINGS.providers),
    pollIntervalSeconds: get("pollIntervalSeconds", DEFAULT_SETTINGS.pollIntervalSeconds),
    warningPercent: get("thresholds.warningPercent", DEFAULT_SETTINGS.warningPercent),
    criticalPercent: get("thresholds.criticalPercent", DEFAULT_SETTINGS.criticalPercent),
    notify: get("notify", DEFAULT_SETTINGS.notify),
    username: get("auth.username", DEFAULT_SETTINGS.username),
  };
  const explicit = new Set<OverridableKey>();
  const explicitKeys: [OverridableKey, string][] = [
    ["providers", "providers"],
    ["pollIntervalSeconds", "pollIntervalSeconds"],
    ["warningPercent", "thresholds.warningPercent"],
    ["criticalPercent", "thresholds.criticalPercent"],
  ];
  for (const [key, path] of explicitKeys) {
    const info = cfg.inspect(path);
    if (
      info &&
      (info.globalValue !== undefined ||
        info.workspaceValue !== undefined ||
        info.workspaceFolderValue !== undefined ||
        info.globalLanguageValue !== undefined ||
        info.workspaceLanguageValue !== undefined ||
        info.workspaceFolderLanguageValue !== undefined)
    ) {
      explicit.add(key);
    }
  }
  return { settings, explicit };
}

class OnWatchController implements vscode.Disposable {
  private readonly statusBar = new StatusBarController();
  readonly quickView = new QuickViewProvider((m) => this.log(m));
  private readonly tracker = new NotificationTracker();
  private loaded: LoadedSettings = loadSettings();
  private baseUrl = "";
  private state: DaemonState = { kind: "loading" };
  private timer: NodeJS.Timeout | undefined;
  private polling = false;
  private disposed = false;
  private lastStateKind = "";

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly output: vscode.OutputChannel,
  ) {
    this.resolveUrl();
    this.render();
  }

  get dashboardUrl(): string {
    return `${this.baseUrl}/`;
  }

  get quickViewUrl(): string {
    return `${this.baseUrl}/menubar`;
  }

  get openIn() {
    return this.loaded.settings.openIn;
  }

  log(message: string): void {
    this.output.appendLine(`[${new Date().toISOString()}] ${message}`);
  }

  private resolveUrl(): void {
    const resolved = discoverBaseUrl(this.loaded.settings.daemonUrl);
    if (resolved.warning) {
      this.log(resolved.warning);
    }
    if (resolved.url !== this.baseUrl) {
      this.baseUrl = resolved.url;
      this.log(`Daemon URL: ${this.baseUrl} (from ${resolved.source})`);
    }
  }

  onConfigurationChanged(): void {
    this.loaded = loadSettings();
    this.resolveUrl();
    this.tracker.reset();
    this.render();
    void this.pollNow();
  }

  async pollNow(): Promise<void> {
    if (this.disposed) {
      return;
    }
    this.clearTimer();
    await this.poll();
  }

  private clearTimer(): void {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }

  private schedule(seconds: number): void {
    this.clearTimer();
    if (this.disposed) {
      return;
    }
    this.timer = setTimeout(() => void this.poll(), seconds * 1000);
  }

  private async credentials(): Promise<Credentials | undefined> {
    const username = this.loaded.settings.username.trim();
    if (username === "") {
      return undefined;
    }
    const password = (await this.context.secrets.get(SECRET_PASSWORD_KEY)) ?? "";
    return { username, password };
  }

  private async poll(): Promise<void> {
    if (this.polling || this.disposed) {
      return;
    }
    this.polling = true;
    const { settings, explicit } = this.loaded;
    let nextSeconds = Math.max(10, settings.pollIntervalSeconds);
    try {
      // Re-run discovery each poll so a daemon restarted on a different port is picked up.
      this.resolveUrl();
      const client = new DaemonClient({ baseUrl: this.baseUrl, credentials: await this.credentials() });

      let prefs: Preferences | undefined;
      if (settings.followDaemonSettings) {
        try {
          prefs = await client.fetchPreferences();
        } catch (err) {
          if (err instanceof DaemonError && (err.kind === "auth" || err.kind === "remoteUnsupported")) {
            throw err;
          }
          this.log(`Preferences unavailable, using extension settings: ${err instanceof Error ? err.message : String(err)}`);
        }
      }

      const snapshot = await client.fetchSummary();
      const effective = resolveEffectiveConfig(settings, explicit, prefs);
      nextSeconds = effective.pollIntervalSeconds;

      let providers: ProviderCard[] = selectProviders(snapshot.providers, {
        order: effective.providersOrder,
        visible: effective.visibleProviders,
      });
      if (effective.recomputeStatus) {
        providers = applyThresholds(providers, effective.warningPercent, effective.criticalPercent);
      }

      this.state = { kind: "ok", providers, fetchedAt: Date.now(), daemonUpdatedAgo: snapshot.updated_ago || undefined };
      this.logStateChange(`ok (${providers.length} providers, config from ${effective.source}, next poll in ${nextSeconds}s)`);
      this.notify(providers);
    } catch (err) {
      this.state = this.stateFromError(err);
      nextSeconds = Math.max(nextSeconds, ERROR_POLL_SECONDS);
      this.logStateChange(`${this.state.kind}: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      this.polling = false;
      this.render();
      this.schedule(nextSeconds);
    }
  }

  private stateFromError(err: unknown): DaemonState {
    const url = this.baseUrl;
    if (err instanceof DaemonError) {
      switch (err.kind) {
        case "unreachable":
        case "timeout":
          return { kind: "noDaemon", url, message: err.kind === "timeout" ? "timed out" : undefined };
        case "auth":
          return { kind: "auth", url };
        case "remoteUnsupported":
          return { kind: "remoteUnsupported", url };
        default:
          return { kind: "error", url, message: err.message };
      }
    }
    return { kind: "error", url, message: err instanceof Error ? err.message : String(err) };
  }

  private logStateChange(summary: string): void {
    const key = summary.replace(/\d+ providers.*$/, "");
    if (key !== this.lastStateKind) {
      this.lastStateKind = key;
      this.log(`State: ${summary}`);
    }
  }

  private notify(providers: ProviderCard[]): void {
    const pending = this.tracker.evaluate(providers, this.loaded.settings.notify);
    for (const n of pending) {
      this.log(`Notify: ${n.message}`);
      void vscode.window.showWarningMessage(n.message, "Open dashboard").then((choice) => {
        if (choice === "Open dashboard") {
          void vscode.commands.executeCommand("onwatch.openDashboard");
        }
      });
    }
  }

  private render(): void {
    const { settings } = this.loaded;
    const items = buildStatusBarItems(this.state, {
      mode: settings.statusBarMode,
      visibility: settings.visibility,
      nowMs: Date.now(),
      url: this.baseUrl,
    });
    this.statusBar.render(items);
    this.quickView.update(this.state, this.quickViewUrl);
  }

  async setCredentials(): Promise<void> {
    const username = await vscode.window.showInputBox({
      title: "onWatch daemon username",
      prompt: "Username for the onWatch dashboard (leave empty to disable auth)",
      value: this.loaded.settings.username,
      ignoreFocusOut: true,
    });
    if (username === undefined) {
      return;
    }
    if (username.trim() === "") {
      await this.clearCredentials();
      return;
    }
    const password = await vscode.window.showInputBox({
      title: "onWatch daemon password",
      prompt: `Password for ${username.trim()} (stored in VS Code secret storage)`,
      password: true,
      ignoreFocusOut: true,
    });
    if (password === undefined) {
      return;
    }
    await this.context.secrets.store(SECRET_PASSWORD_KEY, password);
    await vscode.workspace.getConfiguration(CONFIG_SECTION).update("auth.username", username.trim(), vscode.ConfigurationTarget.Global);
    this.log("Credentials updated.");
    // The configuration change event re-reads settings and polls.
  }

  async clearCredentials(): Promise<void> {
    await this.context.secrets.delete(SECRET_PASSWORD_KEY);
    await vscode.workspace.getConfiguration(CONFIG_SECTION).update("auth.username", undefined, vscode.ConfigurationTarget.Global);
    this.log("Credentials cleared.");
    void this.pollNow();
  }

  dispose(): void {
    this.disposed = true;
    this.clearTimer();
    this.statusBar.dispose();
    this.quickView.dispose();
  }
}

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel("onWatch");
  const controller = new OnWatchController(context, output);
  const log = (m: string) => controller.log(m);

  context.subscriptions.push(
    output,
    controller,
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration(CONFIG_SECTION)) {
        controller.onConfigurationChanged();
      }
    }),
    vscode.commands.registerCommand("onwatch.openDashboard", () => openUrl(controller.dashboardUrl, controller.openIn, log)),
    vscode.window.registerWebviewViewProvider(QUICK_VIEW_ID, controller.quickView),
    vscode.commands.registerCommand("onwatch.openQuickView", () => openQuickView(controller.quickViewUrl, controller.openIn, log)),
    vscode.commands.registerCommand("onwatch.openDashboardExternal", () => openExternal(controller.dashboardUrl)),
    vscode.commands.registerCommand("onwatch.refresh", () => controller.pollNow()),
    vscode.commands.registerCommand("onwatch.setCredentials", () => controller.setCredentials()),
    vscode.commands.registerCommand("onwatch.clearCredentials", () => controller.clearCredentials()),
    vscode.commands.registerCommand("onwatch.showLogs", () => output.show(true)),
  );

  log(`onWatch extension ${context.extension.packageJSON.version} activated.`);
  void controller.pollNow();
}

export function deactivate(): void {
  // Everything is disposed through context.subscriptions.
}
