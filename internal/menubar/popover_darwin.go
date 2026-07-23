//go:build menubar && darwin

package menubar

import "errors"

const (
	menubarPopoverWidth  = 360
	menubarPopoverHeight = 680
)

var errNativePopoverUnavailable = errors.New("native macOS menubar host unavailable")

type menubarPopover interface {
	ShowURL(string) error
	ToggleURL(string) error
	// Preload warms the WebView document without showing the panel.
	// Optional: implementations may no-op if preload is unsupported.
	Preload(string) error
	Close()
	Destroy()
}
