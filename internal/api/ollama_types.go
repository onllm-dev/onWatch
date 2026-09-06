package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Ollama Cloud quota model.
//
// ollama.com exposes account usage through two authenticated JSON endpoints:
//
//	GET  /api/usage  - included monthly usage (USD) plus per-model request counts
//	POST /api/me     - plan tier, account name/email and creation time
//
// Neither endpoint returns the monthly dollar cap or the reset time, so those
// are derived: the cap from the plan tier (see OllamaPlanMonthlyLimit) or a
// user override, the reset from the account anniversary day or a user override.

// OllamaQuotaFormat mirrors the other quota-list providers.
type OllamaQuotaFormat string

const (
	OllamaQuotaFormatCurrency OllamaQuotaFormat = "currency"
	OllamaQuotaFormatPercent  OllamaQuotaFormat = "percent"
)

// OllamaQuotaMonthly is the quota key for included monthly usage.
const OllamaQuotaMonthly = "monthly"

// OllamaQuota is one tracked quota window.
type OllamaQuota struct {
	Name        string
	Used        float64
	Limit       float64 // 0 when the cap is unknown (Free plan without override)
	Utilization float64 // percent; 0 when Limit is unknown
	Format      OllamaQuotaFormat
	ResetsAt    *time.Time
}

// OllamaModelUsage is the per-model breakdown for the current month.
type OllamaModelUsage struct {
	Name         string
	RequestCount int
	Cost         float64
}

// OllamaSnapshot is a point-in-time capture of Ollama Cloud usage.
type OllamaSnapshot struct {
	ID         int64
	CapturedAt time.Time
	RawJSON    string

	Plan         string // "free", "pro", "max", "team" as reported by /api/me
	AccountName  string
	AccountEmail string

	MonthlyUsedUSD  float64 // limits.monthly.usage
	MonthlyLimitUSD float64 // resolved cap, 0 when unknown
	ExtraCostUSD    float64 // activity.cost (pay-as-you-go spend in the activity period)
	ActivityStart   *time.Time
	ActivityEnd     *time.Time

	Models []OllamaModelUsage
	Quotas []OllamaQuota
}

// ollamaFlexFloat accepts JSON numbers or numeric strings ("0.00000").
type ollamaFlexFloat float64

func (f *ollamaFlexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(strings.TrimPrefix(str, "$"))
		if str == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(str, ",", ""), 64)
		if err != nil {
			return err
		}
		*f = ollamaFlexFloat(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = ollamaFlexFloat(v)
	return nil
}

type ollamaUsageModel struct {
	Name         string          `json:"name"`
	RequestCount int             `json:"request_count"`
	Cost         ollamaFlexFloat `json:"cost"`
}

// OllamaUsageResponse is the JSON body of GET https://ollama.com/api/usage.
type OllamaUsageResponse struct {
	Activity struct {
		Cost   ollamaFlexFloat `json:"cost"`
		Period struct {
			Type       string     `json:"type"`
			StartingAt *time.Time `json:"starting_at"`
			EndingAt   *time.Time `json:"ending_at"`
		} `json:"period"`
		Models []ollamaUsageModel `json:"models"`
	} `json:"activity"`
	Limits struct {
		Monthly struct {
			Usage  ollamaFlexFloat    `json:"usage"`
			Models []ollamaUsageModel `json:"models"`
		} `json:"monthly"`
	} `json:"limits"`
}

// OllamaMeResponse is the JSON body of POST https://ollama.com/api/me.
// ollama.com serialises this struct with Go field names, hence the casing.
type OllamaMeResponse struct {
	Email     string     `json:"Email"`
	Name      string     `json:"Name"`
	Plan      string     `json:"Plan"`
	CreatedAt *time.Time `json:"CreatedAt"`
}

// ParseOllamaUsage decodes the /api/usage body.
func ParseOllamaUsage(data []byte) (*OllamaUsageResponse, error) {
	var resp OllamaUsageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseOllamaMe decodes the /api/me body.
func ParseOllamaMe(data []byte) (*OllamaMeResponse, error) {
	var resp OllamaMeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// OllamaPlanMonthlyLimit returns the included monthly usage in USD for a plan
// tier as published on ollama.com/pricing, or 0 when unknown. The Free plan
// includes "starter usage" with no published dollar figure.
func OllamaPlanMonthlyLimit(plan string) float64 {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro":
		return 60
	case "max":
		return 300
	case "team":
		return 1000
	}
	return 0
}

// OllamaNextReset returns the next monthly reset after now, anchored to the
// day of month (and time of day) of anchor. Days past the end of a shorter
// month clamp to that month's last day, matching billing anniversaries.
func OllamaNextReset(anchor, now time.Time) time.Time {
	anchor = anchor.UTC()
	now = now.UTC()
	day := anchor.Day()
	h, m, s := anchor.Clock()
	candidate := ollamaAnchoredDate(now.Year(), now.Month(), day, h, m, s)
	if !candidate.After(now) {
		// Step to the next month via its 1st so a 31st never rolls over twice.
		firstOfNext := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		candidate = ollamaAnchoredDate(firstOfNext.Year(), firstOfNext.Month(), day, h, m, s)
	}
	return candidate
}

func ollamaAnchoredDate(year int, month time.Month, day, h, m, s int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, h, m, s, 0, time.UTC)
}

