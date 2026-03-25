//go:build menubar && linux && cgo

package menubar

import "testing"

func TestNewMenubarPopoverLifecycle(t *testing.T) {
	var (
		popover menubarPopover
		err     error
	)

	runOnMainThread(t, func() {
		popover, err = newMenubarPopover(320, 240)
	})
	if err != nil {
		t.Fatalf("newMenubarPopover returned error: %v", err)
	}
	if popover == nil {
		t.Fatal("expected popover instance")
	}

	runOnMainThread(t, func() {
		popover.Close()
		popover.Destroy()
		popover.Destroy()
	})
}
