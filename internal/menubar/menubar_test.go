package menubar

import (
	"strings"
	"testing"
)

func TestDefaultConfigUsesRepoDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != 9211 {
		t.Fatalf("expected port 9211, got %d", cfg.Port)
	}
	if cfg.DefaultView != ViewStandard {
		t.Fatalf("expected standard view, got %s", cfg.DefaultView)
	}
	if cfg.RefreshSeconds != 60 {
		t.Fatalf("expected refresh 60, got %d", cfg.RefreshSeconds)
	}
}

func TestSettingsNormalizeRepairsInvalidValues(t *testing.T) {
	settings := (&Settings{
		DefaultView:      "",
		RefreshSeconds:   5,
		VisibleProviders: []string{"synthetic", "", "synthetic"},
		WarningPercent:   99,
		CriticalPercent:  60,
		StatusDisplay: StatusDisplay{
			Mode:       StatusDisplayMode("provider_specific"),
			ProviderID: "synthetic",
			QuotaKey:   "search",
		},
	}).Normalize()

	if settings.DefaultView != ViewStandard {
		t.Fatalf("expected standard view, got %s", settings.DefaultView)
	}
	if settings.RefreshSeconds != 60 {
		t.Fatalf("expected refresh 60, got %d", settings.RefreshSeconds)
	}
	if settings.WarningPercent != 70 || settings.CriticalPercent != 90 {
		t.Fatalf("expected fallback thresholds 70/90, got %d/%d", settings.WarningPercent, settings.CriticalPercent)
	}
	if settings.Theme != ThemeSystem {
		t.Fatalf("expected system theme, got %s", settings.Theme)
	}
	if settings.ProvidersOrder == nil {
		t.Fatal("expected providers order to be initialized")
	}
	if len(settings.VisibleProviders) != 1 || settings.VisibleProviders[0] != "synthetic" {
		t.Fatalf("unexpected visible providers: %#v", settings.VisibleProviders)
	}
	if settings.StatusDisplay.Mode != StatusDisplayMultiProvider {
		t.Fatalf("expected multi_provider status display, got %s", settings.StatusDisplay.Mode)
	}
	if len(settings.StatusDisplay.SelectedQuotas) != 1 {
		t.Fatalf("expected one migrated tray selection, got %#v", settings.StatusDisplay.SelectedQuotas)
	}
	if settings.StatusDisplay.SelectedQuotas[0].ProviderID != "synthetic" || settings.StatusDisplay.SelectedQuotas[0].QuotaKey != "search" {
		t.Fatalf("unexpected migrated tray selection: %#v", settings.StatusDisplay.SelectedQuotas[0])
	}
}

func TestInlineHTMLUsesRequestedView(t *testing.T) {
	html, err := InlineHTML(ViewMinimal, DefaultSettings())
	if err != nil {
		t.Fatalf("InlineHTML returned error: %v", err)
	}
	if !strings.Contains(html, `"default_view":"minimal"`) {
		t.Fatalf("expected minimal default view in inline html, got: %s", html)
	}
}

func TestIsSupportedSmoke(t *testing.T) {
	t.Logf("menubar supported: %v", IsSupported())
}
