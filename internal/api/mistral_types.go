package api

import (
	"encoding/json"
	"time"
)

type MistralQuota struct {
	PercentOnly bool       `json:"percentOnly,omitempty"`
	Name        string     `json:"name"`
	Used        float64    `json:"used"`
	Limit       float64    `json:"limit"`
	Utilization float64    `json:"utilization"`
	Currency    string     `json:"currency"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	CapturedAt  time.Time  `json:"capturedAt"`
}

type MistralBilling struct {
	Amount      *float64  `json:"amount"`
	Currency    string    `json:"currency"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	CapturedAt  time.Time `json:"capturedAt"`
	Status      string    `json:"status"`
}

type MistralSnapshot struct {
	RetryAfter time.Duration   `json:"-"`
	AuthFailed bool            `json:"-"`
	ID         int64           `json:"id"`
	Identity   string          `json:"identity"`
	CapturedAt time.Time       `json:"capturedAt"`
	Quotas     []MistralQuota  `json:"quotas"`
	Billing    *MistralBilling `json:"billing"`
	Status     string          `json:"status"`
}

// Percentage-only console data must not serialize unknown monetary amounts as zero.
func (q MistralQuota) MarshalJSON() ([]byte, error) {
	type quota MistralQuota
	var used, limit *float64
	if !q.PercentOnly {
		used = &q.Used
		limit = &q.Limit
	}
	return json.Marshal(struct {
		quota
		Used  *float64 `json:"used"`
		Limit *float64 `json:"limit"`
	}{quota(q), used, limit})
}
