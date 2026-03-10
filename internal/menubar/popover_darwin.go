//go:build menubar && darwin

package menubar

import "errors"

const (
	menubarPopoverWidth  = 420
	menubarPopoverHeight = 600
)

var errNativePopoverUnavailable = errors.New("native macOS menubar host unavailable")

type menubarPopover interface {
	ShowURL(string) error
	ToggleURL(string) error
	Close()
	Destroy()
}
