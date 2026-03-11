package menubar

import (
	"strings"
	"time"
)

// SnapshotProvider returns the latest menubar snapshot.
type SnapshotProvider func() (*Snapshot, error)

// Config holds runtime configuration for the menubar companion.
type Config struct {
	Port             int
	Enabled          bool
	DefaultView      ViewType
	RefreshSeconds   int
	ProvidersOrder   []string
	WarningPercent   int
	CriticalPercent  int
	BinaryPath       string
	TestMode         bool
	SnapshotProvider SnapshotProvider
}

// Settings holds persisted menubar preferences.
type Settings struct {
	Enabled          bool          `json:"enabled"`
	DefaultView      ViewType      `json:"default_view"`
	RefreshSeconds   int           `json:"refresh_seconds"`
	ProvidersOrder   []string      `json:"providers_order"`
	VisibleProviders []string      `json:"visible_providers"`
	WarningPercent   int           `json:"warning_percent"`
	CriticalPercent  int           `json:"critical_percent"`
	StatusDisplay    StatusDisplay `json:"status_display"`
	Theme            ThemeMode     `json:"theme"`
}

// StatusDisplayMode controls which compact metric is rendered beside the tray icon.
type StatusDisplayMode string

const (
	StatusDisplayMultiProvider          StatusDisplayMode = "multi_provider"
	StatusDisplayCriticalCount          StatusDisplayMode = "critical_count"
	StatusDisplayIconOnly               StatusDisplayMode = "icon_only"
	statusDisplayProviderSpecificLegacy StatusDisplayMode = "provider_specific"
)

// StatusDisplaySelection identifies one provider quota to surface in the tray title.
type StatusDisplaySelection struct {
	ProviderID string `json:"provider_id"`
	QuotaKey   string `json:"quota_key,omitempty"`
}

// StatusDisplay stores tray-title preferences shared by the popover and native companion.
type StatusDisplay struct {
	Mode           StatusDisplayMode        `json:"mode"`
	SelectedQuotas []StatusDisplaySelection `json:"selected_quotas,omitempty"`
	ProviderID     string                   `json:"provider_id,omitempty"`
	QuotaKey       string                   `json:"quota_key,omitempty"`
}

// ViewType controls which preset layout is rendered.
type ViewType string

const (
	ViewMinimal  ViewType = "minimal"
	ViewStandard ViewType = "standard"
	ViewDetailed ViewType = "detailed"
)

// ThemeMode controls visual theme behavior for menubar UI.
type ThemeMode string

const (
	ThemeSystem ThemeMode = "system"
	ThemeLight  ThemeMode = "light"
	ThemeDark   ThemeMode = "dark"
)

// Snapshot is the normalized UI contract shared by the desktop app and the
// browser-testable menubar page.
type Snapshot struct {
	GeneratedAt time.Time      `json:"generated_at"`
	UpdatedAgo  string         `json:"updated_ago"`
	Aggregate   Aggregate      `json:"aggregate"`
	Providers   []ProviderCard `json:"providers"`
}

// Aggregate summarizes the overall health across all visible providers.
type Aggregate struct {
	ProviderCount  int     `json:"provider_count"`
	WarningCount   int     `json:"warning_count"`
	CriticalCount  int     `json:"critical_count"`
	HighestPercent float64 `json:"highest_percent"`
	Status         string  `json:"status"`
	Label          string  `json:"label"`
}

// ProviderCard is the top-level card rendered for each provider.
type ProviderCard struct {
	ID             string        `json:"id"`
	BaseProvider   string        `json:"base_provider"`
	Label          string        `json:"label"`
	Subtitle       string        `json:"subtitle,omitempty"`
	Status         string        `json:"status"`
	HighestPercent float64       `json:"highest_percent"`
	UpdatedAt      string        `json:"updated_at,omitempty"`
	Quotas         []QuotaMeter  `json:"quotas"`
	Trends         []TrendSeries `json:"trends,omitempty"`
}

// QuotaMeter represents one circular quota meter inside a provider card.
type QuotaMeter struct {
	Key             string    `json:"key"`
	Label           string    `json:"label"`
	DisplayValue    string    `json:"display_value"`
	Percent         float64   `json:"percent"`
	Status          string    `json:"status"`
	Used            float64   `json:"used,omitempty"`
	Limit           float64   `json:"limit,omitempty"`
	ResetAt         string    `json:"reset_at,omitempty"`
	TimeUntilReset  string    `json:"time_until_reset,omitempty"`
	ProjectedValue  float64   `json:"projected_value,omitempty"`
	CurrentRate     float64   `json:"current_rate,omitempty"`
	SparklinePoints []float64 `json:"sparkline_points,omitempty"`
}

// TrendSeries groups sparkline points for a provider-level detailed view.
type TrendSeries struct {
	Key    string    `json:"key"`
	Label  string    `json:"label"`
	Status string    `json:"status"`
	Points []float64 `json:"points"`
}

