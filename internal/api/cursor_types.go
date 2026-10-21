package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

var errInvalidCursorNumber = errors.New("invalid cursor numeric value")

// cursorFlexInt is an int64 that accepts both JSON numbers and quoted numeric
// strings. The Cursor usage API has drifted to encoding cent amounts such as
// pooledLimit as strings ("50000"), which previously aborted the whole decode.
// An empty string, an empty value or null is treated as zero.
type cursorFlexInt int64

func (v *cursorFlexInt) UnmarshalJSON(data []byte) error {
	n, err := parseCursorFlexInt(data)
	if err != nil {
		return err
	}
	*v = cursorFlexInt(n)
	return nil
}

// cursorFlexFloat is the float64 counterpart, used for percentage fields.
type cursorFlexFloat float64

func (v *cursorFlexFloat) UnmarshalJSON(data []byte) error {
	f, err := parseCursorFlexFloat(data)
	if err != nil {
		return err
	}
	*v = cursorFlexFloat(f)
	return nil
}

// cursorNumberToken normalizes a raw JSON token to the string to parse. Null
// and an empty quoted string both yield "", which callers treat as zero.
func cursorNumberToken(data []byte) (string, error) {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return "", errInvalidCursorNumber
		}
		return strings.TrimSpace(str), nil
	}
	return s, nil
}

func parseCursorFlexInt(data []byte) (int64, error) {
	s, err := cursorNumberToken(data)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	// Cursor occasionally sends decimals in fields that are integral. Round
	// rather than fail so a single drifted field does not hide the provider.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errInvalidCursorNumber
	}
	r := math.Round(f)
	if r >= float64(math.MaxInt64) || r < float64(math.MinInt64) {
		return 0, errInvalidCursorNumber
	}
	return int64(r), nil
}

func parseCursorFlexFloat(data []byte) (float64, error) {
	s, err := cursorNumberToken(data)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errInvalidCursorNumber
	}
	return f, nil
}

// cursorBadFields collects numeric fields that could not be parsed so a
// response can degrade them to zero with one warning instead of failing.
type cursorBadFields struct {
	count int
}

func (b *cursorBadFields) intField(raw json.RawMessage, dst *cursorFlexInt) {
	if len(raw) == 0 {
		return
	}
	n, err := parseCursorFlexInt(raw)
	if err != nil {
		b.count++
		return
	}
	*dst = cursorFlexInt(n)
}

func (b *cursorBadFields) floatField(raw json.RawMessage, dst *cursorFlexFloat) {
	if len(raw) == 0 {
		return
	}
	f, err := parseCursorFlexFloat(raw)
	if err != nil {
		b.count++
		return
	}
	*dst = cursorFlexFloat(f)
}

// intStringField decodes a cent amount that is normally a quoted string but
// may arrive as a bare JSON number, keeping the existing string representation
// so downstream ParseIntString call sites are unchanged.
func (b *cursorBadFields) intStringField(raw json.RawMessage, dst *string) {
	if len(raw) == 0 {
		return
	}
	s, err := cursorNumberToken(raw)
	if err != nil {
		b.count++
		return
	}
	if s == "" {
		*dst = ""
		return
	}
	n, err := parseCursorFlexInt(raw)
	if err != nil {
		b.count++
		return
	}
	*dst = strconv.FormatInt(n, 10)
}

type CursorAccountType string

const (
	CursorAccountIndividual CursorAccountType = "individual"
	CursorAccountTeam       CursorAccountType = "team"
	CursorAccountEnterprise CursorAccountType = "enterprise"
)

type CursorQuotaFormat string

const (
	CursorFormatPercent CursorQuotaFormat = "percent"
	CursorFormatDollars CursorQuotaFormat = "dollars"
	CursorFormatCount   CursorQuotaFormat = "count"
)

type CursorQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      CursorQuotaFormat
	ResetsAt    *time.Time
}

type CursorSnapshot struct {
	ID          int64
	CapturedAt  time.Time
	AccountType CursorAccountType
	PlanName    string
	Quotas      []CursorQuota
	RawJSON     string
}

type CursorUsageResponse struct {
	BillingCycleStart string                 `json:"billingCycleStart"`
	BillingCycleEnd   string                 `json:"billingCycleEnd"`
	PlanUsage         *CursorPlanUsage       `json:"planUsage"`
	SpendLimitUsage   *CursorSpendLimitUsage `json:"spendLimitUsage"`
	Enabled           bool                   `json:"enabled"`
	DisplayThreshold  cursorFlexInt          `json:"displayThreshold"`
	DisplayMessage    string                 `json:"displayMessage"`

	badNumericFields int
}

