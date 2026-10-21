//go:build menubar && windows

package menubar

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/jchv/go-webview2/webviewloader"
	"github.com/pkg/browser"
	"golang.org/x/sys/windows/registry"
)

// The Windows quick view: /menubar in a WebView2 control hosted by a
// borderless, rounded tool window that behaves like a tray flyout. It is the
// counterpart of the macOS WKWebView popover (popover_darwin.m): created once
// and kept warm, anchored to the tray click, sized by the page through the
// onwatchResize channel, closed by the page's onwatchAction channel, by Esc,
// or by clicking anywhere else. WebView2 ships with Windows 10 and 11; where
// the runtime is missing the companion falls back to an app-mode browser
// window (quickview_desktop.go).

const (
	wsPopup         = 0x80000000
	wsClipChildren  = 0x02000000
	wsExToolWindow  = 0x00000080
	wsExTopmost     = 0x00000008
	csDropShadow    = 0x00020000
	idcArrow        = 32512
	swHide          = 0
	swShow          = 5
	swpNoActivate   = 0x0010
	waInactive      = 0
	wmDestroy       = 0x0002
	wmMove          = 0x0003
	wmSize          = 0x0005
	wmActivate      = 0x0006
	wmClose         = 0x0010
	wmDPIChanged    = 0x02E0
	wmApp           = 0x8000
	wmDispatch      = wmApp + 1
	wmTeardown      = wmApp + 2
	hwndTopmost     = ^uintptr(0) // (HWND)-1
	coinitApartment = 0x2

	dwmwaUseImmersiveDarkMode = 20
	dwmwaWindowCorner         = 33
	dwmwcpRound               = 2

	errorClassAlreadyExists = 1410
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procSetWindowPos         = user32.NewProc("SetWindowPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procRegisterWindowMsgW   = user32.NewProc("RegisterWindowMessageW")
	gdi32                    = syscall.NewLazyDLL("gdi32.dll")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	dwmapi                   = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttr     = dwmapi.NewProc("DwmSetWindowAttribute")
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	webView2WndProcOnce      sync.Once
	webView2WndProcCallback  uintptr
	errWebView2Unavailable   = errors.New("WebView2 runtime unavailable")
	errWebView2InitTimedOut  = errors.New("WebView2 initialisation timed out")
	activeWebView2           *webView2Popover // the one popover per process; read by the window procedure
	webView2InitTimeout      = 20 * time.Second
	webView2ClassName        = "onWatchQuickView"
	webView2TaskbarCreatedID uint32
)

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type winMsg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       winPoint
	LPrivate uint32
}

// webView2HostScript runs at document creation. Links that leave the local
// dashboard belong in the user's browser (as on macOS), not in a second
// WebView2 window, and Esc closes the flyout like any Windows flyout.
const webView2HostScript = `(() => {
	const post = (body) => window.chrome.webview.postMessage(JSON.stringify({ channel: "onwatchAction", body }));
	const external = (href) => {
		try {
			const u = new URL(href, location.href);
			return (u.protocol === "http:" || u.protocol === "https:") && u.origin !== location.origin ? u.href : "";
		} catch (_) {
			return "";
		}
	};
	document.addEventListener("click", (event) => {
		const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
		const url = link ? external(link.href) : "";
		if (url) {
			event.preventDefault();
			post({ action: "open_url", url });
		}
	}, true);
	const open = window.open;
	window.open = function (href, ...rest) {
		const url = href ? external(String(href)) : "";
		if (url) {
			post({ action: "open_url", url });
			return null;
		}
		return open.call(window, href, ...rest);
	};
	document.addEventListener("keydown", (event) => {
		if (event.key === "Escape" && !event.defaultPrevented) {
			post("close");
		}
	});
})();`

type webView2Popover struct {
	width, height int // logical pixels; height until the page reports its own
	dataDir       string
	dark          bool

	mu    sync.Mutex
	queue []func()
	hwnd  uintptr
	done  chan struct{} // closed when the UI thread exits

	// Owned by the UI thread.
	chromium     *edge.Chromium
	loadedURL    string
	cssHeight    float64
	anchor       winPoint
	visible      bool
	lastAutoHide time.Time
}

