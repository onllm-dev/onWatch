package api

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// AnthropicQuotaEntry represents a single quota entry from the Anthropic API.
// All fields are pointers because null values indicate the quota is not applicable.
type AnthropicQuotaEntry struct {
	Utilization  *float64 `json:"utilization"`
	ResetsAt     *string  `json:"resets_at"`
	IsEnabled    *bool    `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit,omitempty"`
	UsedCredits  *float64 `json:"used_credits,omitempty"`
}

// AnthropicQuotaResponse is the full response from the Anthropic usage API.
// Keys are dynamic (five_hour, seven_day, etc.).
//
// Anthropic also returns non-quota companion fields at the top level (e.g.
// "limits" as an array, "spend" as an unrelated object, booleans). Custom
// UnmarshalJSON keeps only null/object values that look like quota entries so
// those metadata fields do not fail the whole decode (see #82 / #84).
type AnthropicQuotaResponse map[string]*AnthropicQuotaEntry

// UnmarshalJSON accepts the quota-object values used by the Claude usage API
// while ignoring top-level metadata fields such as "limits" (array) and
// "spend" (non-quota object), which Anthropic began returning in 2026.
//
// Compared with a minimal "skip non-objects" decoder, this also:
//   - drops objects that share no AnthropicQuotaEntry fields (e.g. spend)
//   - skips a single bad key instead of failing the entire response, so future
//     companion objects cannot take down Anthropic polling again
func (r *AnthropicQuotaResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	resp := make(AnthropicQuotaResponse, len(raw))
	for key, value := range raw {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			resp[key] = nil
			continue
		}
		// Quota entries are JSON objects. Skip arrays/scalars (limits[],
		// member_dashboard_available, …).
		if trimmed[0] != '{' {
			continue
		}
		// Drop companion objects that do not look like quota buckets (spend, …).
		if !looksLikeAnthropicQuotaObject(trimmed) {
			continue
		}

		var entry AnthropicQuotaEntry
		if err := json.Unmarshal(trimmed, &entry); err != nil {
			// Forward-compat: one unreadable companion/experimental object must
			// not discard five_hour / seven_day and the rest of the payload.
			continue
		}
		e := entry
		resp[key] = &e
	}

	*r = resp
	return nil
}

// looksLikeAnthropicQuotaObject reports whether a JSON object has at least one
// field belonging to AnthropicQuotaEntry.
func looksLikeAnthropicQuotaObject(val json.RawMessage) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(val, &probe); err != nil {
		return false
	}
	for _, field := range []string{
		"utilization",
		"resets_at",
		"is_enabled",
		"monthly_limit",
		"used_credits",
	} {
		if _, ok := probe[field]; ok {
			return true
		}
	}
	return false
}

// AnthropicQuota represents a single normalized quota for storage.
type AnthropicQuota struct {
	Name        string
	Utilization float64
	ResetsAt    *time.Time
}

// AnthropicSnapshot represents a point-in-time capture of all Anthropic quotas.
type AnthropicSnapshot struct {
	ID         int64
	CapturedAt time.Time
	Quotas     []AnthropicQuota
	RawJSON    string
}

// anthropicDisplayNames maps API keys to human-readable labels.
var anthropicDisplayNames = map[string]string{
	"five_hour":        "5-Hour Limit",
	"seven_day":        "Weekly All-Model",
	"seven_day_sonnet": "Weekly Sonnet",
	"monthly_limit":    "Monthly Limit",
	"extra_usage":      "Extra Usage",
}

// AnthropicDisplayName returns the human-readable name for a quota key.
func AnthropicDisplayName(key string) string {
	if name, ok := anthropicDisplayNames[key]; ok {
		return name
	}
	return key
}

// IsKnownAnthropicQuota reports whether the given quota key is in the whitelist.
// Used by both the write path (to filter out experimental keys before storage)
// and read paths (to hide historical rows written before the whitelist existed).
func IsKnownAnthropicQuota(key string) bool {
	_, ok := anthropicDisplayNames[key]
	return ok
}

// ActiveQuotaNames returns sorted names of quotas that are active (non-null utilization,
// and not disabled via is_enabled=false). extra_usage with is_enabled=false is skipped.
// Unknown/experimental quota keys returned by the Anthropic API (e.g.
// seven_day_omelette, omelette_promotional, iguana_necktie, seven_day_cowork,
// seven_day_oauth_apps) are filtered out so they never reach storage or the UI.
// To support a new quota, add it to anthropicDisplayNames above.
func (r AnthropicQuotaResponse) ActiveQuotaNames() []string {
	var names []string
	for key, entry := range r {
		if entry == nil || entry.Utilization == nil {
			continue
		}
		// Skip disabled quotas (e.g., extra_usage with is_enabled=false)
		if entry.IsEnabled != nil && !*entry.IsEnabled {
			continue
		}
		// Whitelist known quota keys; skip experimental/unknown keys.
		if _, ok := anthropicDisplayNames[key]; !ok {
			continue
		}
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

// ToSnapshot converts an AnthropicQuotaResponse to an AnthropicSnapshot.
func (r AnthropicQuotaResponse) ToSnapshot(capturedAt time.Time) *AnthropicSnapshot {
	snapshot := &AnthropicSnapshot{
		CapturedAt: capturedAt,
	}

	for _, name := range r.ActiveQuotaNames() {
		entry := r[name]
		q := AnthropicQuota{
			Name:        name,
			Utilization: *entry.Utilization,
		}
		if entry.ResetsAt != nil && *entry.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, *entry.ResetsAt); err == nil {
				q.ResetsAt = &t
			}
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	// Store raw JSON for debugging/auditing
	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// ParseAnthropicResponse parses raw JSON bytes into an AnthropicQuotaResponse.
func ParseAnthropicResponse(data []byte) (*AnthropicQuotaResponse, error) {
	var resp AnthropicQuotaResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
