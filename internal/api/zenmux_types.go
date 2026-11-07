package api

import "time"

// ZenmuxSnapshot represents the documented subscription-detail response.
// Numeric pointers distinguish an absent or null value from a reported zero.
type ZenmuxSnapshot struct {
	ID                  int64          `json:"-"`
	CapturedAt          time.Time      `json:"captured_at"`
	Plan                *ZenmuxPlan    `json:"plan"`
	Currency            *string        `json:"currency"`
	BaseUSDPerFlow      *float64       `json:"base_usd_per_flow"`
	EffectiveUSDPerFlow *float64       `json:"effective_usd_per_flow"`
	AccountStatus       *string        `json:"account_status"`
	Quota5Hour          *ZenmuxWindow  `json:"quota_5_hour"`
	Quota7Day           *ZenmuxWindow  `json:"quota_7_day"`
	QuotaMonthly        *ZenmuxMonthly `json:"quota_monthly"`
}

type ZenmuxPlan struct {
	Tier      *string    `json:"tier"`
	AmountUSD *float64   `json:"amount_usd"`
	Interval  *string    `json:"interval"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type ZenmuxWindow struct {
	UsagePercentage *float64   `json:"usage_percentage"` // fraction, not percent
	ResetsAt        *time.Time `json:"resets_at"`
	RemainingResets *int       `json:"remaining_resets,omitempty"` // only documented for 5-hour
	MaxFlows        *float64   `json:"max_flows"`
	UsedFlows       *float64   `json:"used_flows"`
	RemainingFlows  *float64   `json:"remaining_flows"`
	UsedValueUSD    *float64   `json:"used_value_usd"`
	MaxValueUSD     *float64   `json:"max_value_usd"`
}

// ZenmuxMonthly contains only the limits the subscription API publishes.
type ZenmuxMonthly struct {
	MaxFlows    *float64 `json:"max_flows"`
	MaxValueUSD *float64 `json:"max_value_usd"`
}
