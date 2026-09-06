package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ZaiResponse is the generic wrapper for all Z.ai API responses
type ZaiResponse[T any] struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Success bool   `json:"success"`
	Data    T      `json:"data"`
}

// ZaiQuotaResponse is the response from GET /monitor/usage/quota/limit
type ZaiQuotaResponse struct {
	Limits []ZaiLimit `json:"limits"`
	// Level is the plan tier ("lite", …). Credit-based plans report quotas as
	// CREDIT_LIMIT entries rather than TIME_LIMIT / TOKENS_LIMIT.
	Level string `json:"level"`
}

// Limit type values Z.ai returns in limits[].type. Legacy coding plans report
// TIME_LIMIT / TOKENS_LIMIT; the GLM Coding Plan Lite tier reports credit
// windows as CREDIT_LIMIT instead (issue #122).
const (
	ZaiLimitTypeTime   = "TIME_LIMIT"
	ZaiLimitTypeTokens = "TOKENS_LIMIT"
	ZaiLimitTypeCredit = "CREDIT_LIMIT"
)

// ZaiLimit represents an individual limit (TIME_LIMIT, TOKENS_LIMIT or CREDIT_LIMIT)
type ZaiLimit struct {
	Type         string           `json:"type"`
	Unit         int              `json:"unit"`
	Number       int              `json:"number"`
	Usage        float64          `json:"usage"`
	CurrentValue float64          `json:"currentValue"`
	Remaining    float64          `json:"remaining"`
	Percentage   int              `json:"percentage"`
	NextResetMs  *int64           `json:"nextResetTime,omitempty"`
	UsageDetails []ZaiUsageDetail `json:"usageDetails,omitempty"`
}

// ZaiUsageDetail represents per-model usage breakdown
type ZaiUsageDetail struct {
	ModelCode string  `json:"modelCode"`
	Usage     float64 `json:"usage"`
}

// GetResetTime returns the reset time as a time.Time pointer.
// Returns nil if there is no reset time (TIME_LIMIT has no reset).
func (l *ZaiLimit) GetResetTime() *time.Time {
	if l.NextResetMs == nil {
		return nil
	}
	// Z.ai returns epoch milliseconds
	t := time.UnixMilli(*l.NextResetMs)
	return &t
}

// ZaiSnapshot is the storage representation (flat, for SQLite)
type ZaiSnapshot struct {
	ID         int64
	CapturedAt time.Time
	// TIME_LIMIT fields
	TimeLimit        int
	TimeUnit         int
	TimeNumber       int
	TimeUsage        float64
	TimeCurrentValue float64
	TimeRemaining    float64
	TimePercentage   int
	TimeUsageDetails string // JSON: [{"modelCode":"search-prime","usage":16}, ...]
	// TimeLimitType records which upstream limit type filled the fields above.
	// Empty on rows written before credit plans existed, which the UI reads as
	// the legacy TIME_LIMIT labelling.
	TimeLimitType string
	// TimeNextResetTime is nil for legacy TIME_LIMIT entries, which carry no
	// reset. Credit windows do report one.
	TimeNextResetTime *time.Time
	// TOKENS_LIMIT fields
	TokensLimit         int
	TokensUnit          int
	TokensNumber        int
	TokensUsage         float64
	TokensCurrentValue  float64
	TokensRemaining     float64
	TokensPercentage    int
	TokensNextResetTime *time.Time
	// TokensLimitType records which upstream limit type filled the fields
	// above. Empty means legacy TOKENS_LIMIT.
	TokensLimitType string
}

