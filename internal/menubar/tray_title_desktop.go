//go:build menubar && (linux || windows)

package menubar

import "fyne.io/systray"

// setTrayTitle writes the title through systray's plain text path. Desktop
// shells render the compact metric inside the icon, so the title is not the
// visible affordance and dimming is applied by the badge renderer instead.
func setTrayTitle(title string, _ bool) {
	systray.SetTitle(title)
}
