//go:build menubar && windows

package menubar

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW  = user32.NewProc("GetMonitorInfoW")
	procSystemParameters = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	spiGetWorkArea          = 0x0030
	smCXScreen              = 0
	smCYScreen              = 1
	monitorDefaultToNearest = 2
)

type winPoint struct{ X, Y int32 }

type winRect struct{ Left, Top, Right, Bottom int32 }

// winMonitorInfo mirrors MONITORINFO: rcWork is the monitor's area minus its
// taskbar, in virtual-screen coordinates (negative on displays left of or
// above the primary one).
type winMonitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

// quickViewAnchor places the window next to the cursor that clicked the tray,
// pressed against the taskbar edge of the display that owns that taskbar, so
// it opens where the macOS popover would even when the taskbar is not on the
// primary display.
func quickViewAnchor(width, height int) *windowPosition {
	var pt winPoint
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return nil
	}
	rect := workAreaAt(pt)
	pos := quickViewPosition(
		cursorPoint{X: int(pt.X), Y: int(pt.Y)},
		screenRect{Left: int(rect.Left), Top: int(rect.Top), Right: int(rect.Right), Bottom: int(rect.Bottom)},
		width, height,
	)
	return &pos
}

// workAreaAt returns the work area of the monitor under pt. SPI_GETWORKAREA
// only describes the primary display, so it is the fallback, not the answer.
func workAreaAt(pt winPoint) winRect {
	if rect, ok := monitorWorkArea(pt); ok {
		return rect
	}
	var rect winRect
	if r, _, _ := procSystemParameters.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&rect)), 0); r == 0 {
		cx, _, _ := procGetSystemMetrics.Call(smCXScreen)
		cy, _, _ := procGetSystemMetrics.Call(smCYScreen)
		rect = winRect{Right: int32(cx), Bottom: int32(cy)}
	}
	return rect
}

func monitorWorkArea(pt winPoint) (winRect, bool) {
	var hmon uintptr
	if unsafe.Sizeof(uintptr(0)) == 8 {
		// POINT is passed by value; on 64-bit Windows the 8-byte struct
		// travels packed in a single register argument.
		packed := uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32
		hmon, _, _ = procMonitorFromPoint.Call(packed, monitorDefaultToNearest)
	} else {
		hmon, _, _ = procMonitorFromPoint.Call(uintptr(pt.X), uintptr(pt.Y), monitorDefaultToNearest)
	}
	if hmon == 0 {
		return winRect{}, false
	}
	info := winMonitorInfo{CbSize: uint32(unsafe.Sizeof(winMonitorInfo{}))}
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&info))); r == 0 {
		return winRect{}, false
	}
	return info.RcWork, true
}