// ToSnapshot converts ZaiQuotaResponse to ZaiSnapshot.
//
// Legacy TIME_LIMIT / TOKENS_LIMIT entries always win, so accounts that still
// receive them are unaffected. Any slot they leave empty is then filled from
// CREDIT_LIMIT entries, which is all a GLM Coding Plan Lite account returns.
func (r *ZaiQuotaResponse) ToSnapshot(capturedAt time.Time) *ZaiSnapshot {
	snapshot := &ZaiSnapshot{
		CapturedAt: capturedAt,
	}

	for _, limit := range r.Limits {
		switch limit.Type {
		case ZaiLimitTypeTime:
			snapshot.TimeLimit = limit.Unit * limit.Number
			snapshot.TimeUnit = limit.Unit
			snapshot.TimeNumber = limit.Number
			snapshot.TimeUsage = limit.Usage
			snapshot.TimeCurrentValue = limit.CurrentValue
			snapshot.TimeRemaining = limit.Remaining
			snapshot.TimePercentage = limit.Percentage
			snapshot.TimeLimitType = ZaiLimitTypeTime
			snapshot.TimeNextResetTime = limit.GetResetTime()
			if len(limit.UsageDetails) > 0 {
				b, _ := json.Marshal(limit.UsageDetails)
				snapshot.TimeUsageDetails = string(b)
			}
		case ZaiLimitTypeTokens:
			snapshot.TokensLimit = limit.Unit * limit.Number
			snapshot.TokensUnit = limit.Unit
			snapshot.TokensNumber = limit.Number
			snapshot.TokensUsage = limit.Usage
			snapshot.TokensCurrentValue = limit.CurrentValue
			snapshot.TokensRemaining = limit.Remaining
			snapshot.TokensPercentage = limit.Percentage
			snapshot.TokensLimitType = ZaiLimitTypeTokens
			if limit.NextResetMs != nil {
				t := time.UnixMilli(*limit.NextResetMs)
				snapshot.TokensNextResetTime = &t
			}
		}
	}

	r.applyCreditLimits(snapshot)

	return snapshot
}

// applyCreditLimits maps CREDIT_LIMIT entries onto whichever of the two storage
// slots the legacy entries left empty: the shortest credit window goes to the
// time slot, the longest to the tokens slot.
//
// Windows are ordered by (unit, number) rather than by nextResetTime. Those two
// numbers are fixed properties of the plan, so the assignment stays stable poll
// to poll - reset times would swap the slots whenever the weekly window happens
// to reset before the 5-hour one, which would break cycle detection.
func (r *ZaiQuotaResponse) applyCreditLimits(snapshot *ZaiSnapshot) {
	credits := make([]ZaiLimit, 0, len(r.Limits))
	for _, limit := range r.Limits {
		if limit.Type == ZaiLimitTypeCredit {
			credits = append(credits, limit)
		}
	}
	if len(credits) == 0 {
		return
	}
	sort.SliceStable(credits, func(i, j int) bool {
		if credits[i].Unit != credits[j].Unit {
			return credits[i].Unit < credits[j].Unit
		}
		return credits[i].Number < credits[j].Number
	})

	shortest := credits[0]
	longest := credits[len(credits)-1]

	if snapshot.TimeLimitType == "" {
		snapshot.TimeLimit = shortest.Unit * shortest.Number
		snapshot.TimeUnit = shortest.Unit
		snapshot.TimeNumber = shortest.Number
		snapshot.TimeUsage = shortest.Usage
		snapshot.TimeCurrentValue = shortest.CurrentValue
		snapshot.TimeRemaining = shortest.Remaining
		snapshot.TimePercentage = shortest.Percentage
		snapshot.TimeLimitType = ZaiLimitTypeCredit
		snapshot.TimeNextResetTime = shortest.GetResetTime()
		if len(shortest.UsageDetails) > 0 {
			b, _ := json.Marshal(shortest.UsageDetails)
			snapshot.TimeUsageDetails = string(b)
		}
	}

	// A single credit window fills only the time slot; there is no second
	// window to report and duplicating it would double-count usage.
	if len(credits) < 2 || snapshot.TokensLimitType != "" {
		return
	}
	snapshot.TokensLimit = longest.Unit * longest.Number
	snapshot.TokensUnit = longest.Unit
	snapshot.TokensNumber = longest.Number
	snapshot.TokensUsage = longest.Usage
	snapshot.TokensCurrentValue = longest.CurrentValue
	snapshot.TokensRemaining = longest.Remaining
	snapshot.TokensPercentage = longest.Percentage
	snapshot.TokensLimitType = ZaiLimitTypeCredit
	if longest.NextResetMs != nil {
		t := time.UnixMilli(*longest.NextResetMs)
		snapshot.TokensNextResetTime = &t
	}
}

// IsCreditBased reports whether either storage slot was filled from a
// CREDIT_LIMIT entry, so callers can label the quota as credits rather than
// prompts or tokens.
func (s *ZaiSnapshot) IsCreditBased() bool {
	return s.TimeLimitType == ZaiLimitTypeCredit || s.TokensLimitType == ZaiLimitTypeCredit
}

// ParseZaiResponse parses a Z.ai API response from JSON bytes
func ParseZaiResponse(data []byte) (*ZaiQuotaResponse, error) {
	var wrapper ZaiResponse[ZaiQuotaResponse]
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}

	if !wrapper.Success {
		return nil, fmt.Errorf("API error: code=%d, msg=%s", wrapper.Code, wrapper.Msg)
	}

	return &wrapper.Data, nil
}
