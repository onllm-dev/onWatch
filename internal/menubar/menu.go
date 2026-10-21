package menubar

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// MenuLine is one per-provider row shown in the tray context menu.
type MenuLine struct {
	ProviderID string
	Text       string
	Status     string
}

// BuildMenuLines summarises each provider for the tray menu: label, highest
// percent, and the reset countdown of the quota driving that percent.
func BuildMenuLines(snapshot *Snapshot) []MenuLine {
	if snapshot == nil {
		return nil
	}
	lines := make([]MenuLine, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		label := strings.TrimSpace(provider.Label)
		if label == "" {
			label = provider.ID
		}
		if sub := strings.TrimSpace(provider.Subtitle); sub != "" {
			label = fmt.Sprintf("%s (%s)", label, sub)
		}
		text := fmt.Sprintf("%s  %d%%", label, int(math.Round(provider.HighestPercent)))
		if reset := drivingQuotaReset(provider); reset != "" {
			text += "  resets in " + reset
		}
		lines = append(lines, MenuLine{ProviderID: provider.ID, Text: text, Status: provider.Status})
	}
	return lines
}

func drivingQuotaReset(provider ProviderCard) string {
	best := -1.0
	reset := ""
	for _, quota := range provider.Quotas {
		if quota.Percent > best {
			best = quota.Percent
			reset = strings.TrimSpace(quota.TimeUntilReset)
		}
	}
	return reset
}

// TrayTooltipText renders the hover text: one "Label NN%" pair per provider
// joined with middle dots, then the freshness. maxRunes > 0 truncates with an
// ellipsis, which Windows needs because NOTIFYICONDATA caps tips at 127.
func TrayTooltipText(snapshot *Snapshot, maxRunes int) string {
	if snapshot == nil {
		return "onWatch: daemon unreachable"
	}
	text := trayProviderLines(snapshot)
	if updated := strings.TrimSpace(snapshot.UpdatedAgo); updated != "" {
		text += "\nupdated " + updated
	}
	return truncateTooltip(text, maxRunes)
}

// TrayTooltipStaleText renders the hover text when the last refresh failed and
// the tray is showing a cached snapshot. The provider lines are identical; only
// the freshness line differs, and it is computed from age (client wall-clock
// time since the last successful fetch) so it keeps counting up while the
// daemon is unreachable. The snapshot's own UpdatedAgo is frozen at build time
// and would stall on the stale value.
func TrayTooltipStaleText(snapshot *Snapshot, maxRunes int, age time.Duration) string {
	if snapshot == nil {
		return "onWatch: daemon unreachable"
	}
	return truncateTooltip(trayProviderLines(snapshot)+"\nlast updated "+formatAgeAgo(age), maxRunes)
}

// trayProviderLines is the shared "Label NN%" body of both tooltips.
func trayProviderLines(snapshot *Snapshot) string {
	if len(snapshot.Providers) == 0 {
		return "onWatch: no provider data"
	}
	parts := make([]string, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		label := strings.TrimSpace(provider.Label)
		if label == "" {
			label = provider.ID
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", label, int(math.Round(provider.HighestPercent))))
	}
	return strings.Join(parts, " · ")
}

func truncateTooltip(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes-3]) + "..."
}

// formatAgeAgo renders a duration as a compact age string. It is computed from
// exact timestamps so it grows smoothly on its scale (no integer-day step
// jumps): "just now", "2m ago", "1h ago", "2d ago".
func formatAgeAgo(age time.Duration) string {
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(age.Hours()/24))
}
