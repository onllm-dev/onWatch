//go:build menubar && linux

package menubar

import "syscall"

// quickViewSysProcAttr detaches the browser into its own process group so a
// stray signal to the companion does not tear the window down mid-read.
func quickViewSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
