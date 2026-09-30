//go:build menubar && darwin && cgo && granttest

package menubar

import (
	"sync/atomic"
	"testing"
	"time"
)

func grantTestHost(t *testing.T, fn func() string) *webViewPopover {
	t.Helper()
	var host menubarPopover
	var err error
	runOnMainThread(t, func() {
		host, err = newMenubarPopover(320, 240)
		if err == nil {
			host.(*webViewPopover).SetBrowserGrantHandler(fn)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	p := host.(*webViewPopover)
	t.Cleanup(func() { runOnMainThread(t, p.Destroy) })
	return p
}

func awaitGrantResult(t *testing.T, p *webViewPopover, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		runOnMainThread(t, func() { pumpNativeGrantTest(); got = nativeGrantTestResult(p) })
		if got == want {
			return
		}
	}
	t.Fatalf("native completion did not deliver %q", want)
}

func TestBrowserGrantNativeCallback(t *testing.T) {
	for _, result := range []string{"retrying", "cancelled", "unavailable", "retry_failed"} {
		t.Run(result, func(t *testing.T) {
			var calls atomic.Int32
			p := grantTestHost(t, func() string { calls.Add(1); return result })
			runOnMainThread(t, func() { startNativeGrantTest(p.grantToken) })
			awaitGrantResult(t, p, result)
			if calls.Load() != 1 {
				t.Fatalf("callback ran %d times", calls.Load())
			}
		})
	}
}

func TestBrowserGrantNativeCallbackAfterDestroy(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := grantTestHost(t, func() string {
		calls.Add(1)
		close(started)
		<-release
		return "cancelled"
	})
	token := p.grantToken
	runOnMainThread(t, func() { startNativeGrantTest(token) })
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not started")
	}
	runOnMainThread(t, p.Destroy)
	close(release)
	// A stale native message must not invoke the removed handler again.
	startNativeGrantTest(token)
	// Pump real completions with a replacement host. The old completion must
	// neither crash nor overwrite the replacement host's result.
	replacement := grantTestHost(t, func() string { return "retrying" })
	startNativeGrantTest(replacement.grantToken)
	awaitGrantResult(t, replacement, "retrying")
	if calls.Load() != 1 {
		t.Fatal("destroyed handler invoked again")
	}
}
