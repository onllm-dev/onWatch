package api

import (
	"encoding/json"
	"sort"
	"time"
)

// GeminiQuotaBucket is the raw API response for one model's quota.
type GeminiQuotaBucket struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
	ModelID           string  `json:"modelId"`
}

// GeminiQuotaResponse is the full response from retrieveUserQuota.
type GeminiQuotaResponse struct {
	Quotas []GeminiQuotaBucket `json:"buckets"`
}

// GeminiTierResponse is the response from loadCodeAssist.
type GeminiTierResponse struct {
	Tier                     string `json:"tier"`
	CloudAICompanionProject  string `json:"cloudaicompanionProject"`
	PlanName                 string `json:"planName,omitempty"`
}

// GeminiQuota is a normalized per-model quota for storage.
type GeminiQuota struct {
	ModelID           string
	RemainingFraction float64
	UsagePercent      float64
	ResetTime         *time.Time
	TimeUntilReset    time.Duration
}

// GeminiSnapshot represents a point-in-time capture of Gemini quotas.
type GeminiSnapshot struct {
	ID         int64
	CapturedAt time.Time
	Tier       string
	ProjectID  string
	Quotas     []GeminiQuota
	RawJSON    string
}

// geminiDisplayNames maps model IDs to human-readable labels.
var geminiDisplayNames = map[string]string{
	"gemini-2.5-pro":             "Gemini 2.5 Pro",
	"gemini-2.5-flash":           "Gemini 2.5 Flash",
	"gemini-2.5-flash-lite":      "Gemini 2.5 Flash Lite",
	"gemini-3-pro-preview":       "Gemini 3 Pro",
	"gemini-3-flash-preview":     "Gemini 3 Flash",
	"gemini-3.1-flash-lite-preview": "Gemini 3.1 Flash Lite",
}

// geminiModelSortOrder defines display ordering for Gemini models.
var geminiModelSortOrder = map[string]int{
	"gemini-3-pro-preview":          0,
	"gemini-2.5-pro":                1,
	"gemini-3-flash-preview":        2,
	"gemini-2.5-flash":              3,
	"gemini-3.1-flash-lite-preview": 4,
	"gemini-2.5-flash-lite":         5,
}

// GeminiDisplayName returns the human-readable name for a Gemini model ID.
func GeminiDisplayName(modelID string) string {
	if name, ok := geminiDisplayNames[modelID]; ok {
		return name
	}
	return modelID
}

func geminiSortOrder(modelID string) int {
	if order, ok := geminiModelSortOrder[modelID]; ok {
		return order
	}
	return 100
}

func geminiStatusFromUsage(usagePercent float64) string {
	switch {
	case usagePercent >= 95:
		return "critical"
	case usagePercent >= 80:
		return "danger"
	case usagePercent >= 50:
		return "warning"
	default:
		return "healthy"
	}
}

// ParseGeminiQuotaResponse parses raw JSON bytes into GeminiQuotaResponse.
func ParseGeminiQuotaResponse(data []byte) (*GeminiQuotaResponse, error) {
	var resp GeminiQuotaResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ToSnapshot converts a GeminiQuotaResponse to a GeminiSnapshot.
func (r GeminiQuotaResponse) ToSnapshot(capturedAt time.Time) *GeminiSnapshot {
	snapshot := &GeminiSnapshot{
		CapturedAt: capturedAt,
	}

	now := time.Now()
	for _, bucket := range r.Quotas {
		usagePercent := (1.0 - bucket.RemainingFraction) * 100
		if usagePercent < 0 {
			usagePercent = 0
		}
		if usagePercent > 100 {
			usagePercent = 100
		}

		quota := GeminiQuota{
			ModelID:           bucket.ModelID,
			RemainingFraction: bucket.RemainingFraction,
			UsagePercent:      usagePercent,
		}

		if bucket.ResetTime != "" {
			if t, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil {
				quota.ResetTime = &t
				d := t.Sub(now)
				if d < 0 {
					d = 0
				}
				quota.TimeUntilReset = d
			}
		}

		snapshot.Quotas = append(snapshot.Quotas, quota)
	}

	// Sort by model priority
	sort.Slice(snapshot.Quotas, func(i, j int) bool {
		oi := geminiSortOrder(snapshot.Quotas[i].ModelID)
		oj := geminiSortOrder(snapshot.Quotas[j].ModelID)
		if oi != oj {
			return oi < oj
		}
		return snapshot.Quotas[i].ModelID < snapshot.Quotas[j].ModelID
	})

	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// ActiveModelIDs returns sorted model IDs present in the response.
func (r GeminiQuotaResponse) ActiveModelIDs() []string {
	var ids []string
	for _, q := range r.Quotas {
		if q.ModelID != "" {
			ids = append(ids, q.ModelID)
		}
	}
	sort.Strings(ids)
	return ids
}
