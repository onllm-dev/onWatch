package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseCommandCodeFloat parses a JSON scalar that arrived as a bare number or
// a numeric string.
func parseCommandCodeFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

// isAllDigits reports whether s is a non-empty run of ASCII digits, which is
// how an epoch timestamp arrives when it is stringified.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Command Code provider quota model.
//
// Command Code (commandcode.ai) exposes an alpha JSON API authenticated with
// the same `user_...` API key the `cmd` CLI uses. Three GET endpoints matter:
//
//	/alpha/whoami                 - account login plus the org id used for the
//	                                billing lookups below
//	/alpha/billing/credits        - credit balances and the two rate-limit
//	                                windows (fiveHour, weekly) with reset times
//	/alpha/billing/subscriptions  - plan id, status and current period bounds
//	/alpha/usage/summary          - billed cost, request count and token totals
//
// Verified response shapes:
//
//	{"credits":{"monthlyCredits":50.93,"purchasedCredits":0,"freeCredits":0},
//	 "windowLimits":{"fiveHour":{"used":0.98,"cap":14,"resetAt":1790262790822},
//	                 "weekly":{"used":19.06,"cap":35,"resetAt":1790429616789}}}
//
//	{"success":true,"data":{"planId":"individual-goat","status":"active",
//	 "currentPeriodStart":"2026-09-19T12:53:45.000Z",
//	 "currentPeriodEnd":"2026-10-19T12:53:45.000Z"}}
//
//	{"totalCost":19.01,"totalCount":6197,"totalTokens":1066605925}
//
// resetAt arrives as epoch milliseconds; epoch seconds are also accepted.
// duration_mins is derived from the reset window itself, not from the API.

// Command Code quota window names.
const (
	CommandCodeQuotaFiveHour = "five_hour"
	CommandCodeQuotaWeekly   = "weekly"
	CommandCodeQuotaMonthly  = "monthly"
)

// CommandCodeQuotaFormat mirrors the other quota-list providers. The two
// rate-limit windows are denominated in Command Code credits; the monthly
// billing balance is denominated in USD.
type CommandCodeQuotaFormat string

const (
	CommandCodeQuotaFormatCurrency CommandCodeQuotaFormat = "currency"
	CommandCodeQuotaFormatCredits  CommandCodeQuotaFormat = "credits"
)

// CommandCodeQuota is one tracked quota window.
type CommandCodeQuota struct {
	Name        string
	Used        float64 // credits used, or USD spent for the monthly balance
	Limit       float64 // cap; 0 when the provider has not set one
	Utilization float64 // percent used; 0 when Limit is unknown
	Format      CommandCodeQuotaFormat
	ResetsAt    *time.Time

	// Remaining is set only on the monthly quota: the credit balance the
	// provider reports. It is what the card shows when the billing grant
	// cannot be derived (no period summary), where Used/Limit would read 0.
	Remaining float64
}

// CommandCodeSnapshot is a point-in-time capture of Command Code usage.
type CommandCodeSnapshot struct {
	ID         int64
	CapturedAt time.Time
	RawJSON    string

	AccountName string // whoami user.userName (fallback: org login)
	AccountID   string
	OrgID       string

	Plan   string // planId, e.g. "individual-goat"
	Status string // subscription status, e.g. "active"

	MonthlyCredits   float64 // included credits remaining this period
	PurchasedCredits float64
	FreeCredits      float64
	RemainingCredits float64 // sum of the three above

	PeriodStart *time.Time
	PeriodEnd   *time.Time

	PeriodCostUSD float64
	PeriodReqs    int64
	PeriodTokens  int64

	Quotas []CommandCodeQuota
}

// commandCodeFlexFloat accepts JSON numbers or numeric strings ("19.06").
type commandCodeFlexFloat float64

func (f *commandCodeFlexFloat) UnmarshalJSON(b []byte) error {
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
		v, err := parseCommandCodeFloat(strings.ReplaceAll(str, ",", ""))
		if err != nil {
			return err
		}
		*f = commandCodeFlexFloat(v)
		return nil
	}
	v, err := parseCommandCodeFloat(s)
	if err != nil {
		return err
	}
	*f = commandCodeFlexFloat(v)
	return nil
}

// commandCodeWhoamiResponse is the JSON body of GET /alpha/whoami.
type commandCodeWhoamiResponse struct {
	Success bool `json:"success"`
	User    struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		UserName string `json:"userName"`
	} `json:"user"`
	Org *struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	} `json:"org"`
}

