package api

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
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

// AnthropicLimit is one entry of the top-level "limits" array Anthropic began
// returning in 2026. Entries with kind=session / kind=weekly_all duplicate the
// five_hour / seven_day quota objects, but kind=weekly_scoped carries per-model
// weekly usage that has no populated top-level equivalent on some plans (#121).
type AnthropicLimit struct {
	Kind     string               `json:"kind"`
	Group    string               `json:"group"`
	Percent  *float64             `json:"percent"`
	Severity string               `json:"severity"`
	ResetsAt *string              `json:"resets_at"`
	IsActive *bool                `json:"is_active"`
	Scope    *AnthropicLimitScope `json:"scope"`
}

// AnthropicLimitScope narrows a limit to a model (and, in future, a surface).
type AnthropicLimitScope struct {
	Model *AnthropicLimitModel `json:"model"`
}

// AnthropicLimitModel identifies the model a scoped limit applies to. In
// observed payloads "id" is null, so display_name is the only discriminator.
type AnthropicLimitModel struct {
	ID          *string `json:"id"`
	DisplayName *string `json:"display_name"`
}

// anthropicScopedQuotaPrefix marks quota keys synthesised from a weekly_scoped
// limit. Only promoteScopedWeeklyLimits writes this prefix, so widening the
// quota whitelist to cover it cannot let experimental top-level keys back in.
const anthropicScopedQuotaPrefix = "seven_day_scoped_"

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

	// Second pass: limits[] was skipped above as a non-object. Parse it on its
	// own so a malformed array still cannot take down five_hour / seven_day.
	promoteScopedWeeklyLimits(resp, raw["limits"])

	*r = resp
	return nil
}

// promoteScopedWeeklyLimits copies per-model weekly buckets out of the
// top-level "limits" array into quota entries. On plans where Anthropic reports
// them only there - leaving seven_day_opus / seven_day_sonnet null - this is the
// only path by which the currently binding limit reaches the tracker (#121).
//
// Keys are derived from the model's display name rather than enumerated, so a
// bucket scoped to Sonnet, Opus or Fable each gets its own quota without a code
// change. A populated top-level key wins: limits[] is the fallback, not an
// override.
func promoteScopedWeeklyLimits(resp AnthropicQuotaResponse, raw json.RawMessage) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for _, item := range items {
		var limit AnthropicLimit
		if err := json.Unmarshal(item, &limit); err != nil {
			// One unreadable entry must not discard the rest of the array.
			continue
		}
		if limit.Kind != "weekly_scoped" || limit.Percent == nil {
			continue
		}
		if limit.Scope == nil || limit.Scope.Model == nil || limit.Scope.Model.DisplayName == nil {
			continue
		}
		slug := anthropicModelSlug(*limit.Scope.Model.DisplayName)
		if slug == "" {
			continue
		}
		// seven_day_sonnet and friends stay authoritative where the account
		// still gets them.
		if e := resp["seven_day_"+slug]; e != nil && e.Utilization != nil {
			continue
		}
		key := anthropicScopedQuotaPrefix + slug
		if e := resp[key]; e != nil && e.Utilization != nil {
			continue
		}
		percent := *limit.Percent
		entry := &AnthropicQuotaEntry{Utilization: &percent}
		if limit.ResetsAt != nil && *limit.ResetsAt != "" {
			resetsAt := *limit.ResetsAt
			entry.ResetsAt = &resetsAt
		}
		resp[key] = entry
	}
}

// anthropicModelSlug converts a model display name into a quota key fragment:
// runs of non-alphanumeric characters collapse to a single underscore.
func anthropicModelSlug(displayName string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(displayName) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
		default:
			pendingSep = true
		}
	}
	return b.String()
}

// anthropicScopedModelLabel reverses anthropicModelSlug for display:
// seven_day_scoped_claude_fable -> "Claude Fable".
func anthropicScopedModelLabel(key string) (string, bool) {
	slug, ok := strings.CutPrefix(key, anthropicScopedQuotaPrefix)
	if !ok {
		return "", false
	}
	return anthropicSlugLabel(slug)
}

// anthropicSlugLabel title-cases an underscore-separated slug: fable -> "Fable",
// oauth_apps -> "Oauth Apps". Reports false for an empty slug or one with an
// empty segment, so a malformed key falls back to being shown verbatim rather
// than as a label with a hole in it.
func anthropicSlugLabel(slug string) (string, bool) {
	if slug == "" {
		return "", false
	}
	words := strings.Split(slug, "_")
	for i, w := range words {
		if w == "" {
			return "", false
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " "), true
}

// IsAnthropicPerModelWeekly reports whether a quota key is a weekly bucket
// scoped to a single model, by either route: synthesised from limits[] on the
// API path, or reported straight from the statusline under a name we have no
// curated entry for. Curated keys such as seven_day_sonnet are excluded - they
// have their own label and sort position already.
func IsAnthropicPerModelWeekly(key string) bool {
	if IsAnthropicScopedQuota(key) {
		return true
	}
	if _, ok := anthropicDisplayNames[key]; ok {
		return false
	}
	slug, ok := strings.CutPrefix(key, "seven_day_")
	if !ok {
		return false
	}
	_, ok = anthropicSlugLabel(slug)
	return ok
}

// IsAnthropicScopedQuota reports whether a quota key was synthesised from a
// weekly_scoped entry in limits[].
func IsAnthropicScopedQuota(key string) bool {
	_, ok := anthropicScopedModelLabel(key)
	return ok
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
	// Per-model weekly buckets are dynamic, so their label is derived from the
	// key instead of being listed above.
	if model, ok := anthropicScopedModelLabel(key); ok {
		return "Weekly " + model
	}
	// The same treatment for a seven_day_* bucket reported under a name we have
	// no entry for - the statusline can surface one at any time, and a card
	// titled "seven_day_fable" reads like a defect.
	if slug, ok := strings.CutPrefix(key, "seven_day_"); ok {
		if model, ok := anthropicSlugLabel(slug); ok {
			return "Weekly " + model
		}
	}
	return key
}

// IsKnownAnthropicQuota reports whether the given quota key is in the whitelist.
// Used by both the write path (to filter out experimental keys before storage)
// and read paths (to hide historical rows written before the whitelist existed).
func IsKnownAnthropicQuota(key string) bool {
	if _, ok := anthropicDisplayNames[key]; ok {
		return true
	}
	return IsAnthropicScopedQuota(key)
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
		if !IsKnownAnthropicQuota(key) {
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
