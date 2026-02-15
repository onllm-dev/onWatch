package api

import (
	"encoding/json"
	"sort"
	"time"
)

// CopilotQuotaSnapshot represents a single quota snapshot from the Copilot API.
type CopilotQuotaSnapshot struct {
	Entitlement      int     `json:"entitlement"`
	OverageCount     int     `json:"overage_count"`
	OveragePermitted bool    `json:"overage_permitted"`
	PercentRemaining float64 `json:"percent_remaining"`
	QuotaID          string  `json:"quota_id"`
	QuotaRemaining   float64 `json:"quota_remaining"`
	Remaining        int     `json:"remaining"`
	Unlimited        bool    `json:"unlimited"`
	TimestampUTC     string  `json:"timestamp_utc"`
}

// CopilotUserResponse is the full response from the GitHub Copilot internal API.
type CopilotUserResponse struct {
	Login             string                           `json:"login"`
	CopilotPlan       string                           `json:"copilot_plan"`
	AccessTypeSKU     string                           `json:"access_type_sku"`
	QuotaResetDate    string                           `json:"quota_reset_date"`
	QuotaResetDateUTC string                           `json:"quota_reset_date_utc"`
	QuotaSnapshots    map[string]*CopilotQuotaSnapshot `json:"quota_snapshots"`
}

// CopilotQuota represents a single normalized quota for storage.
type CopilotQuota struct {
	Name             string
	Entitlement      int
	Remaining        int
	PercentRemaining float64
	Unlimited        bool
	OverageCount     int
}

// CopilotSnapshot represents a point-in-time capture of all Copilot quotas.
type CopilotSnapshot struct {
	ID          int64
	CapturedAt  time.Time
	Quotas      []CopilotQuota
	ResetDate   *time.Time
	CopilotPlan string
	RawJSON     string
}

// copilotDisplayNames maps API keys to human-readable labels.
var copilotDisplayNames = map[string]string{
	"premium_interactions": "Premium Requests",
	"chat":                "Chat",
	"completions":         "Completions",
}

// CopilotDisplayName returns the human-readable name for a quota key.
func CopilotDisplayName(key string) string {
	if name, ok := copilotDisplayNames[key]; ok {
		return name
	}
	return key
}

// ActiveQuotaNames returns sorted names of quotas present in the response.
// Nil entries are skipped.
func (r CopilotUserResponse) ActiveQuotaNames() []string {
	var names []string
	for key, entry := range r.QuotaSnapshots {
		if entry == nil {
			continue
		}
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

// ToSnapshot converts a CopilotUserResponse to a CopilotSnapshot.
func (r CopilotUserResponse) ToSnapshot(capturedAt time.Time) *CopilotSnapshot {
	snapshot := &CopilotSnapshot{
		CapturedAt:  capturedAt,
		CopilotPlan: r.CopilotPlan,
	}

	// Parse reset date
	if r.QuotaResetDateUTC != "" {
		if t, err := time.Parse(time.RFC3339, r.QuotaResetDateUTC); err == nil {
			snapshot.ResetDate = &t
		} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", r.QuotaResetDateUTC); err == nil {
			snapshot.ResetDate = &t
		}
	}

	for _, name := range r.ActiveQuotaNames() {
		entry := r.QuotaSnapshots[name]
		q := CopilotQuota{
			Name:             name,
			Entitlement:      entry.Entitlement,
			Remaining:        entry.Remaining,
			PercentRemaining: entry.PercentRemaining,
			Unlimited:        entry.Unlimited,
			OverageCount:     entry.OverageCount,
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	// Store raw JSON for debugging/auditing
	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// ParseCopilotResponse parses raw JSON bytes into a CopilotUserResponse.
func ParseCopilotResponse(data []byte) (*CopilotUserResponse, error) {
	var resp CopilotUserResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
