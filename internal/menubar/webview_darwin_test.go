//go:build menubar && darwin && cgo

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

func TestNewMenubarPopoverLifecycle(t *testing.T) {
	var (
		popover menubarPopover
		err     error
	)

	runOnMainThread(t, func() {
		popover, err = newMenubarPopover(320, 240)
	})
	if err != nil {
		t.Fatalf("newMenubarPopover returned error: %v", err)
	}
	if popover == nil {
		t.Fatal("expected popover instance")
	}

	runOnMainThread(t, func() {
		popover.Close()
		popover.Destroy()
		popover.Destroy()
	})
}
