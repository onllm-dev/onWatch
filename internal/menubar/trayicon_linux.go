//go:build menubar && linux

package menubar

// StatusNotifierItem hosts accept PNG and scale it to the panel; 32 px stays
// sharp when halved to a 16 px slot and readable at 22-24.
const trayTooltipLimit = 0

func trayIconBytes(v trayVisual) ([]byte, error) {
	return encodePNG(trayIconImage(v, 32))
}

func statusDotBytes(status string) ([]byte, error) {
	return encodePNG(RenderStatusDot(32, status))
}

// The host scales the PNG itself, so there is no native icon to manage.
func applyNativeTrayIcon(trayVisual) {}

func reassertNativeTrayIcon() {}

func prepareTrayProcess() {}
