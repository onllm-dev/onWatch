package menubar

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestTrayIconStyleForStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]color.RGBA{
		"healthy":  trayColorHealthy,
		"warning":  trayColorWarning,
		"danger":   trayColorDanger,
		"critical": trayColorCritical,
		"":         trayColorHealthy,
		"bogus":    trayColorHealthy,
	}
	for status, want := range cases {
		if got := trayColorForStatus(status); got != want {
			t.Fatalf("trayColorForStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

// inkBox returns the bounding box of pixels dominated by the white text. Every
// status fill has a channel below 0x99, so a pixel whose darkest channel is at
// least that bright is mostly text even after anti-aliasing.
func inkBox(img *image.RGBA) image.Rectangle {
	box := image.Rectangle{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.A == 0xff && min(c.R, c.G, c.B) >= 0x99 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

func TestRenderTrayBadgeTextFitsEverySize(t *testing.T) {
	t.Parallel()
	for _, size := range []int{16, 20, 24, 32, 40, 48} {
		for _, text := range []string{"88", "3!", "!", "-", "7"} {
			img := RenderTrayBadge(size, text, "warning")
			box := inkBox(img)
			if box.Empty() {
				t.Fatalf("size %d text %q: no solid text pixels", size, text)
			}
			if box.Min.X < 1 || box.Min.Y < 1 || box.Max.X > size-1 || box.Max.Y > size-1 {
				t.Fatalf("size %d text %q: ink %v touches the badge edge", size, text, box)
			}
		}
	}
}

func TestRenderTrayBadgeDigitsGrowWithIconSize(t *testing.T) {
	t.Parallel()
	// The old pixel font stayed 7 px tall from 16 to 31 px, which is what made
	// the badge unreadable at 125-175% display scaling.
	prev := 0
	for _, size := range []int{16, 20, 24, 32, 48} {
		h := inkBox(RenderTrayBadge(size, "88", "healthy")).Dy()
		if h <= prev {
			t.Fatalf("size %d: digit height %d did not grow past %d", size, h, prev)
		}
		if h*2 < size {
			t.Fatalf("size %d: digit height %d is under half the icon", size, h)
		}
		prev = h
	}
}

func TestRenderTrayBadgeAntialiasesEdges(t *testing.T) {
	t.Parallel()
	img := RenderTrayBadge(24, "", "critical")
	partial := false
	for y := 0; y < 24 && !partial; y++ {
		for x := 0; x < 24; x++ {
			if a := img.RGBAAt(x, y).A; a > 0 && a < 255 {
				partial = true
				break
			}
		}
	}
	if !partial {
		t.Fatal("expected partially covered pixels along the rounded corners")
	}
}

func TestRenderStatusDotIsCenteredStatusDisc(t *testing.T) {
	t.Parallel()
	img := RenderStatusDot(16, "critical")
	if got := img.RGBAAt(8, 8); got != trayColorCritical {
		t.Fatalf("expected critical center, got %v", got)
	}
	if got := RenderStatusDot(16, "healthy").RGBAAt(8, 8); got != trayColorHealthyDot {
		t.Fatalf("expected green healthy dot, got %v", got)
	}
	for _, p := range []image.Point{{0, 0}, {15, 0}, {0, 15}, {15, 15}, {0, 8}} {
		if a := img.RGBAAt(p.X, p.Y).A; a != 0 {
			t.Fatalf("expected transparent margin at %v, alpha=%d", p, a)
		}
	}
}

func TestRenderTrayBadgeDrawsColoredBadgeWithWhiteText(t *testing.T) {
	t.Parallel()
	img := RenderTrayBadge(32, "82", "critical")
	if img == nil {
		t.Fatal("expected image")
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("unexpected bounds %v", b)
	}
	// Corner stays transparent because the badge is rounded.
	if a := img.RGBAAt(0, 0).A; a != 0 {
		t.Fatalf("expected transparent corner, alpha=%d", a)
	}
	// The straight left edge, clear of the text, carries the status color.
	if got := img.RGBAAt(0, 16); got != trayColorCritical {
		t.Fatalf("expected critical fill at badge edge, got %v", got)
	}
	// Some pixel inside must be white text.
	foundWhite := false
	for y := 0; y < 32 && !foundWhite; y++ {
		for x := 0; x < 32; x++ {
			if img.RGBAAt(x, y) == trayColorText {
				foundWhite = true
				break
			}
		}
	}
	if !foundWhite {
		t.Fatal("expected white glyph pixels inside the badge")
	}
}

func TestRenderTrayBadgeEmptyTextIsPlainBadge(t *testing.T) {
	t.Parallel()
	img := RenderTrayBadge(16, "", "healthy")
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if img.RGBAAt(x, y) == trayColorText {
				t.Fatalf("unexpected text pixel at %d,%d", x, y)
			}
		}
	}
	if got := img.RGBAAt(8, 8); got != trayColorHealthy {
		t.Fatalf("expected healthy fill at center, got %v", got)
	}
}

func TestRenderTrayLogoTintsTemplateMask(t *testing.T) {
	t.Parallel()
	img := RenderTrayLogo(32, "warning")
	if img == nil {
		t.Fatal("expected image")
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("unexpected bounds %v", b)
	}
	opaque := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			px := img.RGBAAt(x, y)
			if px.A == 0 {
				continue
			}
			opaque++
			if px.R != trayColorWarning.R || px.G != trayColorWarning.G || px.B != trayColorWarning.B {
				t.Fatalf("expected warning tint at %d,%d, got %v", x, y, px)
			}
		}
	}
	if opaque == 0 {
		t.Fatal("expected logo to have opaque pixels")
	}
	if opaque == 32*32 {
		t.Fatal("expected logo to keep transparent background")
	}
}

func TestTrayIconTextFromSegments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		segments []TraySegment
		online   bool
		want     string
	}{
		{"offline", nil, false, "-"},
		{"icon only", nil, true, ""},
		{"percent", []TraySegment{{Text: "82%", Percent: 82}}, true, "82"},
		{"zero", []TraySegment{{Text: "0%", Percent: 0.4}}, true, "0"},
		{"exhausted", []TraySegment{{Text: "100%", Percent: 100}}, true, "!"},
		{"critical count", []TraySegment{{Text: "2 ⚠", ProviderID: "aggregate"}}, true, "2!"},
		{"first of many", []TraySegment{{Text: "6%", Percent: 6}, {Text: "44%", Percent: 44}}, true, "6"},
	}
	for _, tc := range cases {
		if got := TrayIconText(tc.segments, tc.online); got != tc.want {
			t.Fatalf("%s: TrayIconText() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEncodePNGRoundTrip(t *testing.T) {
	t.Parallel()
	img := RenderTrayBadge(16, "7", "healthy")
	data, err := encodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 16 {
		t.Fatalf("unexpected decoded size %v", decoded.Bounds())
	}
}

func TestEncodeICOProducesValidDirectory(t *testing.T) {
	t.Parallel()
	images := []*image.RGBA{
		RenderTrayBadge(16, "42", "warning"),
		RenderTrayBadge(32, "42", "warning"),
	}
	data, err := EncodeICO(images)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 6+16*2 {
		t.Fatalf("ico too small: %d bytes", len(data))
	}
	if binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatalf("bad ICONDIR header %v", data[:4])
	}
	if count := binary.LittleEndian.Uint16(data[4:6]); count != 2 {
		t.Fatalf("expected 2 entries, got %d", count)
	}
	var expectOffset uint32 = 6 + 16*2
	for i := 0; i < 2; i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		size := int(entry[0])
		if size != images[i].Bounds().Dx() {
			t.Fatalf("entry %d width %d, want %d", i, size, images[i].Bounds().Dx())
		}
		if entry[4] != 1 || entry[5] != 0 || entry[6] != 32 || entry[7] != 0 {
			t.Fatalf("entry %d planes/bitcount wrong: %v", i, entry[4:8])
		}
		bytesInRes := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if offset != expectOffset {
			t.Fatalf("entry %d offset %d, want %d", i, offset, expectOffset)
		}
		// DIB: 40-byte header + BGRA pixels + 1bpp AND mask padded to 4 bytes per row.
		maskRow := ((size + 31) / 32) * 4
		wantBytes := uint32(40 + size*size*4 + maskRow*size)
		if bytesInRes != wantBytes {
			t.Fatalf("entry %d bytesInRes %d, want %d", i, bytesInRes, wantBytes)
		}
		hdr := data[offset : offset+40]
		if binary.LittleEndian.Uint32(hdr[0:4]) != 40 {
			t.Fatalf("entry %d BITMAPINFOHEADER size wrong", i)
		}
		if int32(binary.LittleEndian.Uint32(hdr[8:12])) != int32(size*2) {
			t.Fatalf("entry %d DIB height should be doubled, got %d", i, int32(binary.LittleEndian.Uint32(hdr[8:12])))
		}
		expectOffset += bytesInRes
	}
	if uint32(len(data)) != expectOffset {
		t.Fatalf("ico length %d, want %d", len(data), expectOffset)
	}
}

