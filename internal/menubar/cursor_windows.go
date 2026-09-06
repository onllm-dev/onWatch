//go:build menubar && windows

package menubar

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSystemParameters = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	spiGetWorkArea = 0x0030
	smCXScreen     = 0
	smCYScreen     = 1
)

type winPoint struct{ X, Y int32 }

type winRect struct{ Left, Top, Right, Bottom int32 }

// quickViewAnchor places the window next to the cursor that clicked the tray,
// pressed against the taskbar edge, so it opens where the macOS popover would.
func quickViewAnchor(width, height int) *windowPosition {
	var pt winPoint
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return nil
	}
	var rect winRect
	if r, _, _ := procSystemParameters.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&rect)), 0); r == 0 {
		cx, _, _ := procGetSystemMetrics.Call(smCXScreen)
		cy, _, _ := procGetSystemMetrics.Call(smCYScreen)
		rect = winRect{Right: int32(cx), Bottom: int32(cy)}
	}
	pos := quickViewPosition(
		cursorPoint{X: int(pt.X), Y: int(pt.Y)},
		screenRect{Left: int(rect.Left), Top: int(rect.Top), Right: int(rect.Right), Bottom: int(rect.Bottom)},
		width, height,
	)
	return &pos
}
