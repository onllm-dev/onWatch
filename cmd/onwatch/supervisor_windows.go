//go:build windows

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// supervisorMutexWait covers a restart (`schtasks /End` then `/Run`): the old
// supervisor is still winding down when the new one starts.
const supervisorMutexWait = 10 * time.Second

// runSupervisor is `onwatch service run`, started by the auto-start task. A
// named mutex keeps it to one per session: two supervisors would take the
// daemon over from each other forever.
func runSupervisor() error {
	mutex, err := acquireSupervisorMutex(supervisorMutexWait)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(mutex)

	go exitWithParent()

	opts, err := autostartOptions()
	if err != nil {
		return err
	}
	return superviseLoop(supervisorDeps(opts))
}

func acquireSupervisorMutex(wait time.Duration) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(`Local\onWatchSupervisor`)
	if err != nil {
		return 0, err
	}
	deadline := time.Now().Add(wait)
	for {
		mutex, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			return mutex, nil
		}
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		if err != windows.ERROR_ALREADY_EXISTS {
			return 0, fmt.Errorf("onwatch service run: %w", err)
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("onwatch service run: a supervisor is already running")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// exitWithParent ends the supervisor when the process that started it does.
// Task Scheduler ends the task by terminating that process - the headless
// console host - which would otherwise leave this supervisor running, so an
// uninstall or a post-update restart would never take effect. The daemon is
// left alone: a fresh supervisor's daemon takes over from it, and `onwatch
// stop` stops it directly.
func exitWithParent() {
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(os.Getppid()))
	if err != nil {
		return
	}
	defer windows.CloseHandle(parent)
	if event, err := windows.WaitForSingleObject(parent, windows.INFINITE); err == nil && event == windows.WAIT_OBJECT_0 {
		os.Exit(0)
	}
}