// commandCodeWindow is one rate-limit window in the credits response.
type commandCodeWindow struct {
	Used    commandCodeFlexFloat `json:"used"`
	Cap     commandCodeFlexFloat `json:"cap"`
	ResetAt commandCodeFlexTime  `json:"resetAt"`
}

// commandCodeCreditsResponse is the JSON body of GET /alpha/billing/credits.
type commandCodeCreditsResponse struct {
	Credits struct {
		MonthlyCredits   commandCodeFlexFloat `json:"monthlyCredits"`
		PurchasedCredits commandCodeFlexFloat `json:"purchasedCredits"`
		FreeCredits      commandCodeFlexFloat `json:"freeCredits"`
	} `json:"credits"`
	WindowLimits struct {
		FiveHour *commandCodeWindow `json:"fiveHour"`
		Weekly   *commandCodeWindow `json:"weekly"`
	} `json:"windowLimits"`
}

// commandCodeSubscriptionResponse is the JSON body of
// GET /alpha/billing/subscriptions. The endpoint puts everything under "data".
type commandCodeSubscriptionResponse struct {
	Success bool `json:"success"`
	Data    struct {
		PlanID             string              `json:"planId"`
		Status             string              `json:"status"`
		CurrentPeriodStart commandCodeFlexTime `json:"currentPeriodStart"`
		CurrentPeriodEnd   commandCodeFlexTime `json:"currentPeriodEnd"`
	} `json:"data"`
}

// commandCodeUsageSummaryResponse is the JSON body of GET /alpha/usage/summary.
type commandCodeUsageSummaryResponse struct {
	TotalCost   commandCodeFlexFloat `json:"totalCost"`
	TotalCount  commandCodeFlexFloat `json:"totalCount"`
	TotalTokens commandCodeFlexFloat `json:"totalTokens"`
}

// commandCodeFlexTime accepts an epoch value (seconds or milliseconds) or an
// ISO-8601 string, and normalises to UTC. Zero or unparseable values yield nil.
type commandCodeFlexTime struct {
	t *time.Time
}

func (f *commandCodeFlexTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "" || s == "null":
		return nil
	case s[0] == '"':
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		f.t = parseCommandCodeTimeString(strings.TrimSpace(str))
		return nil
	}
	var num float64
	if err := json.Unmarshal(b, &num); err != nil {
		return err
	}
	f.t = commandCodeEpochToTime(num)
	return nil
}

// commandCodeEpochToTime converts epoch seconds (or milliseconds) to UTC.
// Values <= 0 yield nil so a zero reset never becomes 1970.
func commandCodeEpochToTime(v float64) *time.Time {
	if v <= 0 {
		return nil
	}
	if v >= 1e12 { // milliseconds since epoch
		v /= 1000
	}
	t := time.Unix(int64(v), 0).UTC()
	return &t
}

// parseCommandCodeTimeString accepts either a numeric epoch string or an
// ISO-8601 timestamp.
func parseCommandCodeTimeString(s string) *time.Time {
	if s == "" {
		return nil
	}
	if isAllDigits(s) {
		v, err := parseCommandCodeFloat(s)
		if err != nil {
			return nil
		}
		return commandCodeEpochToTime(v)
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// ParseCommandCodeCredits decodes a /alpha/billing/credits body.
func ParseCommandCodeCredits(data []byte) (*commandCodeCreditsResponse, error) {
	var resp commandCodeCreditsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("credits: %w", err)
	}
	return &resp, nil
}

// ParseCommandCodeSubscription decodes a /alpha/billing/subscriptions body.
func ParseCommandCodeSubscription(data []byte) (*commandCodeSubscriptionResponse, error) {
	var resp commandCodeSubscriptionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("subscription: %w", err)
	}
	return &resp, nil
}

// ParseCommandCodeSummary decodes a /alpha/usage/summary body.
func ParseCommandCodeSummary(data []byte) (*commandCodeUsageSummaryResponse, error) {
	var resp commandCodeUsageSummaryResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}
	return &resp, nil
}

// ParseCommandCodeWhoami decodes a /alpha/whoami body.
func ParseCommandCodeWhoami(data []byte) (*commandCodeWhoamiResponse, error) {
	var resp commandCodeWhoamiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("whoami: %w", err)
	}
	return &resp, nil
}

// CommandCodeAccountName picks the display name: the user's userName wins,
// then their full name, then the organisation login.
func CommandCodeAccountName(resp *commandCodeWhoamiResponse) string {
	if resp == nil {
		return ""
	}
	if name := strings.TrimSpace(resp.User.UserName); name != "" {
		return name
	}
	if name := strings.TrimSpace(resp.User.Name); name != "" {
		return name
	}
	if resp.Org != nil {
		return strings.TrimSpace(resp.Org.Login)
	}
	return ""
}

