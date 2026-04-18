package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
	DisplayThreshold  int                    `json:"displayThreshold"`
	DisplayMessage    string                 `json:"displayMessage"`
}

type CursorPlanUsage struct {
	TotalSpend       int     `json:"totalSpend"`
	IncludedSpend    int     `json:"includedSpend"`
	BonusSpend       int     `json:"bonusSpend"`
	Remaining        int     `json:"remaining"`
	Limit            int     `json:"limit"`
	RemainingBonus   bool    `json:"remainingBonus"`
	AutoPercentUsed  float64 `json:"autoPercentUsed"`
	ApiPercentUsed   float64 `json:"apiPercentUsed"`
	TotalPercentUsed float64 `json:"totalPercentUsed"`
}

type CursorSpendLimitUsage struct {
	TotalSpend          int    `json:"totalSpend"`
	PooledLimit         int    `json:"pooledLimit"`
	PooledUsed          int    `json:"pooledUsed"`
	PooledRemaining     int    `json:"pooledRemaining"`
	IndividualLimit     int    `json:"individualLimit"`
	IndividualUsed      int    `json:"individualUsed"`
	IndividualRemaining int    `json:"individualRemaining"`
	LimitType           string `json:"limitType"`
}

type CursorPlanInfoResponse struct {
	PlanInfo CursorPlanInfo `json:"planInfo"`
}

type CursorPlanInfo struct {
	PlanName            string `json:"planName"`
	IncludedAmountCents int    `json:"includedAmountCents"`
	Price               string `json:"price"`
	BillingCycleEnd     string `json:"billingCycleEnd"`
}

type CursorCreditGrantsResponse struct {
	HasCreditGrants bool   `json:"hasCreditGrants"`
	TotalCents      string `json:"totalCents"`
	UsedCents       string `json:"usedCents"`
}

type CursorStripeResponse struct {
	MembershipType     string `json:"membershipType"`
	SubscriptionStatus string `json:"subscriptionStatus"`
	CustomerBalance    int    `json:"customerBalance"`
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
	NumRequests     int `json:"numRequests"`
	MaxRequestUsage int `json:"maxRequestUsage"`
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
	return &resp, nil
}

func ParseCursorPlanInfoResponse(data []byte) (*CursorPlanInfoResponse, error) {
	var resp CursorPlanInfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func ParseCursorCreditGrantsResponse(data []byte) (*CursorCreditGrantsResponse, error) {
	var resp CursorCreditGrantsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func ParseCursorStripeResponse(data []byte) (*CursorStripeResponse, error) {
	var resp CursorStripeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
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

func ParseCursorRequestUsageResponse(data []byte) (*CursorRequestUsageResponse, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	resp := &CursorRequestUsageResponse{}
	if v, ok := raw["startOfMonth"]; ok {
		_ = json.Unmarshal(v, &resp.StartOfMonth)
	}

	resp.Models = make(map[string]CursorModelUsage)
	for key, val := range raw {
		if key == "startOfMonth" {
			continue
		}
		var usage CursorModelUsage
		if err := json.Unmarshal(val, &usage); err == nil {
			resp.Models[key] = usage
		}
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
		if pu.TotalPercentUsed != 0 || pu.Limit > 0 {
			percentUsed := pu.TotalPercentUsed
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
		if pu.TotalPercentUsed != 0 || pu.Limit > 0 {
			percentUsed := pu.TotalPercentUsed
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
		Utilization: pu.AutoPercentUsed,
		Format:      CursorFormatPercent,
		ResetsAt:    billingCycleEnd,
	})

	quotas = append(quotas, CursorQuota{
		Name:        "api_usage",
		Utilization: pu.ApiPercentUsed,
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
			stripeBalanceCents = -stripeResp.CustomerBalance
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

	if normalizedPlan == "team" ||
		(usage != nil && usage.SpendLimitUsage != nil && usage.SpendLimitUsage.LimitType == "team") ||
		(usage != nil && usage.SpendLimitUsage != nil && usage.SpendLimitUsage.PooledLimit > 0) {
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
