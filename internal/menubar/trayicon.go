package menubar

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"
)

// Tray icon rendering for platforms whose tray cannot show text beside the
// icon (Windows, Linux StatusNotifierItem). The compact metric that macOS
// draws as a title is baked into the icon instead: a status-colored disc
// carrying the percentage in a chunky pixel font that stays legible at
// 16 px. Everything here is pure Go so CGO_ENABLED=0 builds keep working.

var (
	trayColorHealthy  = color.RGBA{R: 0x64, G: 0x74, B: 0x8b, A: 0xff} // slate, quiet
	trayColorWarning  = color.RGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff} // matches --warning
	trayColorDanger   = color.RGBA{R: 0xf9, G: 0x73, B: 0x16, A: 0xff}
	trayColorCritical = color.RGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff} // matches --critical
	trayColorOffline  = color.RGBA{R: 0x9c, G: 0xa3, B: 0xaf, A: 0xff}
	trayColorText     = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

const (
	trayGlyphWidth    = 5
	trayGlyphHeight   = 7
	trayStatusOffline = "offline"
)

// trayGlyphs is a 5x7 pixel font covering what a quota badge needs.
var trayGlyphs = map[rune][]string{
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11111", "00010", "00100", "00010", "00001", "10001", "01110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	'%': {"11000", "11001", "00010", "00100", "01000", "10011", "00011"},
	'-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
	'!': {"00100", "00100", "00100", "00100", "00100", "00000", "00100"},
	'+': {"00000", "00100", "00100", "11111", "00100", "00100", "00000"},
}

func trayColorForStatus(status string) color.RGBA {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "warning":
		return trayColorWarning
	case "danger":
		return trayColorDanger
	case "critical":
		return trayColorCritical
	case trayStatusOffline:
		return trayColorOffline
	default:
		return trayColorHealthy
	}
}

// trayGlyphScale picks an integer glyph scale for a square icon of the given
// size. Above 16 px the scale stays even so the OS can halve the bitmap for a
// smaller slot without blurring the 1 px strokes.
func trayGlyphScale(size int) int {
	scale := size / 16
	if scale < 1 {
		return 1
	}
	if scale > 1 && scale%2 == 1 {
		scale--
	}
	return scale
}

// TrayIconText decides what the badge shows for the given tray segments.
// It mirrors the macOS title rules but collapses to what fits in an icon:
// the first selected quota only, digits without the percent sign.
func TrayIconText(segments []TraySegment, online bool) string {
	if !online {
		return "-"
	}
	if len(segments) == 0 {
		return ""
	}
	seg := segments[0]
	if seg.ProviderID == "aggregate" {
		digits := strings.TrimSpace(strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, seg.Text))
		if digits == "" {
			digits = "0"
		}
		return digits + "!"
	}
	if seg.Percent >= 99.5 {
		return "!"
	}
	return fmt.Sprintf("%d", int(math.Round(seg.Percent)))
}

// RenderTrayBadge draws a status-colored disc with white text centered in it.
func RenderTrayBadge(size int, text, status string) *image.RGBA {
	if size < 8 {
		size = 8
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := trayColorForStatus(status)
	center := float64(size) / 2
	radius := center
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) + 0.5 - center
			dy := float64(y) + 0.5 - center
			if dx*dx+dy*dy <= radius*radius {
				img.SetRGBA(x, y, fill)
			}
		}
	}
	drawTrayText(img, text, trayColorText)
	return img
}

