//go:build menubar && darwin && cgo

package menubar

/*
#include <stdint.h>
#include <stdlib.h>
#include <stdbool.h>
void onwatch_grant_finish(uint64_t token, const char *result);
bool onwatch_grant_origin_allowed(const char *configuredURL, const char *scheme, const char *host, int port, bool mainFrame);
const char *onwatch_grant_reply(bool allowed, bool mainFrame, bool grant, bool pending);
*/
import "C"

import (
	"sync"
	"unsafe"
)

func grantOriginAllowed(configured, scheme, host string, port int, mainFrame bool) bool {
	cURL, cScheme, cHost := C.CString(configured), C.CString(scheme), C.CString(host)
	defer C.free(unsafe.Pointer(cURL))
	defer C.free(unsafe.Pointer(cScheme))
	defer C.free(unsafe.Pointer(cHost))
	return bool(C.onwatch_grant_origin_allowed(cURL, cScheme, cHost, C.int(port), C.bool(mainFrame)))
}

func grantReply(allowed, mainFrame, grant, pending bool) string {
	reply := C.onwatch_grant_reply(C.bool(allowed), C.bool(mainFrame), C.bool(grant), C.bool(pending))
	if reply == nil {
		return ""
	}
	return C.GoString(reply)
}

var grantHandlers = struct {
	sync.Mutex
	next  uint64
	items map[uint64]func() string
}{items: make(map[uint64]func() string)}

func registerGrantHandler(fn func() string) uint64 {
	grantHandlers.Lock()
	defer grantHandlers.Unlock()
	grantHandlers.next++
	grantHandlers.items[grantHandlers.next] = fn
	return grantHandlers.next
}

func unregisterGrantHandler(token uint64) {
	grantHandlers.Lock()
	defer grantHandlers.Unlock()
	delete(grantHandlers.items, token)
}

//export onwatchGoGrantBrowserAccess
func onwatchGoGrantBrowserAccess(token C.uint64_t) {
	grantHandlers.Lock()
	fn := grantHandlers.items[uint64(token)]
	grantHandlers.Unlock()
	if fn == nil {
		return
	}
	go func() {
		result := C.CString(fn())
		defer C.free(unsafe.Pointer(result))
		C.onwatch_grant_finish(token, result)
	}()
}
