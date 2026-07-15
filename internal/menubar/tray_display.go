package menubar

import (
	"fmt"
	"math"
	"strings"
)

// TraySegment is one compact metric slot for tray UIs that can show an icon
// next to the percentage (e.g. GNOME top bar).
type TraySegment struct {
	ProviderID   string  `json:"provider_id"`
	BaseProvider string  `json:"base_provider"`
	Label        string  `json:"label"`
	Text         string  `json:"text"` // e.g. "42%" or "2 ⚠"
	Icon         string  `json:"icon"` // icon stem: anthropic, zai, openai, ...
	Percent      float64 `json:"percent,omitempty"`
}

// TrayTitle formats the compact metric shown next to the macOS tray icon.
func TrayTitle(snapshot *Snapshot, settings *Settings) string {
	segments := TraySegments(snapshot, settings)
	if len(segments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg.Text != "" {
			parts = append(parts, seg.Text)
		}
	}
	return joinTrayParts(parts)
}

// TraySegments returns per-provider tray slots using the same selection rules
// as TrayTitle, plus icon stems for rich tray UIs.
func TraySegments(snapshot *Snapshot, settings *Settings) []TraySegment {
	if snapshot == nil {
		return nil
	}
	normalized := DefaultSettings()
	if settings != nil {
		normalized = settings.Normalize()
	}
	switch normalized.StatusDisplay.Mode {
	case StatusDisplayIconOnly:
		return nil
	case StatusDisplayCriticalCount:
		count := snapshot.Aggregate.WarningCount + snapshot.Aggregate.CriticalCount
		return []TraySegment{{
			ProviderID:   "aggregate",
			BaseProvider: "all",
			Label:        "Alerts",
			Text:         fmt.Sprintf("%d ⚠", count),
			Icon:         "all",
		}}
	case StatusDisplayMultiProvider:
		return multiProviderSegments(snapshot, normalized.StatusDisplay)
	default:
		return nil
	}
}

func multiProviderMetrics(snapshot *Snapshot, display StatusDisplay) []string {
	segments := multiProviderSegments(snapshot, display)
	if len(segments) == 0 {
		return nil
	}
	parts := make([]string, 0, len(segments))
	for _, seg := range segments {
		parts = append(parts, seg.Text)
	}
	return parts
}

func multiProviderSegments(snapshot *Snapshot, display StatusDisplay) []TraySegment {
	if snapshot == nil || len(display.SelectedQuotas) == 0 {
		return nil
	}
	out := make([]TraySegment, 0, len(display.SelectedQuotas))
	for _, selection := range display.SelectedQuotas {
		provider, ok := providerByID(snapshot, selection.ProviderID)
		if !ok {
			continue
		}
		percent := provider.HighestPercent
		if selection.QuotaKey != "" {
			for _, quota := range provider.Quotas {
				if quota.Key == selection.QuotaKey {
					percent = quota.Percent
					break
				}
			}
		}
		base := provider.BaseProvider
		if base == "" {
			base = provider.ID
		}
		out = append(out, TraySegment{
			ProviderID:   provider.ID,
			BaseProvider: base,
			Label:        provider.Label,
			Text:         fmt.Sprintf("%d%%", int(math.Round(percent))),
			Icon:         trayIconStem(base, provider.ID),
			Percent:      percent,
		})
	}
	return out
}

// trayIconStem maps provider ids to files under /static/icons/{stem}.svg.
func trayIconStem(baseProvider, providerID string) string {
	id := strings.ToLower(strings.TrimSpace(baseProvider))
	if id == "" {
		id = strings.ToLower(strings.TrimSpace(providerID))
	}
	// Strip profile suffixes like "codex:work".
	if i := strings.IndexByte(id, ':'); i > 0 {
		id = id[:i]
	}
	switch id {
	case "claude", "anthropic":
		return "anthropic"
	case "openai", "codex", "chatgpt":
		return "openai"
	case "glm", "zhipu", "zai":
		return "zai"
	case "google", "gemini":
		return "gemini"
	case "github", "copilot":
		return "copilot"
	case "xai", "grok":
		return "grok"
	case "moonshot", "kimi":
		return "kimi"
	case "both", "all":
		return "all"
	case "api_integrations", "api-integrations":
		return "api-integrations"
	default:
		if id == "" {
			return "all"
		}
		return id
	}
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