// newMenubarPopover prefers the native WebView2 flyout and falls back to an
// app-mode browser window. ONWATCH_QUICKVIEW_BROWSER asks for the browser
// explicitly.
func newMenubarPopover(width, height int) (menubarPopover, error) {
	if strings.TrimSpace(os.Getenv("ONWATCH_QUICKVIEW_BROWSER")) == "" {
		popover, err := newWebView2Popover(width, height)
		if err == nil {
			return popover, nil
		}
		slog.Default().Info("WebView2 quick view unavailable, using an app-mode browser window", "error", err)
	}
	return newAppModePopover(width, height)
}

func newWebView2Popover(width, height int) (*webView2Popover, error) {
	if activeWebView2 != nil {
		return nil, errors.New("WebView2 quick view already created")
	}
	version, err := webviewloader.GetInstalledVersion()
	if err != nil || version == "" {
		return nil, fmt.Errorf("%w: %v", errWebView2Unavailable, err)
	}
	p := &webView2Popover{
		width:   width,
		height:  height,
		dataDir: filepath.Join(defaultCompanionPIDDir(), "webview2"),
		dark:    systemPrefersDark(),
		done:    make(chan struct{}),
	}
	if err := os.MkdirAll(p.dataDir, 0o700); err != nil {
		return nil, err
	}
	activeWebView2 = p
	ready := make(chan error, 1)
	go p.run(ready)
	select {
	case err := <-ready:
		if err != nil {
			activeWebView2 = nil
			return nil, err
		}
	case <-time.After(webView2InitTimeout):
		p.Destroy()
		return nil, errWebView2InitTimedOut
	}
	slog.Default().Info("WebView2 quick view ready", "runtime", version)
	return p, nil
}

func (p *webView2Popover) ShowURL(url string) error {
	return p.invoke(func() {
		p.load(url)
		p.show()
	})
}

func (p *webView2Popover) ToggleURL(url string) error {
	return p.invoke(func() {
		if p.visible {
			p.hide(false)
			return
		}
		// The tray click that toggles the flyout first takes focus from it,
		// hiding it; that same click must not open it again.
		if suppressToggleReopen(p.lastAutoHide, time.Now()) {
			p.lastAutoHide = time.Time{}
			return
		}
		p.load(url)
		p.show()
	})
}

// Preload navigates the hidden WebView so the first open shows a rendered page.
func (p *webView2Popover) Preload(url string) error {
	return p.invoke(func() { p.load(url) })
}

func (p *webView2Popover) Close() {
	_ = p.invoke(func() { p.hide(false) })
}

