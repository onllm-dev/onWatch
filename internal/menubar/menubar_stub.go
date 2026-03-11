//go:build !menubar || !darwin

package menubar

// Init is a no-op when the menubar companion is not compiled in.
func Init(cfg *Config) error { return nil }

// Stop is a no-op when the menubar companion is not compiled in.
func Stop() error { return nil }

// IsSupported reports whether the current build supports the menubar companion.
func IsSupported() bool { return false }

// IsRunning reports whether the menubar companion is currently running.
func IsRunning() bool { return false }

// TriggerRefresh is a no-op when the menubar companion is not compiled in.
func TriggerRefresh(testMode bool) error {
	_ = testMode
	return nil
}
