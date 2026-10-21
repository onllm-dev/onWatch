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
// applied through the small cgo bridge; the plain call remains the fallback.
func setTrayTitle(title string, dim bool) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	if C.onwatch_status_item_title(cTitle, C.int(boolToInt(dim))) != 0 {
		return
	}
	systray.SetTitle(title)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
