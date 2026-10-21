/**
 * Top-bar indicator: per-provider icon + %  + hover/click panel control.
 *
 * Icon recolor:
 * - SVGs are white monochrome (fill="#ffffff")
 * - Clutter.ColorizeEffect multiplies white × panel tint
 * - Tint must be a real Clutter/Cogl color from get_foreground_color()
 *   (plain {red,green,blue} objects throw on GNOME 50)
 *
 * Code revision: 2026-07-15-colorize-v4
 */

import GObject from 'gi://GObject';
import GLib from 'gi://GLib';
import Gio from 'gi://Gio';
import St from 'gi://St';
import Clutter from 'gi://Clutter';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';

import {fetchTrayTitle, menubarPageURL, resolvePort} from './api.js';
import {PanelHostController} from './panelHost.js';

const REFRESH_MS_DEFAULT = 15_000;
const ICON_SIZE = 14;
/** Delay before opening the quota panel on tray hover (avoids accidental triggers). */
const HOVER_OPEN_DELAY_MS = 750;

/**
 * Format an age in ms the same way the Go tray does
 * (`internal/menubar/menu.go` formatAgeAgo): "just now", "12m ago",
 * "3h ago", "2d ago".
 *
 * @param {number} ageMs
 * @returns {string}
 */
function formatAgeAgo(ageMs) {
    const minutes = Math.floor(Math.max(0, ageMs) / 60_000);
    if (minutes < 1)
        return 'just now';
    if (minutes < 60)
        return `${minutes}m ago`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24)
        return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
}

/**
 * Build a native color for ColorizeEffect.tint.
 * Prefer Clutter.Color.from_string; fall back to Cogl if needed.
 *
 * @param {string} css  e.g. '#f2f2f2'
 * @returns {?object}
 */
function colorFromCss(css) {
    try {
        if (Clutter.Color?.from_string) {
            const result = Clutter.Color.from_string(css);
            // GJS may return [ok, color] or just color
            if (Array.isArray(result))
                return result[0] ? result[1] : result[1] ?? null;
            return result;
        }
    } catch (_e) {
        // continue
    }
    try {
        // Some Shell builds expose parse via new + init
        if (typeof Clutter.Color === 'function') {
            const c = new Clutter.Color();
            if (c.init_from_string) {
                const ok = c.init_from_string(css);
                if (ok === true || ok === 1 || ok === undefined)
                    return c;
            }
        }
    } catch (_e) {
        // continue
    }
    return null;
}

