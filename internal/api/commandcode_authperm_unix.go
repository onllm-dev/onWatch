//go:build !windows

package api

import "io/fs"

// commandCodeAuthFilePermsOK rejects an auth file that is group- or
// world-accessible. The file holds a bearer key, so a readable-by-others file
// is not trusted.
func commandCodeAuthFilePermsOK(perm fs.FileMode) bool {
	return perm&0o077 == 0
}
