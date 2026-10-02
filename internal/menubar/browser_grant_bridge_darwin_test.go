//go:build menubar && darwin && cgo

package menubar

import "testing"

func TestBrowserGrantOrigin(t *testing.T) {
	for _, tc := range []struct {
		scheme, host string
		port         int
		main, want   bool
	}{
		{"http", "127.0.0.1", 9211, true, true},
		{"http", "127.0.0.1", 9211, false, false},
		{"http", "127.0.0.1", 9212, true, false},
		{"https", "127.0.0.1", 9211, true, false},
		{"http", "example.com", 9211, true, false},
		{"", "", 0, true, false},
	} {
		if got := grantOriginAllowed("http://127.0.0.1:9211/watch/menubar", tc.scheme, tc.host, tc.port, tc.main); got != tc.want {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
	if grantOriginAllowed("http://example.com:9211/menubar", "http", "example.com", 9211, true) {
		t.Fatal("nonlocal configured host accepted")
	}
}

func TestBrowserGrantHandlerRemoval(t *testing.T) {
	token := registerGrantHandler(func() string { return "cancelled" })
	unregisterGrantHandler(token)
	grantHandlers.Lock()
	defer grantHandlers.Unlock()
	if _, ok := grantHandlers.items[token]; ok {
		t.Fatal("destroyed host retained callback")
	}
}

func TestBrowserGrantReply(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		allowed, mainFrame, grant, pending bool
		want                               string
	}{
		// A rejected grant must answer, or the page waits forever.
		{"rejected grant", false, true, true, false, "unavailable"},
		{"rejected status", false, true, false, false, ""},
		// Untrusted frames must not drive the trusted page's grant state.
		{"rejected subframe grant", false, false, true, false, ""},
		{"rejected grant while picker open", false, true, true, true, ""},
		{"grant started", true, true, true, true, "pending"},
		{"status while picker open", true, true, false, true, "pending"},
		// Finished results were delivered live; replaying them on every
		// popover open would resurrect stale feedback.
		{"status after completion", true, true, false, false, "idle"},
	} {
		if got := grantReply(tc.allowed, tc.mainFrame, tc.grant, tc.pending); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
