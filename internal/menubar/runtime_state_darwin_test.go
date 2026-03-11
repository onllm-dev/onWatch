//go:build menubar && darwin

package menubar

import (
	"syscall"
	"testing"
)

func TestRefreshCompanionSignalUsesSIGUSR1(t *testing.T) {
	if refreshCompanionSignal != syscall.SIGUSR1 {
		t.Fatalf("expected refresh signal %v, got %v", syscall.SIGUSR1, refreshCompanionSignal)
	}
}
