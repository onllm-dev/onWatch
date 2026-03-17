//go:build menubar && linux && !cgo

package menubar

func newMenubarPopover(width, height int) (menubarPopover, error) {
	return nil, errNativePopoverUnavailable
}
