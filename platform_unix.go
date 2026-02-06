//go:build !windows

package main

import "syscall"

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func defaultPIDDir() string {
	return "/opt/syntrack"
}
