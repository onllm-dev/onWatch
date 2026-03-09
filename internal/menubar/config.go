package menubar

import "time"

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
	Enabled         bool     `json:"enabled"`
	DefaultView     ViewType `json:"default_view"`
	RefreshSeconds  int      `json:"refresh_seconds"`
	ProvidersOrder  []string `json:"providers_order"`
	WarningPercent  int      `json:"warning_percent"`
	CriticalPercent int      `json:"critical_percent"`
}

// ViewType controls which preset layout is rendered.
type ViewType string

const (
	ViewMinimal  ViewType = "minimal"
	ViewStandard ViewType = "standard"
	ViewDetailed ViewType = "detailed"
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
		Enabled:         true,
		DefaultView:     ViewStandard,
		RefreshSeconds:  60,
		ProvidersOrder:  []string{},
		WarningPercent:  70,
		CriticalPercent: 90,
	}
}

// Normalize fills invalid or missing settings with safe defaults.
func (s *Settings) Normalize() *Settings {
	defaults := DefaultSettings()
	if s == nil {
		return defaults
	}
	out := *s
	if out.DefaultView == "" {
		out.DefaultView = defaults.DefaultView
	}
	if out.RefreshSeconds < 10 {
		out.RefreshSeconds = defaults.RefreshSeconds
	}
	if out.WarningPercent < 1 || out.WarningPercent > 99 {
		out.WarningPercent = defaults.WarningPercent
	}
	if out.CriticalPercent < 1 || out.CriticalPercent > 100 {
		out.CriticalPercent = defaults.CriticalPercent
	}
	if out.WarningPercent >= out.CriticalPercent {
		out.WarningPercent = defaults.WarningPercent
		out.CriticalPercent = defaults.CriticalPercent
	}
	if out.ProvidersOrder == nil {
		out.ProvidersOrder = []string{}
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