func drawTrayText(img *image.RGBA, text string, fg color.RGBA) {
	runes := []rune(text)
	if len(runes) == 0 {
		return
	}
	size := img.Bounds().Dx()
	scale := trayGlyphScale(size)
	// Shrink to fit if the text is wider than the disc allows.
	textWidth := func(s int) int { return (len(runes)*trayGlyphWidth + (len(runes) - 1)) * s }
	inner := int(float64(size) * 0.78)
	for scale > 1 && textWidth(scale) > inner {
		scale--
	}
	w := textWidth(scale)
	h := trayGlyphHeight * scale
	x0 := (size - w) / 2
	y0 := (size - h) / 2
	for i, r := range runes {
		glyph, ok := trayGlyphs[r]
		if !ok {
			continue
		}
		gx := x0 + i*(trayGlyphWidth+1)*scale
		for row, bits := range glyph {
			for col, bit := range bits {
				if bit != '1' {
					continue
				}
				px := gx + col*scale
				py := y0 + row*scale
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						if image.Pt(px+sx, py+sy).In(img.Bounds()) {
							img.SetRGBA(px+sx, py+sy, fg)
						}
					}
				}
			}
		}
	}
}

var (
	trayLogoOnce sync.Once
	trayLogoMask *image.RGBA
)

// RenderTrayLogo tints the onWatch template mark (used as the macOS template
// icon) with the status color, for the icon-only display mode.
func RenderTrayLogo(size int, status string) *image.RGBA {
	if size < 8 {
		size = 8
	}
	trayLogoOnce.Do(func() {
		src, err := png.Decode(bytes.NewReader(IconTemplate))
		if err != nil {
			return
		}
		rgba := image.NewRGBA(src.Bounds())
		draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
		trayLogoMask = rgba
	})
	tint := trayColorForStatus(status)
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	if trayLogoMask == nil {
		return RenderTrayBadge(size, "", status)
	}
	mask := trayLogoMask
	if factor := mask.Bounds().Dx() / size; factor > 1 {
		mask = downscaleRGBA(mask, factor)
	}
	for y := 0; y < size && y < mask.Bounds().Dy(); y++ {
		for x := 0; x < size && x < mask.Bounds().Dx(); x++ {
			a := mask.RGBAAt(x, y).A
			if a == 0 {
				continue
			}
			out.SetRGBA(x, y, color.RGBA{R: tint.R, G: tint.G, B: tint.B, A: a})
		}
	}
	return out
}

