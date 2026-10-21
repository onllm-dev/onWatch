package menubar

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// traySnapshotWithNumbers is a snapshot with two providers the custom
// selections below can point at.
func traySnapshotWithNumbers() *Snapshot {
	return &Snapshot{
		LatestAt: time.Unix(1_700_000_000, 0),
		Aggregate: Aggregate{
			ProviderCount: 2,
			Status:        "healthy",
		},
		Providers: []ProviderCard{
			{ID: "anthropic", BaseProvider: "anthropic", Label: "Claude", Status: "healthy", HighestPercent: 10},
			{ID: "zai", BaseProvider: "zai", Label: "GLM", Status: "healthy", HighestPercent: 20},
		},
	}
}

// customTraySettings selects two quotas for the tray title, so a regression to
// DefaultSettings (multi_provider with no selection) is visible as an empty
// title.
func customTraySettings() *Settings {
	settings := DefaultSettings()
	settings.StatusDisplay = StatusDisplay{
		Mode: StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{
			{ProviderID: "anthropic"},
			{ProviderID: "zai"},
		},
	}
	return settings
}

// Finding 1 regression: after a successful refresh with custom tray
// selections, a refresh where both the summary and the preferences fail must
// keep the title and badge text unchanged, change only the status/icon tint,
// and switch the tooltip to the stale age. Before the fix the controller fell
// back to DefaultSettings (no selection), which emptied the title and badge.
func TestPlanTrayRefreshKeepsTitleWhenPreferencesFail(t *testing.T) {
	t.Parallel()
	snapshot := traySnapshotWithNumbers()
	settings := customTraySettings()

	// First refresh: summary and preferences both succeed.
	first := planTrayRefresh(snapshot, false, 0, settings, nil, trayVisual{}, 0)
	if first.Visual.Title != "10%·20%" {
		t.Fatalf("first title = %q, want %q", first.Visual.Title, "10%·20%")
	}
	if first.Visual.Text != "10" {
		t.Fatalf("first badge text = %q, want %q", first.Visual.Text, "10")
	}
	if !first.FreshSettings || first.Settings != settings {
		t.Fatalf("expected the fresh settings to be adopted, got fresh=%v", first.FreshSettings)
	}

	// Second refresh: summary succeeds but preferences fail (freshSettings nil).
	stale := planTrayRefresh(snapshot, true, 3*time.Minute, nil, settings, first.Visual, 0)
	if stale.Visual.Title != first.Visual.Title {
		t.Fatalf("stale title = %q, want unchanged %q", stale.Visual.Title, first.Visual.Title)
	}
	if stale.Visual.Text != first.Visual.Text {
		t.Fatalf("stale badge text = %q, want unchanged %q", stale.Visual.Text, first.Visual.Text)
	}
	if stale.Visual.IconOnly != first.Visual.IconOnly {
		t.Fatalf("stale IconOnly = %v, want unchanged %v", stale.Visual.IconOnly, first.Visual.IconOnly)
	}
	if stale.Visual.Status != trayStatusOffline {
		t.Fatalf("stale status = %q, want %q", stale.Visual.Status, trayStatusOffline)
	}
	if stale.FreshSettings {
		t.Fatal("a failed preferences fetch must not be adopted as fresh settings")
	}
	if !strings.Contains(stale.Tooltip, "last updated 3m ago") {
		t.Fatalf("stale tooltip = %q, want a last-updated age", stale.Tooltip)
	}
	if stale.Visual.Title == "" {
		t.Fatal("stale title must never be empty while numbers are cached")
	}
}

// The last-good visual is the final fallback: if preferences have never been
// fetched successfully and the current fetch fails, a stale refresh still keeps
// the previously applied title/badge rather than blanking the tray.
func TestPlanTrayRefreshFallsBackToLastVisual(t *testing.T) {
	t.Parallel()
	snapshot := traySnapshotWithNumbers()
	last := trayVisual{Title: "10%·20%", Text: "10", Status: "healthy", Online: true}

	stale := planTrayRefresh(snapshot, true, time.Minute, nil, nil, last, 0)
	if stale.Visual.Title != last.Title || stale.Visual.Text != last.Text {
		t.Fatalf("stale visual = %+v, want the last-good title/text %+v", stale.Visual, last)
	}
	if stale.Visual.Status != trayStatusOffline {
		t.Fatalf("stale status = %q, want %q", stale.Visual.Status, trayStatusOffline)
	}
}

// A successful refresh still adopts fresh preferences, so the cache cannot pin
// the tray to an old selection once the daemon is reachable again.
func TestPlanTrayRefreshAdoptsFreshSettings(t *testing.T) {
	t.Parallel()
	snapshot := traySnapshotWithNumbers()
	old := customTraySettings()
	fresh := DefaultSettings()
	fresh.StatusDisplay = StatusDisplay{
		Mode:           StatusDisplayMultiProvider,
		SelectedQuotas: []StatusDisplaySelection{{ProviderID: "zai"}},
	}

	plan := planTrayRefresh(snapshot, false, 0, fresh, old, trayVisual{Title: "10%·20%", Text: "10", Online: true}, 0)
	if plan.Visual.Title != "20%" {
		t.Fatalf("title = %q, want %q (fresh settings must win)", plan.Visual.Title, "20%")
	}
	if !plan.FreshSettings || plan.Settings != fresh {
		t.Fatal("fresh settings must be adopted on a successful refresh")
	}
}

// Before any snapshot has ever succeeded the tray is allowed to show the
// offline placeholder; that is the only case that blanks the numbers.
func TestPlanTrayRefreshOfflineBeforeAnySuccess(t *testing.T) {
	t.Parallel()
	plan := planTrayRefresh(nil, false, 0, nil, nil, trayVisual{}, 0)
	if !plan.Offline {
		t.Fatal("expected Offline=true before any success")
	}
	if plan.Visual.Title != "--" || plan.Visual.Text != "-" {
		t.Fatalf("offline visual = %+v, want --/- placeholder", plan.Visual)
	}
	if !strings.Contains(plan.Tooltip, "daemon") {
		t.Fatalf("offline tooltip = %q, want an unreachable hint", plan.Tooltip)
	}
}

// A stale plan must never surface a fetch error as a success: the stale flag
// stays set so the caller logs a warning rather than an error.
func TestPlanTrayRefreshStaleFlagSurvives(t *testing.T) {
	t.Parallel()
	snapshot := traySnapshotWithNumbers()
	plan := planTrayRefresh(snapshot, true, time.Minute, customTraySettings(), nil, trayVisual{}, 0)
	if !plan.Stale {
		t.Fatal("expected Stale=true")
	}
	// Sanity: the pure helper feeding the plan still reports the error path.
	if _, stale := nextTraySnapshot(snapshot, nil, errors.New("boom")); !stale {
		t.Fatal("nextTraySnapshot must report stale on a failed fetch")
	}
}
