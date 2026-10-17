//go:build !windows

package menubar

// processSessionID only exists on Windows.
func processSessionID() (uint32, bool) { return 0, false }