func TestDownscaleRGBAAveragesBlocks(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if x < 2 {
				src.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}
	dst := downscaleRGBA(src, 2)
	if dst.Bounds().Dx() != 2 || dst.Bounds().Dy() != 2 {
		t.Fatalf("unexpected bounds %v", dst.Bounds())
	}
	if a := dst.RGBAAt(0, 0).A; a != 255 {
		t.Fatalf("left block alpha %d, want 255", a)
	}
	if a := dst.RGBAAt(1, 0).A; a != 0 {
		t.Fatalf("right block alpha %d, want 0", a)
	}
}

func TestTrayBadgeStatus(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	if got := TrayBadgeStatus(nil, nil); got != trayStatusOffline {
		t.Fatalf("nil snapshot -> %q", got)
	}
	if got := TrayBadgeStatus(snapshot, nil); got != "warning" {
		t.Fatalf("no segments should use aggregate, got %q", got)
	}
	segs := []TraySegment{{ProviderID: "anthropic", Percent: 40}}
	if got := TrayBadgeStatus(snapshot, segs); got != "healthy" {
		t.Fatalf("selected weekly quota is healthy, got %q", got)
	}
	segs = []TraySegment{{ProviderID: "anthropic", Percent: 82}}
	if got := TrayBadgeStatus(snapshot, segs); got != "warning" {
		t.Fatalf("selected 5h quota is warning, got %q", got)
	}
	agg := []TraySegment{{ProviderID: "aggregate", Text: "1 ⚠"}}
	if got := TrayBadgeStatus(snapshot, agg); got != "warning" {
		t.Fatalf("aggregate with warnings -> %q", got)
	}
	snapshot.Aggregate.CriticalCount = 1
	if got := TrayBadgeStatus(snapshot, agg); got != "critical" {
		t.Fatalf("aggregate with criticals -> %q", got)
	}
}

func TestTrayIconImageOfflineIgnoresIconOnly(t *testing.T) {
	t.Parallel()
	img := trayIconImage(trayVisual{Text: "-", Status: "healthy", Online: false, IconOnly: true}, 16)
	if got := img.RGBAAt(1, 8); got != trayColorOffline {
		t.Fatalf("expected offline badge, got %v", got)
	}
}
