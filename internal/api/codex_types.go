package api

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CodexUsageResponse is the OAuth usage payload returned by Codex.
type CodexUsageResponse struct {
	PlanType              string                  `json:"plan_type"`
	RateLimit             codexRateLimit          `json:"rate_limit"`
	CodeReviewRateLimit   codexRateLimit          `json:"code_review_rate_limit,omitempty"`
	Credits               *codexCredits           `json:"credits,omitempty"`
	RateLimitResetCredits *codexResetCreditsCount `json:"rate_limit_reset_credits,omitempty"`
}

// codexResetCreditsCount is the summary block on the usage response
// (`rate_limit_reset_credits: {"available_count": N}`). The optional integer
// keeps an absent value distinguishable from an explicit zero (issue #152).
type codexResetCreditsCount struct {
	AvailableCount           CodexOptionalInt64 `json:"available_count"`
	ApplicableAvailableCount CodexOptionalInt64 `json:"applicable_available_count"`
}

// CodexOptionalInt64 distinguishes an absent/null JSON integer from 0.
type CodexOptionalInt64 struct {
	Value int64
	Valid bool
}

// UnmarshalJSON is deliberately lenient: this type sits on the main usage
// response, so rejecting one drifted count would fail the whole decode and
// leave Codex usage stale for every account. Numbers, quoted numbers and
// integral floats decode; anything else becomes unknown (Valid=false).
func (o *CodexOptionalInt64) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == "" {
		o.Value, o.Valid = 0, false
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			o.Value, o.Valid = 0, false
			return nil
		}
		s = strings.TrimSpace(str)
		if s == "" {
			o.Value, o.Valid = 0, false
			return nil
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		o.Value, o.Valid = n, true
		return nil
	}
	// A float representation of a whole number ("2.0") still names a count.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) ||
		f >= float64(math.MaxInt64) || f < float64(math.MinInt64) {
		o.Value, o.Valid = 0, false
		return nil
	}
	o.Value, o.Valid = int64(f), true
	return nil
}

// MarshalJSON emits the original wire shape, so a snapshot's raw JSON keeps an
// unknown count as null rather than the struct's internal fields.
func (o CodexOptionalInt64) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

type codexRateLimit struct {
	PrimaryWindow   *codexWindow `json:"primary_window"`
	SecondaryWindow *codexWindow `json:"secondary_window"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAtUnix        int64   `json:"reset_at"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
}

type codexCredits struct {
	Balance codexFloat64 `json:"balance,omitempty"`
}

type codexFloat64 struct {
	Value *float64
}

const (
	codexFiveHourWindowSeconds = int64(5 * 60 * 60)
	codexSevenDayWindowSeconds = int64(7 * 24 * 60 * 60)
)

func (f *codexFloat64) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == "" {
		f.Value = nil
		return nil
	}

	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		f.Value = &num
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(str), 64)
		if err != nil {
			return fmt.Errorf("invalid float string %q: %w", str, err)
		}
		f.Value = &parsed
		return nil
	}

	return fmt.Errorf("invalid float value: %s", s)
}

// CodexQuota represents one normalized Codex quota.
type CodexQuota struct {
	Name        string
	Utilization float64
	ResetsAt    *time.Time
	Status      string
}

// CodexSnapshot stores normalized Codex usage state.
type CodexSnapshot struct {
	ID             int64
	CapturedAt     time.Time
	AccountID      int64
	Quotas         []CodexQuota
	PlanType       string
	CreditsBalance *float64
	// ResetCreditsAvailableCount is the banked reset credit count reported by
	// the usage response. Valid is false when the field is absent: unknown is
	// not the same as zero. ResetCreditsApplicableCount is the plan-usable
	// subset reported alongside it.
	ResetCreditsAvailableCount  CodexOptionalInt64
	ResetCreditsApplicableCount CodexOptionalInt64
	RawJSON                     string
}

var codexDisplayNames = map[string]string{
	"five_hour":   "5-Hour Limit",
	"seven_day":   "Weekly All-Model",
	"code_review": "Review Requests",
}

// CodexDisplayName returns a display label for a codex quota key.
func CodexDisplayName(key string) string {
	if name, ok := codexDisplayNames[key]; ok {
		return name
	}
	return key
}

