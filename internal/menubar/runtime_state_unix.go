//go:build menubar && (darwin || linux)

package menubar

import (
	"os"
	"os/signal"
	"syscall"
)

const refreshCompanionSignal = syscall.SIGUSR1

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func requestCompanionRefresh(pid int, _ bool) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Signal(refreshCompanionSignal)
	return nil
}

// watchRefreshRequests invokes fn every time the daemon signals SIGUSR1.
func watchRefreshRequests(stop <-chan struct{}, _ bool, fn func()) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, refreshCompanionSignal)
	defer signal.Stop(signalChan)
	for {
		select {
		case <-signalChan:
			fn()
		case <-stop:
			return
		}
	}
}
