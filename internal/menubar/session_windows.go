//go:build windows

package menubar

import (
	"os"

	"golang.org/x/sys/windows"
)

// processSessionID is the Terminal Services session this process runs in.
func processSessionID() (uint32, bool) {
	var id uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &id); err != nil {
		return 0, false
	}
	return id, true
}
