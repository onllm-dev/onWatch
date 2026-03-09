//go:build menubar && darwin

package menubar

import "sync/atomic"

var running atomic.Bool

// Init starts the real menubar companion. The implementation lives in
// companion_darwin.go to keep Wails-heavy code isolated.
func Init(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if err := runCompanion(cfg); err != nil {
		running.Store(false)
		return err
	}
	running.Store(true)
	return nil
}

// Stop requests the menubar companion to exit.
func Stop() error {
	running.Store(false)
	return stopCompanion()
}

// IsSupported reports whether this build can run the real menubar companion.
func IsSupported() bool { return true }

// IsRunning reports whether the companion is marked as active.
func IsRunning() bool { return running.Load() || companionProcessRunning() }
