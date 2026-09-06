//go:build menubar && windows

package menubar

import "image"

// NOTIFYICONDATA.szTip holds 128 UTF-16 units including the terminator.
const trayTooltipLimit = 127

// trayIconBytes returns an .ico with every size the shell may request across
// DPI scales, so LoadImage picks an exact match instead of resampling.
func trayIconBytes(v trayVisual) ([]byte, error) {
	sizes := []int{16, 20, 24, 32, 40, 48}
	images := make([]*image.RGBA, 0, len(sizes))
	for _, size := range sizes {
		images = append(images, trayIconImage(v, size))
	}
	return EncodeICO(images)
}
