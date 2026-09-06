//go:build menubar && windows

package menubar

import "syscall"

func quickViewSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: false}
}
