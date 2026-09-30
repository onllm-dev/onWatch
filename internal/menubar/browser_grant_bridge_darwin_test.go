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
