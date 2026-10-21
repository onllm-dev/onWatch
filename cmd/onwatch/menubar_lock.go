package main

// Single-instance guard for the menubar companion process.
//
// Two companions can otherwise run at once. A daemon restart launches a new
// companion before the old one notices its daemon is gone (the watcher grants a
// grace period), and the pid file is then overwritten by the new process, so the
// old one is orphaned with a second tray icon. Every companion takes an
// exclusive OS lock on menubar.lock for its whole lifetime; a second one fails
// the lock and exits before creating a tray UI. The kernel releases the lock
// when the process dies, so a crash never leaves a stale lock.
//
// The daemon also sweeps any previously started companion before launching a
// new one (terminateCompanionSweep). Together these make the duplicate tray a
// bug of the past rather than something the stale indicator has to mask.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// menubarLockName is the lock file, kept beside the pid file.
const menubarLockName = "menubar.lock"

// errMenubarCompanionRunning is returned when another companion holds the lock.
var errMenubarCompanionRunning = errors.New("another menubar companion is already running")

// menubarLock is a held single-instance lock. Close releases it.
type menubarLock struct {
	file *os.File
}

func menubarLockPath(testMode bool) string {
	name := menubarLockName
	if testMode {
		name = "menubar-test.lock"
	}
	return filepath.Join(pidDir, name)
}

// acquireMenubarLock takes the process-wide lock. It returns
// errMenubarCompanionRunning when another companion already holds it.
func acquireMenubarLock(testMode bool) (*menubarLock, error) {
	path := menubarLockPath(testMode)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create menubar lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open menubar lock: %w", err)
	}
	ok, err := lockFileExclusive(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !ok {
		_ = file.Close()
		return nil, errMenubarCompanionRunning
	}
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	return &menubarLock{file: file}, nil
}

// Close releases the lock. Safe to call more than once.
func (l *menubarLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockFile(l.file)
	err := l.file.Close()
	l.file = nil
	return err
}
