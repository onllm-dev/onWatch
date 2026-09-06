package menubar

import (
	"fmt"
	"math"
	"strings"
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
	var text string
	if len(snapshot.Providers) == 0 {
		text = "onWatch: no provider data"
	} else {
		parts := make([]string, 0, len(snapshot.Providers))
		for _, provider := range snapshot.Providers {
			label := strings.TrimSpace(provider.Label)
			if label == "" {
				label = provider.ID
			}
			parts = append(parts, fmt.Sprintf("%s %d%%", label, int(math.Round(provider.HighestPercent))))
		}
		text = strings.Join(parts, " · ")
	}
	if updated := strings.TrimSpace(snapshot.UpdatedAgo); updated != "" {
		text += "\nupdated " + updated
	}
	if maxRunes > 0 {
		runes := []rune(text)
		if len(runes) > maxRunes {
			text = string(runes[:maxRunes-3]) + "..."
		}
	}
	return text
}
