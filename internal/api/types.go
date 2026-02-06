package api

import (
	"time"
)

// QuotaResponse represents the complete response from Synthetic API /v2/quotas
type QuotaResponse struct {
	Subscription      QuotaInfo  `json:"subscription"`
	Search            SearchInfo `json:"search"`
	ToolCallDiscounts QuotaInfo  `json:"toolCallDiscounts"`
}

// QuotaInfo represents a single quota type (subscription, tool calls, etc.)
type QuotaInfo struct {
	Limit    float64   `json:"limit"`
	Requests float64   `json:"requests"`
	RenewsAt time.Time `json:"renewsAt"`
}

// SearchInfo wraps the hourly search quota
type SearchInfo struct {
	Hourly QuotaInfo `json:"hourly"`
}

// Snapshot is the storage representation (flat, for SQLite)
type Snapshot struct {
	ID         int64
	CapturedAt time.Time
	Sub        QuotaInfo
	Search     QuotaInfo
	ToolCall   QuotaInfo
}
