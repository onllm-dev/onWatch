//go:build !windows

package service

// headlessConsoleHost only matters for Windows task definitions.
func headlessConsoleHost() (string, bool) { return "", false }
