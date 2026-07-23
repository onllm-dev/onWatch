#!/usr/bin/env python3
"""Thin WebKitGTK host for the shared onWatch /menubar page.

Loaded by the GNOME Shell extension. Speaks a tiny line protocol on stdin/stdout:
  SHOW <url>
  HIDE
  TOGGLE <url>
  QUIT
  PING

Replies with lines: OK | ERR <msg> | VISIBLE 0|1 | PONG | READY

Also bridges page actions via webkit.messageHandlers.onwatchAction:
  close | open_dashboard
"""

from __future__ import annotations

import os
import sys
import threading
from urllib.parse import urlparse

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")

from gi.repository import Gdk, GLib, Gtk, WebKit2  # noqa: E402


def _is_menubar_surface(uri: str) -> bool:
    """Allow only /menubar page + /api/menubar/* inside the popup WebView."""
    try:
        parsed = urlparse(uri)
    except Exception:
        return False
    if parsed.scheme not in ("http", "https", "about"):
        return False
    if parsed.scheme == "about":
        return True
    host = (parsed.hostname or "").lower()
    if host not in ("127.0.0.1", "localhost", "::1"):
        return False
    path = parsed.path or "/"
    return path == "/menubar" or path.startswith("/menubar/") or path.startswith("/api/menubar")


