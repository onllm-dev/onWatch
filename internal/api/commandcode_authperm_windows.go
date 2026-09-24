package api

import "io/fs"

// commandCodeAuthFilePermsOK is a no-op on Windows. Go reports a plain
// writable file as 0666 there, so the unix group/other check would reject
// every auth file and leave Command Code users with no auto-detection at all.
// Windows ACLs are not represented in fs.FileMode, so there is nothing
// meaningful to assert.
func commandCodeAuthFilePermsOK(fs.FileMode) bool {
	return true
}
