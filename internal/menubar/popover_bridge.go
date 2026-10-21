package menubar

import (
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"time"
)

// Message bridge for popover hosts that expose a single string channel to the
// page (WebView2's chrome.webview.postMessage on Windows). The page wraps each
// message as {"channel": "...", "body": ...}, where channel and body match the
// per-name WKScriptMessageHandlers the macOS host registers, so every host
// understands the same vocabulary. Kept free of build tags so it is tested on
// every platform.

type hostMessageKind int

const (
	hostMessageUnknown hostMessageKind = iota
	hostMessageResize
	hostMessageClose
	hostMessageOpenDashboard
	hostMessageOpenURL
	hostMessageTheme
)

type hostMessage struct {
	Kind   hostMessageKind
	Height float64
	URL    string
	// Theme is the page's effective theme ("dark" or "light") after applyTheme.
	Theme string
}

// parseHostMessage decodes one page message. Anything malformed or not meant
// for this host decodes as hostMessageUnknown and is ignored by the caller.
func parseHostMessage(raw string) hostMessage {
	var envelope struct {
		Channel string          `json:"channel"`
		Body    json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return hostMessage{}
	}
	switch envelope.Channel {
	case "onwatchResize":
		var height float64
		if err := json.Unmarshal(envelope.Body, &height); err == nil && height > 0 {
			return hostMessage{Kind: hostMessageResize, Height: height}
		}
		var sized struct {
			Height float64 `json:"height"`
		}
		if err := json.Unmarshal(envelope.Body, &sized); err == nil && sized.Height > 0 {
			return hostMessage{Kind: hostMessageResize, Height: sized.Height}
		}
	case "onwatchAction":
		var action struct {
			Action string `json:"action"`
			URL    string `json:"url"`
			Theme  string `json:"theme"`
		}
		if err := json.Unmarshal(envelope.Body, &action.Action); err != nil {
			if err := json.Unmarshal(envelope.Body, &action); err != nil {
				return hostMessage{}
			}
		}
		switch action.Action {
		case "close":
			return hostMessage{Kind: hostMessageClose}
		case "open_dashboard":
			return hostMessage{Kind: hostMessageOpenDashboard}
		case "open_url":
			return hostMessage{Kind: hostMessageOpenURL, URL: action.URL}
		case "theme":
			if action.Theme == "dark" || action.Theme == "light" {
				return hostMessage{Kind: hostMessageTheme, Theme: action.Theme}
			}
		}
	}
	return hostMessage{}
}

// isLocalPopoverURL reports whether the popover may navigate to u itself.
// Everything else belongs in the user's browser, as on macOS.
func isLocalPopoverURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme == "about" {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1"
}

// dashboardURLFor derives the dashboard origin from the loaded popover URL so
// the runtime port (ONWATCH_PORT / --port) is honoured without plumbing.
func dashboardURLFor(loaded string) (string, bool) {
	if !isLocalPopoverURL(loaded) {
		return "", false
	}
	u, err := url.Parse(loaded)
	if err != nil || u.Host == "" {
		return "", false
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String(), true
}

// externalURLAllowed limits what the page can ask the host to open in the
// system browser to absolute web links.
func externalURLAllowed(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// popoverReopenGuard covers the gap between the popover hiding on focus loss
// and the tray click that caused it arriving as a toggle.
const popoverReopenGuard = 400 * time.Millisecond

// suppressToggleReopen reports whether a toggle should stay closed because the
// popover was just hidden by the very click that is now asking to toggle it.
func suppressToggleReopen(lastAutoHide, now time.Time) bool {
	if lastAutoHide.IsZero() {
		return false
	}
	elapsed := now.Sub(lastAutoHide)
	return elapsed >= 0 && elapsed < popoverReopenGuard
}

// scaleForDPI converts logical (96 DPI) pixels to physical pixels.
func scaleForDPI(v, dpi int) int {
	if dpi <= 0 {
		return v
	}
	return int(math.Round(float64(v) * float64(dpi) / 96))
}

// unscaleForDPI converts physical pixels back to logical (96 DPI) pixels.
func unscaleForDPI(v, dpi int) int {
	if dpi <= 0 {
		return v
	}
	return int(math.Round(float64(v) * 96 / float64(dpi)))
}

// popoverHeightPixels turns the page's CSS height into a physical window
// height: clamped in logical units like the macOS host (work area minus a
// margin), then scaled for the monitor.
func popoverHeightPixels(cssHeight float64, dpi, workHeightPx int) int {
	maxAllowed := 0
	if workHeightPx > 0 {
		maxAllowed = unscaleForDPI(workHeightPx, dpi) - 24
	}
	return scaleForDPI(ClampPopoverHeight(int(math.Ceil(cssHeight)), maxAllowed), dpi)
}
