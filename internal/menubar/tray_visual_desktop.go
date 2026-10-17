//go:build menubar && (linux || windows)

package menubar

import (
	"log/slog"

	"fyne.io/systray"
)

func setupTrayIcon() {
	systray.SetTitle("onWatch")
	updateTrayVisual(trayVisual{Status: trayStatusOffline, Online: false})
}

// updateTrayVisual bakes the compact metric into the icon because neither
// Windows nor StatusNotifierItem hosts render a title next to the icon.
func updateTrayVisual(v trayVisual) {
	applyTrayIconBytes(v)
	applyNativeTrayIcon(v)
}

func applyTrayIconBytes(v trayVisual) {
	data, err := trayIconBytes(v)
	if err != nil {
		slog.Default().Warn("failed to render tray icon", "error", err)
		return
	}
	systray.SetIcon(data)
}

// setTrayTooltip re-sends the exact-size Windows icon afterwards: systray
// re-sends its own icon with every tooltip update.
func setTrayTooltip(text string) {
	systray.SetTooltip(text)
	reassertNativeTrayIcon()
}

// setMenuLineStatus marks a provider row with a status-colored dot so the
// rows read as status, not as more actions.
func setMenuLineStatus(item *systray.MenuItem, status string) {
	data, err := statusDotBytes(status)
	if err != nil {
		return
	}
	item.SetIcon(data)
}
