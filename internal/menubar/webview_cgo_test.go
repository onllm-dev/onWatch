//go:build menubar && (darwin || linux) && cgo

package menubar

import (
	"os"
	"runtime"
	"testing"
)

var (
	mainThreadTasks = make(chan func())
	testExitCode    = make(chan int, 1)
)

// TestMain locks the OS thread so that CGO UI calls (Cocoa on macOS,
// GTK on Linux) execute on the main thread as required by both toolkits.
func TestMain(m *testing.M) {
	runtime.LockOSThread()

	go func() {
		testExitCode <- m.Run()
	}()

	for {
		select {
		case fn := <-mainThreadTasks:
			fn()
		case code := <-testExitCode:
			os.Exit(code)
		}
	}
}

func runOnMainThread(t *testing.T, fn func()) {
	t.Helper()

	done := make(chan struct{})
	mainThreadTasks <- func() {
		defer close(done)
		fn()
	}
	<-done
}
