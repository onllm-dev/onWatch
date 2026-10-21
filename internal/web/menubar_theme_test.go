package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The header age must track the newest provider data (latest_at), not the
// snapshot build time, so it keeps growing when every provider has stopped
// polling. The page must also report its effective theme to a native host so
// the host can match its own background layers.
func TestMenubarHeaderAgeAndThemeReport(t *testing.T) {
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	// The header must read latest_at, not generated_at.
	if !strings.Contains(html, "state.snapshot.latest_at") {
		t.Fatal("menubar.html header age must use snapshot.latest_at")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for menubar header age test")
	}

	// applyTheme reports the effective theme through sendNativeAction.
	themeStart := strings.Index(html, "function applyTheme()")
	themeEnd := strings.Index(html[themeStart:], "function queueResizeReport()") + themeStart
	if themeStart < 0 || themeEnd < themeStart {
		t.Fatal("applyTheme not found in menubar.html")
	}
	themeSlice := html[themeStart:themeEnd]

	// ageLabelFromTimestamp is shared with the provider labels.
	ageStart := strings.Index(html, "function compactUpdatedLabel(")
	ageEnd := strings.Index(html, "// quotaLabelSemantic maps")
	if ageStart < 0 || ageEnd < ageStart {
		t.Fatal("age label helpers not found in menubar.html")
	}
	ageSlice := html[ageStart:ageEnd]

	test := `
const assert = require('node:assert/strict');
` + ageSlice + `
// The header age for a stale snapshot counts from latest_at, not generated_at.
const nowMs = 1_700_000_000_000;
Date.now = () => nowMs;
const hour = 60 * 60 * 1000;
const snapshot = {
  generated_at: new Date(nowMs).toISOString(),
  latest_at: new Date(nowMs - 2 * hour).toISOString(),
  updated_ago: "just now"
};
assert.equal(ageLabelFromTimestamp(snapshot.latest_at, snapshot.updated_ago), "2h");
// Missing latest_at falls back to the server string.
assert.equal(ageLabelFromTimestamp(undefined, snapshot.updated_ago), "now");

// applyTheme posts the effective theme through the native channel.
const posted = [];
function sendNativeAction(action) { posted.push(action); return true; }
const state = { preferences: { theme: "dark" }, settingsOpen: false, draft: null, systemPrefersDark: false };
function resolveThemeMode() { return "dark"; }
const document = { documentElement: { setAttribute() {}, style: {} } };
` + themeSlice + `
applyTheme();
assert.deepEqual(posted, [{ action: "theme", theme: "dark" }]);
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(test)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
