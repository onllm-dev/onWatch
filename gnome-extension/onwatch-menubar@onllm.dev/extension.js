/**
 * onWatch Menubar – GNOME Shell extension entry.
 *
 * Wayland cannot re-read the same file:// module URI after disable/enable
 * (GJS module cache). We import indicator.js via a mtime-stamped copy so
 * install + disable/enable picks up code changes without logout.
 */

import GLib from 'gi://GLib';
import Gio from 'gi://Gio';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

export default class OnWatchMenubarExtension extends Extension {
    /**
     * Import indicator module, busting GJS cache when the file changes.
     *
     * @returns {Promise<{addIndicator: function(string): object}>}
     */
    async _loadIndicatorModule() {
        const srcPath = GLib.build_filenamev([this.path, 'indicator.js']);
        const src = Gio.File.new_for_path(srcPath);

        let mtime = 0;
        try {
            const info = src.query_info('time::modified', Gio.FileQueryInfoFlags.NONE, null);
            mtime = info.get_attribute_uint64('time::modified');
        } catch (_e) {
            mtime = Date.now();
        }

        // Unique path per content revision → new module identity in GJS.
        const bustName = `.indicator_cache_${mtime}.js`;
        const bustPath = GLib.build_filenamev([this.path, bustName]);
        const bust = Gio.File.new_for_path(bustPath);

        try {
            if (!bust.query_exists(null)) {
                // Drop older cache copies to avoid litter.
                this._cleanupIndicatorCaches(bustName);
                src.copy(bust, Gio.FileCopyFlags.OVERWRITE, null, null);
            }
        } catch (e) {
            console.error('onWatch: cache-bust copy failed, importing indicator.js directly', e);
            return import(`file://${srcPath}`);
        }

        return import(`file://${bustPath}`);
    }

    /**
     * @param {string} keepName
     */
    _cleanupIndicatorCaches(keepName) {
        try {
            const dir = Gio.File.new_for_path(this.path);
            const enumerator = dir.enumerate_children(
                'standard::name',
                Gio.FileQueryInfoFlags.NONE,
                null);
            let info;
            while ((info = enumerator.next_file(null)) !== null) {
                const name = info.get_name();
                if (name.startsWith('.indicator_cache_') && name !== keepName) {
                    try {
                        dir.get_child(name).delete(null);
                    } catch (_e) {
                        // ignore
                    }
                }
            }
            enumerator.close(null);
        } catch (_e) {
            // ignore
        }
    }

    async enable() {
        try {
            const mod = await this._loadIndicatorModule();
            this._indicator = mod.addIndicator(this.path);
            console.log('onWatch menubar: extension enabled (cache-bust import)');
        } catch (e) {
            console.error('onWatch menubar: enable failed', e);
            // Last resort: direct import (may be cached).
            try {
                const mod = await import(`file://${this.path}/indicator.js`);
                this._indicator = mod.addIndicator(this.path);
            } catch (e2) {
                console.error('onWatch menubar: fallback import failed', e2);
            }
        }
    }

    disable() {
        this._indicator?.destroy();
        this._indicator = null;
    }
}
