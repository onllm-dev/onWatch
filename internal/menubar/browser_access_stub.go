//go:build !menubar || !darwin || !cgo

package menubar

// requestFolderAccess is macOS-only: no other supported platform gates reading
// another application's data directory behind a per-application grant.
func requestFolderAccess(string, string) (bool, string) {
	return false, "unsupported on this platform"
}
