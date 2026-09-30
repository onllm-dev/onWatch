//go:build menubar && darwin && cgo && granttest

package menubar

// This harness is excluded from release binaries. Tests use the real C -> Go
// callback and Cocoa completion queue, substituting the permission-flow result.

/*
#import <Cocoa/Cocoa.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
extern void onwatchGoGrantBrowserAccess(uint64_t token);
static void test_grant_start(uint64_t token) { onwatchGoGrantBrowserAccess(token); }
static void test_grant_pump(void) {
  [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
}
static char *test_grant_result(void *handle) {
  id host = (__bridge id)handle;
  NSString *result = [host valueForKey:@"grantResult"];
  return strdup(result ? result.UTF8String : "");
}
*/
import "C"

import "unsafe"

func startNativeGrantTest(token uint64) { C.test_grant_start(C.uint64_t(token)) }
func pumpNativeGrantTest()              { C.test_grant_pump() }
func nativeGrantTestResult(p *webViewPopover) string {
	value := C.test_grant_result(p.handle)
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
