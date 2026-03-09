package menubar

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

func trayIcons() ([]byte, []byte) {
	return renderTrayIcon(true), renderTrayIcon(false)
}

func renderTrayIcon(template bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 18, 18))
	active := color.NRGBA{R: 96, G: 165, B: 250, A: 255}
	warn := color.NRGBA{R: 52, G: 211, B: 153, A: 255}
	templateColor := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	if template {
		active = templateColor
		warn = templateColor
	}

	fillRect(img, 3, 3, 15, 5, active)
	fillRect(img, 3, 8, 12, 10, warn)
	fillRect(img, 3, 13, 9, 15, active)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func fillRect(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}