// DefaultConfig returns runtime defaults aligned with the existing app.
func DefaultConfig() *Config {
	settings := DefaultSettings()
	return &Config{
		Port:            9211,
		Enabled:         settings.Enabled,
		DefaultView:     settings.DefaultView,
		RefreshSeconds:  settings.RefreshSeconds,
		ProvidersOrder:  append([]string(nil), settings.ProvidersOrder...),
		WarningPercent:  settings.WarningPercent,
		CriticalPercent: settings.CriticalPercent,
	}
}

// DefaultSettings returns persisted defaults for a new install.
func DefaultSettings() *Settings {
	return &Settings{
		Enabled:          true,
		DefaultView:      ViewStandard,
		RefreshSeconds:   60,
		ProvidersOrder:   []string{},
		VisibleProviders: []string{},
		WarningPercent:   70,
		CriticalPercent:  90,
		StatusDisplay: StatusDisplay{
			Mode: StatusDisplayMultiProvider,
		},
		Theme: ThemeSystem,
	}
}

// Normalize fills invalid or missing settings with safe defaults.
func (s *Settings) Normalize() *Settings {
	defaults := DefaultSettings()
	if s == nil {
		return defaults
	}
	out := *s
	switch out.DefaultView {
	case ViewMinimal, ViewStandard, ViewDetailed:
	default:
		out.DefaultView = defaults.DefaultView
	}

	switch {
	case out.RefreshSeconds < 10:
		out.RefreshSeconds = defaults.RefreshSeconds
	}
	switch {
	case out.WarningPercent < 1 || out.WarningPercent > 99:
		out.WarningPercent = defaults.WarningPercent
	}
	switch {
	case out.CriticalPercent < 1 || out.CriticalPercent > 100:
		out.CriticalPercent = defaults.CriticalPercent
	}
	switch {
	case out.WarningPercent >= out.CriticalPercent:
		out.WarningPercent = defaults.WarningPercent
		out.CriticalPercent = defaults.CriticalPercent
	}
	if out.ProvidersOrder == nil {
		out.ProvidersOrder = []string{}
	}
	out.ProvidersOrder = normalizedStringList(out.ProvidersOrder)
	if out.VisibleProviders == nil {
		out.VisibleProviders = []string{}
	}
	out.VisibleProviders = normalizedStringList(out.VisibleProviders)
	out.StatusDisplay = out.StatusDisplay.normalize(defaults.StatusDisplay)
	switch out.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		out.Theme = ThemeSystem
	}
	return &out
}

// ToConfig converts persisted settings into runtime config values.
func (s *Settings) ToConfig(port int, snapshotProvider SnapshotProvider) *Config {
	normalized := s.Normalize()
	cfg := DefaultConfig()
	cfg.Port = port
	cfg.Enabled = normalized.Enabled
	cfg.DefaultView = normalized.DefaultView
	cfg.RefreshSeconds = normalized.RefreshSeconds
	cfg.ProvidersOrder = append([]string(nil), normalized.ProvidersOrder...)
	cfg.WarningPercent = normalized.WarningPercent
	cfg.CriticalPercent = normalized.CriticalPercent
	cfg.SnapshotProvider = snapshotProvider
	return cfg
}

func (s StatusDisplay) normalize(fallback StatusDisplay) StatusDisplay {
	out := StatusDisplay{
		Mode:           fallback.Mode,
		SelectedQuotas: normalizeStatusSelections(s.SelectedQuotas),
	}
	legacyProviderID := strings.TrimSpace(s.ProviderID)
	legacyQuotaKey := strings.TrimSpace(s.QuotaKey)
	switch s.Mode {
	case StatusDisplayMultiProvider,
		StatusDisplayCriticalCount,
		StatusDisplayIconOnly:
		out.Mode = s.Mode
	case statusDisplayProviderSpecificLegacy:
		out.Mode = StatusDisplayMultiProvider
	}
	if len(out.SelectedQuotas) == 0 && legacyProviderID != "" {
		out.SelectedQuotas = []StatusDisplaySelection{{
			ProviderID: legacyProviderID,
			QuotaKey:   legacyQuotaKey,
		}}
	}
	if out.Mode != StatusDisplayMultiProvider {
		out.SelectedQuotas = []StatusDisplaySelection{}
		return out
	}
	return out
}

func normalizeStatusSelections(values []StatusDisplaySelection) []StatusDisplaySelection {
	if len(values) == 0 {
		return []StatusDisplaySelection{}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]StatusDisplaySelection, 0, len(values))
	for _, value := range values {
		providerID := strings.TrimSpace(value.ProviderID)
		if providerID == "" {
			continue
		}
		quotaKey := strings.TrimSpace(value.QuotaKey)
		key := providerID + "\x00" + quotaKey
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, StatusDisplaySelection{
			ProviderID: providerID,
			QuotaKey:   quotaKey,
		})
		if len(out) == 3 {
			break
		}
	}
	return out
}

func normalizedStringList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