func (p *webView2Popover) Destroy() {
	p.mu.Lock()
	hwnd := p.hwnd
	p.hwnd = 0
	p.mu.Unlock()
	if hwnd == 0 {
		return
	}
	_, _, _ = procPostMessageW.Call(hwnd, wmTeardown, 0, 0)
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

// invoke runs fn on the UI thread, which owns the window and the WebView, and
// waits for it.
func (p *webView2Popover) invoke(fn func()) error {
	p.mu.Lock()
	hwnd := p.hwnd
	if hwnd == 0 {
		p.mu.Unlock()
		return errNativePopoverUnavailable
	}
	finished := make(chan struct{})
	p.queue = append(p.queue, func() {
		defer close(finished)
		fn()
	})
	p.mu.Unlock()
	if r, _, _ := procPostMessageW.Call(hwnd, wmDispatch, 0, 0); r == 0 {
		return errNativePopoverUnavailable
	}
	select {
	case <-finished:
		return nil
	case <-p.done:
		return errNativePopoverUnavailable
	}
}

func (p *webView2Popover) drain() {
	p.mu.Lock()
	queue := p.queue
	p.queue = nil
	p.mu.Unlock()
	for _, fn := range queue {
		fn()
	}
}

func (p *webView2Popover) run(ready chan<- error) {
	// The window, the WebView and every COM call must stay on one STA thread.
	// The thread is never unlocked, so it exits with this goroutine.
	runtime.LockOSThread()
	defer close(p.done)
	_, _, _ = procCoInitializeEx.Call(0, coinitApartment)

	hwnd, err := p.createWindow()
	if err != nil {
		ready <- err
		return
	}
	chromium := edge.NewChromium()
	chromium.DataPath = p.dataDir
	chromium.MessageCallback = p.onMessage
	if !chromium.Embed(hwnd) {
		_, _, _ = procDestroyWindow.Call(hwnd)
		ready <- fmt.Errorf("%w: could not create the WebView2 controller", errWebView2Unavailable)
		return
	}
	p.chromium = chromium
	p.configure()
	chromium.Resize()

	p.mu.Lock()
	p.hwnd = hwnd
	p.mu.Unlock()
	ready <- nil

	var msg winMsg
	for {
		if r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0); int32(r) <= 0 {
			return
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (p *webView2Popover) createWindow() (uintptr, error) {
	webView2WndProcOnce.Do(func() {
		webView2WndProcCallback = syscall.NewCallback(webView2WndProc)
		if name, err := syscall.UTF16PtrFromString("TaskbarCreated"); err == nil {
			id, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(name)))
			webView2TaskbarCreatedID = uint32(id)
		}
	})
	instance, _, _ := procGetModuleHandleW.Call(0)
	className, err := syscall.UTF16PtrFromString(webView2ClassName)
	if err != nil {
		return 0, err
	}
	title, err := syscall.UTF16PtrFromString("onWatch")
	if err != nil {
		return 0, err
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	bg := p.background()
	brush, _, _ := procCreateSolidBrush.Call(uintptr(bg.R) | uintptr(bg.G)<<8 | uintptr(bg.B)<<16)
	wc := wndClassExW{
		Style:      csDropShadow,
		WndProc:    webView2WndProcCallback,
		Instance:   instance,
		Cursor:     cursor,
		Background: brush,
		ClassName:  className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if errno, ok := err.(syscall.Errno); !ok || errno != errorClassAlreadyExists {
			return 0, fmt.Errorf("register quick view window class: %w", err)
		}
	}
	dpi := systemDPI()
	hwnd, _, err := procCreateWindowExW.Call(
		wsExToolWindow|wsExTopmost,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsPopup|wsClipChildren,
		0, 0, uintptr(scaleForDPI(p.width, dpi)), uintptr(scaleForDPI(p.height, dpi)),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create quick view window: %w", err)
	}
	// Windows 11 rounds and borders the flyout like the system's own; both
	// attributes are ignored on Windows 10.
	corner := int32(dwmwcpRound)
	_, _, _ = procDwmSetWindowAttr.Call(hwnd, dwmwaWindowCorner, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	if p.dark {
		on := int32(1)
		_, _, _ = procDwmSetWindowAttr.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
	}
	return hwnd, nil
}

func (p *webView2Popover) configure() {
	if settings, err := p.chromium.GetSettings(); err == nil {
		_ = settings.PutAreDefaultContextMenusEnabled(false)
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutIsZoomControlEnabled(false)
		_ = settings.PutIsStatusBarEnabled(false)
		_ = settings.PutAreBrowserAcceleratorKeysEnabled(false)
		_ = settings.PutIsPinchZoomEnabled(false)
		_ = settings.PutIsSwipeNavigationEnabled(false)
	}
	// Paint the page background before the document does, so opening the
	// flyout never flashes white.
	p.applyBackground()
	p.chromium.Init(webView2HostScript)
}

// background matches the page's --bg-body for the system theme.
func (p *webView2Popover) background() edge.COREWEBVIEW2_COLOR {
	if p.dark {
		return edge.COREWEBVIEW2_COLOR{A: 0xff, R: 0x0d, G: 0x0d, B: 0x0d}
	}
	return edge.COREWEBVIEW2_COLOR{A: 0xff, R: 0xf3, G: 0xf5, B: 0xf8}
}

func (p *webView2Popover) load(url string) {
	if url == "" || url == p.loadedURL {
		return
	}
	p.loadedURL = url
	p.chromium.Navigate(url)
}

func (p *webView2Popover) show() {
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&p.anchor)))
	p.layout()
	if p.visible {
		return
	}
	p.visible = true
	// Re-fetch on the warm page instead of reloading it, as macOS does. The
	// show hook always issues a real, no-store summary fetch and coalesces
	// with any in-flight request.
	p.chromium.Eval("window.__onwatchMenubarShown && window.__onwatchMenubarShown()")
	p.lastAutoHide = time.Time{}
	hwnd := p.windowHandle()
	_, _, _ = procShowWindow.Call(hwnd, swShow)
	// The controller was created under a hidden window and does not render
	// until told it is visible.
	_ = p.chromium.Show()
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	p.chromium.Focus()
}

