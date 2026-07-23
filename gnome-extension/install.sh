#!/usr/bin/env bash
# Install the onWatch GNOME menubar extension for the current user.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
UUID="onwatch-menubar@onllm.dev"
SRC="$ROOT/$UUID"
DEST="${XDG_DATA_HOME:-$HOME/.local/share}/gnome-shell/extensions/$UUID"

if [[ ! -d "$SRC" ]]; then
  echo "error: extension source not found: $SRC" >&2
  exit 1
fi

mkdir -p "$(dirname "$DEST")"
rm -rf "$DEST"
cp -a "$SRC" "$DEST"
chmod +x "$DEST/panel_host.py"
# Ensure provider icons are present for the top-bar icon+% layout.
if [[ ! -d "$DEST/icons" ]]; then
  echo "warning: icons/ missing in extension package" >&2
fi

echo "Installed to $DEST"
echo
if command -v gnome-extensions >/dev/null 2>&1; then
  gnome-extensions disable "$UUID" 2>/dev/null || true
  sleep 0.3
  gnome-extensions enable "$UUID" 2>/dev/null || true
  gnome-extensions info "$UUID" 2>/dev/null || true
fi
echo
echo "=============================================="
echo "  IMPORTANT: reload GNOME Shell to pick up code"
echo "=============================================="
echo
if [[ "${XDG_SESSION_TYPE:-}" == "wayland" ]]; then
  echo "You are on Wayland."
  echo
  echo "  First time after install/code change of extension.js:"
  echo "    Log out and log back in (required once)."
  echo
  echo "  Later indicator-only fixes: re-run this script, then:"
  echo "    gnome-extensions disable $UUID && gnome-extensions enable $UUID"
  echo "  (extension uses mtime cache-bust imports)"
  echo
  echo "  1. Save your work"
  echo "  2. Top-right menu → Log Out"
  echo "  3. Log back in"
else
  echo "On X11: press Alt+F2, type  r  , press Enter."
fi
echo
echo "Requires: onwatch daemon (default http://127.0.0.1:9211)"
echo "Optional: echo 9211 > ~/.onwatch/port"
