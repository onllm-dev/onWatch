package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestKimiUsagesToSnapshot(t *testing.T) {
	raw := []byte(`{
	  "user": {"userId": "u1", "region": "REGION_CN", "membership": {"level": "LEVEL_INTERMEDIATE"}},
	  "usage": {"limit": "100", "used": "66", "remaining": "34", "resetTime": "2026-07-15T07:13:41.897674Z"},
	  "limits": [{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"}, "detail": {"limit": "100", "remaining": "100", "resetTime": "2026-07-14T16:13:41Z"}}],
	  "totalQuota": {"limit": "100", "remaining": "99"}
	}`)
	var resp KimiUsagesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	snap := resp.ToSnapshot(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	if snap.UserID != "u1" {
		t.Fatalf("user id: %s", snap.UserID)
	}
	if snap.Membership != "LEVEL_INTERMEDIATE" {
		t.Fatalf("membership: %s", snap.Membership)
	}
	if len(snap.Quotas) != 2 {
		t.Fatalf("expected exactly 7-day + 5h, got %d: %+v", len(snap.Quotas), snap.Quotas)
	}
	var weekly, fiveH *KimiQuota
	for i := range snap.Quotas {
		switch snap.Quotas[i].Name {
		case KimiQuotaSevenDay:
			weekly = &snap.Quotas[i]
		case KimiQuota5h:
			fiveH = &snap.Quotas[i]
		default:
			t.Fatalf("unexpected quota name: %s", snap.Quotas[i].Name)
		}
	}
	if weekly == nil {
		t.Fatal("missing seven_day quota")
	}
	if fiveH == nil {
		t.Fatal("missing 5h quota")
	}
	if weekly.Utilization < 65.9 || weekly.Utilization > 66.1 {
		t.Fatalf("seven_day util: %v", weekly.Utilization)
	}
	if weekly.ResetsAt == nil {
		t.Fatal("expected weekly reset time")
	}
	if weekly.Status != "warning" {
		// 66% is warning (>=50)
		t.Fatalf("status: %s", weekly.Status)
	}
	// totalQuota in fixture must not become a card
	for _, q := range snap.Quotas {
		if q.Name == "total" {
			t.Fatal("totalQuota must not be mapped")
		}
	}
}

func TestKimiDisplayName(t *testing.T) {
	if KimiDisplayName(KimiQuotaSevenDay) != "7-day" {
		t.Fatal(KimiDisplayName(KimiQuotaSevenDay))
	}
	if KimiDisplayName("custom") != "custom" {
		t.Fatal(KimiDisplayName("custom"))
	}
}

func TestKimiMembershipDisplayName(t *testing.T) {
	cases := map[string]string{
		"LEVEL_FREE":         "Free",
		"LEVEL_BASIC":        "Adagio",
		"LEVEL_STANDARD":     "Moderato",
		"LEVEL_INTERMEDIATE": "Allegretto",
		"LEVEL_ADVANCED":     "Allegro",
		"LEVEL_PREMIUM":      "Vivace",
		"":                   "",
		"LEVEL_UNKNOWN_FUTURE": "LEVEL_UNKNOWN_FUTURE",
	}
	for in, want := range cases {
		if got := KimiMembershipDisplayName(in); got != want {
			t.Errorf("KimiMembershipDisplayName(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestUtilizationFromRemainingOnly(t *testing.T) {
	d := &KimiUsageDetail{Limit: "100", Remaining: "40"}
	util, limit, used, rem, _, ok := utilizationFromDetail(d)
	if !ok {
		t.Fatal("expected ok")
	}
	if limit != 100 || used != 60 || rem != 40 {
		t.Fatalf("limit=%v used=%v rem=%v", limit, used, rem)
	}
	if util < 59.9 || util > 60.1 {
		t.Fatalf("util=%v", util)
	}
}
