# VS Code Extension

The onWatch VS Code extension puts your AI quotas in the status bar and the compact quick view in the sidebar. It is a thin client for the onWatch daemon: the daemon keeps polling providers and storing history, and the extension reads the same compact menubar API that the macOS menubar and the GNOME extension use.

Source lives in [`extensions/vscode`](../extensions/vscode). Tracking issue: [#117](https://github.com/onllm-dev/onWatch/issues/117).

## Install

### From the Marketplace

Listing: https://marketplace.visualstudio.com/items?itemName=onllm-dev.onwatch

Search for "onWatch" (publisher `onllm-dev`) in the Extensions view, or run:

```bash
code --install-extension onllm-dev.onwatch
```

### From a .vsix

Every daemon release on GitHub ships `onwatch-vscode-<version>.vsix` next to the binaries (VSCodium and other forks without Marketplace access can use this). Download it, then:

```bash
code --install-extension onwatch-vscode-<version>.vsix
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

1. the port file the daemon writes on every start, containing a single port number (1-65535): `~/.onwatch/port` on macOS and Linux, `%LOCALAPPDATA%\onwatch\port` on Windows (with `~/.onwatch/port` as a second try)
2. the `ONWATCH_PORT` environment variable
3. port `9211`

and always uses `http://127.0.0.1:<port>`. To point it elsewhere, set `onwatch.daemonUrl` to a full base URL. A base path is honoured (`http://host:9211/onwatch`), and a trailing slash is stripped.

## What the status bar shows

| Label | Meaning |
|---|---|
| `<mark> 5h 85%` | Provider mark, compact limit name and percent of the tightest (highest percent used) quota across visible providers. Background turns to the theme's warning color at warning and error color at critical. |
| `<mark> 5h 92% · 2h 27m` | Critical, plus time until that quota resets. |
| `<mark> Weekly 0%` | Per-provider mode: one item per visible provider, each showing its own tightest limit. |
| `onWatch: no daemon` | Nothing answered at the daemon URL. |
| `onWatch: sign in` | The daemon returned 401 or 403. Click to enter credentials. |
| `onWatch: remote unsupported` | A daemon at a non-localhost URL returned 404 for the compact API. |

The mark is the same monochrome provider logo the dashboard uses. The extension ships them as an icon font (`media/onwatch-icons.woff`, built from `media/icons/*.svg` with `npm run icons`) and contributes them as theme icons named `onwatch-<provider>`. Limit names are shortened for the status bar: `5-Hour Limit` becomes `5h`, `Weekly All-Model` becomes `Weekly`, `Premium Requests` becomes `Premium`.

Hover for a tooltip with one section per provider, in the daemon's order. Each section has the provider mark and name, an italic account subtitle when there is one, and a table with one row per limit: status icon (`$(pass)` healthy, `$(warning)` warning, `$(error)` critical), limit name, percent used with used/limit when the daemon knows both, and time to reset. Missing values are left out rather than shown as placeholders. The footer shows when the data was last fetched and links to open the quick view, the dashboard, or refresh.

Click reveals the quick view, the same panel the macOS menubar shows.

## Sidebar quick view

The extension contributes an onWatch icon to the activity bar. Its view frames the daemon's quick view page (`<daemonUrl>/menubar`) so the panel sits next to the file explorer and stays visible while you work; the page keeps refreshing on its own. The view's title bar has Refresh and Open Dashboard buttons. When the daemon is unreachable, needs a sign in, or is remote, the view shows a short notice with the matching actions (retry, set credentials, open dashboard, show logs) instead of an empty frame.

The daemon allows this page, and only this page, to be framed by VS Code (it sends a `frame-ancestors` policy for the VS Code webview origins and skips `X-Frame-Options` there). Daemons older than 2.14.0 still send `X-Frame-Options: DENY` for every page, so the sidebar and Simple Browser show a blank frame with them; upgrade the daemon or set `onwatch.openIn` to `externalBrowser`.

Prefer a window? Set `onwatch.openIn` to `simpleBrowser` or `externalBrowser`; the sidebar view stays available but clicks open the quick view there instead.

## Commands

| Command | Description |
|---|---|
| `onWatch: Open Dashboard` | Open `<daemonUrl>/` per `onwatch.openIn`. |
| `onWatch: Open Quick View` | Reveal the sidebar quick view, or open `<daemonUrl>/menubar` per `onwatch.openIn`. |
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
| `openIn` | `sidebar` / `simpleBrowser` / `externalBrowser` | `sidebar` | Where the quick view and dashboard open. `sidebar` reveals the onWatch sidebar view for the quick view and uses Simple Browser for the dashboard. |
| `followDaemonSettings` | boolean | `true` | Take provider visibility, provider order, warning and critical thresholds, and refresh cadence from the daemon's menubar preferences (`Settings > Menubar` in the dashboard). Each of the four settings below overrides its daemon value only when you set it explicitly. When off, only the extension settings are used. |
| `providers` | string[] | `[]` | Provider IDs to show, in order (for example `["anthropic", "codex", "copilot"]`). Profile-scoped IDs such as `codex:work` work too, and a bare `codex` matches all Codex profiles. Empty means all providers the daemon marks visible. |
| `pollIntervalSeconds` | number | `60` | Poll interval, minimum 10. Fallback only: when following daemon settings, the daemon's `refresh_seconds` wins unless this is set explicitly. |
| `thresholds.warningPercent` | number | `70` | Warning threshold. Used only when the extension computes severity itself (not following daemon settings, or set explicitly); otherwise the daemon's computed status is trusted. |
| `thresholds.criticalPercent` | number | `90` | Critical threshold, same rules as above. |
| `notify` | `off` / `critical` / `warningAndCritical` | `critical` | Show a non-modal warning notification with an "Open dashboard" action when a quota enters that severity. Fires once per provider, quota and reset window - never repeatedly while it stays there. |
| `auth.username` | string | `""` | Username for HTTP Basic auth. The password is only in VS Code secret storage. |

Provider IDs match what the daemon reports on `/api/menubar/summary`: `anthropic`, `codex`, `copilot`, `gemini`, `antigravity`, `cursor`, `kimi`, `grok`, `moonshot`, `deepseek`, `openrouter`, `opencode`, `ollama`, `minimax`, `synthetic`, `zai`. Multi-profile providers appear as `codex:<profile>`.

## Privacy

The extension has zero telemetry. It only ever talks to the daemon URL it discovered or you configured.

## Troubleshooting

### `onWatch: no daemon`

The extension could not connect. Hover to see the URL it tried, then:

1. Start the daemon: run `onwatch` in a terminal (or `onwatch service start` if you installed it as a service).
2. Check the port. If the daemon logs `Starting web server port=9300`, either write `9300` to the port file (`~/.onwatch/port`, or `%LOCALAPPDATA%\onwatch\port` on Windows), export `ONWATCH_PORT=9300`, or set `onwatch.daemonUrl` to `http://127.0.0.1:9300`.
3. Run `onWatch: Show Logs` to see the resolved URL and the exact error.

### `onWatch: remote unsupported`

You pointed `onwatch.daemonUrl` at a daemon on another machine and it returned 404 for `/api/menubar/summary`. Current daemon versions serve the compact menubar API only to loopback clients. Options:

- Run VS Code (or the VS Code server, for Remote SSH) on the same machine as the daemon so the request comes from `127.0.0.1`.
- Forward the daemon port over SSH (`ssh -L 9211:127.0.0.1:9211 host`) and leave `onwatch.daemonUrl` empty or set to `http://127.0.0.1:9211`.
- The full dashboard still opens from the `onWatch: Open Dashboard` command and the sidebar view's title bar.

### `onWatch: sign in`

The daemon has dashboard authentication enabled and returned 401 or 403. Click the item (or run `onWatch: Set Daemon Credentials`) and enter the same username and password you use for the dashboard. Credentials are sent as HTTP Basic auth on every request. For a daemon on `127.0.0.1` no credentials are needed for the menubar endpoints.

### Percentages differ from the dashboard or menubar

With `onwatch.followDaemonSettings` on (default), the extension shows the same providers, order and thresholds as the macOS menubar. If you set `onwatch.providers` or a threshold explicitly, that value overrides the daemon's. Remove the setting to follow the daemon again.

### Nothing in the status bar

Check `onwatch.statusBar.visibility`. With `whenAnyProviderNearLimit`, the item is hidden while every provider is healthy. With `never`, nothing is shown but the commands still work.

## Releasing

The extension carries the onWatch daemon's version and ships with every daemon release. There is no separate extension release.

1. The daemon release pipeline (`release.yml`) runs `npm run version:sync` in `extensions/vscode`, which stamps `package.json` from the repo `VERSION` file, then lints, tests, builds and packages `onwatch-vscode-<version>.vsix` and attaches it to the GitHub release next to the binaries.
2. Preview builds (`preview.yml`, tags like `v2.14.0-beta.1`) do the same with the tag's `major.minor.patch`; the beta suffix appears only in the file name because the Marketplace accepts plain three-part versions only.
3. Marketplace publishing is manual for now: download the `.vsix` from the release, open https://marketplace.visualstudio.com/manage/publishers/onllm-dev and upload it. The Marketplace never accepts a version twice, so upload a given version once.
4. `.github/workflows/vscode-extension.yml` is CI only (lint, typecheck, test, build, package) for changes under `extensions/vscode`.

Add a `CHANGELOG.md` entry under the daemon version whenever the extension changes.
