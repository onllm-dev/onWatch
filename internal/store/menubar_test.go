package store

import (
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

func TestStoreMenubarSettingsRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "onwatch.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	settings := &menubar.Settings{
		Enabled:          false,
		DefaultView:      menubar.ViewDetailed,
		RefreshSeconds:   120,
		ProvidersOrder:   []string{"codex:1", "synthetic"},
		VisibleProviders: []string{"synthetic"},
		WarningPercent:   60,
		CriticalPercent:  85,
		StatusDisplay: menubar.StatusDisplay{
			Mode: menubar.StatusDisplayMultiProvider,
			SelectedQuotas: []menubar.StatusDisplaySelection{
				{ProviderID: "synthetic", QuotaKey: "search"},
				{ProviderID: "anthropic", QuotaKey: "five_hour"},
			},
		},
		Theme: menubar.ThemeDark,
	}
	if err := s.SetMenubarSettings(settings); err != nil {
		t.Fatalf("SetMenubarSettings returned error: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetMenubarSettings()
	if err != nil {
		t.Fatalf("GetMenubarSettings returned error: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected menubar to stay disabled after round-trip")
	}
	if got.DefaultView != menubar.ViewDetailed {
		t.Fatalf("expected detailed view, got %s", got.DefaultView)
	}
	if got.RefreshSeconds != 120 {
		t.Fatalf("expected refresh 120, got %d", got.RefreshSeconds)
	}
	if len(got.ProvidersOrder) != 2 || got.ProvidersOrder[0] != "codex:1" {
		t.Fatalf("unexpected provider order: %#v", got.ProvidersOrder)
	}
	if got.WarningPercent != 60 || got.CriticalPercent != 85 {
		t.Fatalf("unexpected thresholds: %d/%d", got.WarningPercent, got.CriticalPercent)
	}
	if len(got.VisibleProviders) != 1 || got.VisibleProviders[0] != "synthetic" {
		t.Fatalf("unexpected visible providers: %#v", got.VisibleProviders)
	}
	if got.StatusDisplay.Mode != menubar.StatusDisplayMultiProvider {
		t.Fatalf("unexpected status display: %#v", got.StatusDisplay)
	}
	if len(got.StatusDisplay.SelectedQuotas) != 2 {
		t.Fatalf("expected two tray selections, got %#v", got.StatusDisplay.SelectedQuotas)
	}
	if got.StatusDisplay.SelectedQuotas[0].ProviderID != "synthetic" || got.StatusDisplay.SelectedQuotas[0].QuotaKey != "search" {
		t.Fatalf("unexpected first tray selection: %#v", got.StatusDisplay.SelectedQuotas[0])
	}
	if got.Theme != menubar.ThemeDark {
		t.Fatalf("expected dark theme, got %s", got.Theme)
	}
}

func TestStoreMenubarSettingsDefaults(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer s.Close()

	got, err := s.GetMenubarSettings()
	if err != nil {
		t.Fatalf("GetMenubarSettings returned error: %v", err)
	}
	if got.DefaultView != menubar.ViewStandard {
		t.Fatalf("expected standard view, got %s", got.DefaultView)
	}
	if got.RefreshSeconds != 60 {
		t.Fatalf("expected refresh 60, got %d", got.RefreshSeconds)
	}
}
