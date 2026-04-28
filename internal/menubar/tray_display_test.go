package menubar

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTrayTitleDefaultIsEmptyUntilProviderSelectionIsResolved(t *testing.T) {
	t.Parallel()
	snapshot := &Snapshot{
		Aggregate: Aggregate{
			ProviderCount:  2,
			HighestPercent: 84,
		},
		Providers: []ProviderCard{
			{ID: "anthropic", Label: "Anthropic", HighestPercent: 84, Quotas: []QuotaMeter{{Key: "seven_day", Label: "Weekly All-Model", Percent: 84}}},
			{ID: "copilot", Label: "Copilot", HighestPercent: 45, Quotas: []QuotaMeter{{Key: "premium_interactions", Label: "Premium Requests", Percent: 45}}},
		},
	}

	if got := TrayTitle(snapshot, DefaultSettings()); got != "" {
		t.Fatalf("TrayTitle() = %q, want empty string", got)
	}
}

func TestTrayTitleProviderSpecific(t *testing.T) {
	t.Parallel()
	snapshot := &Snapshot{
		Aggregate: Aggregate{ProviderCount: 2, HighestPercent: 84},
		Providers: []ProviderCard{
			{
				ID:             "anthropic",
				Label:          "Anthropic",
				HighestPercent: 84,
				Quotas: []QuotaMeter{
					{Key: "five_hour", Label: "5-Hour Limit", Percent: 84},
				},
			},
		},
	}
	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{
		Mode: StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{
			{ProviderID: "anthropic", QuotaKey: "five_hour"},
		},
	}

	if got := TrayTitle(snapshot, settings); got != "84%" {
		t.Fatalf("TrayTitle(multi_provider) = %q, want %q", got, "84%")
	}
}

func TestTrayTitleCriticalCountAndIconOnly(t *testing.T) {
	t.Parallel()
	snapshot := &Snapshot{
		Aggregate: Aggregate{
			ProviderCount:  2,
			HighestPercent: 84,
			WarningCount:   1,
			CriticalCount:  1,
		},
		Providers: []ProviderCard{
			{ID: "anthropic", Label: "Anthropic", HighestPercent: 84, Quotas: []QuotaMeter{{Percent: 84}, {Percent: 45}}},
			{ID: "copilot", Label: "Copilot", HighestPercent: 12, Quotas: []QuotaMeter{{Percent: 12}}},
		},
	}

	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{Mode: StatusDisplayCriticalCount}
	if got := TrayTitle(snapshot, settings); got != "2 ⚠" {
		t.Fatalf("TrayTitle(critical_count) = %q, want %q", got, "2 ⚠")
	}

	settings.StatusDisplay = StatusDisplay{
		Mode: StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{
			{ProviderID: "anthropic"},
			{ProviderID: "copilot"},
		},
	}
	if got := TrayTitle(snapshot, settings); got != "84%·12%" {
		t.Fatalf("TrayTitle(multi_provider multiple) = %q, want %q", got, "84%·12%")
	}

	settings.StatusDisplay = StatusDisplay{Mode: StatusDisplayIconOnly}
	if got := TrayTitle(snapshot, settings); got != "" {
		t.Fatalf("TrayTitle(icon_only) = %q, want empty string", got)
	}
}

// TestTrayTitleThreeQuotasFitInMenubar guards issue #67: the user
// selected 3 quotas, but only 2 rendered on the macOS menubar because
// the prior "·"-with-spaces format combined with a leading thin space
// pushed the title over the menubar's available width. The compact
// middle-dot join keeps three quotas under 12 runes so they fit on
// even crowded menubars (notched MacBooks with many status items).
func TestTrayTitleThreeQuotasFitInMenubar(t *testing.T) {
	t.Parallel()
	snapshot := &Snapshot{
		Aggregate: Aggregate{ProviderCount: 3, HighestPercent: 99},
		Providers: []ProviderCard{
			{
				ID:             "anthropic",
				HighestPercent: 17,
				Quotas:         []QuotaMeter{{Key: "five_hour", Percent: 17}},
			},
			{
				ID:             "copilot",
				HighestPercent: 15,
				Quotas:         []QuotaMeter{{Key: "premium", Percent: 15}},
			},
			{
				ID:             "codex",
				HighestPercent: 14,
				Quotas:         []QuotaMeter{{Key: "five_hour", Percent: 14}},
			},
		},
	}

	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{
		Mode: StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{
			{ProviderID: "anthropic", QuotaKey: "five_hour"},
			{ProviderID: "copilot", QuotaKey: "premium"},
			{ProviderID: "codex", QuotaKey: "five_hour"},
		},
	}

	got := TrayTitle(snapshot, settings)
	want := "17%·15%·14%"
	if got != want {
		t.Fatalf("TrayTitle(3 quotas) = %q, want %q", got, want)
	}

	// No leading whitespace: any U+2009 / regular space at index 0 reproduces
	// the macOS title-clipping seen in #67.
	if strings.HasPrefix(got, "\u2009") || strings.HasPrefix(got, " ") {
		t.Fatalf("tray title must not have leading whitespace, got %q", got)
	}

	// Width budget guard: 3 quotas at worst case (100%·100%·100%) is 14 runes,
	// well within the ~16-rune budget we observed on the user's machine.
	worst := []string{"100%", "100%", "100%"}
	worstTitle := joinTrayParts(worst)
	if utf8.RuneCountInString(worstTitle) > 16 {
		t.Fatalf("worst-case 3-quota title %q is %d runes, exceeds menubar budget", worstTitle, utf8.RuneCountInString(worstTitle))
	}
}

// TestTrayTitleSingleQuotaIsBare guards the format choice: a single
// quota should render with no surrounding decoration so the icon sits
// flush against the percentage.
func TestTrayTitleSingleQuotaIsBare(t *testing.T) {
	t.Parallel()
	got := joinTrayParts([]string{"42%"})
	if got != "42%" {
		t.Fatalf("joinTrayParts single = %q, want %q", got, "42%")
	}
}

// TestTrayTitleEmptyParts confirms we return empty for empty input.
func TestTrayTitleEmptyParts(t *testing.T) {
	t.Parallel()
	if got := joinTrayParts(nil); got != "" {
		t.Fatalf("joinTrayParts(nil) = %q, want empty", got)
	}
	if got := joinTrayParts([]string{}); got != "" {
		t.Fatalf("joinTrayParts([]) = %q, want empty", got)
	}
}
