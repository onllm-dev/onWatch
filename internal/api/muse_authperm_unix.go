//go:build !windows

package api

import "io/fs"

// museAuthFilePermsOK rejects a login file that is group- or world-accessible.
// The key is a bearer credential, so a readable-by-others file is not trusted.
func museAuthFilePermsOK(perm fs.FileMode) bool {
	return perm&0o077 == 0
}
