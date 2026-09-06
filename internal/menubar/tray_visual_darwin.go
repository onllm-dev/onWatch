//go:build menubar && darwin

package menubar

import "fyne.io/systray"

// trayTooltipLimit is 0 on macOS: NSStatusItem tooltips have no hard cap.
const trayTooltipLimit = 0

func setupTrayIcon() {
	templateIcon, regularIcon := trayIcons()
	if len(templateIcon) > 0 && len(regularIcon) > 0 {
		systray.SetTemplateIcon(templateIcon, regularIcon)
	}
}

// updateTrayVisual keeps the macOS behaviour: template icon plus the compact
// title text beside it.
func updateTrayVisual(v trayVisual) {
	systray.SetTitle(v.Title)
}
