//go:build menubar && darwin && cgo

package menubar

/*
#cgo CFLAGS: -DDARWIN -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>

// Implemented in popover_darwin.m. Returns 0 when no status item is
// reachable, so the caller can fall back to systray's plain SetTitle. A plain
// int avoids depending on how cgo maps <stdbool.h>'s bool.
int onwatch_status_item_title(const char *title, int dim);
*/
import "C"

import (
	"fyne.io/systray"
	"unsafe"
)

// setTrayTitle writes the title with the secondary-label color when dim is
// set. fyne/systray only exposes plain SetTitle, so the attributed variant is
// applied through the small cgo bridge.
//
// The plain SetTitle call comes FIRST: it runs fyne's updateTitleButtonStyle,
// which switches the button out of NSImageOnly (the mode setupTrayIcon leaves
// it in when the icon was installed with an empty title). Setting
// attributedTitle directly would leave the title invisible.
func setTrayTitle(title string, dim bool) {
	systray.SetTitle(title)
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	// Failure is fine: the plain title above is already the correct fallback.
	_ = C.onwatch_status_item_title(cTitle, C.int(boolToInt(dim)))
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
