# Changelog

All notable changes to the onWatch VS Code extension are documented here.

## 0.1.0

Initial release.

- Combined status bar item showing the tightest quota across enabled providers, with time to reset at critical.
- Per-provider mode (`onwatch.statusBar.mode`).
- Theme-aware warning and critical colors.
- Markdown tooltip with one line per provider and quick links to the dashboard, quick view and refresh.
- Quick view and dashboard open in Simple Browser with an external browser fallback.
- Follows the daemon's menubar preferences (visibility, order, thresholds, refresh cadence), with explicit extension settings as overrides.
- Auto-discovery of the daemon via `~/.onwatch/port`, `ONWATCH_PORT`, then port 9211.
- Optional HTTP Basic auth with the password kept in VS Code secret storage.
- One-time threshold notifications per provider, quota and reset window.
- Clear status bar states for no daemon, sign in required and remote daemon unsupported.
- Zero telemetry.
