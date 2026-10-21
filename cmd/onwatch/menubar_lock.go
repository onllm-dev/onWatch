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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// menubarLockName is the lock file, kept beside the pid file.
const menubarLockName = "menubar.lock"

// menubarLockOffset is where the locked byte range starts. The pid is written
// at offset 0 so the file stays readable for diagnostics; Windows byte-range
// locks are mandatory, so locking that region would make it unreadable. Unix
// flock locks the whole file regardless and ignores the offset.
const menubarLockOffset = 1 << 30

// menubarLockLength is the size of the locked region.
const menubarLockLength = 1

// errMenubarCompanionRunning is returned when another companion holds the lock.
var errMenubarCompanionRunning = errors.New("another menubar companion is already running")

// menubarLockWaitTimeout is how long a replacement companion waits for the
// previous one to release the lock. A companion can be inside a slow refresh
// (two 15s snapshot attempts) when its daemon asks it to quit.
const menubarLockWaitTimeout = 10 * time.Second

// menubarLockRetryInterval is the retry cadence inside that window.
const menubarLockRetryInterval = 250 * time.Millisecond

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

// acquireMenubarLock takes the process-wide lock once. It returns
// errMenubarCompanionRunning when another companion already holds it.
func acquireMenubarLock(testMode bool) (*menubarLock, error) {
	return acquireMenubarLockWait(testMode, 0, 0)
}

// acquireMenubarLockWait retries the non-blocking lock every retryInterval
// until it succeeds or the timeout expires. A slow-exiting predecessor is the
// normal case after a daemon restart, which is why the companion waits instead
// of exiting immediately.
func acquireMenubarLockWait(testMode bool, timeout, retryInterval time.Duration) (*menubarLock, error) {
	path := menubarLockPath(testMode)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create menubar lock dir: %w", err)
	}
	if retryInterval <= 0 {
		retryInterval = menubarLockRetryInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open menubar lock: %w", err)
		}
		ok, err := lockFileExclusive(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if ok {
			_ = file.Truncate(0)
			_, _ = file.Seek(0, 0)
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			return &menubarLock{file: file}, nil
		}
		_ = file.Close()
		if timeout <= 0 || !time.Now().Before(deadline) {
			return nil, errMenubarCompanionRunning
		}
		time.Sleep(retryInterval)
	}
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

// menubarLockOwnerProbe reports the pid recorded inside the lock file and
// whether the lock is currently held. It never takes the lock: it opens the
// lock file read-only (so the holder is unaffected) and tries a non-blocking
// lock on that second handle. blocked means another process holds it; owner is
// that process's pid as written at acquisition (0 when unknown).
//
// The pid inside the lock is written by the holder and only trusted while the
// lock is held, so it distinguishes the companion from an unrelated process
// that happens to reuse the pid in the runtime pid file.
func menubarLockOwnerProbe(testMode bool) (owner int, blocked bool) {
	path := menubarLockPath(testMode)
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return 0, false
	}
	defer file.Close()

	if ok, lockErr := lockFileExclusive(file); lockErr != nil {
		return 0, false
	} else if ok {
		unlockFile(file)
		return 0, false
	}

	// Held by someone: read the pid the holder wrote at offset 0. The read is
	// done on the same handle so Windows' mandatory lock semantics cannot get
	// in the way (the locked range starts past the text).
	if _, err := file.Seek(0, io.SeekStart); err == nil {
		if data, readErr := io.ReadAll(io.LimitReader(file, 32)); readErr == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				owner = pid
			}
		}
	}
	return owner, true
}
