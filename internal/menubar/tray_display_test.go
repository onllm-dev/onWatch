package menubar

import "testing"

func TestTrayTitleDefaultIsEmptyUntilProviderSelectionIsResolved(t *testing.T) {
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
	if got := TrayTitle(snapshot, settings); got != "84% │ 12%" {
		t.Fatalf("TrayTitle(multi_provider multiple) = %q, want %q", got, "84% │ 12%")
	}

	settings.StatusDisplay = StatusDisplay{Mode: StatusDisplayIconOnly}
	if got := TrayTitle(snapshot, settings); got != "" {
		t.Fatalf("TrayTitle(icon_only) = %q, want empty string", got)
	}
}
