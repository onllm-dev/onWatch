package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseGrokBillingResponse(t *testing.T) {
	raw := []byte(`{
		"billingCycle": {"billingPeriodStart": "2026-05-01T00:00:00Z", "billingPeriodEnd": "2026-06-01T00:00:00Z"},
		"monthlyLimit": {"val": 99900},
		"usage": {"totalUsed": {"val": 12345}}
	}`)
	resp, err := ParseGrokBillingResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.MonthlyLimit == nil || resp.MonthlyLimit.Val == nil || *resp.MonthlyLimit.Val != 99900 {
		t.Errorf("limit not parsed")
	}
	if resp.Usage == nil || resp.Usage.TotalUsed == nil || *resp.Usage.TotalUsed.Val != 12345 {
		t.Errorf("usage not parsed")
	}
}

func TestBillingToUsedPercentAndReset(t *testing.T) {
	limit := 10000
	used := 2500
	resp := &GrokBillingResponse{
		BillingCycle: &GrokBillingCycle{BillingPeriodEnd: "2026-06-15T00:00:00Z"},
		MonthlyLimit: &GrokCent{Val: &limit},
		Usage:        &GrokBillingUsage{TotalUsed: &GrokCent{Val: &used}},
	}
	u, r := billingToUsedPercentAndReset(resp)
	if u < 24.9 || u > 25.1 {
		t.Errorf("usedPercent = %v, want ~25", u)
	}
	if r == nil || r.Format("2006-01-02") != "2026-06-15" {
		t.Errorf("resets = %v", r)
	}
}

func TestGrokBillingResponse_ToSnapshot(t *testing.T) {
	limit := 10000
	used := 8000
	resp := &GrokBillingResponse{
		BillingCycle: &GrokBillingCycle{BillingPeriodEnd: "2026-06-20T00:00:00Z"},
		MonthlyLimit: &GrokCent{Val: &limit},
		Usage:        &GrokBillingUsage{TotalUsed: &GrokCent{Val: &used}},
	}
	snap := resp.ToSnapshot(time.Now(), "user@ex.com", "t1", "SuperGrok", nil)
	if len(snap.Quotas) != 1 {
		t.Fatalf("quotas len = %d", len(snap.Quotas))
	}
	q := snap.Quotas[0]
	if q.Name != "credits" || q.Utilization < 79.9 || q.Utilization > 80.1 {
		t.Errorf("quota: %+v", q)
	}
	if q.Status != "danger" {
		t.Errorf("status = %s, want danger", q.Status)
	}
	if snap.Email != "user@ex.com" {
		t.Error("identity not carried")
	}
	if snap.RawJSON == "" {
		t.Error("raw json not set")
	}
}

func TestFromWebBilling(t *testing.T) {
	pct := 42.0
	reset := time.Now().Add(24 * time.Hour)
	web := &GrokWebBillingSnapshot{UsedPercent: pct, ResetsAt: &reset}
	snap := FromWebBilling(web, time.Now(), "e@x", "team", "SuperGrok")
	if len(snap.Quotas) != 1 || snap.Quotas[0].Utilization != 42.0 {
		t.Errorf("web snap: %+v", snap.Quotas)
	}
}

func TestGrokStatusAndDisplay(t *testing.T) {
	if grokStatusFromUtilization(96) != "critical" {
		t.Error("critical")
	}
	if grokStatusFromUtilization(85) != "danger" {
		t.Error("danger")
	}
	if GrokDisplayName("credits") != "Credits" {
		t.Error("display name")
	}
	if GrokDisplayName("foo") != "foo" {
		t.Error("passthrough")
	}
}

func TestSortQuotas(t *testing.T) {
	qs := []GrokQuota{{Name: "other"}, {Name: "credits"}}
	SortQuotas(qs)
	if qs[0].Name != "credits" {
		t.Error("credits should sort first")
	}
}

func TestGrokWebBillingSnapshot_RoundtripJSON(t *testing.T) {
	// Ensure our web type is json friendly for tests/fixtures
	web := GrokWebBillingSnapshot{UsedPercent: 12.5, ResetsAt: nil}
	b, _ := json.Marshal(web)
	if len(b) == 0 {
		t.Error("marshal web snap")
	}
}
