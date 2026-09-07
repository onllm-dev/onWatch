# onWatch for VS Code

AI quota tracking in your status bar - Anthropic, Codex, Copilot, Gemini and more, powered by the [onWatch](https://github.com/onllm-dev/onWatch) daemon.

The extension is a thin client. It polls the onWatch daemon running on your machine and shows the tightest quota in the status bar. All provider polling, storage and history stay in the daemon.

## What you get

- Status bar item showing the provider mark, the limit and the highest-used quota across your providers, for example `<Anthropic mark> 5h 85%`. At critical it adds the time to reset: `5h 92% · 2h 27m`.
- Warning and critical colors that follow your VS Code theme.
- Hover tooltip with one section per provider: its mark and name, then a table of every limit with status icon, percent used (and used/limit when known) and time to reset.
- A quick view in the sidebar: an onWatch activity bar icon opens the same compact panel the macOS menubar shows, next to the file explorer, so it stays visible while you work. A status bar click reveals it. Simple Browser and the system browser remain options.
- Optional per-provider items instead of one combined item.
- Optional one-time notification when a quota crosses into warning or critical.
- Follows the daemon's own menubar preferences (provider order, visibility, thresholds, refresh cadence) so everything stays in sync with the dashboard.

## Install

From the [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=onllm-dev.onwatch), or:

```bash
code --install-extension onllm-dev.onwatch
```

Every [onWatch release](https://github.com/onllm-dev/onWatch/releases) also ships the extension as `onwatch-vscode-<version>.vsix` for editors without Marketplace access.

## Requirements

- The onWatch daemon, running locally. Install it from the [onWatch repository](https://github.com/onllm-dev/onWatch) and start it with `onwatch`.
- VS Code 1.90 or newer.

The extension auto-discovers the daemon: it reads the port file the daemon writes (`~/.onwatch/port` on macOS and Linux, `%LOCALAPPDATA%\onwatch\port` on Windows), then the `ONWATCH_PORT` environment variable, then falls back to `http://127.0.0.1:9211`. Set `onwatch.daemonUrl` if your daemon listens somewhere else.

## Commands

All commands live under the `onWatch` category in the Command Palette.

| Command | What it does |
|---|---|
| `onWatch: Open Dashboard` | Opens the full dashboard (Simple Browser or external, see `onwatch.openIn`). |
| `onWatch: Open Quick View` | Reveals the quick view in the onWatch sidebar (or opens it per `onwatch.openIn`). This is also the status bar click action. |
| `onWatch: Open Dashboard in External Browser` | Always opens the dashboard in your system browser. |
| `onWatch: Refresh` | Polls the daemon now. |
| `onWatch: Set Daemon Credentials` | Prompts for username and password for a daemon with authentication enabled. The password is stored in VS Code secret storage. |
| `onWatch: Clear Daemon Credentials` | Removes the stored username and password. |
| `onWatch: Show Logs` | Reveals the `onWatch` output channel. |

## Settings

| Setting | Default | Description |
|---|---|---|
| `onwatch.daemonUrl` | `""` | Full base URL of the daemon. Empty means auto-discover. A base path such as `http://host:9211/onwatch` is honoured. |
| `onwatch.statusBar.mode` | `combined` | `combined` (one item, tightest quota) or `perProvider` (one item per provider). |
| `onwatch.statusBar.visibility` | `always` | `always`, `whenAnyProviderNearLimit`, or `never`. |
| `onwatch.openIn` | `sidebar` | `sidebar` (quick view in the onWatch sidebar view, dashboard in Simple Browser), `simpleBrowser` (falls back to external) or `externalBrowser`. |
| `onwatch.followDaemonSettings` | `true` | Use the daemon's menubar preferences for provider visibility, order, thresholds and refresh cadence. Explicitly set extension settings override individual values. |
| `onwatch.providers` | `[]` | Provider IDs to show, in order. Empty means all visible providers. |
| `onwatch.pollIntervalSeconds` | `60` | Poll interval in seconds (minimum 10). Fallback when not following daemon settings. |
| `onwatch.thresholds.warningPercent` | `70` | Warning threshold, used only when the extension computes severity itself. |
| `onwatch.thresholds.criticalPercent` | `90` | Critical threshold, used only when the extension computes severity itself. |
| `onwatch.notify` | `critical` | `off`, `critical`, or `warningAndCritical`. Notifies once per provider, quota and reset window. |
| `onwatch.auth.username` | `""` | Username for HTTP Basic auth. The password lives in secret storage only. |

See [docs/VSCODE_EXTENSION.md](https://github.com/onllm-dev/onWatch/blob/main/docs/VSCODE_EXTENSION.md) for the full reference and troubleshooting.

## Status bar states

| Label | Meaning |
|---|---|
| `<mark> 5h 85%` | Provider mark, compact limit name and percent of the tightest quota across visible providers. Colored at warning and critical. |
| `<mark> 5h 92% · 2h 27m` | Critical, with time until that quota resets. |
| `onWatch: no daemon` | The daemon could not be reached at the discovered URL. Start it with `onwatch`. |
| `onWatch: sign in` | The daemon requires authentication. Click to enter credentials. |
| `onWatch: remote unsupported` | The daemon at a non-local URL does not expose the compact quota API (it only serves it to localhost in this version). |

## Privacy

Zero telemetry. The extension talks only to the onWatch daemon URL you configure or that it discovers on localhost. It sends nothing anywhere else, collects no usage data, and stores only the optional daemon username (settings) and password (VS Code secret storage).

## Development

```bash
cd extensions/vscode
npm ci
npm run test        # vitest unit tests for the pure modules
npm run lint
npm run typecheck
npm run build       # bundles dist/extension.js with esbuild
npm run package     # produces onwatch-<version>.vsix
```

Press F5 in VS Code with this folder open to launch an Extension Development Host.

## License

GPL-3.0-only, the same as the onWatch daemon. See [LICENSE](./LICENSE).