export const OnWatchIndicator = GObject.registerClass(
class OnWatchIndicator extends PanelMenu.Button {
    /**
     * @param {string} extensionDir
     */
    _init(extensionDir) {
        super._init(0.5, 'onWatch Menubar', false);
        this.add_style_class_name('onwatch-menubar-button');

        this._extensionDir = extensionDir;
        this._iconsDir = GLib.build_filenamev([extensionDir, 'icons']);
        this._port = resolvePort();
        this._host = new PanelHostController(extensionDir);
        this._refreshId = 0;
        this._hoverOpenId = 0;
        // Set first thing in destroy(). Every continuation after an await and
        // every timer creation checks it, so a request that outlives the
        // indicator cannot touch destroyed actors or start new timers.
        this._destroyed = false;
        // Cancels the in-flight soup request on destroy, where libsoup allows
        // it. One cancellable per indicator, reused by every attempt.
        this._cancellable = new Gio.Cancellable();
        this._giconCache = new Map();
        this._icons = [];
        // Native color object (from theme node or from_string) - never a plain {}
        this._tintColor = colorFromCss('#f2f2f2');
        // Last good render, kept so a failed refresh can change only the tint
        // and tooltip instead of wiping the numbers. Null until the first
        // success, which is the only case that shows the offline placeholder.
        this._lastGood = null;
        // True while a refresh is in flight, so overlapping ticks coalesce
        // instead of stacking requests against a slow daemon.
        this._refreshInFlight = false;
        // Set when a refresh is requested while one is already running; the
        // in-flight run loops once more so the request is not dropped.
        this._refreshPending = false;
        this._staleAgeId = 0;
        // True while the tray is showing retained (stale) data.
        this._staleMode = false;

        console.log('onWatch menubar: colorize-v4 starting');

        this._box = new St.BoxLayout({
            style_class: 'onwatch-menubar-box',
            x_expand: false,
            y_align: Clutter.ActorAlign.CENTER,
        });
        this.add_child(this._box);

        this._fallbackLabel = new St.Label({
            text: '…',
            y_align: Clutter.ActorAlign.CENTER,
            style_class: 'onwatch-menubar-label',
        });
        this._box.add_child(this._fallbackLabel);

        const openItem = new PopupMenu.PopupMenuItem('Show Quota Panel');
        openItem.connect('activate', () => this._openPanel('menu'));
        this.menu.addMenuItem(openItem);

        const dashItem = new PopupMenu.PopupMenuItem('Open Dashboard');
        dashItem.connect('activate', () => {
            const url = `http://127.0.0.1:${this._port}/`;
            try {
                GLib.spawn_command_line_async(`xdg-open '${url}'`);
            } catch (e) {
                console.error('onWatch: open dashboard failed', e);
            }
        });
        this.menu.addMenuItem(dashItem);

        this.connect('notify::hover', () => this._onHoverChanged());
        this.connect('style-changed', () => {
            this._updateTintFromTheme();
            this._applyTintToIcons();
        });
        this.connect('button-press-event', (_a, event) => {
            if (event.get_button() === Clutter.BUTTON_PRIMARY) {
                this._cancelHoverOpen();
                this._openPanel('click');
                return Clutter.EVENT_STOP;
            }
            return Clutter.EVENT_PROPAGATE;
        });

        this._refreshTitle().catch(e => console.error('onWatch: initial refresh failed', e));
        this._refreshId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, REFRESH_MS_DEFAULT, () => {
            if (this._destroyed) {
                this._refreshId = 0;
                return GLib.SOURCE_REMOVE;
            }
            this._refreshTitle().catch(e => console.error('onWatch: refresh failed', e));
            return GLib.SOURCE_CONTINUE;
        });
    }

    destroy() {
        // Mark disposed before any teardown: a refresh that is already past
        // its await must see this and return without touching actors or
        // creating timers.
        this._destroyed = true;
        if (this._refreshId) {
            GLib.source_remove(this._refreshId);
            this._refreshId = 0;
        }
        this._stopStaleAgeTimer();
        this._cancelHoverOpen();
        // Abort the in-flight request (libsoup reports it as a cancelled
        // error, which _refreshTitle swallows for a destroyed instance).
        try {
            this._cancellable?.cancel();
        } catch (e) {
            console.debug?.('onWatch: cancel on destroy failed', e);
        }
        this._cancellable = null;
        this._lastGood = null;
        this._staleMode = false;
        this._host?.destroy();
        this._host = null;
        this._giconCache?.clear();
        this._icons = [];
        super.destroy();
    }

    _cancelHoverOpen() {
        if (this._hoverOpenId) {
            GLib.source_remove(this._hoverOpenId);
            this._hoverOpenId = 0;
        }
    }

    _onHoverChanged() {
        this._cancelHoverOpen();
        if (!this.hover)
            return;

        // Wait briefly so a quick pass over the tray does not pop the panel.
        this._hoverOpenId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, HOVER_OPEN_DELAY_MS, () => {
            this._hoverOpenId = 0;
            if (this.hover)
                this._openPanel('hover');
            return GLib.SOURCE_REMOVE;
        });
    }

    /**
     * Detect whether the GNOME top bar is dark (light foreground text).
     * Used so menubar theme=system matches the panel, not WebKit's
     * often-wrong prefers-color-scheme (usually light under GTK3).
     *
     * @returns {boolean}
     */
    _panelPrefersDark() {
        const toByte = v => {
            if (v === undefined || v === null || Number.isNaN(v))
                return 0;
            if (typeof v === 'number' && v >= 0 && v <= 1)
                return Math.round(v * 255);
            return Math.max(0, Math.min(255, Math.round(v)));
        };
        try {
            // Prefer this button's fg (same as tray text) - light text ⇒ dark panel.
            const node = this.get_theme_node() || Main.panel?.get_theme_node?.();
            if (!node)
                return true;
            const fg = node.get_foreground_color();
            if (!fg)
                return true;
            const lum = 0.2126 * toByte(fg.red) + 0.7152 * toByte(fg.green) + 0.0722 * toByte(fg.blue);
            return lum >= 120;
        } catch (_e) {
            return true;
        }
    }

    /**
     * @param {string} _reason
     */
    async _openPanel(_reason) {
        if (this._destroyed)
            return;
        try {
            const url = menubarPageURL(this._port, {
                systemPrefersDark: this._panelPrefersDark(),
            });
            await this._host.show(url);
        } catch (e) {
            if (this._destroyed)
                return;
            console.error('onWatch: failed to open panel', e);
            // A panel-host failure must not wipe the tray numbers either.
            if (!this._lastGood)
                this._setOffline('!');
        }
    }

    _clearBox() {
        this._icons = [];
        const children = this._box.get_children();
        for (const child of children)
            child.destroy();
    }

    /**
     * Show the offline placeholder. Only reached when there has never been a
     * successful response, so there is no last good render to keep.
     *
     * @param {string} text
     * @param {boolean} [offline]
     */
    _setOffline(text, offline = true) {
        if (this._destroyed)
            return;
        this._stopStaleAgeTimer();
        this._staleMode = false;
        this._clearBox();
        const label = new St.Label({
            text: text || '--',
            y_align: Clutter.ActorAlign.CENTER,
            style_class: offline
                ? 'onwatch-menubar-label onwatch-menubar-offline'
                : 'onwatch-menubar-label',
        });
        this._box.add_child(label);
    }

    _stopStaleAgeTimer() {
        if (this._staleAgeId) {
            GLib.source_remove(this._staleAgeId);
            this._staleAgeId = 0;
        }
    }

    /**
     * Age to display for retained data: from the payload's `latest_at` when it
     * carries one, otherwise from when the last successful fetch landed. Keeps
     * counting up while the daemon is unreachable, so it never freezes at the
     * build-time value the Go tray also avoids.
     *
     * @param {?string} latestAt ISO timestamp from the response, if any
     * @returns {number} age in ms, or 0 when nothing is known
     */
    _staleAgeMs(latestAt) {
        const parse = value => {
            if (!value)
                return 0;
            const t = Date.parse(value);
            return Number.isFinite(t) ? t : 0;
        };
        let from = parse(latestAt);
        if (!from && this._lastGood)
            from = parse(this._lastGood.latestAt) || this._lastGood.fetchedAtMs;
        if (!from)
            return 0;
        return Math.max(0, Date.now() - from);
    }

    /**
     * Tick the stale tooltip while retained data is on screen, so "last
     * updated 12m ago" advances instead of sticking at the first failure.
     */
    _startStaleAgeTimer() {
        if (this._destroyed)
            return;
        this._stopStaleAgeTimer();
        this._staleAgeId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 60_000, () => {
            if (this._destroyed || !this._staleMode || !this._lastGood) {
                this._staleAgeId = 0;
                return GLib.SOURCE_REMOVE;
            }
            this._applyStaleChrome();
            return GLib.SOURCE_CONTINUE;
        });
    }

    /**
     * Update only the tint and accessible tooltip for retained data, leaving
     * the rendered title/segments untouched.
     */
    _applyStaleChrome() {
        if (this._destroyed || !this._lastGood)
            return;
        const age = this._staleAgeMs(this._lastGood.latestAt);
        this._setName(`onWatch: offline, last updated ${formatAgeAgo(age)}`);
    }

    /**
     * Mark the tray stale: keep the last good numbers, change only the tint
     * and the accessible tooltip/label.
     *
     * @param {string} reason
     */
    _markStale(reason) {
        if (this._destroyed)
            return;
        if (!this._lastGood) {
            this._setOffline('--', true);
            this._setName('onWatch daemon offline');
            return;
        }
        this._staleMode = true;
        // Offline tint on the retained segments without rebuilding them.
        // Remove first so repeated failures cannot stack duplicate classes.
        this._box.remove_style_class_name('onwatch-menubar-stale');
        this._box.add_style_class_name('onwatch-menubar-stale');
        this._applyStaleChrome();
        this._startStaleAgeTimer();
        console.debug?.(`onWatch: retaining last good data (${reason})`);
    }

    /**
     * Clear the stale tint after a successful refresh.
     */
    _clearStale() {
        if (this._destroyed)
            return;
        this._stopStaleAgeTimer();
        this._staleMode = false;
        this._box.remove_style_class_name('onwatch-menubar-stale');
    }

    /**
     * Render a successful payload and remember it as the last good render.
     *
     * @param {{title: string, tooltip: string, online: boolean, segments: object[], latestAt: ?string}} payload
     */
    _applySuccess(payload) {
        if (this._destroyed)
            return;
        const {title, tooltip, online, segments, latestAt} = payload;
        // A response that reports itself offline is not a success: retain the
        // last good numbers rather than replacing them with a placeholder.
        if (!online) {
            this._markStale('daemon reported offline');
            return;
        }
        this._lastGood = {
            title,
            tooltip,
            online,
            segments,
            latestAt: latestAt || null,
            fetchedAtMs: Date.now(),
        };
        this._clearStale();
        this._renderSegments(segments, title, online);
        this.set_reactive(true);
        // The server puts one line per stale provider in the tooltip; nothing
        // extra is needed here, and the panel menu stays minimal.
        // Must not throw after a successful render (old bug wiped UI to "-").
        this._setName(tooltip || title || 'onWatch');
    }

    /**
     * @param {string} name
     */
    _setName(name) {
        // PanelMenu.Button / St.Widget: property varies by Shell version.
        try {
            if (typeof this.set_accessible_name === 'function')
                this.set_accessible_name(name);
            else if ('accessible_name' in this)
                this.accessible_name = name;
            else if (typeof this.setAccessibleName === 'function')
                this.setAccessibleName(name);
        } catch (e) {
            // Non-fatal - never wipe the tray for a11y failures.
            console.debug?.('onWatch: set accessible name skipped', e);
        }
    }

    /**
     * Sample native foreground color from a live label (inherits panel theme).
     * Keep the GObject color struct - ColorizeEffect rejects plain objects.
     */
    _updateTintFromTheme() {
        let sample = null;

        const walk = actor => {
            if (!actor || sample)
                return;
            if (actor instanceof St.Label) {
                try {
                    const node = actor.get_theme_node();
                    if (node)
                        sample = node.get_foreground_color();
                } catch (_e) {
                    // ignore
                }
                return;
            }
            if (actor.get_children) {
                for (const c of actor.get_children())
                    walk(c);
            }
        };
        walk(this._box);

        if (!sample) {
            try {
                sample = this.get_theme_node()?.get_foreground_color();
            } catch (_e) {
                sample = null;
            }
        }

        if (sample)
            this._tintColor = sample;
        else if (!this._tintColor)
            this._tintColor = colorFromCss('#f2f2f2');
    }

    /**
     * @param {St.Icon} icon
     */
    _colorizeIcon(icon) {
        if (!icon || !this._tintColor)
            return;
        try {
            icon.clear_effects();
            const effect = new Clutter.ColorizeEffect();
            effect.tint = this._tintColor;
            icon.add_effect(effect);
        } catch (e) {
            console.error('onWatch: ColorizeEffect failed', e);
        }
    }

    _applyTintToIcons() {
        this._icons = this._icons.filter(icon => {
            if (!icon)
                return false;
            try {
                if (!icon.get_stage || !icon.get_stage())
                    return false;
            } catch (_e) {
                return false;
            }
            this._colorizeIcon(icon);
            return true;
        });
    }

    /**
     * @param {string} stem
     * @returns {?string}
     */
    _iconPath(stem) {
        if (!stem)
            stem = 'all';
        const candidates = [
            GLib.build_filenamev([this._iconsDir, `${stem}-symbolic.svg`]),
            GLib.build_filenamev([this._iconsDir, `${stem}.svg`]),
            GLib.build_filenamev([this._iconsDir, 'all-symbolic.svg']),
            GLib.build_filenamev([this._iconsDir, 'all.svg']),
        ];
        for (const path of candidates) {
            if (GLib.file_test(path, GLib.FileTest.IS_REGULAR))
                return path;
        }
        return null;
    }

    /**
     * @param {string} stem
     * @returns {?Gio.Icon}
     */
    _getGicon(stem) {
        const path = this._iconPath(stem);
        if (!path)
            return null;
        if (this._giconCache.has(path))
            return this._giconCache.get(path);
        try {
            const gicon = Gio.icon_new_for_string(path);
            this._giconCache.set(path, gicon);
            return gicon;
        } catch (e) {
            console.error('onWatch: icon load failed', path, e);
            return null;
        }
    }

    /**
     * @param {string} stem
     * @returns {?St.Icon}
     */
    _makeIcon(stem) {
        const gicon = this._getGicon(stem);
        if (!gicon)
            return null;
        const icon = new St.Icon({
            gicon,
            icon_size: ICON_SIZE,
            style_class: 'onwatch-menubar-provider-icon',
            y_align: Clutter.ActorAlign.CENTER,
            y_expand: false,
        });
        this._colorizeIcon(icon);
        this._icons.push(icon);
        return icon;
    }

    /**
     * @param {Array<{text?: string, icon?: string, label?: string}>} segments
     * @param {string} fallbackTitle
     * @param {boolean} online
     */
    _renderSegments(segments, fallbackTitle, online) {
        if (this._destroyed)
            return;
        this._clearBox();

        if (!online) {
            this._setOffline('--', true);
            return;
        }

        if (!segments || segments.length === 0) {
            const text = fallbackTitle && fallbackTitle.length > 0 ? fallbackTitle : 'onWatch';
            this._setOffline(text, false);
            return;
        }

        segments.forEach((seg, index) => {
            if (index > 0) {
                this._box.add_child(new St.Label({
                    text: '·',
                    y_align: Clutter.ActorAlign.CENTER,
                    style_class: 'onwatch-menubar-sep',
                }));
            }

            const slot = new St.BoxLayout({
                // A stale provider keeps its numbers but reads dimmed, matching
                // the popover's amber/hollow treatment on the panel surface.
                style_class: seg.stale
                    ? 'onwatch-menubar-slot onwatch-menubar-segment-stale'
                    : 'onwatch-menubar-slot',
                y_align: Clutter.ActorAlign.CENTER,
            });

            const icon = this._makeIcon(seg.icon || 'all');
            if (icon)
                slot.add_child(icon);

            const label = new St.Label({
                text: seg.text || '',
                y_align: Clutter.ActorAlign.CENTER,
                style_class: 'onwatch-menubar-label',
            });
            if (seg.label) {
                const accessible = seg.stale
                    ? `${seg.label} ${seg.text || ''} - stale${seg.stale_reason ? `: ${seg.stale_reason}` : ''}`.trim()
                    : `${seg.label} ${seg.text || ''}`.trim();
                label.set_accessible_name?.(accessible);
            }
            slot.add_child(label);
            this._box.add_child(slot);
        });

        GLib.idle_add(GLib.PRIORITY_DEFAULT_IDLE, () => {
            if (this._destroyed)
                return GLib.SOURCE_REMOVE;
            this._updateTintFromTheme();
            this._applyTintToIcons();
            return GLib.SOURCE_REMOVE;
        });
        GLib.timeout_add(GLib.PRIORITY_DEFAULT, 200, () => {
            if (this._destroyed)
                return GLib.SOURCE_REMOVE;
            this._updateTintFromTheme();
            this._applyTintToIcons();
            return GLib.SOURCE_REMOVE;
        });
    }

    /**
     * One fetch attempt. Kept separate so the retry loop reads clearly.
     *
     * @returns {Promise<object>}
     */
    _fetchOnce() {
        this._port = resolvePort();
        return fetchTrayTitle(this._port, this._cancellable);
    }

    /**
     * Fetch with one retry, matching the Go tray client: a timeout is often a
     * transient write burst that clears within a moment, and a single miss
     * must not be treated as failure. Fully async; never blocks the Shell.
     *
     * @returns {Promise<object>}
     */
    async _fetchWithRetry() {
        try {
            return await this._fetchOnce();
        } catch (firstErr) {
            if (this._destroyed)
                throw firstErr;
            try {
                return await this._fetchOnce();
            } catch (retryErr) {
                throw new Error(`${firstErr.message} (retry: ${retryErr.message})`);
            }
        }
    }

    /**
     * Refresh the tray. Coalesces overlapping calls: if one is already in
     * flight, mark a pending request and return; the in-flight run loops once
     * more so the request is not dropped but also does not stack requests
     * against a slow daemon.
     *
     * Every continuation after an await checks `_destroyed`. destroy() cancels
     * the soup request, so a late failure or success lands here with the flag
     * set and returns without touching actors or arming a timer.
     */
    async _refreshTitle() {
        if (this._destroyed)
            return;
        if (this._refreshInFlight) {
            this._refreshPending = true;
            return;
        }
        this._refreshInFlight = true;
        try {
            do {
                this._refreshPending = false;
                if (this._destroyed)
                    return;
                try {
                    const payload = await this._fetchWithRetry();
                    // The await is the suspension point: the indicator may
                    // have been destroyed while the request was in flight.
                    if (this._destroyed)
                        return;
                    this._applySuccess(payload);
                } catch (e) {
                    if (this._destroyed)
                        return;
                    console.error('onWatch: _refreshTitle failed', e);
                    // Keep the last good numbers; only the tint and tooltip
                    // change. Offline placeholder only before any success.
                    this._markStale(e.message || 'fetch failed');
                }
            } while (this._refreshPending && !this._destroyed);
        } finally {
            this._refreshInFlight = false;
        }
    }
});

/**
 * @param {string} extensionDir
 * @returns {OnWatchIndicator}
 */
export function addIndicator(extensionDir) {
    const indicator = new OnWatchIndicator(extensionDir);
    Main.panel.addToStatusArea('onwatch-menubar', indicator, 1, 'right');
    return indicator;
}
