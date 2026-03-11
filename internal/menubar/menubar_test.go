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

	assert := func(ok bool, format string, args ...any) {
		if !ok {
			t.Fatalf(format, args...)
		}
	}

	assert(settings.DefaultView == ViewStandard, "expected standard view, got %s", settings.DefaultView)
	assert(settings.RefreshSeconds == 60, "expected refresh 60, got %d", settings.RefreshSeconds)
	assert(
		settings.WarningPercent == 70 && settings.CriticalPercent == 90,
		"expected fallback thresholds 70/90, got %d/%d",
		settings.WarningPercent,
		settings.CriticalPercent,
	)
	assert(settings.Theme == ThemeSystem, "expected system theme, got %s", settings.Theme)
	assert(settings.ProvidersOrder != nil, "expected providers order to be initialized")
	assert(
		len(settings.VisibleProviders) == 1 && settings.VisibleProviders[0] == "synthetic",
		"unexpected visible providers: %#v",
		settings.VisibleProviders,
	)
	assert(
		settings.StatusDisplay.Mode == StatusDisplayMultiProvider,
		"expected multi_provider status display, got %s",
		settings.StatusDisplay.Mode,
	)
	assert(
		len(settings.StatusDisplay.SelectedQuotas) == 1,
		"expected one migrated tray selection, got %#v",
		settings.StatusDisplay.SelectedQuotas,
	)
	selection := settings.StatusDisplay.SelectedQuotas[0]
	assert(
		selection.ProviderID == "synthetic" && selection.QuotaKey == "search",
		"unexpected migrated tray selection: %#v",
		selection,
	)
}

func TestSettingsNormalizePreservesMinimalView(t *testing.T) {
	settings := (&Settings{DefaultView: ViewMinimal}).Normalize()
	if settings.DefaultView != ViewMinimal {
		t.Fatalf("expected minimal view to be preserved, got %s", settings.DefaultView)
	}
}

func TestInlineHTMLUsesRequestedView(t *testing.T) {
	html, err := InlineHTML(ViewDetailed, DefaultSettings())
	if err != nil {
		t.Fatalf("InlineHTML returned error: %v", err)
	}
	if !strings.Contains(html, `"default_view":"detailed"`) {
		t.Fatalf("expected detailed default view in inline html, got: %s", html)
	}
}

func TestIsSupportedSmoke(t *testing.T) {
	t.Logf("menubar supported: %v", IsSupported())
}
