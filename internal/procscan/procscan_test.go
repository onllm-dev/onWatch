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

func TestValidWindowsImage(t *testing.T) {
	valid := []string{"claude.exe", "kimi-code.exe", "muse.exe", "a_b-1.exe"}
	for _, name := range valid {
		if !validWindowsImage(name) {
			t.Errorf("validWindowsImage(%q) = false, want true", name)
		}
	}
	// Anything that could escape the quoted findstr argument must be refused.
	invalid := []string{
		"",
		`" & calc & "`,
		"claude.exe\" & del /q C:\\ & \"",
		`c:\windows\system32\claude.exe`,
		"claude exe",
		"claude|findstr",
		"claude&calc",
		"claude%PATH%",
		"cláude.exe",
	}
	for _, name := range invalid {
		if validWindowsImage(name) {
			t.Errorf("validWindowsImage(%q) = true, want false", name)
		}
	}
}

// Agent contexts are cancel-only with no deadline. The scan must still be
// bounded, or a wedged `ps` holds the poll goroutine until daemon shutdown.
func TestRunningContextBoundsAnUnboundedCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("test precondition: caller context must have no deadline")
	}

	var seen context.Context
	restore := execCommandContext
	execCommandContext = func(c context.Context, name string, args ...string) ([]byte, error) {
		seen = c
		return nil, context.Canceled
	}
	t.Cleanup(func() { execCommandContext = restore })

	RunningContext(ctx, "x.exe", func(string) bool { return false })
	if seen == nil {
		t.Fatal("scan never ran")
	}
	if _, ok := seen.Deadline(); !ok {
		t.Fatal("scan context has no deadline: ScanTimeout is not being applied")
	}
}
