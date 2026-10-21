//go:build menubar && darwin && cgo

package menubar

import "testing"

// syscall_tests: the attributed title path bypasses fyne's updateTitleButtonStyle,
// so the image position must be mirrored. This is the C function the bridge
// calls; it is pure and testable.
func TestOnwatchTitleImagePosition(t *testing.T) {
	if got := onwatchTitleImagePosition(true, true); got != imagePositionLeft {
		t.Fatalf("image+title = %d, want left", got)
	}
	if got := onwatchTitleImagePosition(true, false); got != imagePositionOnly {
		t.Fatalf("image only = %d", got)
	}
	if got := onwatchTitleImagePosition(false, true); got != imagePositionNoImage {
		t.Fatalf("title only = %d", got)
	}
}
