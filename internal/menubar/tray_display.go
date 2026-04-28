package menubar

import (
	"fmt"
	"math"
	"strings"
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

// joinTrayParts assembles the metrics shown next to the macOS tray icon.
// Width budget on the macOS menubar is tight, so the join uses the narrowest
// readable separator that survives crowded menubars (notch, many status items).
// A single quota uses no separator. Two or more quotas use a middle dot
// without surrounding spaces, keeping a 3-quota title under 12 characters.
func joinTrayParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, "·")
}
