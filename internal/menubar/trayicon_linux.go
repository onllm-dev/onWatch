//go:build menubar && linux

package menubar

// StatusNotifierItem hosts accept PNG and scale it to the panel; 32 px with
// glyph scale 2 stays crisp when halved to a 16 px slot and readable at 22-24.
const trayTooltipLimit = 0

func trayIconBytes(v trayVisual) ([]byte, error) {
	return encodePNG(trayIconImage(v, 32))
}
