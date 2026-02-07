#!/usr/bin/env bash
# migrate-from-syntrack.sh - Migrate SynTrack data to onWatch
#
# This script migrates configuration, database, and service files
# from the old SynTrack (~/.syntrack/) layout to onWatch (~/.onwatch/).
# It is designed to be run once and deletes itself upon completion.

set -euo pipefail

OLD_DIR="$HOME/.syntrack"
NEW_DIR="$HOME/.onwatch"

# -------------------------------------------------------------------
# 1. Check if migration is needed
# -------------------------------------------------------------------
if [ ! -d "$OLD_DIR" ]; then
    echo "[migrate] No ~/.syntrack/ directory found. Nothing to migrate."
    exit 0
fi

if [ -d "$NEW_DIR" ]; then
    echo "[migrate] ~/.onwatch/ already exists. Skipping migration."
    exit 0
fi

echo "[migrate] Migrating SynTrack -> onWatch ..."

# -------------------------------------------------------------------
# 2. Create new directory structure
# -------------------------------------------------------------------
echo "[migrate] Creating ~/.onwatch/ directory structure ..."
mkdir -p "$NEW_DIR/bin"
mkdir -p "$NEW_DIR/data"

# -------------------------------------------------------------------
# 3. Migrate .env file (rename SYNTRACK_ -> ONWATCH_ prefixes)
# -------------------------------------------------------------------
if [ -f "$OLD_DIR/.env" ]; then
    echo "[migrate] Migrating .env file ..."
    sed \
        -e 's/SYNTRACK_/ONWATCH_/g' \
        -e 's/SynTrack/onWatch/g' \
        -e 's/syntrack/onwatch/g' \
        "$OLD_DIR/.env" > "$NEW_DIR/.env"
    chmod 600 "$NEW_DIR/.env"
    echo "[migrate] .env migrated: $NEW_DIR/.env"
else
    echo "[migrate] No .env file found in $OLD_DIR, skipping."
fi

# -------------------------------------------------------------------
# 4. Migrate database files
# -------------------------------------------------------------------
DB_FOUND=false

# Check ~/.syntrack/data/syntrack.db first (canonical path)
if [ -f "$OLD_DIR/data/syntrack.db" ]; then
    echo "[migrate] Moving database from $OLD_DIR/data/syntrack.db ..."
    mv "$OLD_DIR/data/syntrack.db" "$NEW_DIR/data/onwatch.db"
    [ -f "$OLD_DIR/data/syntrack.db-wal" ] && mv "$OLD_DIR/data/syntrack.db-wal" "$NEW_DIR/data/onwatch.db-wal"
    [ -f "$OLD_DIR/data/syntrack.db-shm" ] && mv "$OLD_DIR/data/syntrack.db-shm" "$NEW_DIR/data/onwatch.db-shm"
    DB_FOUND=true
fi

# Check ~/.syntrack/syntrack.db (old default)
if [ "$DB_FOUND" = false ] && [ -f "$OLD_DIR/syntrack.db" ]; then
    echo "[migrate] Moving database from $OLD_DIR/syntrack.db ..."
    mv "$OLD_DIR/syntrack.db" "$NEW_DIR/data/onwatch.db"
    [ -f "$OLD_DIR/syntrack.db-wal" ] && mv "$OLD_DIR/syntrack.db-wal" "$NEW_DIR/data/onwatch.db-wal"
    [ -f "$OLD_DIR/syntrack.db-shm" ] && mv "$OLD_DIR/syntrack.db-shm" "$NEW_DIR/data/onwatch.db-shm"
    DB_FOUND=true
fi

# Check ./syntrack.db (working directory)
if [ "$DB_FOUND" = false ] && [ -f "./syntrack.db" ]; then
    echo "[migrate] Moving database from ./syntrack.db ..."
    mv "./syntrack.db" "$NEW_DIR/data/onwatch.db"
    [ -f "./syntrack.db-wal" ] && mv "./syntrack.db-wal" "$NEW_DIR/data/onwatch.db-wal"
    [ -f "./syntrack.db-shm" ] && mv "./syntrack.db-shm" "$NEW_DIR/data/onwatch.db-shm"
    DB_FOUND=true
fi

if [ "$DB_FOUND" = true ]; then
    echo "[migrate] Database migrated to $NEW_DIR/data/onwatch.db"
else
    echo "[migrate] No database found to migrate."
fi

# -------------------------------------------------------------------
# 5. Stop old systemd service (syntrack.service)
# -------------------------------------------------------------------
echo "[migrate] Checking for old syntrack.service ..."

# Try user-level systemd first
if command -v systemctl &>/dev/null; then
    if systemctl --user is-active syntrack.service &>/dev/null; then
        echo "[migrate] Stopping user syntrack.service ..."
        systemctl --user stop syntrack.service 2>/dev/null || true
        systemctl --user disable syntrack.service 2>/dev/null || true
    fi

    # Try system-level systemd
    if systemctl is-active syntrack.service &>/dev/null 2>&1; then
        echo "[migrate] Stopping system syntrack.service ..."
        sudo systemctl stop syntrack.service 2>/dev/null || true
        sudo systemctl disable syntrack.service 2>/dev/null || true
    fi
fi

# -------------------------------------------------------------------
# 6. Remove old systemd service files
# -------------------------------------------------------------------
USER_SERVICE="$HOME/.config/systemd/user/syntrack.service"
if [ -f "$USER_SERVICE" ]; then
    echo "[migrate] Removing user service file: $USER_SERVICE"
    rm -f "$USER_SERVICE"
    systemctl --user daemon-reload 2>/dev/null || true
fi

SYSTEM_SERVICE="/etc/systemd/system/syntrack.service"
if [ -f "$SYSTEM_SERVICE" ]; then
    echo "[migrate] Removing system service file: $SYSTEM_SERVICE"
    sudo rm -f "$SYSTEM_SERVICE" 2>/dev/null || true
    sudo systemctl daemon-reload 2>/dev/null || true
fi

# -------------------------------------------------------------------
# 7. Update shell rc files (PATH exports: .syntrack -> .onwatch)
# -------------------------------------------------------------------
RC_FILES=(
    "$HOME/.bashrc"
    "$HOME/.zshrc"
    "$HOME/.bash_profile"
)

for rc in "${RC_FILES[@]}"; do
    if [ -f "$rc" ] && grep -q '\.syntrack' "$rc"; then
        echo "[migrate] Updating PATH in $rc ..."
        sed -i.bak 's/\.syntrack/\.onwatch/g' "$rc"
        rm -f "${rc}.bak"
    fi
done

# -------------------------------------------------------------------
# 8. Remove old ~/.syntrack/ directory
# -------------------------------------------------------------------
echo "[migrate] Removing old ~/.syntrack/ directory ..."
rm -rf "$OLD_DIR"

echo "[migrate] Migration complete!"
echo "[migrate]   Config: $NEW_DIR/.env"
if [ "$DB_FOUND" = true ]; then
    echo "[migrate]   Database: $NEW_DIR/data/onwatch.db"
fi
echo "[migrate]   Old directory removed: $OLD_DIR"

# -------------------------------------------------------------------
# 9. Self-delete
# -------------------------------------------------------------------
SCRIPT_PATH="$(realpath "$0" 2>/dev/null || readlink -f "$0" 2>/dev/null || echo "$0")"
if [ -f "$SCRIPT_PATH" ]; then
    rm -f "$SCRIPT_PATH"
fi