class PanelHost:
    def __init__(self) -> None:
        self._window = Gtk.Window(type=Gtk.WindowType.TOPLEVEL)
        self._window.set_title("onWatch")
        self._window.set_decorated(False)
        self._window.set_skip_taskbar_hint(True)
        self._window.set_skip_pager_hint(True)
        self._window.set_keep_above(True)
        self._window.set_type_hint(Gdk.WindowTypeHint.UTILITY)
        self._window.set_default_size(360, 680)
        self._window.set_resizable(False)
        self._window.connect("delete-event", self._on_delete)
        self._window.connect("key-press-event", self._on_key)
        self._window.connect("leave-notify-event", self._on_leave)
        self._window.connect("enter-notify-event", self._on_enter)

        # UserContentManager so menubar.html can call
        # webkit.messageHandlers.onwatchAction.postMessage("close")
        self._ucm = WebKit2.UserContentManager()
        self._ucm.register_script_message_handler("onwatchAction")
        self._ucm.connect(
            "script-message-received::onwatchAction",
            self._on_script_action,
        )
        # Optional resize bridge (macOS companion also uses onwatchResize).
        try:
            self._ucm.register_script_message_handler("onwatchResize")
            self._ucm.connect(
                "script-message-received::onwatchResize",
                self._on_script_resize,
            )
        except Exception:
            pass

        self._webview = WebKit2.WebView.new_with_user_content_manager(self._ucm)
        settings = self._webview.get_settings()
        settings.set_enable_developer_extras(False)
        self._webview.connect("decide-policy", self._on_decide_policy)

        self._window.add(self._webview)
        self._webview.show()

        self._loaded_url = ""
        self._visible = False
        self._pointer_inside = False
        self._hide_timer: int | None = None
        self._leave_ms = 500

    def _on_delete(self, *_args):
        self.hide()
        return True

    def _on_key(self, _w, event):
        if event.keyval == Gdk.KEY_Escape:
            self.hide()
            return True
        return False

    def _on_enter(self, _w, event):
        if event.detail == Gdk.NotifyType.INFERIOR:
            return False
        self._pointer_inside = True
        self._cancel_hide_timer()
        return False

    def _on_leave(self, _w, event):
        if event.detail == Gdk.NotifyType.INFERIOR:
            return False
        self._pointer_inside = False
        self._schedule_hide()
        return False

    def _cancel_hide_timer(self) -> None:
        if self._hide_timer is not None:
            GLib.source_remove(self._hide_timer)
            self._hide_timer = None

    def _schedule_hide(self) -> None:
        self._cancel_hide_timer()

        def fire() -> bool:
            self._hide_timer = None
            if not self._pointer_inside and self._visible:
                self.hide()
            return False

        self._hide_timer = GLib.timeout_add(self._leave_ms, fire)

    @staticmethod
    def _js_message_to_text(message) -> str:
        """Extract string payload from a WebKit script message (API varies)."""
        # WebKit2.JavascriptResult
        try:
            js_val = message.get_js_value()
            if js_val is not None:
                if hasattr(js_val, "to_string"):
                    return str(js_val.to_string())
                return str(js_val)
        except Exception:
            pass
        try:
            val = message.get_value()
            if val is not None:
                if hasattr(val, "to_string"):
                    return str(val.to_string())
                return str(val)
        except Exception:
            pass
        try:
            return str(message)
        except Exception:
            return ""

    def _on_script_action(self, _manager, message) -> None:
        raw = self._js_message_to_text(message).strip().strip('"').lower()
        # postMessage may send a plain string or a small JSON blob.
        if raw.startswith("{") and "close" in raw:
            action = "close"
        elif raw.startswith("{") and "open_dashboard" in raw:
            action = "open_dashboard"
        else:
            action = raw

        if action in ("close", "hide"):
            self.hide()
        elif action in ("open_dashboard", "dashboard", "open"):
            # Prefer same host/port as the loaded menubar page.
            dash = "http://127.0.0.1:9211/"
            if self._loaded_url:
                try:
                    p = urlparse(self._loaded_url)
                    if p.scheme and p.hostname:
                        port = f":{p.port}" if p.port else ""
                        dash = f"{p.scheme}://{p.hostname}{port}/"
                except Exception:
                    pass
            self._open_external(dash)
            self.hide()
        # ignore unknown

    def _on_script_resize(self, _manager, message) -> None:
        # Optional: could resize the window from page height. Ignore for now.
        return

    def _open_external(self, uri: str) -> None:
        try:
            Gtk.show_uri_on_window(self._window, uri, Gdk.CURRENT_TIME)
        except Exception:
            try:
                Gtk.show_uri(None, uri, Gdk.CURRENT_TIME)
            except Exception:
                pass

    def _on_decide_policy(self, _web, decision, decision_type):
        if decision_type != WebKit2.PolicyDecisionType.NAVIGATION_ACTION:
            return False
        nav = decision.get_navigation_action()
        req = nav.get_request()
        uri = req.get_uri() or ""

        # Keep the popup on the menubar surface only. Navigating to "/" would
        # load the dashboard/login inside the tray popover (looks like logout).
        if _is_menubar_surface(uri):
            decision.use()
            return True

        # Anything else (/, /login, external): open in the default browser.
        if uri.startswith("http://") or uri.startswith("https://"):
            self._open_external(uri)
        decision.ignore()
        return True

    def _position_top_right(self) -> None:
        display = self._window.get_display()
        monitor = display.get_primary_monitor()
        if monitor is None and display.get_n_monitors() > 0:
            monitor = display.get_monitor(0)
        if monitor is None:
            return
        geo = monitor.get_workarea()
        w, h = self._window.get_size()
        if w <= 1:
            w = 360
        if h <= 1:
            h = 680
        x = geo.x + geo.width - w - 12
        y = geo.y + 8
        self._window.move(x, y)

    def show(self, url: str) -> None:
        url = (url or "").strip()
        if not url:
            self._reply("ERR empty url")
            return
        if url != self._loaded_url:
            self._webview.load_uri(url)
            self._loaded_url = url
        self._cancel_hide_timer()
        self._position_top_right()
        self._window.show_all()
        self._window.present()
        self._position_top_right()
        GLib.idle_add(self._position_top_right)
        GLib.timeout_add(50, self._position_top_right_once)
        self._visible = True
        self._reply("OK")

    def _position_top_right_once(self):
        self._position_top_right()
        return False

    def hide(self) -> None:
        self._cancel_hide_timer()
        self._window.hide()
        self._visible = False
        self._pointer_inside = False
        self._reply("OK")

    def toggle(self, url: str) -> None:
        if self._visible:
            self.hide()
        else:
            self.show(url)

    def _reply(self, line: str) -> None:
        sys.stdout.write(line + "\n")
        sys.stdout.flush()

    def handle_line(self, line: str) -> None:
        line = line.strip()
        if not line:
            return
        parts = line.split(None, 1)
        cmd = parts[0].upper()
        arg = parts[1] if len(parts) > 1 else ""

        if cmd == "PING":
            self._reply("PONG")
        elif cmd == "SHOW":
            self.show(arg)
        elif cmd == "HIDE":
            self.hide()
        elif cmd == "TOGGLE":
            self.toggle(arg)
        elif cmd == "QUIT":
            self._reply("OK")
            Gtk.main_quit()
        elif cmd == "VISIBLE":
            self._reply(f"VISIBLE {1 if self._visible else 0}")
        else:
            self._reply(f"ERR unknown {cmd}")


def main() -> int:
    # Prefer X11 so move() can pin top-right under GNOME Wayland.
    if not os.environ.get("GDK_BACKEND") and (
        os.environ.get("WAYLAND_DISPLAY") or os.environ.get("XDG_SESSION_TYPE") == "wayland"
    ):
        os.environ["GDK_BACKEND"] = "x11"

    Gtk.init(sys.argv)
    host = PanelHost()

    def read_stdin() -> None:
        for line in sys.stdin:
            GLib.idle_add(host.handle_line, line)
        GLib.idle_add(Gtk.main_quit)

    t = threading.Thread(target=read_stdin, daemon=True)
    t.start()
    host._reply("READY")
    Gtk.main()
    return 0


if __name__ == "__main__":
    sys.exit(main())
