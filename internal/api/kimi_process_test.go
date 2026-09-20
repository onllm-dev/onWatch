package api

import (
	"context"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/procscan"
)

func TestIsKimiCodeCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"tui process name", "kimi-code", true},
		{"tui with args", "/opt/homebrew/bin/kimi-code --resume", true},
		{"native launcher path", "/Users/dev/.kimi-code/bin/kimi", true},
		{"native launcher via node", "node /Users/dev/.kimi-code/bin/kimi", true},
		{"bare kimi on PATH is not the CLI", "/usr/local/bin/kimi", false},
		{"onwatch polling kimi", "/usr/local/bin/onwatch --provider kimi", false},
		{"unrelated process mentioning kimi", "grep -r kimi-code /etc", false},
		{"desktop helper bundle", "/Applications/Kimi.app/Contents/MacOS/kimi-code", false},
		{"electron helper", "kimi-code --type=renderer", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKimiCodeCommandLine(tt.cmdline); got != tt.want {
				t.Fatalf("isKimiCodeCommandLine(%q) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestKimiCodeMatchesProcessListing(t *testing.T) {
	listing := []byte("/sbin/launchd\n/usr/local/bin/onwatch serve\n/Users/dev/.kimi-code/bin/kimi\n")
	if !procscan.Scan(listing, isKimiCodeCommandLine) {
		t.Fatal("expected the CLI to be found in the process listing")
	}
	if procscan.Scan([]byte("/sbin/launchd\n/usr/local/bin/onwatch serve\n"), isKimiCodeCommandLine) {
		t.Fatal("did not expect a match in a listing without the CLI")
	}
	if procscan.Scan(nil, isKimiCodeCommandLine) {
		t.Fatal("did not expect a match in an empty listing")
	}
}

// IsKimiCodeRunning shells out to ps/tasklist; assert only that it is callable
// and returns without panicking on the host running the suite.
func TestIsKimiCodeRunningIsCallable(t *testing.T) {
	_ = IsKimiCodeRunning(context.Background())
}
