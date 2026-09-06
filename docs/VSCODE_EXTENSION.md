# VS Code Extension

The onWatch VS Code extension puts your AI quotas in the status bar. It is a thin client for the onWatch daemon: the daemon keeps polling providers and storing history, and the extension reads the same compact menubar API that the macOS menubar and the GNOME extension use.

Source lives in [`extensions/vscode`](../extensions/vscode). Tracking issue: [#117](https://github.com/onllm-dev/onWatch/issues/117).

## Install

### From the Marketplace or Open VSX

Search for "onWatch" (publisher `onllm-dev`) in the Extensions view, or run:

```bash
code --install-extension onllm-dev.onwatch
```

Open VSX (VSCodium, Gitpod, code-server):

```bash
codium --install-extension onllm-dev.onwatch
```

### From a .vsix

Download `onwatch-<version>.vsix` from the GitHub release tagged `vscode-v<version>`, then:

```bash
code --install-extension onwatch-<version>.vsix
```

Or in VS Code: Extensions view > `...` menu > **Install from VSIX...**.

### Build it yourself

```bash
cd extensions/vscode
npm ci
npm run build
npm run package     # writes onwatch-<version>.vsix
```

## Requirements

- onWatch daemon running on the same machine (install steps in the [README](../README.md)). Start it with `onwatch`.
- VS Code 1.90 or newer.

## How it finds the daemon

When `onwatch.daemonUrl` is empty (the default) the extension tries, in order:

1. `~/.onwatch/port` - a file containing a single port number (1-65535)
2. the `ONWATCH_PORT` environment variable
3. port `9211`

and always uses `http://127.0.0.1:<port>`. To point it elsewhere, set `onwatch.daemonUrl` to a full base URL. A base path is honoured (`http://host:9211/onwatch`), and a trailing slash is stripped.

## What the status bar shows

| Label | Meaning |
|---|---|
| `82%` | The tightest (highest percent used) quota across visible providers. Background turns to the theme's warning color at warning and error color at critical. |
| `82% · 2h14m` | Critical, plus time until that quota resets. |
| `Claude 62%` | Per-provider mode: one item per visible provider. |
| `onWatch: no daemon` | Nothing answered at the daemon URL. |
| `onWatch: sign in` | The daemon returned 401 or 403. Click to enter credentials. |
| `onWatch: remote unsupported` | A daemon at a non-localhost URL returned 404 for the compact API. |

Hover for a tooltip with one line per provider (`$(pass)` healthy, `$(warning)` warning, `$(error)` critical), the quota name, time to reset, when the data was last fetched, and links to open the dashboard, open the quick view, or refresh.

Click opens the quick view (`<daemonUrl>/menubar`), the same panel the macOS menubar shows. By default it opens inside VS Code with Simple Browser and falls back to your system browser when Simple Browser is not available.

## Commands

| Command | Description |
|---|---|
| `onWatch: Open Dashboard` | Open `<daemonUrl>/` per `onwatch.openIn`. |
| `onWatch: Open Quick View` | Open `<daemonUrl>/menubar` per `onwatch.openIn`. |
| `onWatch: Open Dashboard in External Browser` | Always use the system browser. |
| `onWatch: Refresh` | Poll the daemon now. |
| `onWatch: Set Daemon Credentials` | Prompt for username, then password. Password goes to VS Code secret storage, username to `onwatch.auth.username`. |
| `onWatch: Clear Daemon Credentials` | Remove both. |
| `onWatch: Show Logs` | Reveal the `onWatch` output channel. |

## Settings reference

All settings are under `onwatch.*`.

| Setting | Type | Default | Description |
|---|---|---|---|
| `daemonUrl` | string | `""` | Full base URL of the daemon. Empty means auto-discover (see above). |
| `statusBar.mode` | `combined` / `perProvider` | `combined` | One item with the tightest quota, or one item per provider. |
| `statusBar.visibility` | `always` / `whenAnyProviderNearLimit` / `never` | `always` | `whenAnyProviderNearLimit` shows the item only when a provider is at warning or worse, or when the daemon is unreachable. |
| `openIn` | `simpleBrowser` / `externalBrowser` | `simpleBrowser` | Where dashboard and quick view open. |
| `followDaemonSettings` | boolean | `true` | Take provider visibility, provider order, warning and critical thresholds, and refresh cadence from the daemon's menubar preferences (`Settings > Menubar` in the dashboard). Each of the four settings below overrides its daemon value only when you set it explicitly. When off, only the extension settings are used. |
| `providers` | string[] | `[]` | Provider IDs to show, in order (for example `["anthropic", "codex", "copilot"]`). Profile-scoped IDs such as `codex:work` work too, and a bare `codex` matches all Codex profiles. Empty means all providers the daemon marks visible. |
| `pollIntervalSeconds` | number | `60` | Poll interval, minimum 10. Fallback only: when following daemon settings, the daemon's `refresh_seconds` wins unless this is set explicitly. |
| `thresholds.warningPercent` | number | `70` | Warning threshold. Used only when the extension computes severity itself (not following daemon settings, or set explicitly); otherwise the daemon's computed status is trusted. |
| `thresholds.criticalPercent` | number | `90` | Critical threshold, same rules as above. |
| `notify` | `off` / `critical` / `warningAndCritical` | `critical` | Show a non-modal warning notification with an "Open dashboard" action when a quota enters that severity. Fires once per provider, quota and reset window - never repeatedly while it stays there. |
| `auth.username` | string | `""` | Username for HTTP Basic auth. The password is only in VS Code secret storage. |

Provider IDs match what the daemon reports on `/api/menubar/summary`: `anthropic`, `codex`, `copilot`, `gemini`, `antigravity`, `cursor`, `kimi`, `grok`, `moonshot`, `deepseek`, `openrouter`, `opencode`, `minimax`, `synthetic`, `zai`. Multi-profile providers appear as `codex:<profile>`.

## Privacy

The extension has zero telemetry. It only ever talks to the daemon URL it discovered or you configured.

## Troubleshooting

### `onWatch: no daemon`

The extension could not connect. Hover to see the URL it tried, then:

1. Start the daemon: run `onwatch` in a terminal (or `onwatch service start` if you installed it as a service).
2. Check the port. If the daemon logs `Starting web server port=9300`, either write `9300` to `~/.onwatch/port`, export `ONWATCH_PORT=9300`, or set `onwatch.daemonUrl` to `http://127.0.0.1:9300`.
3. Run `onWatch: Show Logs` to see the resolved URL and the exact error.

### `onWatch: remote unsupported`

You pointed `onwatch.daemonUrl` at a daemon on another machine and it returned 404 for `/api/menubar/summary`. Current daemon versions serve the compact menubar API only to loopback clients. Options:

- Run VS Code (or the VS Code server, for Remote SSH) on the same machine as the daemon so the request comes from `127.0.0.1`.
- Forward the daemon port over SSH (`ssh -L 9211:127.0.0.1:9211 host`) and leave `onwatch.daemonUrl` empty or set to `http://127.0.0.1:9211`.
- The full dashboard still opens from the status bar item and the `onWatch: Open Dashboard` command.

### `onWatch: sign in`

The daemon has dashboard authentication enabled and returned 401 or 403. Click the item (or run `onWatch: Set Daemon Credentials`) and enter the same username and password you use for the dashboard. Credentials are sent as HTTP Basic auth on every request. For a daemon on `127.0.0.1` no credentials are needed for the menubar endpoints.

### Percentages differ from the dashboard or menubar

With `onwatch.followDaemonSettings` on (default), the extension shows the same providers, order and thresholds as the macOS menubar. If you set `onwatch.providers` or a threshold explicitly, that value overrides the daemon's. Remove the setting to follow the daemon again.

### Nothing in the status bar

Check `onwatch.statusBar.visibility`. With `whenAnyProviderNearLimit`, the item is hidden while every provider is healthy. With `never`, nothing is shown but the commands still work.

## Releasing

The extension has its own release pipeline in `.github/workflows/vscode-extension.yml`, separate from the daemon's `release.yml`:

1. Bump `version` in `extensions/vscode/package.json` and add a `CHANGELOG.md` entry.
2. Commit and tag `vscode-v<version>` (for example `vscode-v0.1.0`), then push the tag.
3. The workflow verifies the tag matches `package.json`, runs lint, typecheck, tests and build, packages the `.vsix`, publishes to the Marketplace (`VSCE_PAT` secret) and Open VSX (`OVSX_TOKEN` secret, skipped when empty), and attaches the `.vsix` to a GitHub release for the tag.
