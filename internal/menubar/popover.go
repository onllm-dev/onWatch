//go:build menubar && (darwin || linux || windows)

package menubar

import "errors"

const (
	menubarPopoverWidth  = 360
	menubarPopoverHeight = 680
)

var errNativePopoverUnavailable = errors.New("native menubar host unavailable")

// menubarPopover is the quick-view host behind the tray icon. macOS backs it
// with a WKWebView panel; Linux and Windows back it with a Chromium-family
// browser window in --app mode. The controller only talks to this interface.
type menubarPopover interface {
	ShowURL(string) error
	ToggleURL(string) error
	// Preload warms the WebView document without showing the panel.
	// Optional: implementations may no-op if preload is unsupported.
	Preload(string) error
	Close()
	Destroy()
}
