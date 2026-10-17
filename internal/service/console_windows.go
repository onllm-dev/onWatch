//go:build windows

package service

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// headlessConsoleHost returns conhost.exe when it supports --headless
// (Windows 10 1903 and later). Task Scheduler opens a visible console for a
// console program such as onwatch.exe; hosting it headless keeps logon free of
// a flashing window, and of a Windows Terminal tab where that is the default
// terminal.
func headlessConsoleHost() (string, bool) {
	if windows.RtlGetVersion().BuildNumber < 18362 {
		return "", false
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	host := filepath.Join(root, "System32", "conhost.exe")
	if _, err := os.Stat(host); err != nil {
		return "", false
	}
	return host, true
}
