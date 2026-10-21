/**
 * HTTP client for the onWatch daemon menubar APIs (loopback only).
 */

import GLib from 'gi://GLib';
import Soup from 'gi://Soup';

const DEFAULT_PORT = 9211;

/**
 * Per-attempt timeout for daemon requests, matching the Go tray client
 * (`cmd/onwatch/menubar_runtime.go`): 15 s, with one retry driven by the
 * caller. libsoup sets this per session, so it lives here next to the session.
 */
export const SESSION_TIMEOUT_SECONDS = 15;
const SESSION = new Soup.Session({timeout: SESSION_TIMEOUT_SECONDS});

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
 * GET a daemon JSON endpoint.
 *
 * Resolves with the decoded body on success and rejects on any failure
 * (network error, non-2xx status, empty body, malformed JSON). Callers must
 * tell success from failure by catching; there is no fake-offline payload, so
 * a caller can keep showing its last good data instead of wiping the screen.
 *
 * @param {string} path
 * @param {number} [port]
 * @param {?Gio.Cancellable} [cancellable] cancels the request when the caller
 *        is torn down
 * @returns {Promise<object>}
 */
export async function fetchJSON(path, port = resolvePort(), cancellable = null) {
    const url = `${baseURL(port)}${path}`;
    const message = Soup.Message.new('GET', url);
    if (!message)
        throw new Error(`onWatch: invalid request URL ${url}`);

    const bytes = await SESSION.send_and_read_async(
        message,
        GLib.PRIORITY_DEFAULT,
        cancellable);

    const status = message.get_status();
    if (status !== Soup.Status.OK)
        throw new Error(`onWatch: ${path} returned ${status}`);

    const data = bytes.get_data();
    if (!data || data.length === 0)
        throw new Error(`onWatch: ${path} returned an empty body`);

    const text = new TextDecoder().decode(data);
    try {
        return JSON.parse(text);
    } catch (e) {
        throw new Error(`onWatch: ${path} returned invalid JSON: ${e.message}`);
    }
}

/**
 * Compact tray title matching macOS TrayTitle (server-side), plus segments
 * with icon stems for the GNOME top bar.
 *
 * Rejects when the daemon cannot be reached, so the indicator can retain its
 * last good render. `latestAt` is passed through when the payload carries a
 * `latest_at` timestamp (used for the stale "last updated Xm ago" label).
 *
 * @param {number} [port]
 * @param {?Gio.Cancellable} [cancellable]
 * @returns {Promise<{title: string, tooltip: string, online: boolean, segments: object[], latestAt: ?string}>}
 */
export async function fetchTrayTitle(port = resolvePort(), cancellable = null) {
    const body = await fetchJSON('/api/menubar/tray-title', port, cancellable);
    if (!body || typeof body !== 'object')
        throw new Error('onWatch: tray-title returned no payload');
    return {
        title: body.title || '',
        tooltip: body.tooltip || 'onWatch',
        online: body.online !== false,
        segments: Array.isArray(body.segments) ? body.segments : [],
        latestAt: typeof body.latest_at === 'string' ? body.latest_at : null,
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
