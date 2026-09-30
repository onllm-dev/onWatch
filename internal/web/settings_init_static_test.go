package web

import (
	"strings"
	"testing"
)

// The password form must be wired before initSettingsPage awaits the menubar
// and settings loads; otherwise a click during a slow load does nothing. The
// data-ready marker tells the e2e suite every control is wired.
func TestSettingsInitWiresPasswordBeforeAsyncLoads(t *testing.T) {
	// A Windows checkout may convert app.js to CRLF line endings.
	js := strings.ReplaceAll(readStaticFile(t, "static/app.js"), "\r\n", "\n")
	start := strings.Index(js, "async function initSettingsPage() {")
	if start < 0 {
		t.Fatal("initSettingsPage not found")
	}
	end := strings.Index(js[start:], "\n}\n")
	if end < 0 {
		t.Fatal("end of initSettingsPage not found")
	}
	body := js[start : start+end]
	password := strings.Index(body, "setupSettingsPassword();")
	firstAwait := strings.Index(body, "await ")
	if password < 0 || firstAwait < 0 || password > firstAwait {
		t.Fatalf("setupSettingsPassword must run before the first await in initSettingsPage:\n%s", body)
	}
	if !strings.Contains(body, "setAttribute('data-ready', 'true')") {
		t.Fatal("initSettingsPage must mark .settings-page data-ready when wiring completes")
	}
}
