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

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Tray icon rendering for platforms whose tray cannot show text beside the
// icon (Windows, Linux StatusNotifierItem). The compact metric that macOS
// draws as a title is baked into the icon instead: a status-colored rounded
// badge carrying the percentage in Go Bold, rasterized for the exact pixel
// size the shell asks for so it stays sharp at every display scale.
// Everything here is pure Go so CGO_ENABLED=0 builds keep working.

var (
	trayColorHealthy  = color.RGBA{R: 0x64, G: 0x74, B: 0x8b, A: 0xff} // slate, quiet
	trayColorWarning  = color.RGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xff} // matches --warning
	trayColorDanger   = color.RGBA{R: 0xf9, G: 0x73, B: 0x16, A: 0xff}
	trayColorCritical = color.RGBA{R: 0xef, G: 0x44, B: 0x44, A: 0xff} // matches --critical
	trayColorOffline  = color.RGBA{R: 0x9c, G: 0xa3, B: 0xaf, A: 0xff}
	trayColorText     = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	trayColorHealthyDot = color.RGBA{R: 0x22, G: 0xc5, B: 0x5e, A: 0xff} // matches --healthy
)

const trayStatusOffline = "offline"

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

// traySupersample is the per-axis sample count used to anti-alias shapes.
const traySupersample = 4

// RenderTrayBadge draws a status-colored rounded square with white text
// centered in it. The square leaves the digits more room than a disc, which
// is what keeps two digits legible in a 16 px slot.
func RenderTrayBadge(size int, text, status string) *image.RGBA {
	if size < 8 {
		size = 8
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := trayColorForStatus(status)
	radius := float64(size) * 0.28
	fillRoundedRect(img, fill, radius)
	drawTrayText(img, text, trayColorText)
	return img
}

// RenderStatusDot draws a small anti-aliased status disc, used to mark
// provider rows in the tray menu.
func RenderStatusDot(size int, status string) *image.RGBA {
	if size < 8 {
		size = 8
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := trayColorForStatus(status)
	// The badge keeps healthy quiet, but in a list of rows a slate dot reads
	// as "disabled"; use the page's --healthy green instead.
	if fill == trayColorHealthy {
		fill = trayColorHealthyDot
	}
	center := float64(size) / 2
	radius := float64(size) * 0.3
	paintCoverage(img, fill, func(x, y float64) bool {
		dx, dy := x-center, y-center
		return dx*dx+dy*dy <= radius*radius
	})
	return img
}

func fillRoundedRect(img *image.RGBA, fill color.RGBA, radius float64) {
	size := float64(img.Bounds().Dx())
	paintCoverage(img, fill, func(x, y float64) bool {
		// Distance from the point to the inner rectangle the corners round off.
		cx := math.Max(radius-x, math.Max(x-(size-radius), 0))
		cy := math.Max(radius-y, math.Max(y-(size-radius), 0))
		return cx*cx+cy*cy <= radius*radius
	})
}

// paintCoverage fills each pixel with fill at an alpha proportional to how
// many of its sub-samples fall inside the shape.
func paintCoverage(img *image.RGBA, fill color.RGBA, inside func(x, y float64) bool) {
	b := img.Bounds()
	const n = traySupersample
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			hits := 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					if inside(float64(x)+(float64(sx)+0.5)/n, float64(y)+(float64(sy)+0.5)/n) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			a := uint32(fill.A) * uint32(hits) / (n * n)
			// image.RGBA stores premultiplied color.
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(uint32(fill.R) * a / 0xff),
				G: uint8(uint32(fill.G) * a / 0xff),
				B: uint8(uint32(fill.B) * a / 0xff),
				A: uint8(a),
			})
		}
	}
}

var (
	trayFontOnce sync.Once
	trayFont     *opentype.Font
)

func trayTextFont() *opentype.Font {
	trayFontOnce.Do(func() {
		f, err := opentype.Parse(gobold.TTF)
		if err == nil {
			trayFont = f
		}
	})
	return trayFont
}

// drawTrayText centers text in the badge, starting with digits about two
// thirds of the icon tall and shrinking until the ink fits inside a 1 px
// (or larger, on big icons) inset.
func drawTrayText(img *image.RGBA, text string, fg color.RGBA) {
	if strings.TrimSpace(text) == "" {
		return
	}
	f := trayTextFont()
	if f == nil {
		return
	}
	size := img.Bounds().Dx()
	inset := size / 16
	if inset < 1 {
		inset = 1
	}
	avail := size - 2*inset
	for px := float64(size) * 0.9; px >= 5; px -= 0.5 {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return
		}
		bounds, _ := font.BoundString(face, text)
		minX, minY := bounds.Min.X.Floor(), bounds.Min.Y.Floor()
		w, h := bounds.Max.X.Ceil()-minX, bounds.Max.Y.Ceil()-minY
		// Cap the ink height so digits never crowd the top and bottom edges.
		if w > avail || h > avail || float64(h) > float64(size)*0.66 {
			_ = face.Close()
			continue
		}
		d := &font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face}
		d.Dot = fixed.P((size-w+1)/2-minX, (size-h+1)/2-minY)
		d.DrawString(text)
		_ = face.Close()
		return
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