// ParseCodexUsageResponse parses raw JSON bytes into CodexUsageResponse.
func ParseCodexUsageResponse(data []byte) (*CodexUsageResponse, error) {
	var resp CodexUsageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ToSnapshot converts a CodexUsageResponse to a normalized CodexSnapshot.
func (r CodexUsageResponse) ToSnapshot(capturedAt time.Time) *CodexSnapshot {
	snapshot := &CodexSnapshot{
		CapturedAt: capturedAt,
		PlanType:   r.PlanType,
	}

	if r.Credits != nil && r.Credits.Balance.Value != nil {
		balance := *r.Credits.Balance.Value
		snapshot.CreditsBalance = &balance
	}

	if r.RateLimitResetCredits != nil {
		snapshot.ResetCreditsAvailableCount = CodexOptionalInt64{
			Value: r.RateLimitResetCredits.AvailableCount.Value,
			Valid: r.RateLimitResetCredits.AvailableCount.Valid,
		}
		snapshot.ResetCreditsApplicableCount = CodexOptionalInt64{
			Value: r.RateLimitResetCredits.ApplicableAvailableCount.Value,
			Valid: r.RateLimitResetCredits.ApplicableAvailableCount.Valid,
		}
	}

	if name := codexPrimaryQuotaName(r.PlanType, r.RateLimit.PrimaryWindow, r.RateLimit.SecondaryWindow); name != "" {
		snapshot.Quotas = append(snapshot.Quotas, codexQuotaFromWindow(name, r.RateLimit.PrimaryWindow))
	}
	if r.RateLimit.SecondaryWindow != nil {
		snapshot.Quotas = append(snapshot.Quotas, codexQuotaFromWindow("seven_day", r.RateLimit.SecondaryWindow))
	}
	if r.CodeReviewRateLimit.PrimaryWindow != nil {
		snapshot.Quotas = append(snapshot.Quotas, codexQuotaFromWindow("code_review", r.CodeReviewRateLimit.PrimaryWindow))
	}

	sort.Slice(snapshot.Quotas, func(i, j int) bool {
		left := codexQuotaSortOrder(snapshot.Quotas[i].Name)
		right := codexQuotaSortOrder(snapshot.Quotas[j].Name)
		if left != right {
			return left < right
		}
		return snapshot.Quotas[i].Name < snapshot.Quotas[j].Name
	})

	// Re-marshal from a carrier that includes the reset-credit count: the raw
	// JSON is persisted and re-parsed for history, so an unknown field dropped
	// here would be lost (issue #152).
	carrier := struct {
		CodexUsageResponse
		RateLimitResetCredits *codexResetCreditsCount `json:"rate_limit_reset_credits,omitempty"`
	}{CodexUsageResponse: r}
	if r.RateLimitResetCredits != nil {
		carrier.RateLimitResetCredits = r.RateLimitResetCredits
	}
	if raw, err := json.Marshal(carrier); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

func codexPrimaryQuotaName(planType string, primary, secondary *codexWindow) string {
	if primary == nil {
		return ""
	}
	// Paid plans typically expose 5-hour + weekly windows.
	if secondary != nil {
		return "five_hour"
	}
	// Free plans expose only a weekly window in primary.
	if strings.EqualFold(strings.TrimSpace(planType), "free") {
		return "seven_day"
	}
	switch {
	case primary.LimitWindowSeconds >= codexSevenDayWindowSeconds:
		return "seven_day"
	case primary.LimitWindowSeconds > 0 && primary.LimitWindowSeconds <= codexFiveHourWindowSeconds:
		return "five_hour"
	default:
		return "five_hour"
	}
}

func codexQuotaFromWindow(name string, window *codexWindow) CodexQuota {
	q := CodexQuota{
		Name:        name,
		Utilization: window.UsedPercent,
		Status:      codexStatusFromUtilization(window.UsedPercent),
	}
	if window.ResetAtUnix > 0 {
		resetAt := time.Unix(window.ResetAtUnix, 0).UTC()
		q.ResetsAt = &resetAt
	}
	return q
}

func codexQuotaSortOrder(name string) int {
	switch name {
	case "five_hour":
		return 0
	case "seven_day":
		return 1
	case "code_review":
		return 2
	default:
		return 100
	}
}

func codexStatusFromUtilization(utilization float64) string {
	switch {
	case utilization >= 95:
		return "critical"
	case utilization >= 80:
		return "danger"
	case utilization >= 50:
		return "warning"
	default:
		return "healthy"
	}
}
