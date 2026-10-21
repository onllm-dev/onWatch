package menubar

import (
	"testing"
	"time"
)

func TestParseHostMessageResize(t *testing.T) {
	cases := map[string]float64{
		`{"channel":"onwatchResize","body":{"height":412}}`:  412,
		`{"channel":"onwatchResize","body":398.5}`:           398.5,
		`{"channel":"onwatchResize","body":{"height":"12"}}`: 0,
	}
	for raw, want := range cases {
		msg := parseHostMessage(raw)
		if want == 0 {
			if msg.Kind != hostMessageUnknown {
				t.Fatalf("%s: kind = %v, want unknown", raw, msg.Kind)
			}
			continue
		}
		if msg.Kind != hostMessageResize || msg.Height != want {
			t.Fatalf("%s: got %+v, want resize %v", raw, msg, want)
		}
	}
}

func TestParseHostMessageActions(t *testing.T) {
	cases := []struct {
		raw  string
		kind hostMessageKind
		url  string
	}{
		{`{"channel":"onwatchAction","body":"close"}`, hostMessageClose, ""},
		{`{"channel":"onwatchAction","body":{"action":"close"}}`, hostMessageClose, ""},
		{`{"channel":"onwatchAction","body":"open_dashboard"}`, hostMessageOpenDashboard, ""},
		{`{"channel":"onwatchAction","body":{"action":"open_url","url":"https://onllm.dev"}}`, hostMessageOpenURL, "https://onllm.dev"},
		{`{"channel":"onwatchAction","body":"browser_access_status"}`, hostMessageUnknown, ""},
		{`{"channel":"other","body":"close"}`, hostMessageUnknown, ""},
		{`not json`, hostMessageUnknown, ""},
		{``, hostMessageUnknown, ""},
	}
	for _, tc := range cases {
		msg := parseHostMessage(tc.raw)
		if msg.Kind != tc.kind || msg.URL != tc.url {
			t.Fatalf("%q: got %+v, want kind %v url %q", tc.raw, msg, tc.kind, tc.url)
		}
	}
}

// The page reports its effective theme so a native host can match its own
// background layers to the page, even when onWatch's theme differs from the OS.
func TestParseHostMessageTheme(t *testing.T) {
	cases := []struct {
		raw   string
		kind  hostMessageKind
		theme string
	}{
		{`{"channel":"onwatchAction","body":{"action":"theme","theme":"dark"}}`, hostMessageTheme, "dark"},
		{`{"channel":"onwatchAction","body":{"action":"theme","theme":"light"}}`, hostMessageTheme, "light"},
		{`{"channel":"onwatchAction","body":{"action":"theme","theme":"neon"}}`, hostMessageUnknown, ""},
		{`{"channel":"onwatchAction","body":{"action":"theme"}}`, hostMessageUnknown, ""},
		{`{"channel":"onwatchAction","body":{"action":"close"}}`, hostMessageClose, ""},
	}
	for _, tc := range cases {
		msg := parseHostMessage(tc.raw)
		if msg.Kind != tc.kind || msg.Theme != tc.theme {
			t.Fatalf("%q: got %+v, want kind %v theme %q", tc.raw, msg, tc.kind, tc.theme)
		}
	}
}

func TestDashboardURLForKeepsRuntimeOrigin(t *testing.T) {
	cases := map[string]string{
		"http://localhost:9211/menubar":          "http://localhost:9211",
		"http://127.0.0.1:8123/menubar?view=x":   "http://127.0.0.1:8123",
		"http://localhost:9211/base/menubar#top": "http://localhost:9211",
	}
	for loaded, want := range cases {
		got, ok := dashboardURLFor(loaded)
		if !ok || got != want {
			t.Fatalf("dashboardURLFor(%q) = %q, %v; want %q", loaded, got, ok, want)
		}
	}
	for _, bad := range []string{"", "https://example.com/menubar", "file:///C:/menubar", "::"} {
		if got, ok := dashboardURLFor(bad); ok {
			t.Fatalf("dashboardURLFor(%q) = %q, want rejection", bad, got)
		}
	}
}

func TestExternalURLAllowed(t *testing.T) {
	for _, ok := range []string{"https://github.com/onllm-dev/onwatch", "http://onllm.dev", "https://www.reddit.com/r/x"} {
		if !externalURLAllowed(ok) {
			t.Fatalf("expected %q to be allowed", ok)
		}
	}
	for _, bad := range []string{"", "javascript:alert(1)", "file:///C:/Windows", "ms-settings:", "//evil.example", "https://"} {
		if externalURLAllowed(bad) {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestIsLocalPopoverURL(t *testing.T) {
	for _, ok := range []string{"http://localhost:9211/menubar", "http://127.0.0.1:1/x", "about:blank"} {
		if !isLocalPopoverURL(ok) {
			t.Fatalf("expected %q to be local", ok)
		}
	}
	for _, bad := range []string{"https://github.com", "http://localhost.evil.com/", "file:///C:/x", ""} {
		if isLocalPopoverURL(bad) {
			t.Fatalf("expected %q to be external", bad)
		}
	}
}

func TestSuppressToggleReopen(t *testing.T) {
	now := time.Unix(1000, 0)
	if suppressToggleReopen(time.Time{}, now) {
		t.Fatal("never auto-hidden must not suppress")
	}
	if !suppressToggleReopen(now.Add(-100*time.Millisecond), now) {
		t.Fatal("a tray click right after the focus-loss hide must not reopen")
	}
	if suppressToggleReopen(now.Add(-2*time.Second), now) {
		t.Fatal("an old auto-hide must not suppress a fresh open")
	}
	if suppressToggleReopen(now.Add(time.Second), now) {
		t.Fatal("a clock step backwards must not suppress")
	}
}

func TestScaleForDPI(t *testing.T) {
	cases := []struct{ v, dpi, want int }{
		{360, 96, 360},
		{360, 120, 450},
		{360, 144, 540},
		{16, 168, 28},
		{360, 0, 360},
	}
	for _, tc := range cases {
		if got := scaleForDPI(tc.v, tc.dpi); got != tc.want {
			t.Fatalf("scaleForDPI(%d, %d) = %d, want %d", tc.v, tc.dpi, got, tc.want)
		}
	}
	if got := unscaleForDPI(540, 144); got != 360 {
		t.Fatalf("unscaleForDPI(540, 144) = %d, want 360", got)
	}
}

func TestPopoverHeightPixelsClampsInLogicalUnits(t *testing.T) {
	// 150% scaling, 1040 px tall work area: logical ceiling is 693-24.
	if got := popoverHeightPixels(400.2, 144, 1040); got != scaleForDPI(401, 144) {
		t.Fatalf("got %d, want %d", got, scaleForDPI(401, 144))
	}
	if got := popoverHeightPixels(5000, 144, 1040); got != scaleForDPI(1040*96/144-24, 144) {
		t.Fatalf("tall content should clamp to the work area, got %d", got)
	}
	if got := popoverHeightPixels(10, 96, 1040); got != MenubarPopoverMinHeight {
		t.Fatalf("tiny content should clamp to the floor, got %d", got)
	}
	if got := popoverHeightPixels(5000, 96, 0); got != MenubarPopoverMaxHeight {
		t.Fatalf("unknown work area should use the default ceiling, got %d", got)
	}
}