// OllamaResetAnchor picks the anniversary anchor: an explicit day-of-month
// override wins, otherwise the account creation time, otherwise nil.
func OllamaResetAnchor(resetDay int, createdAt *time.Time, now time.Time) *time.Time {
	if resetDay >= 1 && resetDay <= 31 {
		t := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, resetDay-1)
		if t.Day() != resetDay { // clamped by a short month; keep the requested day via a long month
			t = time.Date(2000, time.January, resetDay, 0, 0, 0, 0, time.UTC)
		}
		return &t
	}
	if createdAt != nil && !createdAt.IsZero() {
		return createdAt
	}
	return nil
}

// BuildOllamaSnapshot combines the two API responses into a snapshot.
// monthlyLimitOverride > 0 replaces the plan-derived cap; resetDay 1-31
// replaces the account-anniversary reset anchor.
func BuildOllamaSnapshot(usage *OllamaUsageResponse, me *OllamaMeResponse, rawJSON string, monthlyLimitOverride float64, resetDay int, capturedAt time.Time) *OllamaSnapshot {
	capturedAt = capturedAt.UTC()
	snap := &OllamaSnapshot{
		CapturedAt: capturedAt,
		RawJSON:    rawJSON,
	}
	if me != nil {
		snap.Plan = strings.ToLower(strings.TrimSpace(me.Plan))
		snap.AccountName = strings.TrimSpace(me.Name)
		snap.AccountEmail = strings.TrimSpace(me.Email)
	}
	if usage != nil {
		snap.MonthlyUsedUSD = float64(usage.Limits.Monthly.Usage)
		snap.ExtraCostUSD = float64(usage.Activity.Cost)
		snap.ActivityStart = usage.Activity.Period.StartingAt
		snap.ActivityEnd = usage.Activity.Period.EndingAt

		costByModel := make(map[string]float64, len(usage.Activity.Models))
		for _, m := range usage.Activity.Models {
			costByModel[m.Name] = float64(m.Cost)
		}
		for _, m := range usage.Limits.Monthly.Models {
			snap.Models = append(snap.Models, OllamaModelUsage{
				Name:         m.Name,
				RequestCount: m.RequestCount,
				Cost:         costByModel[m.Name],
			})
		}
	}

	limit := monthlyLimitOverride
	if limit <= 0 {
		limit = OllamaPlanMonthlyLimit(snap.Plan)
	}
	snap.MonthlyLimitUSD = limit

	var createdAt *time.Time
	if me != nil {
		createdAt = me.CreatedAt
	}
	var resetsAt *time.Time
	if anchor := OllamaResetAnchor(resetDay, createdAt, capturedAt); anchor != nil {
		t := OllamaNextReset(*anchor, capturedAt)
		resetsAt = &t
	}

	util := 0.0
	if limit > 0 {
		util = snap.MonthlyUsedUSD / limit * 100
		if util < 0 {
			util = 0
		}
	}
	snap.Quotas = []OllamaQuota{{
		Name:        OllamaQuotaMonthly,
		Used:        snap.MonthlyUsedUSD,
		Limit:       limit,
		Utilization: util,
		Format:      OllamaQuotaFormatCurrency,
		ResetsAt:    resetsAt,
	}}
	return snap
}

// ApplyResetAnchor recomputes the monthly quota's ResetsAt from a learned
// anchor (the moment a real reset was observed), overriding the
// account-anniversary guess. Explicit OLLAMA_RESET_DAY handling is the
// caller's responsibility.
func (s *OllamaSnapshot) ApplyResetAnchor(anchor time.Time) {
	if s == nil || anchor.IsZero() {
		return
	}
	next := OllamaNextReset(anchor, s.CapturedAt)
	for i := range s.Quotas {
		if s.Quotas[i].Name == OllamaQuotaMonthly {
			t := next
			s.Quotas[i].ResetsAt = &t
		}
	}
}