func (p *webView2Popover) hide(lostFocus bool) {
	if !p.visible {
		return
	}
	p.visible = false
	if lostFocus {
		p.lastAutoHide = time.Now()
	}
	_, _, _ = procShowWindow.Call(p.windowHandle(), swHide)
	// A hidden controller stops rendering and throttles timers.
	_ = p.chromium.Hide()
}

// layout sizes the window for the monitor under the anchor and presses it
// against that monitor's taskbar edge.
func (p *webView2Popover) layout() {
	hwnd := p.windowHandle()
	if hwnd == 0 {
		return
	}
	dpi := dpiAt(p.anchor)
	work, _ := workAreaAt(p.anchor)
	cssHeight := p.cssHeight
	if cssHeight <= 0 {
		cssHeight = float64(p.height)
	}
	width := scaleForDPI(p.width, dpi)
	height := popoverHeightPixels(cssHeight, dpi, int(work.Bottom-work.Top))
	pos := quickViewPosition(
		cursorPoint{X: int(p.anchor.X), Y: int(p.anchor.Y)},
		screenRect{Left: int(work.Left), Top: int(work.Top), Right: int(work.Right), Bottom: int(work.Bottom)},
		width, height,
	)
	_, _, _ = procSetWindowPos.Call(hwnd, hwndTopmost,
		uintptr(int32(pos.X)), uintptr(int32(pos.Y)), uintptr(width), uintptr(height), swpNoActivate)
}

func (p *webView2Popover) windowHandle() uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hwnd
}

// onMessage handles one page message; WebView2 delivers it on the UI thread.
func (p *webView2Popover) onMessage(raw string) {
	msg := parseHostMessage(raw)
	switch msg.Kind {
	case hostMessageResize:
		p.cssHeight = msg.Height
		if p.visible {
			p.layout()
		}
	case hostMessageClose:
		p.hide(false)
	case hostMessageOpenDashboard:
		if target, ok := dashboardURLFor(p.loadedURL); ok {
			p.hide(false)
			go openInBrowser(target)
		}
	case hostMessageOpenURL:
		if externalURLAllowed(msg.URL) {
			p.hide(false)
			go openInBrowser(msg.URL)
		}
	case hostMessageTheme:
		// The page's effective theme can differ from the OS; match the default
		// background so a repaint never flashes the wrong color.
		p.dark = msg.Theme == "dark"
		p.applyBackground()
	}
}

// applyBackground repaints the WebView default background for the current
// theme. It is a no-op before the controller exists.
func (p *webView2Popover) applyBackground() {
	if p.chromium == nil {
		return
	}
	if controller := p.chromium.GetController(); controller != nil {
		if controller2 := controller.GetICoreWebView2Controller2(); controller2 != nil {
			_ = controller2.PutDefaultBackgroundColor(p.background())
		}
	}
}

func openInBrowser(target string) {
	if err := browser.OpenURL(target); err != nil {
		slog.Default().Warn("failed to open browser from quick view", "error", err)
	}
}

func webView2WndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	p := activeWebView2
	if p == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return r
	}
	switch uint32(msg) {
	case wmDispatch:
		p.drain()
		return 0
	case wmTeardown:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	case wmSize:
		if p.chromium != nil {
			p.chromium.Resize()
		}
		return 0
	case wmMove:
		if p.chromium != nil {
			_ = p.chromium.NotifyParentWindowPositionChanged()
		}
		return 0
	case wmActivate:
		// Clicking anywhere outside the flyout dismisses it.
		if wParam&0xffff == waInactive {
			p.hide(true)
		}
	case wmClose:
		// Alt+F4 hides; the window lives as long as the companion.
		p.hide(false)
		return 0
	case wmDPIChanged:
		// layout already sized the window for the monitor it moved to.
		return 0
	default:
		if webView2TaskbarCreatedID != 0 && uint32(msg) == webView2TaskbarCreatedID {
			// Explorer restarted; fyne.io/systray re-adds its icon, so put
			// the exact-size one back once it has.
			time.AfterFunc(time.Second, reassertNativeTrayIcon)
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

// systemPrefersDark reads the "app mode" the user picked in Settings >
// Personalization > Colors; the page follows the same setting through
// prefers-color-scheme.
func systemPrefersDark() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	light, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && light == 0
}
