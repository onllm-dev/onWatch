package web

import (
	"strings"
	"testing"
)

// The password form must be wired before initSettingsPage awaits the menubar
// and settings loads; otherwise a click during a slow load does nothing. The
// data-ready marker tells the e2e suite every control is wired.
func TestSettingsInitWiresPasswordBeforeAsyncLoads(t *testing.T) {
	js := readStaticFile(t, "static/app.js")
	start := strings.Index(js, "async function initSettingsPage() {")
	if start < 0 {
		t.Fatal("initSettingsPage not found")
	}
	end := strings.Index(js[start:], "\n}\n")
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
