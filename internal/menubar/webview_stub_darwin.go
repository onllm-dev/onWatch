//go:build menubar && darwin && !cgo

package menubar

// newMenubarPopover returns errNativePopoverUnavailable when CGO is disabled;
// the companion falls back to opening the menubar page in the default browser.
func newMenubarPopover(width, height int) (menubarPopover, error) {
	return nil, errNativePopoverUnavailable
}
