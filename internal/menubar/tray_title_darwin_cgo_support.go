//go:build menubar && darwin && cgo

package menubar

/*
#include <stdbool.h>

// Plain int avoids importing Cocoa's headers into this translation unit; the
// enum values match NSNoImage/NSImageOnly/NSImageLeft.
int onwatch_title_image_position(bool hasImage, bool hasTitle);
*/
import "C"

// Mirror NSNoImage / NSImageOnly / NSImageLeft. The constants are repeated
// here because importing Cocoa into this translation unit would pull the whole
// framework header into cgo's precompiled set.
const (
	imagePositionNoImage = 0
	imagePositionOnly    = 1
	imagePositionLeft    = 2
)

func onwatchTitleImagePosition(hasImage, hasTitle bool) int {
	toC := func(v bool) C.bool {
		if v {
			return C.bool(true)
		}
		return C.bool(false)
	}
	return int(C.onwatch_title_image_position(toC(hasImage), toC(hasTitle)))
}
