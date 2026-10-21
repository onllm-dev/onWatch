//go:build !windows

package main

import (
	"os"
	"syscall"
)

// lockFileExclusive takes a non-blocking exclusive advisory lock. It reports
// false (nil error) when the lock is already held by another process.
func lockFileExclusive(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return false, nil
	}
	return false, err
}

// unlockFile releases the advisory lock.
func unlockFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
