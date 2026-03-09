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
		DefaultView:     "",
		RefreshSeconds:  5,
		WarningPercent:  99,
		CriticalPercent: 60,
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
	if settings.ProvidersOrder == nil {
		t.Fatal("expected providers order to be initialized")
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
