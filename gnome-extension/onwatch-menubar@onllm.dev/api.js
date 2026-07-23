/**
 * HTTP client for the onWatch daemon menubar APIs (loopback only).
 */

import GLib from 'gi://GLib';
import Soup from 'gi://Soup';

const DEFAULT_PORT = 9211;
const SESSION = new Soup.Session({timeout: 3});

/**
 * @returns {number}
 */
export function resolvePort() {
    // Optional override file written by the daemon or the user.
    const path = GLib.build_filenamev([GLib.get_home_dir(), '.onwatch', 'port']);
    try {
        const [ok, contents] = GLib.file_get_contents(path);
        if (ok) {
            const text = new TextDecoder().decode(contents).trim();
            const n = parseInt(text, 10);
            if (n > 0 && n < 65536)
                return n;
        }
    } catch (_e) {
        // ignore missing file
    }

    const envPort = GLib.getenv('ONWATCH_PORT');
    if (envPort) {
        const n = parseInt(envPort, 10);
        if (n > 0 && n < 65536)
            return n;
    }
    return DEFAULT_PORT;
}

/**
 * @param {number} port
 * @returns {string}
 */
export function baseURL(port = resolvePort()) {
    return `http://127.0.0.1:${port}`;
}

/**
 * @param {string} path
 * @param {number} [port]
 * @returns {Promise<?object>}
 */
export async function fetchJSON(path, port = resolvePort()) {
    const url = `${baseURL(port)}${path}`;
    const message = Soup.Message.new('GET', url);
    if (!message)
        return null;

    try {
        const bytes = await SESSION.send_and_read_async(
            message,
            GLib.PRIORITY_DEFAULT,
            null);
        if (message.get_status() !== Soup.Status.OK)
            return null;
        const data = bytes.get_data();
        if (!data || data.length === 0)
            return null;
        const text = new TextDecoder().decode(data);
        return JSON.parse(text);
    } catch (_e) {
        return null;
    }
}

/**
 * Compact tray title matching macOS TrayTitle (server-side), plus segments
 * with icon stems for the GNOME top bar.
 *
 * @param {number} [port]
 * @returns {Promise<{title: string, tooltip: string, online: boolean, segments: object[]}>}
 */
export async function fetchTrayTitle(port = resolvePort()) {
    const body = await fetchJSON('/api/menubar/tray-title', port);
    if (!body) {
        return {
            title: '—',
            tooltip: 'onWatch daemon offline',
            online: false,
            segments: [],
        };
    }
    return {
        title: body.title || '—',
        tooltip: body.tooltip || 'onWatch',
        online: body.online !== false,
        segments: Array.isArray(body.segments) ? body.segments : [],
    };
}

/**
 * Shared /menubar page URL. Optionally pins system dark/light so WebKitGTK
 * (which often reports prefers-color-scheme: light under GNOME) matches the
 * GNOME panel appearance when menubar theme mode is "system".
 *
 * @param {number} [port]
 * @param {{systemPrefersDark?: boolean}} [opts]
 * @returns {string}
 */
export function menubarPageURL(port = resolvePort(), opts = {}) {
    const url = `${baseURL(port)}/menubar`;
    if (opts && typeof opts.systemPrefersDark === 'boolean')
        return `${url}?system_prefers_dark=${opts.systemPrefersDark ? '1' : '0'}`;
    return url;
}
