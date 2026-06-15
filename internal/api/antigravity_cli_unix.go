//go:build !windows

package api

import (
	"os"
	"syscall"
)

// killAgyTree terminates the managed agy process and its language-server child.
// agy is launched in a new PTY session (setsid), so it leads its own process
// group; signalling the negative PID reaps the whole tree. We only ever pass a
// process this runner started, never a user's interactive agy.
func killAgyTree(p *os.Process) {
	if p == nil {
		return
	}
	if pgid, err := syscall.Getpgid(p.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	_ = p.Kill()
}
