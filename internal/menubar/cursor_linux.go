//go:build menubar && linux

package menubar

// quickViewAnchor is unavailable without X11/Wayland bindings; the window
// manager decides placement and remembers it for the dedicated profile.
func quickViewAnchor(_, _ int) *windowPosition { return nil }