func (u *CursorUsageResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	decodeCursorString(raw["billingCycleStart"], &u.BillingCycleStart)
	decodeCursorString(raw["billingCycleEnd"], &u.BillingCycleEnd)
	decodeCursorString(raw["displayMessage"], &u.DisplayMessage)
	if v, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(v, &u.Enabled); err != nil {
			bad.count++
		}
	}
	bad.intField(raw["displayThreshold"], &u.DisplayThreshold)

	if v, ok := raw["planUsage"]; ok && !isCursorJSONNull(v) {
		u.PlanUsage = &CursorPlanUsage{}
		if err := json.Unmarshal(v, u.PlanUsage); err != nil {
			u.PlanUsage = nil
			bad.count++
		} else {
			bad.count += u.PlanUsage.badNumericFields
		}
	}
	if v, ok := raw["spendLimitUsage"]; ok && !isCursorJSONNull(v) {
		u.SpendLimitUsage = &CursorSpendLimitUsage{}
		if err := json.Unmarshal(v, u.SpendLimitUsage); err != nil {
			u.SpendLimitUsage = nil
			bad.count++
		} else {
			bad.count += u.SpendLimitUsage.badNumericFields
		}
	}

	u.badNumericFields = bad.count
	return nil
}

func isCursorJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func decodeCursorString(raw json.RawMessage, dst *string) {
	if len(raw) == 0 {
		return
	}
	_ = json.Unmarshal(raw, dst)
}

type CursorPlanUsage struct {
	TotalSpend       cursorFlexInt   `json:"totalSpend"`
	IncludedSpend    cursorFlexInt   `json:"includedSpend"`
	BonusSpend       cursorFlexInt   `json:"bonusSpend"`
	Remaining        cursorFlexInt   `json:"remaining"`
	Limit            cursorFlexInt   `json:"limit"`
	RemainingBonus   bool            `json:"remainingBonus"`
	AutoPercentUsed  cursorFlexFloat `json:"autoPercentUsed"`
	ApiPercentUsed   cursorFlexFloat `json:"apiPercentUsed"`
	TotalPercentUsed cursorFlexFloat `json:"totalPercentUsed"`

	badNumericFields int
}

func (p *CursorPlanUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	bad.intField(raw["totalSpend"], &p.TotalSpend)
	bad.intField(raw["includedSpend"], &p.IncludedSpend)
	bad.intField(raw["bonusSpend"], &p.BonusSpend)
	bad.intField(raw["remaining"], &p.Remaining)
	bad.intField(raw["limit"], &p.Limit)
	bad.floatField(raw["autoPercentUsed"], &p.AutoPercentUsed)
	bad.floatField(raw["apiPercentUsed"], &p.ApiPercentUsed)
	bad.floatField(raw["totalPercentUsed"], &p.TotalPercentUsed)
	if v, ok := raw["remainingBonus"]; ok {
		_ = json.Unmarshal(v, &p.RemainingBonus)
	}

	p.badNumericFields = bad.count
	return nil
}

type CursorSpendLimitUsage struct {
	TotalSpend          cursorFlexInt `json:"totalSpend"`
	PooledLimit         cursorFlexInt `json:"pooledLimit"`
	PooledUsed          cursorFlexInt `json:"pooledUsed"`
	PooledRemaining     cursorFlexInt `json:"pooledRemaining"`
	IndividualLimit     cursorFlexInt `json:"individualLimit"`
	IndividualUsed      cursorFlexInt `json:"individualUsed"`
	IndividualRemaining cursorFlexInt `json:"individualRemaining"`
	LimitType           string        `json:"limitType"`

	badNumericFields int
}

func (s *CursorSpendLimitUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	bad.intField(raw["totalSpend"], &s.TotalSpend)
	bad.intField(raw["pooledLimit"], &s.PooledLimit)
	bad.intField(raw["pooledUsed"], &s.PooledUsed)
	bad.intField(raw["pooledRemaining"], &s.PooledRemaining)
	bad.intField(raw["individualLimit"], &s.IndividualLimit)
	bad.intField(raw["individualUsed"], &s.IndividualUsed)
	bad.intField(raw["individualRemaining"], &s.IndividualRemaining)
	decodeCursorString(raw["limitType"], &s.LimitType)

	s.badNumericFields = bad.count
	return nil
}

type CursorPlanInfoResponse struct {
	PlanInfo CursorPlanInfo `json:"planInfo"`
}

