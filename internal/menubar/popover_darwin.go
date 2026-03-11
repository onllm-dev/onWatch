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
	Close()
	Destroy()
}
