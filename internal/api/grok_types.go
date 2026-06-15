package api

import (
	"encoding/json"
	"sort"
	"time"
)

// Grok cent wrapper (monetary/usage values are { "val": cents } in the RPC response).
type GrokCent struct {
	Val *int `json:"val"`
}

// GrokBillingCycle holds the period.
type GrokBillingCycle struct {
	BillingPeriodStart string `json:"billingPeriodStart"`
	BillingPeriodEnd   string `json:"billingPeriodEnd"`
}

// GrokBillingUsage holds included/on-demand/total used.
type GrokBillingUsage struct {
	IncludedUsed *GrokCent `json:"includedUsed"`
	OnDemandUsed *GrokCent `json:"onDemandUsed"`
	TotalUsed    *GrokCent `json:"totalUsed"`
}

// GrokBillingResponse is the shape returned by `x.ai/billing` RPC (and synthesized from web probe).
// All monetary are cents via GrokCent.
type GrokBillingResponse struct {
	BillingCycle    *GrokBillingCycle `json:"billingCycle"`
	MonthlyLimit    *GrokCent         `json:"monthlyLimit"`
	OnDemandCap     *GrokCent         `json:"onDemandCap"`
	OnDemandEnabled *bool             `json:"on_demand_enabled"`
	DisabledByConfig *bool            `json:"disabledByConfig"`
	Usage           *GrokBillingUsage `json:"usage"`
}

// GrokWebBillingSnapshot is the normalized result from the gRPC-web fallback.
type GrokWebBillingSnapshot struct {
	UsedPercent float64
	ResetsAt    *time.Time
}

// GrokLocalSessionSummary aggregates ~/.grok/sessions/.../signals.json (informational only).
type GrokLocalSessionSummary struct {
	SessionCount  int
	TotalTokens   int
	LastSessionAt *time.Time
	PrimaryModel  string
	Models        []string
}

// GrokQuota is one normalized credit-style quota (primarily "credits").
type GrokQuota struct {
	Name        string
	Utilization float64
	ResetsAt    *time.Time
	Status      string
}

// GrokSnapshot is the storage + UI representation.
type GrokSnapshot struct {
	ID             int64
	CapturedAt     time.Time
	AccountID      int64 // default 1 for single-account
	Email          string
	TeamID         string
	LoginMethod    string
	Quotas         []GrokQuota
	RawJSON        string
	// LocalSessions is informational fallback data (may be nil).
	LocalSessions *GrokLocalSessionSummary
}

var grokDisplayNames = map[string]string{
	"credits": "Credits",
}

// GrokDisplayName returns a UI label for a grok quota key.
func GrokDisplayName(key string) string {
	if name, ok := grokDisplayNames[key]; ok {
		return name
	}
	return key
}

// grokStatusFromUtilization maps 0-100 utilization to status badge (same thresholds as Codex).
func grokStatusFromUtilization(util float64) string {
	switch {
	case util >= 95:
		return "critical"
	case util >= 80:
		return "danger"
	case util >= 50:
		return "warning"
	default:
		return "healthy"
	}
}

const (
	grokCreditsQuotaName = "credits"
)

// ParseGrokBillingResponse parses the JSON from RPC.
func ParseGrokBillingResponse(data []byte) (*GrokBillingResponse, error) {
	var resp GrokBillingResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// billingToUsedPercentAndReset extracts the primary values used for the snapshot.
func billingToUsedPercentAndReset(r *GrokBillingResponse) (used float64, resets *time.Time) {
	if r == nil {
		return 0, nil
	}
	limit := 0
	if r.MonthlyLimit != nil && r.MonthlyLimit.Val != nil {
		limit = *r.MonthlyLimit.Val
	}
	usedCents := 0
	if r.Usage != nil && r.Usage.TotalUsed != nil && r.Usage.TotalUsed.Val != nil {
		usedCents = *r.Usage.TotalUsed.Val
	}
	if limit > 0 {
		used = float64(usedCents) / float64(limit) * 100.0
		if used > 100 {
			used = 100
		}
	}
	if r.BillingCycle != nil && r.BillingCycle.BillingPeriodEnd != "" {
		if t, err := time.Parse(time.RFC3339, r.BillingCycle.BillingPeriodEnd); err == nil {
			resets = &t
		} else if t, err := time.Parse(time.RFC3339Nano, r.BillingCycle.BillingPeriodEnd); err == nil {
			resets = &t
		}
	}
	return used, resets
}

// ToSnapshot converts a billing response + identity into a GrokSnapshot.
// webSnap may be used as fallback when RPC billing is unavailable.
func (r *GrokBillingResponse) ToSnapshot(capturedAt time.Time, email, teamID, loginMethod string, webSnap *GrokWebBillingSnapshot) *GrokSnapshot {
	snap := &GrokSnapshot{
		CapturedAt:  capturedAt.UTC(),
		AccountID:   1,
		Email:       email,
		TeamID:      teamID,
		LoginMethod: loginMethod,
	}

	used, resets := billingToUsedPercentAndReset(r)
	if webSnap != nil && used == 0 && webSnap.UsedPercent > 0 {
		used = webSnap.UsedPercent
		resets = webSnap.ResetsAt
	}

	if used > 0 || resets != nil {
		q := GrokQuota{
			Name:        grokCreditsQuotaName,
			Utilization: used,
			ResetsAt:    resets,
			Status:      grokStatusFromUtilization(used),
		}
		snap.Quotas = append(snap.Quotas, q)
	}

	if raw, err := json.Marshal(r); err == nil {
		snap.RawJSON = string(raw)
	}
	return snap
}

// FromWebBilling creates a minimal snapshot from the gRPC-web probe result + creds.
func FromWebBilling(web *GrokWebBillingSnapshot, capturedAt time.Time, email, teamID, loginMethod string) *GrokSnapshot {
	snap := &GrokSnapshot{
		CapturedAt:  capturedAt.UTC(),
		AccountID:   1,
		Email:       email,
		TeamID:      teamID,
		LoginMethod: loginMethod,
	}
	if web != nil {
		u := web.UsedPercent
		if u > 100 {
			u = 100
		}
		q := GrokQuota{
			Name:        grokCreditsQuotaName,
			Utilization: u,
			ResetsAt:    web.ResetsAt,
			Status:      grokStatusFromUtilization(u),
		}
		snap.Quotas = append(snap.Quotas, q)
	}
	return snap
}

// SortQuotas ensures stable order (credits first).
func SortQuotas(quotas []GrokQuota) {
	sort.Slice(quotas, func(i, j int) bool {
		if quotas[i].Name == grokCreditsQuotaName {
			return true
		}
		if quotas[j].Name == grokCreditsQuotaName {
			return false
		}
		return quotas[i].Name < quotas[j].Name
	})
}
