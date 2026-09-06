//go:build menubar && (darwin || linux)

package menubar

import (
	"syscall"
	"testing"
)

func TestRefreshCompanionSignalUsesSIGUSR1(t *testing.T) {
	t.Parallel()
	if refreshCompanionSignal != syscall.SIGUSR1 {
		t.Fatalf("expected refresh signal %v, got %v", syscall.SIGUSR1, refreshCompanionSignal)
	}
}

func TestProcessAliveForSelf(t *testing.T) {
	t.Parallel()
	if !processAlive(syscall.Getpid()) {
		t.Fatal("expected own pid to be alive")
	}
}
