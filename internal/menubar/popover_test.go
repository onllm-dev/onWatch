package menubar

import "testing"

func TestPopoverDimensionConstants(t *testing.T) {
	if menubarPopoverWidth != 360 {
		t.Fatalf("expected popover width 360, got %d", menubarPopoverWidth)
	}
	if menubarPopoverHeight != 680 {
		t.Fatalf("expected popover height 680, got %d", menubarPopoverHeight)
	}
}

func TestPopoverDimensionsArePositive(t *testing.T) {
	if menubarPopoverWidth <= 0 {
		t.Fatal("popover width must be positive")
	}
	if menubarPopoverHeight <= 0 {
		t.Fatal("popover height must be positive")
	}
}

func TestErrNativePopoverUnavailableMessage(t *testing.T) {
	if errNativePopoverUnavailable == nil {
		t.Fatal("errNativePopoverUnavailable must not be nil")
	}
	msg := errNativePopoverUnavailable.Error()
	if msg == "" {
		t.Fatal("errNativePopoverUnavailable message must not be empty")
	}
}
