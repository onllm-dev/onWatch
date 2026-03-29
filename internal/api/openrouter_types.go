package api

import (
	"encoding/json"
	"time"
)

// OpenRouterAuthKeyData is the inner data from GET /api/v1/auth/key.
type OpenRouterAuthKeyData struct {
	Label          string   `json:"label"`
	Usage          float64  `json:"usage"`
	Limit          *float64 `json:"limit"`
	LimitRemaining *float64 `json:"limit_remaining"`
	IsFreeTier     bool     `json:"is_free_tier"`
	RateLimit      struct {
		Requests int    `json:"requests"`
		Interval string `json:"interval"`
	} `json:"rate_limit"`
	UsageDaily   float64 `json:"usage_daily"`
	UsageWeekly  float64 `json:"usage_weekly"`
	UsageMonthly float64 `json:"usage_monthly"`
}

// OpenRouterAuthKeyResponse is the top-level response from GET /api/v1/auth/key.
type OpenRouterAuthKeyResponse struct {
	Data OpenRouterAuthKeyData `json:"data"`
}

// OpenRouterSnapshot is the storage representation (flat, for SQLite).
type OpenRouterSnapshot struct {
	ID                int64
	CapturedAt        time.Time
	Label             string
	Usage             float64
	UsageDaily        float64
	UsageWeekly       float64
	UsageMonthly      float64
	Limit             *float64
	LimitRemaining    *float64
	IsFreeTier        bool
	RateLimitRequests int
	RateLimitInterval string
}

// ToSnapshot converts OpenRouterAuthKeyResponse to OpenRouterSnapshot.
func (r *OpenRouterAuthKeyResponse) ToSnapshot(capturedAt time.Time) *OpenRouterSnapshot {
	snapshot := &OpenRouterSnapshot{
		CapturedAt:        capturedAt.UTC(),
		Label:             r.Data.Label,
		Usage:             r.Data.Usage,
		UsageDaily:        r.Data.UsageDaily,
		UsageWeekly:       r.Data.UsageWeekly,
		UsageMonthly:      r.Data.UsageMonthly,
		Limit:             r.Data.Limit,
		LimitRemaining:    r.Data.LimitRemaining,
		IsFreeTier:        r.Data.IsFreeTier,
		RateLimitRequests: r.Data.RateLimit.Requests,
		RateLimitInterval: r.Data.RateLimit.Interval,
	}

	return snapshot
}

// ParseOpenRouterResponse parses an OpenRouter API response from JSON bytes.
func ParseOpenRouterResponse(data []byte) (*OpenRouterAuthKeyResponse, error) {
	var resp OpenRouterAuthKeyResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
