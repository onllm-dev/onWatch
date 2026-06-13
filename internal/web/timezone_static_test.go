package web

import (
	"strings"
	"testing"
)

func readStaticAppJS(t *testing.T) string {
	t.Helper()
	data, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read static/app.js: %v", err)
	}
	return string(data)
}

func TestAppJS_TimezoneSettingsPopulateBeforeLoad(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	initIdx := strings.Index(appJS, "async function initSettingsPage()")
	if initIdx < 0 {
		t.Fatal("initSettingsPage function not found")
	}

	initBody := appJS[initIdx:]
	populateIdx := strings.Index(initBody, "populateTimezoneSelect();")
	loadIdx := strings.Index(initBody, "await loadSettings();")
	if populateIdx < 0 {
		t.Fatal("initSettingsPage does not populate timezone options")
	}
	if loadIdx < 0 {
		t.Fatal("initSettingsPage does not load settings")
	}
	if populateIdx > loadIdx {
		t.Fatal("timezone options must be populated before saved settings are applied")
	}
}

func TestAppJS_TimezoneDashboardUsesPersistedTimezoneBeforeRender(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	if !strings.Contains(appJS, "async function initTimezoneBadge()") {
		t.Fatal("initTimezoneBadge must be async so dashboard startup can await persisted timezone loading")
	}
	if !strings.Contains(appJS, "await initTimezoneBadge();") {
		t.Fatal("dashboard initialization must await initTimezoneBadge before initial data fetch")
	}
}

func TestAppJS_TimezoneAwareDashboardTimestamps(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	if strings.Contains(appJS, "new Date().toLocaleTimeString()") {
		t.Fatal("dashboard last-updated text must use a timezone-aware formatter, not raw toLocaleTimeString")
	}
	if !strings.Contains(appJS, "function formatResetTime(") {
		t.Fatal("reset labels must use a dedicated formatter that includes the effective timezone")
	}
	if strings.Contains(appJS, "Resets: ${formatDateTime") || strings.Contains(appJS, "'Resets: ' + formatDateTime") {
		t.Fatal("reset labels must use formatResetTime instead of bare formatDateTime")
	}
	if !strings.Contains(appJS, "Browser Default (") {
		t.Fatal("Browser Default timezone option must show the detected browser timezone")
	}
}
