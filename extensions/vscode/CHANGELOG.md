# Changelog

All notable changes to the onWatch VS Code extension are documented here.

## 2.14.1

Version bump only: the extension tracks the daemon's version and nothing in the
extension changed. The 2.14.1 daemon fixes the updater for pre-release builds
and retires tray icons whose daemon is gone.

## 2.14.0

Initial release. The extension shares the onWatch daemon's version number from here on.

- Combined status bar item showing the provider mark, compact limit name and percent of the tightest quota across enabled providers, with time to reset at critical.
- Provider marks shipped as a contributed icon font built from the dashboard's monochrome logos.
- Per-provider mode (`onwatch.statusBar.mode`).
- Theme-aware warning and critical colors.
- Markdown tooltip with one section per provider: a table of every limit with status, percent used, used/limit and time to reset, plus quick links to the quick view, dashboard and refresh.
- Sidebar quick view: an onWatch activity bar view frames the daemon's quick view page, with Refresh and Open Dashboard in its title bar and a notice with actions when the daemon is unreachable, needs a sign in, or is remote. A status bar click reveals it (`onwatch.openIn` defaults to `sidebar`).
- Quick view and dashboard can still open in Simple Browser or the external browser.
- Follows the daemon's menubar preferences (visibility, order, thresholds, refresh cadence), with explicit extension settings as overrides.
- Auto-discovery of the daemon via its port file (`~/.onwatch/port`; `%LOCALAPPDATA%\onwatch\port` on Windows), `ONWATCH_PORT`, then port 9211.
- Optional HTTP Basic auth with the password kept in VS Code secret storage.
- One-time threshold notifications per provider, quota and reset window.
- Clear status bar states for no daemon, sign in required and remote daemon unsupported.
- Zero telemetry.
