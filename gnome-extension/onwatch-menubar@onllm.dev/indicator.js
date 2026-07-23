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
        this._giconCache = new Map();
        this._icons = [];
        // Native color object (from theme node or from_string) — never a plain {}
        this._tintColor = colorFromCss('#f2f2f2');

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
                this._openPanel('click');
                return Clutter.EVENT_STOP;
            }
            return Clutter.EVENT_PROPAGATE;
        });

        this._refreshTitle().catch(e => console.error('onWatch: initial refresh failed', e));
        this._refreshId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, REFRESH_MS_DEFAULT, () => {
            this._refreshTitle().catch(e => console.error('onWatch: refresh failed', e));
            return GLib.SOURCE_CONTINUE;
        });
    }

    destroy() {
        if (this._refreshId) {
            GLib.source_remove(this._refreshId);
            this._refreshId = 0;
        }
        this._host?.destroy();
        this._host = null;
        this._giconCache?.clear();
        this._icons = [];
        super.destroy();
    }

    _onHoverChanged() {
        if (this.hover)
            this._openPanel('hover');
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
            // Prefer this button's fg (same as tray text) — light text ⇒ dark panel.
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
        try {
            const url = menubarPageURL(this._port, {
                systemPrefersDark: this._panelPrefersDark(),
            });
            await this._host.show(url);
        } catch (e) {
            console.error('onWatch: failed to open panel', e);
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
     * @param {string} text
     * @param {boolean} [offline]
     */
    _setOffline(text, offline = true) {
        this._clearBox();
        const label = new St.Label({
            text: text || '—',
            y_align: Clutter.ActorAlign.CENTER,
            style_class: offline
                ? 'onwatch-menubar-label onwatch-menubar-offline'
                : 'onwatch-menubar-label',
        });
        this._box.add_child(label);
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
            // Non-fatal — never wipe the tray for a11y failures.
            console.debug?.('onWatch: set accessible name skipped', e);
        }
    }

    /**
     * Sample native foreground color from a live label (inherits panel theme).
     * Keep the GObject color struct — ColorizeEffect rejects plain objects.
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
        this._clearBox();

        if (!online) {
            this._setOffline('—', true);
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
                style_class: 'onwatch-menubar-slot',
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
            if (seg.label)
                label.set_accessible_name?.(`${seg.label} ${seg.text || ''}`.trim());
            slot.add_child(label);
            this._box.add_child(slot);
        });

        GLib.idle_add(GLib.PRIORITY_DEFAULT_IDLE, () => {
            this._updateTintFromTheme();
            this._applyTintToIcons();
            return GLib.SOURCE_REMOVE;
        });
        GLib.timeout_add(GLib.PRIORITY_DEFAULT, 200, () => {
            this._updateTintFromTheme();
            this._applyTintToIcons();
            return GLib.SOURCE_REMOVE;
        });
    }

    async _refreshTitle() {
        this._port = resolvePort();
        try {
            const {title, tooltip, online, segments} = await fetchTrayTitle(this._port);
            this._renderSegments(segments, title, online);
            this.set_reactive(true);
            // Must not throw after a successful render (old bug wiped UI to "—").
            this._setName(tooltip || title || 'onWatch');
        } catch (e) {
            console.error('onWatch: _refreshTitle failed', e);
            // Only show offline if we have nothing useful on screen.
            if (!this._box.get_n_children || this._box.get_n_children() === 0)
                this._setOffline('—', true);
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
