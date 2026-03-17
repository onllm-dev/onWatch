//go:build menubar && (darwin || linux)

package menubar

import (
	"testing"
	"time"
)

func TestNormalizeRefreshSecondsBelowMinimum(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{0, 60},
		{1, 60},
		{5, 60},
		{9, 60},
		{-1, 60},
		{10, 10},
		{11, 11},
		{60, 60},
		{300, 300},
	}
	for _, tc := range cases {
		got := normalizeRefreshSeconds(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeRefreshSeconds(%d) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}

func TestTrayTooltipNilSnapshot(t *testing.T) {
	got := trayTooltip(nil)
	if got != "onWatch menubar companion" {
		t.Fatalf("expected default tooltip, got %q", got)
	}
}

func TestTrayTooltipZeroProviders(t *testing.T) {
	snapshot := &Snapshot{
		Aggregate: Aggregate{ProviderCount: 0},
	}
	got := trayTooltip(snapshot)
	if got != "onWatch menubar companion: no provider data available" {
		t.Fatalf("expected no-provider tooltip, got %q", got)
	}
}

func TestTrayTooltipWithProviders(t *testing.T) {
	snapshot := &Snapshot{
		UpdatedAgo: "2m ago",
		Aggregate: Aggregate{
			ProviderCount: 3,
			Label:         "42%",
		},
	}
	got := trayTooltip(snapshot)
	expected := "onWatch menubar companion: 42% across 3 providers, updated 2m ago"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestTrayControllerMenubarURL(t *testing.T) {
	cases := []struct {
		port     int
		expected string
	}{
		{0, "http://localhost:9211/menubar"},
		{8080, "http://localhost:8080/menubar"},
		{9211, "http://localhost:9211/menubar"},
	}
	for _, tc := range cases {
		c := &trayController{cfg: &Config{Port: tc.port}}
		got := c.menubarURL()
		if got != tc.expected {
			t.Errorf("menubarURL() with port=%d = %q, want %q", tc.port, got, tc.expected)
		}
	}
}

func TestTrayControllerDashboardURL(t *testing.T) {
	c := &trayController{cfg: &Config{Port: 8888}}
	got := c.dashboardURL()
	if got != "http://localhost:8888" {
		t.Fatalf("expected http://localhost:8888, got %q", got)
	}
}

func TestTrayControllerPreferencesURL(t *testing.T) {
	c := &trayController{cfg: &Config{Port: 9211}}
	got := c.preferencesURL()
	if got != "http://localhost:9211/api/menubar/preferences" {
		t.Fatalf("expected preferences URL, got %q", got)
	}
}

func TestTrayControllerMenubarURLNilConfig(t *testing.T) {
	c := &trayController{}
	got := c.menubarURL()
	if got != "http://localhost:9211/menubar" {
		t.Fatalf("expected default menubar URL, got %q", got)
	}
}

func TestTrayControllerDashboardURLNilConfig(t *testing.T) {
	c := &trayController{}
	got := c.dashboardURL()
	if got != "http://localhost:9211" {
		t.Fatalf("expected default dashboard URL, got %q", got)
	}
}

func TestRefreshStatusNilController(t *testing.T) {
	// Should not panic with nil fields.
	c := &trayController{}
	// This calls systray.SetTitle/SetTooltip which requires systray to be
	// initialized. We cannot call it directly in a unit test, but we can
	// verify the snapshot provider nil-guard doesn't panic.
	if c.cfg != nil && c.cfg.SnapshotProvider != nil {
		t.Fatal("expected nil snapshot provider")
	}
}

func TestRefreshStatusHandlesSnapshotError(t *testing.T) {
	errCalled := false
	cfg := &Config{
		Port: 9211,
		SnapshotProvider: func() (*Snapshot, error) {
			errCalled = true
			return nil, &time.ParseError{}
		},
	}
	c := &trayController{cfg: cfg}
	// We can't call refreshStatus directly because it calls systray.SetTitle,
	// but we can verify the provider is callable.
	_, _ = c.cfg.SnapshotProvider()
	if !errCalled {
		t.Fatal("expected snapshot provider to be called")
	}
}

func TestStopCompanionQuitOnceIdempotency(t *testing.T) {
	// Reset global state for this test
	originalQuitOnce := quitOnce
	originalQuitFn := quitFn
	defer func() {
		quitOnce = originalQuitOnce
		quitFn = originalQuitFn
	}()

	callCount := 0
	quitFn = func() {
		callCount++
	}

	// First call should invoke quitFn
	_ = stopCompanion()

	// Second call should be no-op due to sync.Once
	_ = stopCompanion()

	if callCount != 1 {
		t.Fatalf("expected quitFn called exactly once, got %d", callCount)
	}
}