type CursorPlanInfo struct {
	PlanName            string        `json:"planName"`
	IncludedAmountCents cursorFlexInt `json:"includedAmountCents"`
	Price               string        `json:"price"`
	BillingCycleEnd     string        `json:"billingCycleEnd"`

	badNumericFields int
}

func (p *CursorPlanInfo) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	decodeCursorString(raw["planName"], &p.PlanName)
	decodeCursorString(raw["price"], &p.Price)
	decodeCursorString(raw["billingCycleEnd"], &p.BillingCycleEnd)
	bad.intField(raw["includedAmountCents"], &p.IncludedAmountCents)

	p.badNumericFields = bad.count
	return nil
}

type CursorCreditGrantsResponse struct {
	HasCreditGrants bool   `json:"hasCreditGrants"`
	TotalCents      string `json:"totalCents"`
	UsedCents       string `json:"usedCents"`

	badNumericFields int
}

func (c *CursorCreditGrantsResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	if v, ok := raw["hasCreditGrants"]; ok {
		_ = json.Unmarshal(v, &c.HasCreditGrants)
	}
	bad.intStringField(raw["totalCents"], &c.TotalCents)
	bad.intStringField(raw["usedCents"], &c.UsedCents)

	c.badNumericFields = bad.count
	return nil
}

type CursorStripeResponse struct {
	MembershipType     string        `json:"membershipType"`
	SubscriptionStatus string        `json:"subscriptionStatus"`
	CustomerBalance    cursorFlexInt `json:"customerBalance"`

	badNumericFields int
}

func (s *CursorStripeResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	decodeCursorString(raw["membershipType"], &s.MembershipType)
	decodeCursorString(raw["subscriptionStatus"], &s.SubscriptionStatus)
	bad.intField(raw["customerBalance"], &s.CustomerBalance)

	s.badNumericFields = bad.count
	return nil
}

type CursorOAuthResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ShouldLogout bool   `json:"shouldLogout"`
}

type CursorRequestUsageResponse struct {
	StartOfMonth string                      `json:"startOfMonth"`
	Models       map[string]CursorModelUsage `json:"-"`
}

type CursorModelUsage struct {
	NumRequests     cursorFlexInt `json:"numRequests"`
	MaxRequestUsage cursorFlexInt `json:"maxRequestUsage"`

	badNumericFields int
}

func (m *CursorModelUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var bad cursorBadFields
	bad.intField(raw["numRequests"], &m.NumRequests)
	bad.intField(raw["maxRequestUsage"], &m.MaxRequestUsage)

	m.badNumericFields = bad.count
	return nil
}

type CursorCredentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	ExpiresIn    time.Duration
	Source       string // "sqlite" or "keychain"
}

func (c *CursorCredentials) IsExpiringSoon(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return c.ExpiresIn < threshold
}

func ParseCursorUsageResponse(data []byte) (*CursorUsageResponse, error) {
	var resp CursorUsageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.badNumericFields > 0 {
		slog.Warn("cursor: usage response had unparseable numeric fields, treating them as zero", "field_count", resp.badNumericFields)
	}
	return &resp, nil
}

func ParseCursorPlanInfoResponse(data []byte) (*CursorPlanInfoResponse, error) {
	var resp CursorPlanInfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.PlanInfo.badNumericFields > 0 {
		slog.Warn("cursor: plan info response had unparseable numeric fields, treating them as zero", "field_count", resp.PlanInfo.badNumericFields)
	}
	return &resp, nil
}

func ParseCursorCreditGrantsResponse(data []byte) (*CursorCreditGrantsResponse, error) {
	var resp CursorCreditGrantsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.badNumericFields > 0 {
		slog.Warn("cursor: credit grants response had unparseable numeric fields, treating them as zero", "field_count", resp.badNumericFields)
	}
	return &resp, nil
}

func ParseCursorStripeResponse(data []byte) (*CursorStripeResponse, error) {
	var resp CursorStripeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.badNumericFields > 0 {
		slog.Warn("cursor: stripe response had unparseable numeric fields, treating them as zero", "field_count", resp.badNumericFields)
	}
	return &resp, nil
}

