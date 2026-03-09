//go:build menubar && darwin

package menubar

import "sync/atomic"

var running atomic.Bool

// Init starts the real menubar companion. The implementation lives in
// companion_darwin.go to keep macOS-specific UI code isolated.
func Init(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	running.Store(true)
	defer running.Store(false)
	return runCompanion(cfg)
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
