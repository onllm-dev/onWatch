//go:build menubar && darwin && cgo

package menubar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>

int onwatch_request_folder_access(const char* path, const char* message);
*/
import "C"

import "unsafe"

// Outcomes reported by the native panel, kept in sync with the codes in
// browser_access_darwin.m.
const (
	accessCancelled = 0
	accessGranted   = 1
	accessNoApp     = 2
	accessTimeout   = 3
)

// requestFolderAccess asks the user to confirm access to one folder. macOS
// records the resulting grant against this binary, so the daemon shares it.
func requestFolderAccess(path, message string) (bool, string) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cMessage := C.CString(message)
	defer C.free(unsafe.Pointer(cMessage))
	switch int(C.onwatch_request_folder_access(cPath, cMessage)) {
	case accessGranted:
		return true, "granted"
	case accessNoApp:
		return false, "no application context for a panel"
	case accessTimeout:
		return false, "panel was not answered"
	default:
		return false, "cancelled"
	}
}
