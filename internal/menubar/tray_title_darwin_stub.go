//go:build menubar && darwin && !cgo

package menubar

import "fyne.io/systray"

// setTrayTitle falls back to the plain title when the cgo bridge is not built.
func setTrayTitle(title string, _ bool) {
	systray.SetTitle(title)
}