// downscaleRGBA shrinks an image by an integer factor with a box filter.
func downscaleRGBA(src *image.RGBA, factor int) *image.RGBA {
	if factor <= 1 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx()/factor, b.Dy()/factor
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	n := uint32(factor * factor)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a uint32
			for sy := 0; sy < factor; sy++ {
				for sx := 0; sx < factor; sx++ {
					px := src.RGBAAt(b.Min.X+x*factor+sx, b.Min.Y+y*factor+sy)
					r += uint32(px.R)
					g += uint32(px.G)
					bl += uint32(px.B)
					a += uint32(px.A)
				}
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n)})
		}
	}
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeICO packs square RGBA images into a Windows .ico container using
// uncompressed 32-bit DIB entries with a 1-bit AND mask. Every Windows
// version understands this layout, unlike PNG-in-ICO.
func EncodeICO(images []*image.RGBA) ([]byte, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("ico: no images")
	}
	const dirSize, entrySize, dibHeader = 6, 16, 40
	var body bytes.Buffer
	var dir bytes.Buffer
	_ = binary.Write(&dir, binary.LittleEndian, uint16(0))
	_ = binary.Write(&dir, binary.LittleEndian, uint16(1))
	_ = binary.Write(&dir, binary.LittleEndian, uint16(len(images)))
	offset := uint32(dirSize + entrySize*len(images))
	for _, img := range images {
		size := img.Bounds().Dx()
		if size != img.Bounds().Dy() || size <= 0 || size > 256 {
			return nil, fmt.Errorf("ico: images must be square and at most 256 px, got %v", img.Bounds())
		}
		maskRow := ((size + 31) / 32) * 4
		pixelBytes := size * size * 4
		maskBytes := maskRow * size
		bytesInRes := uint32(dibHeader + pixelBytes + maskBytes)

		// BITMAPINFOHEADER
		_ = binary.Write(&body, binary.LittleEndian, uint32(dibHeader))
		_ = binary.Write(&body, binary.LittleEndian, int32(size))
		_ = binary.Write(&body, binary.LittleEndian, int32(size*2)) // XOR + AND masks
		_ = binary.Write(&body, binary.LittleEndian, uint16(1))     // planes
		_ = binary.Write(&body, binary.LittleEndian, uint16(32))    // bit count
		_ = binary.Write(&body, binary.LittleEndian, uint32(0))     // BI_RGB
		_ = binary.Write(&body, binary.LittleEndian, uint32(pixelBytes+maskBytes))
		_ = binary.Write(&body, binary.LittleEndian, int32(0))
		_ = binary.Write(&body, binary.LittleEndian, int32(0))
		_ = binary.Write(&body, binary.LittleEndian, uint32(0))
		_ = binary.Write(&body, binary.LittleEndian, uint32(0))

		// XOR mask: BGRA rows, bottom-up.
		b := img.Bounds()
		for y := size - 1; y >= 0; y-- {
			for x := 0; x < size; x++ {
				px := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
				body.Write([]byte{px.B, px.G, px.R, px.A})
			}
		}
		// AND mask: 1 = transparent, bottom-up, rows padded to 4 bytes.
		for y := size - 1; y >= 0; y-- {
			row := make([]byte, maskRow)
			for x := 0; x < size; x++ {
				if img.RGBAAt(b.Min.X+x, b.Min.Y+y).A == 0 {
					row[x/8] |= 0x80 >> (x % 8)
				}
			}
			body.Write(row)
		}

		width, height := uint8(size), uint8(size) // 256 encodes as 0
		if size == 256 {
			width, height = 0, 0
		}
		dir.Write([]byte{width, height, 0, 0})
		_ = binary.Write(&dir, binary.LittleEndian, uint16(1))
		_ = binary.Write(&dir, binary.LittleEndian, uint16(32))
		_ = binary.Write(&dir, binary.LittleEndian, bytesInRes)
		_ = binary.Write(&dir, binary.LittleEndian, offset)
		offset += bytesInRes
	}
	out := make([]byte, 0, dir.Len()+body.Len())
	out = append(out, dir.Bytes()...)
	out = append(out, body.Bytes()...)
	return out, nil
}

// trayVisual is what a refresh hands to the platform layer: the macOS title
// text and everything needed to render an icon elsewhere.
type trayVisual struct {
	Title    string
	Text     string // digits baked into the icon on Linux/Windows
	Status   string // color driver: healthy|warning|danger|critical|offline
	Online   bool
	IconOnly bool
}

// trayIconImage renders the icon for one visual at the given size.
func trayIconImage(v trayVisual, size int) *image.RGBA {
	status := v.Status
	if !v.Online {
		status = trayStatusOffline
	}
	if v.IconOnly && v.Online {
		return RenderTrayLogo(size, status)
	}
	return RenderTrayBadge(size, v.Text, status)
}

// TrayBadgeStatus picks the status that should color the icon: the provider
// whose number is shown, the alert level for the critical-count mode, or the
// aggregate when nothing is selected.
func TrayBadgeStatus(snapshot *Snapshot, segments []TraySegment) string {
	if snapshot == nil {
		return trayStatusOffline
	}
	if len(segments) == 0 {
		return snapshot.Aggregate.Status
	}
	seg := segments[0]
	if seg.ProviderID == "aggregate" {
		switch {
		case snapshot.Aggregate.CriticalCount > 0:
			return "critical"
		case snapshot.Aggregate.WarningCount > 0:
			return "warning"
		default:
			return "healthy"
		}
	}
	if provider, ok := providerByID(snapshot, seg.ProviderID); ok {
		for _, quota := range provider.Quotas {
			if quota.Percent == seg.Percent && quota.Status != "" {
				return quota.Status
			}
		}
		if provider.Status != "" {
			return provider.Status
		}
	}
	return snapshot.Aggregate.Status
}
