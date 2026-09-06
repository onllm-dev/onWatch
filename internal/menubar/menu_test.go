package menubar

import (
	"strings"
	"testing"
)

func sampleSnapshot() *Snapshot {
	return &Snapshot{
		UpdatedAgo: "12s ago",
		Aggregate:  Aggregate{ProviderCount: 2, WarningCount: 1, Label: "1 Warning", Status: "warning"},
		Providers: []ProviderCard{
			{ID: "anthropic", Label: "Claude", Status: "warning", HighestPercent: 82, Quotas: []QuotaMeter{
				{Key: "five_hour", Label: "5h", Percent: 82, Status: "warning", TimeUntilReset: "2h 14m"},
				{Key: "seven_day", Label: "Weekly", Percent: 40, Status: "healthy", TimeUntilReset: "3d 4h"},
			}},
			{ID: "codex", Label: "Codex", Subtitle: "work", Status: "healthy", HighestPercent: 18, Quotas: []QuotaMeter{
				{Key: "primary", Label: "5h", Percent: 18, Status: "healthy"},
			}},
		},
	}
}

func TestBuildMenuLinesOnePerProvider(t *testing.T) {
	t.Parallel()
	lines := BuildMenuLines(sampleSnapshot())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].ProviderID != "anthropic" || lines[0].Text != "Claude  82%  resets in 2h 14m" {
		t.Fatalf("unexpected first line %#v", lines[0])
	}
	if lines[1].Text != "Codex (work)  18%" {
		t.Fatalf("unexpected second line %#v", lines[1])
	}
	if lines[0].Status != "warning" {
		t.Fatalf("expected status carried, got %q", lines[0].Status)
	}
}

func TestBuildMenuLinesNilSnapshot(t *testing.T) {
	t.Parallel()
	if lines := BuildMenuLines(nil); len(lines) != 0 {
		t.Fatalf("expected no lines, got %d", len(lines))
	}
}

func TestTrayTooltipTextListsProviders(t *testing.T) {
	t.Parallel()
	got := TrayTooltipText(sampleSnapshot(), 0)
	for _, want := range []string{"Claude 82%", "Codex 18%", "updated 12s ago"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in tooltip %q", want, got)
		}
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("tooltip should not contain blank lines: %q", got)
	}
}

func TestTrayTooltipTextRespectsLimit(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	for i := 0; i < 20; i++ {
		snapshot.Providers = append(snapshot.Providers, ProviderCard{ID: "p", Label: "Provider With A Long Name", HighestPercent: 50})
	}
	got := TrayTooltipText(snapshot, 127)
	if len([]rune(got)) > 127 {
		t.Fatalf("tooltip exceeds limit: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestTrayTooltipTextOffline(t *testing.T) {
	t.Parallel()
	got := TrayTooltipText(nil, 0)
	if !strings.Contains(strings.ToLower(got), "daemon") {
		t.Fatalf("expected offline hint, got %q", got)
	}
}
