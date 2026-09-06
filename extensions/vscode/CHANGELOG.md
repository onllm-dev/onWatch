# Changelog

All notable changes to the onWatch VS Code extension are documented here.

## 0.1.0

Initial release.

- Combined status bar item showing the provider mark, compact limit name and percent of the tightest quota across enabled providers, with time to reset at critical.
- Provider marks shipped as a contributed icon font built from the dashboard's monochrome logos.
- Per-provider mode (`onwatch.statusBar.mode`).
- Theme-aware warning and critical colors.
- Markdown tooltip with one section per provider: a table of every limit with status, percent used, used/limit and time to reset, plus quick links to the quick view, dashboard and refresh.
- Quick view and dashboard open in Simple Browser with an external browser fallback.
- Follows the daemon's menubar preferences (visibility, order, thresholds, refresh cadence), with explicit extension settings as overrides.
- Auto-discovery of the daemon via `~/.onwatch/port`, `ONWATCH_PORT`, then port 9211.
- Optional HTTP Basic auth with the password kept in VS Code secret storage.
- One-time threshold notifications per provider, quota and reset window.
- Clear status bar states for no daemon, sign in required and remote daemon unsupported.
- Zero telemetry.
