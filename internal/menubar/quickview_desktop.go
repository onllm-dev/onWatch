//go:build menubar && (linux || windows)

package menubar

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// quickViewWindow hosts /menubar in a Chromium-family browser running in
// --app mode with a private profile, giving Linux and Windows a popover-like
// window the companion can open and close from the tray.
type quickViewWindow struct {
	mu         sync.Mutex
	browser    string
	profileDir string
	width      int
	height     int
	cmd        *exec.Cmd
}

func newMenubarPopover(width, height int) (menubarPopover, error) {
	browser, ok := findAppModeBrowser(runtime.GOOS, exec.LookPath, fileExists, os.Getenv)
	if !ok {
		return nil, errNativePopoverUnavailable
	}
	return &quickViewWindow{
		browser:    browser,
		profileDir: filepath.Join(defaultCompanionPIDDir(), "quickview-profile"),
		width:      width,
		height:     height,
	}, nil
}

func (q *quickViewWindow) ShowURL(url string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cmd != nil {
		return nil
	}
	return q.startLocked(url)
}

func (q *quickViewWindow) ToggleURL(url string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cmd != nil {
		q.closeLocked()
		return nil
	}
	return q.startLocked(url)
}

// Preload is a no-op: starting a browser process just to warm it would cost
// more RAM than the whole daemon.
func (q *quickViewWindow) Preload(string) error { return nil }

func (q *quickViewWindow) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeLocked()
}

func (q *quickViewWindow) Destroy() { q.Close() }

func (q *quickViewWindow) startLocked(url string) error {
	if err := os.MkdirAll(q.profileDir, 0o700); err != nil {
		return err
	}
	args := appModeArgs(url, q.profileDir, q.width, q.height, quickViewAnchor(q.width, q.height))
	cmd := exec.Command(q.browser, args...)
	cmd.SysProcAttr = quickViewSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	q.cmd = cmd
	slog.Default().Debug("quick view opened", "browser", q.browser, "pid", cmd.Process.Pid)
	go func(cmd *exec.Cmd) {
		_ = cmd.Wait()
		q.mu.Lock()
		if q.cmd == cmd {
			q.cmd = nil
		}
		q.mu.Unlock()
	}(cmd)
	return nil
}

func (q *quickViewWindow) closeLocked() {
	if q.cmd == nil || q.cmd.Process == nil {
		q.cmd = nil
		return
	}
	_ = q.cmd.Process.Kill()
	q.cmd = nil
}
