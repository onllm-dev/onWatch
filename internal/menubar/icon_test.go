package menubar

import (
	"bytes"
	"image/png"
	"testing"
)

func TestTrayIconsDimensions(t *testing.T) {
	template, retina := trayIcons()

	checkDim := func(data []byte, name string, expected int) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("failed to decode %s: %v", name, err)
		}
		bounds := img.Bounds()
		if bounds.Dx() != expected || bounds.Dy() != expected {
			t.Errorf("%s dimensions expected %dx%d, got %dx%d", name, expected, expected, bounds.Dx(), bounds.Dy())
		}
	}

	checkDim(template, "template", 256)
	checkDim(retina, "retina", 512)
}

func TestTrayIconsPNGNotEmpty(t *testing.T) {
	template, retina := trayIcons()
	if len(template) == 0 {
		t.Fatal("trayIcons() template is empty")
	}
	if len(retina) == 0 {
		t.Fatal("trayIcons() retina is empty")
	}
}

func TestTrayIconsPNGLen(t *testing.T) {
	template, retina := trayIcons()
	// Verify reasonable PNG header
	if string(template[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("template icon does not have PNG header")
	}
	if string(retina[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("retina icon does not have PNG header")
	}
}
