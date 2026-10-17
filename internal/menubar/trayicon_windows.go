//go:build menubar && windows

package menubar

import (
	"image"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NOTIFYICONDATA.szTip holds 128 UTF-16 units including the terminator.
const trayTooltipLimit = 127

// trayIconBytes returns an .ico with every size the shell may request across
// DPI scales. fyne.io/systray keeps it as the fallback icon it re-adds when
// Explorer restarts; the icon actually shown is the exact-size one below.
func trayIconBytes(v trayVisual) ([]byte, error) {
	sizes := []int{16, 20, 24, 32, 40, 48}
	images := make([]*image.RGBA, 0, len(sizes))
	for _, size := range sizes {
		images = append(images, trayIconImage(v, size))
	}
	return EncodeICO(images)
}

// statusDotBytes is the menu row marker: menu bitmaps are drawn at the small
// icon size, so offer the sizes common display scales map to.
func statusDotBytes(status string) ([]byte, error) {
	sizes := []int{16, 20, 24, 32}
	images := make([]*image.RGBA, 0, len(sizes))
	for _, size := range sizes {
		images = append(images, RenderStatusDot(size, status))
	}
	return EncodeICO(images)
}

// fyne.io/systray loads icons with LoadImage(LR_DEFAULTSIZE), which picks the
// large icon size (32 px at 100%) and leaves the shell to shrink it into the
// 16-24 px tray slot - the blurred, dotted badge Windows users saw. The
// companion therefore renders the badge at the exact small-icon size for the
// current DPI and hands it to the shell itself, addressing the notification
// icon fyne.io/systray registered (its hidden "SystrayClass" window, uID 100,
// both fixed in the version pinned by go.mod).
const (
	fyneTrayClassName = "SystrayClass"
	fyneTrayIconID    = 100

	nimModify      = 0x00000001
	nifIcon        = 0x00000002
	smCXSmIcon     = 49
	iconResVersion = 0x00030000
)

var (
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	procFindWindowExW            = user32.NewProc("FindWindowExW")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	procGetSystemMetricsForDpi   = user32.NewProc("GetSystemMetricsForDpi")
)

// notifyIconData mirrors NOTIFYICONDATAW (Vista+ layout).
type notifyIconData struct {
	Size                       uint32
	Wnd                        uintptr
	ID, Flags, CallbackMessage uint32
	Icon                       uintptr
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	Timeout, Version           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	GUIDItem                   [16]byte
	BalloonIcon                uintptr
}

var nativeTray struct {
	mu   sync.Mutex
	hwnd uintptr
	icon uintptr
}

// applyNativeTrayIcon swaps the shell's icon for one rendered at exactly the
// small-icon size. On any failure the fyne.io/systray icon stays in place.
func applyNativeTrayIcon(v trayVisual) {
	size := traySmallIconSize()
	ico, err := EncodeICO([]*image.RGBA{trayIconImage(v, size)})
	if err != nil {
		return
	}
	// The single image's DIB starts right after the 6-byte header and its
	// 16-byte directory entry.
	const dibOffset = 6 + 16
	bits := ico[dibOffset:]
	icon, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&bits[0])), uintptr(len(bits)),
		1, iconResVersion, uintptr(size), uintptr(size), 0,
	)
	if icon == 0 {
		return
	}
	nativeTray.mu.Lock()
	defer nativeTray.mu.Unlock()
	if !modifyTrayIconLocked(icon) {
		_, _, _ = procDestroyIcon.Call(icon)
		return
	}
	if nativeTray.icon != 0 {
		_, _, _ = procDestroyIcon.Call(nativeTray.icon)
	}
	nativeTray.icon = icon
}

// reassertNativeTrayIcon re-sends the exact-size icon. fyne.io/systray
// re-sends its own icon with every tooltip change and when Explorer restarts.
func reassertNativeTrayIcon() {
	nativeTray.mu.Lock()
	defer nativeTray.mu.Unlock()
	if nativeTray.icon != 0 {
		modifyTrayIconLocked(nativeTray.icon)
	}
}

func modifyTrayIconLocked(icon uintptr) bool {
	if nativeTray.hwnd == 0 {
		nativeTray.hwnd = findFyneTrayWindow()
		if nativeTray.hwnd == 0 {
			return false
		}
	}
	nid := notifyIconData{Wnd: nativeTray.hwnd, ID: fyneTrayIconID, Flags: nifIcon, Icon: icon}
	nid.Size = uint32(unsafe.Sizeof(nid))
	if r, _, _ := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		// The window may have been recreated; look it up again next time.
		nativeTray.hwnd = 0
		slog.Default().Debug("exact-size tray icon not applied, keeping systray icon")
		return false
	}
	return true
}

// findFyneTrayWindow returns this process's systray window. The class name is
// shared by every fyne.io/systray app, so the owning process must match too.
func findFyneTrayWindow() uintptr {
	className, err := syscall.UTF16PtrFromString(fyneTrayClassName)
	if err != nil {
		return 0
	}
	pid := uint32(os.Getpid())
	var hwnd uintptr
	for {
		hwnd, _, _ = procFindWindowExW.Call(0, hwnd, uintptr(unsafe.Pointer(className)), 0)
		if hwnd == 0 {
			return 0
		}
		var owner uint32
		_, _, _ = procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner == pid {
			return hwnd
		}
	}
}

// traySmallIconSize is the notification-area icon size for the system DPI:
// 16 px at 100%, 20 at 125%, 24 at 150% and so on.
func traySmallIconSize() int {
	dpi := systemDPI()
	if procGetSystemMetricsForDpi.Find() == nil {
		if size, _, _ := procGetSystemMetricsForDpi.Call(smCXSmIcon, uintptr(dpi)); size > 0 {
			return int(size)
		}
	}
	return scaleForDPI(16, dpi)
}

// systemDPI is the DPI of the primary display, which hosts the taskbar's
// notification area. It reads 96 unless the process is DPI aware.
func systemDPI() int {
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
			return int(dpi)
		}
	}
	return 96
}

var procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")

// dpiAwarenessPerMonitorV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
const dpiAwarenessPerMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4

// prepareTrayProcess opts the companion into per-monitor DPI awareness before
// any window exists. Without it Windows renders the tray menu and the quick
// view at 96 DPI and bitmap-stretches them, which blurs text at 125% and up,
// and reports a 96 DPI icon size for the tray.
func prepareTrayProcess() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		_, _, _ = procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2)
	} // else Windows 10 before 1703: stays system-DPI-unaware.
	allowDarkMenus()
}

// allowDarkMenus lets Win32 popup menus - the tray menu - follow the dark app
// theme, as Explorer's own tray menus do. Windows exposes this only through
// uxtheme's unnamed exports SetPreferredAppMode (ordinal 135) and
// FlushMenuThemes (ordinal 136), stable since Windows 10 1903 and used by
// Chromium and Windows Terminal; older builds keep light menus.
func allowDarkMenus() {
	const (
		minBuild         = 18362 // Windows 10 1903
		appModeAllowDark = 1     // follow the system app theme
	)
	if windows.RtlGetVersion().BuildNumber < minBuild {
		return
	}
	uxtheme, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return
	}
	setPreferredAppMode, err := windows.GetProcAddressByOrdinal(uxtheme, 135)
	if err != nil {
		return
	}
	flushMenuThemes, err := windows.GetProcAddressByOrdinal(uxtheme, 136)
	if err != nil {
		return
	}
	_, _, _ = syscall.SyscallN(setPreferredAppMode, appModeAllowDark)
	_, _, _ = syscall.SyscallN(flushMenuThemes)
}
