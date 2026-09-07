# Native Tray on Linux and Windows

onWatch ships the same menubar companion on every desktop platform. macOS
draws the compact quota text next to a template icon and opens a native
popover. Linux and Windows cannot show text beside a tray icon, so the
companion bakes the number into the icon and opens the same quick view in a
frameless browser window. Everything is pure Go: no cgo, no GTK, no WebView2
runtime, and the release binaries stay static.

## What you get

| Piece | macOS | Linux | Windows |
|---|---|---|---|
| Tray icon | Template mark + title text (`6%·4%·1%`) | Status-colored disc with the selected percent | Status-colored disc with the selected percent |
| Hover | Tooltip | Tooltip (KDE, Cinnamon, XFCE, MATE, GNOME with AppIndicator) | Tooltip, capped at 127 characters by Windows |
| Left click | Native popover with `/menubar` | Quick view window (Chromium-family `--app` mode) or the menu, depending on the desktop | Quick view window anchored above the taskbar |
| Right click | Menu | Menu | Menu |
| Menu | One row per provider, Open Quick View, Open Dashboard, Refresh Now, Quit | same | same |

The icon color follows the quota it shows: slate when healthy, amber at the
warning threshold, red at critical, gray with a dash when the daemon is
unreachable. Thresholds and the quota selection come from **Settings >
Menubar** in the dashboard, exactly as on macOS. The `icon_only` display
mode draws the onWatch mark tinted by the aggregate status instead of a
number.

## How it starts

The daemon spawns `onwatch menubar` as a child process once the web server is
up, when all of these hold:

- the binary was built with the `menubar` tag (all release binaries are),
- the menubar is enabled in Settings > Menubar (default: enabled),
- a desktop session is reachable.

"Desktop session" means:

- **Linux**: a D-Bus session bus, detected through `DBUS_SESSION_BUS_ADDRESS`
  or `$XDG_RUNTIME_DIR/bus`. A `systemd --user` unit has this on every modern
  desktop. A system-wide unit running as root does not, so no tray is spawned
  there. Containers are skipped.
- **Windows**: an interactive logon session, detected through `SESSIONNAME`.
  The installer starts the daemon in your session, so the tray appears
  automatically. A Windows service in session 0 gets no tray.

Set `ONWATCH_DISABLE_TRAY=1` to keep the daemon headless on a desktop.

### How it stops

`onwatch stop` ends the companion along with the daemon. The companion also
watches the daemon that spawned it and quits on its own about 30 seconds after
that daemon disappears, so a crash, a `kill`, or a stop that ran with a
different `HOME` or `LOCALAPPDATA` cannot leave a dead icon behind. A restart
inside that window is adopted, including one under a new PID, so the icon does
not flicker while the daemon comes back.

### Running the tray by hand

When the daemon runs elsewhere (system service, another machine over an SSH
tunnel, Docker), start the companion yourself:

```bash
onwatch menubar --port 9211
```

It reads the same dashboard the daemon serves, so the only requirement is
that `http://localhost:<port>` reaches an onWatch instance. To start it at
login on Linux, drop a desktop entry into `~/.config/autostart/`:

```ini
[Desktop Entry]
Type=Application
Name=onWatch Tray
Exec=onwatch menubar
X-GNOME-Autostart-enabled=true
```

On Windows, create a shortcut to `onwatch.exe menubar` in
`shell:startup`.

## Linux desktop support

The tray speaks the StatusNotifierItem protocol over D-Bus. It works out of
the box on KDE Plasma, Cinnamon, XFCE, MATE, LXQt, Budgie, and on GNOME when
an AppIndicator extension is enabled (Ubuntu ships one by default). On stock
GNOME without AppIndicator support, either enable the extension
(`gnome-extensions enable ubuntu-appindicators@ubuntu.com` on Ubuntu, or
install "AppIndicator and KStatusNotifierItem Support" from
extensions.gnome.org) or use the
[onWatch GNOME Shell extension](GNOME_MENUBAR_EXTENSION.md), which draws the
same quota text directly in the top bar.

## The quick view window

Clicking the icon opens `http://localhost:<port>/menubar` in a Chromium-family
browser running in `--app` mode: no address bar, popover size (360x680), a
private profile under `~/.onwatch/quickview-profile` (Linux) or
`%LOCALAPPDATA%\onwatch\quickview-profile` (Windows). Clicking again closes it.

Browsers detected, in order:

- **Windows**: Microsoft Edge (present on every supported Windows), Chrome,
  Brave, Vivaldi, Chromium.
- **Linux**: `chromium`, `chromium-browser`, `google-chrome`, `brave-browser`,
  `microsoft-edge`, `vivaldi`, plus common Flatpak and Snap install paths.

Set `ONWATCH_QUICKVIEW_BROWSER=/path/to/browser` to force one. When no
Chromium-family browser exists, the companion opens the quick view in your
default browser instead.

On Windows the window is anchored to the display under the cursor and pressed
against whichever edge holds the taskbar. Every open logs what it saw and where
it put the window:

```
level=INFO msg="quick view anchor" cursor=1830,1417 work_area=0,0,2560,1400 work_area_source=monitor window=1552,712 size=360x680
```

If the window lands on the wrong display or edge, paste that line into
[issue #125](https://github.com/onllm-dev/onWatch/issues/125); it carries the
work area and the placement decision, which is all the geometry needed.

## Port discovery

The daemon writes its dashboard port to `~/.onwatch/port` (Linux, macOS) or
`%LOCALAPPDATA%\onwatch\port` (Windows) each time it starts. Thin clients such
as the GNOME extension and the VS Code extension read this file before
falling back to `ONWATCH_PORT` and then 9211.

## Building

```bash
./app.sh --build                                  # host binary, tray included on every OS
CGO_ENABLED=0 GOOS=linux   go build -tags menubar .
CGO_ENABLED=0 GOOS=windows go build -tags menubar .
```

CI runs the tagged tests on Ubuntu and Windows (`tray-linux`, `tray-windows`
jobs in `ci.yml`) and the release workflow passes `-tags menubar` for the
linux and windows targets. Docker images stay untagged: containers never
have a tray.

## Troubleshooting

- **No icon on Linux**: check `onwatch status` reports the companion running,
  then confirm a StatusNotifier host exists (`busctl --user list | grep
  StatusNotifierWatcher`). On GNOME, enable an AppIndicator extension.
- **Icon shows a gray dash**: the companion cannot reach the daemon. Check
  `onwatch status` and the port in `~/.onwatch/port`. If the daemon is really
  gone, the icon disappears on its own within about 30 seconds.
- **Quick view opens in a full browser**: no Chromium-family browser was
  found. Install one or set `ONWATCH_QUICKVIEW_BROWSER`.
- **Logs**: `~/.onwatch/data/menubar.log` (Linux, macOS) or
  `%LOCALAPPDATA%\onwatch\data\menubar.log` (Windows).
