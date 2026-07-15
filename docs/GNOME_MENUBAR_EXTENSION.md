# GNOME Menubar Extension (shared panel with macOS)

## Goal

macOS-like menubar UX on GNOME **without re-implementing the panel UI**.

| Piece | Owner | Shared with macOS? |
|---|---|---|
| Quota polling, SQLite, settings | Go daemon (`onwatch`) | yes |
| Panel HTML/CSS/JS | `GET /menubar` | **yes – single source** |
| Tray icon + three `%` | GNOME extension | no |
| Hover / click / dismiss | Extension + panel host | no |

## Layout

```
gnome-extension/
  install.sh
  onwatch-menubar@onllm.dev/
    metadata.json
    extension.js          # enable/disable
    indicator.js          # top-bar label + hover/click
    api.js                # loopback HTTP client
    panelHost.js          # spawns panel_host.py
    panel_host.py         # Gtk3+WebKit loads /menubar
    stylesheet.css
```

## Install

```bash
# 1) Daemon (normal onwatch, any Linux build – menubar tags not required)
onwatch   # or your existing install; default port 9211

# 2) Extension
cd gnome-extension
./install.sh
# Wayland: log out/in once so the extension loads
gnome-extensions enable onwatch-menubar@onllm.dev
```

Optional port file:

```bash
mkdir -p ~/.onwatch
echo 9211 > ~/.onwatch/port
```

Dependencies for the panel host: `python3`, `gir1.2-gtk-3.0`, `gir1.2-webkit2-4.1` (or distro equivalents already used for desktop WebKit).

## Runtime behaviour

1. Indicator polls `GET /api/menubar/tray-title` every 15s → label `6%·4%·1%` (same rules as macOS `TrayTitle`).
2. **Hover** or **left-click** the indicator → `panel_host.py` shows a WebKit window at `http://127.0.0.1:<port>/menubar`.
3. Pointer **leaves the panel window for 0.5s** → host hides (no re-render; same page stays loaded).
4. **Escape** or panel **X** (native `onwatchAction` close) → hide.
5. Right-click indicator → shell menu: Show Quota Panel / Open Dashboard.

## APIs used (loopback, no auth)

| Path | Purpose |
|---|---|
| `/api/menubar/tray-title` | Compact title + tooltip for the top bar |
| `/api/menubar/summary` | Full panel data (fetched by the HTML page) |
| `/api/menubar/preferences` | Panel settings UI |
| `/menubar` | Shared HTML panel |

## Why not pure native SNI + GTK companion?

GNOME Wayland: no tray hover in SNI, XWayland cannot see pointer over the shell bar, AppIndicator always opens a DBus menu. Extension owns the chrome; daemon owns data + panel HTML.

## Decision log

- Linux native GTK companion path abandoned.
- Panel = HTTP to existing `/menubar` only.
- Tray title computed server-side for parity with macOS.
