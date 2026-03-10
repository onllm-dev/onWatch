package menubar

import (
	"fmt"
	"math"
)

// TrayTitle formats the compact metric shown next to the macOS tray icon.
func TrayTitle(snapshot *Snapshot, settings *Settings) string {
	if snapshot == nil {
		return ""
	}
	normalized := DefaultSettings()
	if settings != nil {
		normalized = settings.Normalize()
	}
	switch normalized.StatusDisplay.Mode {
	case StatusDisplayIconOnly:
		return ""
	case StatusDisplayCriticalCount:
		count := snapshot.Aggregate.WarningCount + snapshot.Aggregate.CriticalCount
		return fmt.Sprintf("%d ⚠", count)
	case StatusDisplayMultiProvider:
		parts := multiProviderMetrics(snapshot, normalized.StatusDisplay)
		if len(parts) == 0 {
			return ""
		}
		return joinTrayParts(parts)
	default:
		return ""
	}
}

func multiProviderMetrics(snapshot *Snapshot, display StatusDisplay) []string {
	if snapshot == nil || len(display.SelectedQuotas) == 0 {
		return nil
	}
	parts := make([]string, 0, len(display.SelectedQuotas))
	for _, selection := range display.SelectedQuotas {
		provider, ok := providerByID(snapshot, selection.ProviderID)
		if !ok {
			continue
		}
		if selection.QuotaKey != "" {
			matched := false
			for _, quota := range provider.Quotas {
				if quota.Key == selection.QuotaKey {
					parts = append(parts, fmt.Sprintf("%d%%", int(math.Round(quota.Percent))))
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		parts = append(parts, fmt.Sprintf("%d%%", int(math.Round(provider.HighestPercent))))
	}
	return parts
}

func providerByID(snapshot *Snapshot, providerID string) (ProviderCard, bool) {
	if snapshot == nil || providerID == "" {
		return ProviderCard{}, false
	}
	for _, provider := range snapshot.Providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return ProviderCard{}, false
}

func joinTrayParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += " │ " + parts[i]
	}
	return out
}
