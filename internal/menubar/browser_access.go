package menubar

import (
	"os"
	"path/filepath"
	"runtime"
)

// browserDataRoots lists the per-browser data directories the Mistral cookie
// importer has to enumerate to find profiles. These live inside other
// applications' data directories, which recent macOS releases gate behind an
// explicit per-application grant.
func browserDataRoots(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, "Library/Application Support/Google/Chrome"),
			filepath.Join(home, "Library/Application Support/Microsoft Edge"),
			filepath.Join(home, "Library/Application Support/Firefox"),
		}
	case "windows":
		return []string{
			filepath.Join(home, "AppData/Local/Google/Chrome/User Data"),
			filepath.Join(home, "AppData/Local/Microsoft/Edge/User Data"),
			filepath.Join(home, "AppData/Roaming/Mozilla/Firefox"),
		}
	default:
		return []string{
			filepath.Join(home, ".config/google-chrome"),
			filepath.Join(home, ".config/microsoft-edge"),
			filepath.Join(home, ".mozilla/firefox"),
		}
	}
}

// blockedBrowserRoot returns the first root that exists but cannot be
// enumerated. A directory that is present and unreadable is the signature of a
// privacy denial rather than a browser that is simply not installed, and it is
// the folder the user has to grant access to. An empty string means nothing is
// blocked.
func blockedBrowserRoot(roots []string) string {
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		if _, err := os.ReadDir(root); err != nil {
			return root
		}
	}
	return ""
}
