//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive takes a non-blocking exclusive lock. It reports false (nil
// error) when another process already holds the range.
//
// The range starts at menubarLockOffset rather than 0: Windows locks are
// mandatory, so locking the first byte would make the pid written there
// unreadable to every other opener, including diagnostics and tests.
func lockFileExclusive(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	overlapped.Offset = menubarLockOffset
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, menubarLockLength, 0, &overlapped,
	)
	if err == nil {
		return true, nil
	}
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	return false, err
}

// unlockFile releases the Windows byte-range lock.
func unlockFile(file *os.File) {
	var overlapped windows.Overlapped
	overlapped.Offset = menubarLockOffset
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, menubarLockLength, 0, &overlapped)
}