func ParseCursorOAuthResponse(data []byte) (*CursorOAuthResponse, error) {
	var resp CursorOAuthResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseCursorRequestUsageResponse decodes the per-model request usage payload.
// Each model entry is decoded independently so one malformed model does not
// discard the rest.
func ParseCursorRequestUsageResponse(data []byte) (*CursorRequestUsageResponse, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	resp := &CursorRequestUsageResponse{}
	if v, ok := raw["startOfMonth"]; ok {
		_ = json.Unmarshal(v, &resp.StartOfMonth)
	}

	badFields := 0
	skippedEntries := 0
	resp.Models = make(map[string]CursorModelUsage)
	for key, val := range raw {
		if key == "startOfMonth" {
			continue
		}
		var usage CursorModelUsage
		if err := json.Unmarshal(val, &usage); err != nil {
			skippedEntries++
			continue
		}
		badFields += usage.badNumericFields
		resp.Models[key] = usage
	}
	if badFields > 0 || skippedEntries > 0 {
		slog.Warn("cursor: request usage had unparseable entries, treating them as zero",
			"field_count", badFields, "entry_count", skippedEntries)
	}

	return resp, nil
}

func ExtractJWTSubject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return ""
	}

	if idx := strings.LastIndex(sub, "|"); idx >= 0 {
		return sub[idx+1:]
	}
	return sub
}

func ExtractJWTExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return 0
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		return 0
	}
	return int64(exp)
}

func base64URLDecode(s string) ([]byte, error) {
	for len(s)%4 != 0 {
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

func NormalizeCursorPlanName(planName string) string {
	switch strings.ToLower(strings.TrimSpace(planName)) {
	case "team", "business":
		return "team"
	case "enterprise":
		return "enterprise"
	case "pro":
		return "pro"
	case "ultra":
		return "ultra"
	case "free", "free trial":
		return "free"
	default:
		return strings.ToLower(strings.TrimSpace(planName))
	}
}

func ParseUnixMsString(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp string")
	}
	ms, err := ParseIntString(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms), nil
}

func ParseIntString(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	result, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, errInvalidIntString
	}
	return result, nil
}

var errInvalidIntString = errors.New("invalid integer string")

