package menubar

import (
	"testing"
)

func TestTrayIconsPNGNotEmpty(t *testing.T) {
	template, retina := trayIcons()
	if len(template) == 0 {
		t.Fatal("trayIcons() template is empty")
	}
	if len(retina) == 0 {
		t.Fatal("trayIcons() retina is empty")
	}
}

func TestTrayIconPDFNotEmpty(t *testing.T) {
	pdf := trayIconPDF()
	if len(pdf) == 0 {
		t.Fatal("trayIconPDF() is empty")
	}
}

func TestTrayIconPDFStartsWithPDFHeader(t *testing.T) {
	pdf := trayIconPDF()
	if len(pdf) < 4 {
		t.Fatal("trayIconPDF() too short to be valid")
	}
	// PDF files start with "%PDF-"
	if string(pdf[:5]) != "%PDF-" {
		t.Fatalf("trayIconPDF() does not start with %%PDF-, got %q", string(pdf[:5]))
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
