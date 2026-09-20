package procscan

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestScan(t *testing.T) {
	listing := []byte("/sbin/launchd\n/usr/local/bin/onwatch serve\n/opt/homebrew/bin/muse\n")
	match := func(cmdline string) bool { return strings.HasSuffix(strings.TrimSpace(cmdline), "/muse") }

	if !Scan(listing, match) {
		t.Fatal("expected a match in the listing")
	}
	if Scan([]byte("/sbin/launchd\n"), match) {
		t.Fatal("did not expect a match")
	}
	if Scan(nil, match) {
		t.Fatal("did not expect a match in an empty listing")
	}
	if Scan(listing, nil) {
		t.Fatal("a nil matcher must never report a match")
	}
}

func TestRunningContextCancelledReportsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if RunningContext(ctx, "onwatch.exe", func(string) bool { return true }) {
		t.Fatal("a cancelled scan must report false, not a spurious match")
	}
}

func TestRunningContextNilMatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if RunningContext(ctx, "", nil) {
		t.Fatal("a nil matcher must never report a match")
	}
}

// Running shells out to ps/tasklist; assert only that it is callable and
// terminates on the host running the suite.
func TestRunningIsCallable(t *testing.T) {
	_ = Running("definitely-not-a-real-process.exe", func(string) bool { return false })
}