// buildCommandCodeWindowQuota converts one rate-limit window into a quota.
// A window with no cap carries no meaningful percentage and is skipped by the
// caller, matching the reference provider, which drops used == cap == 0.
func buildCommandCodeWindowQuota(name string, w *commandCodeWindow) (CommandCodeQuota, bool) {
	if w == nil {
		return CommandCodeQuota{}, false
	}
	used := float64(w.Used)
	cap := float64(w.Cap)
	if cap <= 0 {
		return CommandCodeQuota{}, false
	}
	return CommandCodeQuota{
		Name:        name,
		Used:        used,
		Limit:       cap,
		Utilization: clampCommandCodePercent(used / cap * 100),
		Format:      CommandCodeQuotaFormatCredits,
		ResetsAt:    w.ResetAt.t,
	}, true
}

func clampCommandCodePercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// BuildCommandCodeSnapshot combines the four API responses into storage form.
func BuildCommandCodeSnapshot(
	whoami *commandCodeWhoamiResponse,
	credits *commandCodeCreditsResponse,
	subscription *commandCodeSubscriptionResponse,
	summary *commandCodeUsageSummaryResponse,
	rawJSON string,
	capturedAt time.Time,
) *CommandCodeSnapshot {
	capturedAt = capturedAt.UTC()
	snap := &CommandCodeSnapshot{
		CapturedAt: capturedAt,
		RawJSON:    rawJSON,
	}

	if whoami != nil {
		snap.AccountName = CommandCodeAccountName(whoami)
		snap.AccountID = strings.TrimSpace(whoami.User.ID)
		if whoami.Org != nil {
			snap.OrgID = strings.TrimSpace(whoami.Org.ID)
		}
	}

	if subscription != nil {
		snap.Plan = strings.TrimSpace(subscription.Data.PlanID)
		snap.Status = strings.TrimSpace(subscription.Data.Status)
		snap.PeriodStart = subscription.Data.CurrentPeriodStart.t
		snap.PeriodEnd = subscription.Data.CurrentPeriodEnd.t
	}

	if summary != nil {
		snap.PeriodCostUSD = float64(summary.TotalCost)
		snap.PeriodReqs = int64(float64(summary.TotalCount))
		snap.PeriodTokens = int64(float64(summary.TotalTokens))
	}

	if credits != nil {
		snap.MonthlyCredits = float64(credits.Credits.MonthlyCredits)
		snap.PurchasedCredits = float64(credits.Credits.PurchasedCredits)
		snap.FreeCredits = float64(credits.Credits.FreeCredits)
		snap.RemainingCredits = snap.MonthlyCredits + snap.PurchasedCredits + snap.FreeCredits

		if q, ok := buildCommandCodeWindowQuota(CommandCodeQuotaFiveHour, credits.WindowLimits.FiveHour); ok {
			snap.Quotas = append(snap.Quotas, q)
		}
		if q, ok := buildCommandCodeWindowQuota(CommandCodeQuotaWeekly, credits.WindowLimits.Weekly); ok {
			snap.Quotas = append(snap.Quotas, q)
		}
	}

	// The monthly credit balance is tracked against the credits granted for
	// the billing period. The API reports what is left, not what was granted,
	// so the grant is reconstructed as remaining + what has been billed this
	// period: 50.99 remaining + 18.99 billed = 69.98 granted, 27% used.
	//
	// Without a usage summary the billed amount is unknown, so the grant cannot
	// be derived at all. Reporting the balance as the limit would show a card
	// that is permanently 0% used, so the cap is left at zero and the card
	// falls back to the credit balance instead.
	if credits != nil {
		granted := 0.0
		if summary != nil {
			granted = snap.RemainingCredits + snap.PeriodCostUSD
		}
		quota := CommandCodeQuota{
			Name:      CommandCodeQuotaMonthly,
			Remaining: snap.RemainingCredits,
			Format:    CommandCodeQuotaFormatCurrency,
			ResetsAt:  snap.PeriodEnd,
		}
		if granted > 0 {
			quota.Limit = granted
			quota.Used = snap.PeriodCostUSD
			quota.Utilization = clampCommandCodePercent(snap.PeriodCostUSD / granted * 100)
		}
		snap.Quotas = append(snap.Quotas, quota)
	}

	return snap
}

// CommandCodeDisplayPlan turns a plan id such as "individual-goat" into a
// human-readable label ("Individual Goat").
func CommandCodeDisplayPlan(plan string) string {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return ""
	}
	words := strings.FieldsFunc(plan, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
