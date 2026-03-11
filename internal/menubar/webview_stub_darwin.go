//go:build menubar && darwin && !cgo

package menubar

func newMenubarPopover(width, height int) (menubarPopover, error) {
	return nil, errNativePopoverUnavailable
}