func ToCursorSnapshot(
	usage *CursorUsageResponse,
	planInfo *CursorPlanInfoResponse,
	creditGrants *CursorCreditGrantsResponse,
	stripeResp *CursorStripeResponse,
	requestUsage *CursorRequestUsageResponse,
	useRequestBased bool,
) *CursorSnapshot {
	snapshot := &CursorSnapshot{
		CapturedAt: time.Now().UTC(),
	}

	planName := ""
	if planInfo != nil {
		planName = planInfo.PlanInfo.PlanName
	}
	accountType := DetermineCursorAccountType(planName, usage, useRequestBased)
	snapshot.AccountType = accountType
	snapshot.PlanName = planName

	if accountType == CursorAccountEnterprise && requestUsage != nil {
		snapshot.Quotas = buildEnterpriseQuotas(usage, requestUsage)
	} else {
		snapshot.Quotas = buildStandardQuotas(usage, creditGrants, stripeResp, accountType)
	}

	if raw, err := json.Marshal(usage); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

func buildStandardQuotas(
	usage *CursorUsageResponse,
	creditGrants *CursorCreditGrantsResponse,
	stripeResp *CursorStripeResponse,
	accountType CursorAccountType,
) []CursorQuota {
	var quotas []CursorQuota

	if usage == nil || usage.PlanUsage == nil {
		return quotas
	}

	pu := usage.PlanUsage
	var billingCycleEnd *time.Time
	if usage.BillingCycleEnd != "" {
		if t, err := ParseUnixMsString(usage.BillingCycleEnd); err == nil {
			billingCycleEnd = &t
		}
	}

	switch accountType {
	case CursorAccountIndividual:
		totalPercent := float64(pu.TotalPercentUsed)
		if totalPercent != 0 || pu.Limit > 0 {
			percentUsed := totalPercent
			if percentUsed == 0 && pu.Limit > 0 {
				planUsed := pu.TotalSpend
				if planUsed == 0 {
					planUsed = pu.Limit - pu.Remaining
				}
				percentUsed = float64(planUsed) / float64(pu.Limit) * 100
			}
			quotas = append(quotas, CursorQuota{
				Name:        "total_usage",
				Used:        float64(pu.TotalSpend) / 100,
				Limit:       float64(pu.Limit) / 100,
				Utilization: percentUsed,
				Format:      CursorFormatPercent,
				ResetsAt:    billingCycleEnd,
			})
		}
	case CursorAccountTeam:
		planUsed := pu.TotalSpend
		if planUsed == 0 && pu.Limit > 0 {
			planUsed = pu.Limit - pu.Remaining
		}
		utilization := 0.0
		if pu.Limit > 0 {
			utilization = float64(planUsed) / float64(pu.Limit) * 100
		}
		quotas = append(quotas, CursorQuota{
			Name:        "total_usage",
			Used:        float64(planUsed) / 100,
			Limit:       float64(pu.Limit) / 100,
			Utilization: utilization,
			Format:      CursorFormatDollars,
			ResetsAt:    billingCycleEnd,
		})
	default:
		totalPercent := float64(pu.TotalPercentUsed)
		if totalPercent != 0 || pu.Limit > 0 {
			percentUsed := totalPercent
			if percentUsed == 0 && pu.Limit > 0 {
				planUsed := pu.TotalSpend
				if planUsed == 0 {
					planUsed = pu.Limit - pu.Remaining
				}
				percentUsed = float64(planUsed) / float64(pu.Limit) * 100
			}
			quotas = append(quotas, CursorQuota{
				Name:        "total_usage",
				Used:        float64(pu.TotalSpend) / 100,
				Limit:       float64(pu.Limit) / 100,
				Utilization: percentUsed,
				Format:      CursorFormatPercent,
				ResetsAt:    billingCycleEnd,
			})
		}
	}

	quotas = append(quotas, CursorQuota{
		Name:        "auto_usage",
		Utilization: float64(pu.AutoPercentUsed),
		Format:      CursorFormatPercent,
		ResetsAt:    billingCycleEnd,
	})

	quotas = append(quotas, CursorQuota{
		Name:        "api_usage",
		Utilization: float64(pu.ApiPercentUsed),
		Format:      CursorFormatPercent,
		ResetsAt:    billingCycleEnd,
	})

	if creditGrants != nil && stripeResp != nil {
		grantTotalCents := 0
		if creditGrants.HasCreditGrants {
			if tc, err := ParseIntString(creditGrants.TotalCents); err == nil {
				grantTotalCents = int(tc)
			}
		}
		stripeBalanceCents := 0
		if stripeResp.CustomerBalance < 0 {
			stripeBalanceCents = int(-stripeResp.CustomerBalance)
		}
		combinedTotal := grantTotalCents + stripeBalanceCents
		if combinedTotal > 0 {
			usedCents := 0
			if creditGrants.HasCreditGrants {
				if uc, err := ParseIntString(creditGrants.UsedCents); err == nil {
					usedCents = int(uc)
				}
			}
			quotas = append(quotas, CursorQuota{
				Name:        "credits",
				Used:        float64(usedCents) / 100,
				Limit:       float64(combinedTotal) / 100,
				Utilization: float64(usedCents) / float64(combinedTotal) * 100,
				Format:      CursorFormatDollars,
			})
		}
	}

	if usage.SpendLimitUsage != nil {
		su := usage.SpendLimitUsage
		limit := su.IndividualLimit
		remaining := su.IndividualRemaining
		if limit == 0 && su.PooledLimit > 0 {
			limit = su.PooledLimit
			remaining = su.PooledRemaining
		}
		if limit > 0 {
			used := limit - remaining
			quotas = append(quotas, CursorQuota{
				Name:        "on_demand",
				Used:        float64(used) / 100,
				Limit:       float64(limit) / 100,
				Utilization: float64(used) / float64(limit) * 100,
				Format:      CursorFormatDollars,
			})
		}
	}

	return quotas
}

func DetermineCursorAccountType(planName string, usage *CursorUsageResponse, useRequestBased bool) CursorAccountType {
	normalizedPlan := NormalizeCursorPlanName(planName)
	hasPlanUsageLimit := usage != nil && usage.PlanUsage != nil && usage.PlanUsage.Limit > 0

	limitType := ""
	if usage != nil && usage.SpendLimitUsage != nil {
		limitType = strings.ToLower(strings.TrimSpace(usage.SpendLimitUsage.LimitType))
	}

	if normalizedPlan == "team" || limitType == "team" {
		return CursorAccountTeam
	}

	if normalizedPlan == "enterprise" || (useRequestBased && !hasPlanUsageLimit) {
		return CursorAccountEnterprise
	}

	return CursorAccountIndividual
}

func buildEnterpriseQuotas(usage *CursorUsageResponse, requestUsage *CursorRequestUsageResponse) []CursorQuota {
	var quotas []CursorQuota

	for model, mu := range requestUsage.Models {
		used := mu.NumRequests
		limit := mu.MaxRequestUsage
		utilization := float64(0)
		if limit > 0 {
			utilization = float64(used) / float64(limit) * 100
		}
		quotas = append(quotas, CursorQuota{
			Name:        "requests_" + model,
			Used:        float64(used),
			Limit:       float64(limit),
			Utilization: utilization,
			Format:      CursorFormatCount,
		})
	}

	return quotas
}
