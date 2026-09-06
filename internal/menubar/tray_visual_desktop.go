//go:build menubar && (linux || windows)

package menubar

import (
	"log/slog"

	"fyne.io/systray"
)

func setupTrayIcon() {
	systray.SetTitle("onWatch")
	applyTrayIconBytes(trayVisual{Status: trayStatusOffline, Online: false})
}

// updateTrayVisual bakes the compact metric into the icon because neither
// Windows nor StatusNotifierItem hosts render a title next to the icon.
func updateTrayVisual(v trayVisual) {
	applyTrayIconBytes(v)
}

func applyTrayIconBytes(v trayVisual) {
	data, err := trayIconBytes(v)
	if err != nil {
		slog.Default().Warn("failed to render tray icon", "error", err)
		return
	}
	systray.SetIcon(data)
}
