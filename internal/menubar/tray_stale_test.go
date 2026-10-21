package menubar

import (
	"image"
	"strings"
	"testing"
	"time"
)

func staleTestSnapshot(now time.Time, cards ...ProviderCard) *Snapshot {
	return &Snapshot{
		GeneratedAt: now,
		UpdatedAgo:  "just now",
		LatestAt:    now,
		Providers:   cards,
	}
}

func staleTestCard(id, label string, captured time.Time, staleAfter int64) ProviderCard {
	return ProviderCard{
		ID:                id,
		BaseProvider:      id,
		Label:             label,
		Status:            "healthy",
		HighestPercent:    40,
		UpdatedAtTime:     captured.UTC().Format(time.RFC3339),
		StaleAfterSeconds: staleAfter,
		Quotas: []QuotaMeter{{
			Key:     "weekly",
			Label:   "Weekly",
			Percent: 40,
			Status:  "healthy",
		}},
	}
}

func TestCardStaleAtUsesServerFlagAndLocalCadence(t *testing.T) {
	now := time.Now().UTC()
	fresh := staleTestCard("cursor", "Cursor", now.Add(-2*time.Minute), 360)
	if cardStaleAt(fresh, now) {
		t.Fatal("a card inside its cadence must not be stale")
	}
	old := staleTestCard("cursor", "Cursor", now.Add(-10*time.Minute), 360)
	if !cardStaleAt(old, now) {
		t.Fatal("a card past its cadence must be stale")
	}
	// The server flag wins even when the local clock disagrees (a cached
	// response whose data was already stale when built).
	flagged := staleTestCard("cursor", "Cursor", now, 0)
	flagged.Stale = true
	if !cardStaleAt(flagged, now) {
		t.Fatal("the server stale flag must be authoritative")
	}
	// A manual (never-stale) card has StaleAfterSeconds 0.
	manual := staleTestCard("cursor", "Cursor", now.Add(-720*time.Hour), 0)
	if cardStaleAt(manual, now) {
		t.Fatal("a card without a cadence must not age out locally")
	}
}

func TestTrayTitleDimmed(t *testing.T) {
	now := time.Now().UTC()
	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{
		Mode: StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{
			{ProviderID: "anthropic"},
		},
	}
	// Only Anthropic is in the title selection; a stale Cursor outside the
	// selection must not dim the title.
	hidden := staleTestCard("cursor", "Cursor", now.Add(-time.Hour), 360)
	shownFresh := staleTestCard("anthropic", "Anthropic", now.Add(-time.Minute), 360)
	if TrayTitleDimmed(staleTestSnapshot(now, hidden, shownFresh), settings, now) {
		t.Fatal("a stale provider absent from the title must not dim it")
	}
	shownStale := staleTestCard("anthropic", "Anthropic", now.Add(-time.Hour), 360)
	shownStale.StaleReason = "network error"
	if !TrayTitleDimmed(staleTestSnapshot(now, hidden, shownStale), settings, now) {
		t.Fatal("a stale provider in the title must dim it")
	}

	// Icon-only mode renders the aggregate badge: dim when anything is stale.
	iconOnly := DefaultSettings()
	iconOnly.StatusDisplay = StatusDisplay{Mode: StatusDisplayIconOnly}
	if !TrayTitleDimmed(staleTestSnapshot(now, hidden, shownFresh), iconOnly, now) {
		t.Fatal("icon-only mode must dim when any provider is stale")
	}
	if TrayTitleDimmed(staleTestSnapshot(now, shownFresh), iconOnly, now) {
		t.Fatal("icon-only mode must not dim when everything is fresh")
	}
}

func TestTrayTooltipListsStaleProviders(t *testing.T) {
	now := time.Now().UTC()
	stale := staleTestCard("anthropic", "Anthropic", now.Add(-14*time.Minute), 360)
	stale.StaleReason = "network error"
	fresh := staleTestCard("cursor", "Cursor", now.Add(-time.Minute), 360)
	snapshot := staleTestSnapshot(now, stale, fresh)

	lines := StaleProviderTooltipLines(snapshot, now)
	if len(lines) != 1 || lines[0] != "Anthropic: 14m ago (network error)" {
		t.Fatalf("stale lines = %#v, want one Anthropic line with its reason", lines)
	}

	text := TrayTooltipWithStale(snapshot, now, 0)
	if !strings.Contains(text, "Anthropic: 14m ago (network error)") {
		t.Fatalf("tooltip = %q, want the stale line", text)
	}
	if !strings.Contains(text, "Cursor 40%") {
		t.Fatalf("tooltip = %q, want the normal provider line too", text)
	}

	// The generic case carries no empty parentheses.
	generic := staleTestCard("cursor", "Cursor", now.Add(-2*time.Hour), 360)
	genericLines := StaleProviderTooltipLines(staleTestSnapshot(now, generic), now)
	if len(genericLines) != 1 || strings.Contains(genericLines[0], "()") {
		t.Fatalf("generic stale line = %#v, want no empty reason", genericLines)
	}
}

func TestPlanTrayRefreshDimsArtifacts(t *testing.T) {
	now := time.Now().UTC()
	stale := staleTestCard("cursor", "Cursor", now.Add(-time.Hour), 360)
	stale.StaleReason = "auth"
	snapshot := staleTestSnapshot(now, stale)
	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{
		Mode:           StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{{ProviderID: "cursor"}},
	}
	plan := planTrayRefreshAt(snapshot, false, 0, settings, nil, trayVisual{}, 0, now)
	if !plan.Visual.Dim {
		t.Fatal("plan visual must dim when the selected provider is stale")
	}
	if !strings.Contains(plan.Tooltip, "Cursor: 1h ago (auth)") {
		t.Fatalf("plan tooltip = %q, want the stale provider line", plan.Tooltip)
	}

	// The icon mode dims the rendered badge, not just the (invisible) title.
	img := trayIconImage(plan.Visual, 32)
	if img == nil {
		t.Fatal("dimmed icon must render")
	}
	bright := RenderTrayBadge(32, plan.Visual.Text, plan.Visual.Status)
	if alphaAt(img, 16, 16) >= alphaAt(bright, 16, 16) {
		t.Fatal("the dimmed badge must be more transparent than the normal one")
	}
}

func alphaAt(img *image.RGBA, x, y int) uint8 {
	return img.RGBAAt(x, y).A
}
