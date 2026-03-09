//go:build menubar && darwin && cgo

package menubar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <stdbool.h>
#include <stdlib.h>

void* onwatch_popover_create(int width, int height);
void onwatch_popover_destroy(void* handle);
bool onwatch_popover_show(void* handle);
bool onwatch_popover_toggle(void* handle);
void onwatch_popover_load_url(void* handle, const char* url);
void onwatch_popover_close(void* handle);
bool onwatch_popover_is_shown(void* handle);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type webViewPopover struct {
	handle unsafe.Pointer
}

func cBool(value C.bool) bool {
	return bool(value)
}

func newMenubarPopover(width, height int) (menubarPopover, error) {
	handle := unsafe.Pointer(C.onwatch_popover_create(C.int(width), C.int(height)))
	if handle == nil {
		return nil, errNativePopoverUnavailable
	}
	return &webViewPopover{handle: handle}, nil
}

func (p *webViewPopover) ShowURL(url string) error {
	if err := p.loadURL(url); err != nil {
		return err
	}
	if !cBool(C.onwatch_popover_show(p.handle)) {
		return fmt.Errorf("%w: status item unavailable", errNativePopoverUnavailable)
	}
	return nil
}

func (p *webViewPopover) ToggleURL(url string) error {
	if !p.isShown() {
		if err := p.loadURL(url); err != nil {
			return err
		}
	}
	if !cBool(C.onwatch_popover_toggle(p.handle)) {
		return fmt.Errorf("%w: status item unavailable", errNativePopoverUnavailable)
	}
	return nil
}

func (p *webViewPopover) Close() {
	if p == nil || p.handle == nil {
		return
	}
	C.onwatch_popover_close(p.handle)
}

func (p *webViewPopover) Destroy() {
	if p == nil || p.handle == nil {
		return
	}
	C.onwatch_popover_destroy(p.handle)
	p.handle = nil
}

func (p *webViewPopover) loadURL(url string) error {
	if p == nil || p.handle == nil {
		return errNativePopoverUnavailable
	}
	rawURL := C.CString(url)
	defer C.free(unsafe.Pointer(rawURL))
	C.onwatch_popover_load_url(p.handle, rawURL)
	return nil
}

func (p *webViewPopover) isShown() bool {
	if p == nil || p.handle == nil {
		return false
	}
	return cBool(C.onwatch_popover_is_shown(p.handle))
}
