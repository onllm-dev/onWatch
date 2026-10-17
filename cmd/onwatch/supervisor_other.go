//go:build !windows

package main

import "fmt"

// runSupervisor only exists for the Windows auto-start task; launchd and
// systemd supervise onWatch themselves.
func runSupervisor() error {
	return fmt.Errorf("onwatch service run is used by the Windows auto-start task; launchd and systemd supervise onWatch directly")
}
