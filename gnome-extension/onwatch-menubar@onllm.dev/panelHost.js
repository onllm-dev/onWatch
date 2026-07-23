/**
 * Manages the Python WebKit panel host process (shared /menubar page).
 */

import GLib from 'gi://GLib';
import Gio from 'gi://Gio';

export class PanelHostController {
    /**
     * @param {string} extensionDir absolute path to extension root
     */
    constructor(extensionDir) {
        this._extensionDir = extensionDir;
        this._proc = null;
        this._stdin = null;
        this._stdout = null;
        this._ready = false;
        this._starting = false;
        this._pending = [];
    }

    destroy() {
        this._send('QUIT');
        try {
            this._proc?.force_exit();
        } catch (_e) {
            // ignore
        }
        this._proc = null;
        this._stdin = null;
        this._stdout = null;
        this._ready = false;
    }

    /**
     * @param {string} url
     */
    async show(url) {
        await this._ensureStarted();
        this._send(`SHOW ${url}`);
    }

    hide() {
        if (!this._proc)
            return;
        this._send('HIDE');
    }

    /**
     * @param {string} url
     */
    async toggle(url) {
        await this._ensureStarted();
        this._send(`TOGGLE ${url}`);
    }

    async _ensureStarted() {
        if (this._ready && this._proc)
            return;
        if (this._starting) {
            await this._waitReady();
            return;
        }
        this._starting = true;
        try {
            await this._spawn();
            await this._waitReady();
        } finally {
            this._starting = false;
        }
    }

    _waitReady() {
        return new Promise((resolve, reject) => {
            const start = GLib.get_monotonic_time();
            const id = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 50, () => {
                if (this._ready) {
                    resolve();
                    return GLib.SOURCE_REMOVE;
                }
                if (GLib.get_monotonic_time() - start > 5_000_000) {
                    reject(new Error('panel host failed to start'));
                    return GLib.SOURCE_REMOVE;
                }
                return GLib.SOURCE_CONTINUE;
            });
            this._pending.push(id);
        });
    }

    _spawn() {
        const script = GLib.build_filenamev([this._extensionDir, 'panel_host.py']);
        if (!GLib.file_test(script, GLib.FileTest.IS_REGULAR))
            return Promise.reject(new Error(`missing ${script}`));

        const launcher = new Gio.SubprocessLauncher({
            flags:
                Gio.SubprocessFlags.STDIN_PIPE |
                Gio.SubprocessFlags.STDOUT_PIPE |
                Gio.SubprocessFlags.STDERR_PIPE,
        });
        // Inherit display; force X11 for positioning under GNOME Wayland.
        if (!GLib.getenv('GDK_BACKEND') &&
            (GLib.getenv('WAYLAND_DISPLAY') || GLib.getenv('XDG_SESSION_TYPE') === 'wayland'))
            launcher.setenv('GDK_BACKEND', 'x11', false);

        const proc = launcher.spawnv(['python3', script]);
        this._proc = proc;
        this._stdin = proc.get_stdin_pipe();
        this._stdout = new Gio.DataInputStream({
            base_stream: proc.get_stdout_pipe(),
        });
        this._readLoop();
        proc.wait_async(null, () => {
            this._ready = false;
            this._proc = null;
            this._stdin = null;
            this._stdout = null;
        });
        return Promise.resolve();
    }

    _readLoop() {
        if (!this._stdout)
            return;
        this._stdout.read_line_async(GLib.PRIORITY_DEFAULT, null, (stream, res) => {
            try {
                const [line] = stream.read_line_finish_utf8(res);
                if (line === null) {
                    this._ready = false;
                    return;
                }
                const text = line.trim();
                if (text === 'READY' || text === 'OK' || text === 'PONG' || text.startsWith('VISIBLE'))
                    this._ready = true;
                if (text.startsWith('ERR'))
                    console.error(`onWatch panel host: ${text}`);
                this._readLoop();
            } catch (e) {
                this._ready = false;
                console.error('onWatch panel host read failed', e);
            }
        });
    }

    /**
     * @param {string} line
     */
    _send(line) {
        if (!this._stdin)
            return;
        try {
            this._stdin.write_all(new TextEncoder().encode(`${line}\n`), null);
        } catch (e) {
            console.error('onWatch panel host write failed', e);
            this._ready = false;
        }
    }
}
