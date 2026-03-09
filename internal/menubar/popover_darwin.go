//go:build menubar && darwin

package menubar

import "errors"

const (
	menubarPopoverWidth  = 400
	menubarPopoverHeight = 500
)

var errNativePopoverUnavailable = errors.New("native macOS popover unavailable")

type menubarPopover interface {
	ShowURL(string) error
	ToggleURL(string) error
	Close()
	Destroy()
}
