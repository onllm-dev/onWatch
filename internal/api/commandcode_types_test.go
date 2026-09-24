package api

import (
	"encoding/json"
	"testing"
	"time"
)

const commandCodeCreditsFixture = `{
  "credits": {"belowThreshold": false, "monthlyCredits": 50.9376066337, "purchasedCredits": 0, "freeCredits": 0},
  "windowLimits": {
    "limited": true,
    "fiveHour": {"used": 0.984914014, "cap": 14, "exceeded": false, "resetAt": 1790262790822},
    "weekly":   {"used": 19.0623933663, "cap": 35, "exceeded": false, "resetAt": 1790429616789}
  }
}`

const commandCodeSubscriptionFixture = `{
  "success": true,
  "data": {
    "id": "sub_1", "status": "active", "planId": "individual-goat",
    "currentPeriodStart": "2026-09-19T12:53:45.000Z",
    "currentPeriodEnd": "2026-10-19T12:53:45.000Z"
  }
}`

const commandCodeSummaryFixture = `{
  "totalCount": 6197, "totalCost": 19.010284393300005, "successRate": 100,
  "totalTokensIn": 1055462381, "totalTokensOut": 11143544, "totalTokens": 1066605925,
  "periodBasis": "billing-period"
}`

const commandCodeWhoamiFixture = `{"success":true,"user":{"id":"u_1","name":"Prakersh Maheshwari","email":"p@example.com","userName":"prakersh"},"org":null}`

func TestParseCommandCodeFlexFloatForms(t *testing.T) {
	data := []byte(`{"a":1.5,"b":"2.25","c":"","d":null,"e":"$3.50"}`)
	var out struct {
		A commandCodeFlexFloat `json:"a"`
		B commandCodeFlexFloat `json:"b"`
		C commandCodeFlexFloat `json:"c"`
		D commandCodeFlexFloat `json:"d"`
		E commandCodeFlexFloat `json:"e"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]float64{"a": 1.5, "b": 2.25, "c": 0, "d": 0, "e": 3.5}
	got := map[string]float64{"a": float64(out.A), "b": float64(out.B), "c": float64(out.C), "d": float64(out.D), "e": float64(out.E)}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestCommandCodeFlexTimeEpochMillis(t *testing.T) {
	// 1790262790822 ms -> 2026-09-24ish UTC seconds.
	got := commandCodeEpochToTime(1790262790822)
	if got == nil {
		t.Fatal("expected a time")
	}
	if got.Unix() != 1790262790 {
		t.Fatalf("unix = %d, want 1790262790", got.Unix())
	}
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
}

func TestCommandCodeFlexTimeEpochSecondsAndZero(t *testing.T) {
	if got := commandCodeEpochToTime(1790262790); got == nil || got.Unix() != 1790262790 {
		t.Fatalf("epoch seconds = %v", got)
	}
	if got := commandCodeEpochToTime(0); got != nil {
		t.Fatalf("zero must yield nil, got %v", got)
	}
	if got := commandCodeEpochToTime(-5); got != nil {
		t.Fatalf("negative must yield nil, got %v", got)
	}
}

func TestParseCommandCodeTimeString(t *testing.T) {
	iso := parseCommandCodeTimeString("2026-10-19T12:53:45.000Z")
	if iso == nil || iso.UTC().Format(time.RFC3339) != "2026-10-19T12:53:45Z" {
		t.Fatalf("iso = %v", iso)
	}
	epoch := parseCommandCodeTimeString("1790262790822")
	if epoch == nil || epoch.Unix() != 1790262790 {
		t.Fatalf("epoch string = %v", epoch)
	}
	if got := parseCommandCodeTimeString(""); got != nil {
		t.Fatalf("empty must yield nil, got %v", got)
	}
	if got := parseCommandCodeTimeString("not-a-date"); got != nil {
		t.Fatalf("garbage must yield nil, got %v", got)
	}
}

func TestBuildCommandCodeSnapshotFull(t *testing.T) {
	whoami, err := ParseCommandCodeWhoami([]byte(commandCodeWhoamiFixture))
	if err != nil {
		t.Fatal(err)
	}
	credits, err := ParseCommandCodeCredits([]byte(commandCodeCreditsFixture))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := ParseCommandCodeSubscription([]byte(commandCodeSubscriptionFixture))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := ParseCommandCodeSummary([]byte(commandCodeSummaryFixture))
	if err != nil {
		t.Fatal(err)
	}

	captured := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	snap := BuildCommandCodeSnapshot(whoami, credits, sub, summary, "{}", captured)

	if snap.AccountName != "prakersh" {
		t.Errorf("account = %q", snap.AccountName)
	}
	if snap.AccountID != "u_1" || snap.OrgID != "" {
		t.Errorf("ids = %q/%q", snap.AccountID, snap.OrgID)
	}
	if snap.Plan != "individual-goat" || snap.Status != "active" {
		t.Errorf("plan/status = %q/%q", snap.Plan, snap.Status)
	}
	if snap.PeriodStart == nil || snap.PeriodEnd == nil {
		t.Fatalf("period bounds missing: %+v", snap)
	}
	if snap.PeriodEnd.UTC().Format(time.RFC3339) != "2026-10-19T12:53:45Z" {
		t.Errorf("period end = %v", snap.PeriodEnd)
	}

	if snap.MonthlyCredits != 50.9376066337 || snap.PurchasedCredits != 0 || snap.FreeCredits != 0 {
		t.Errorf("credits = %+v", snap)
	}
	if snap.RemainingCredits != snap.MonthlyCredits+snap.PurchasedCredits+snap.FreeCredits {
		t.Errorf("remaining = %v", snap.RemainingCredits)
	}
	if snap.PeriodReqs != 6197 || snap.PeriodTokens != 1066605925 {
		t.Errorf("usage totals = %d/%d", snap.PeriodReqs, snap.PeriodTokens)
	}

	if len(snap.Quotas) != 3 {
		t.Fatalf("quotas = %d, want five_hour, weekly, monthly", len(snap.Quotas))
	}

	fiveHour := snap.Quotas[0]
	if fiveHour.Name != CommandCodeQuotaFiveHour {
		t.Fatalf("first quota = %q", fiveHour.Name)
	}
	if fiveHour.Limit != 14 {
		t.Errorf("5h cap = %v", fiveHour.Limit)
	}
	wantPct := 0.984914014 / 14 * 100
	if diff := fiveHour.Utilization - wantPct; diff > 0.001 || diff < -0.001 {
		t.Errorf("5h util = %v, want %v", fiveHour.Utilization, wantPct)
	}
	if fiveHour.Format != CommandCodeQuotaFormatCredits {
		t.Errorf("5h format = %q", fiveHour.Format)
	}
	if fiveHour.ResetsAt == nil || fiveHour.ResetsAt.Unix() != 1790262790 {
		t.Errorf("5h reset = %v", fiveHour.ResetsAt)
	}

	weekly := snap.Quotas[1]
	if weekly.Name != CommandCodeQuotaWeekly || weekly.Limit != 35 {
		t.Errorf("weekly = %+v", weekly)
	}

	monthly := snap.Quotas[2]
	if monthly.Name != CommandCodeQuotaMonthly {
		t.Fatalf("third quota = %q", monthly.Name)
	}
	if monthly.Format != CommandCodeQuotaFormatCurrency {
		t.Errorf("monthly format = %q", monthly.Format)
	}
	// 50.94 remaining + 19.01 billed = 69.95 granted.
	wantGrant := snap.RemainingCredits + snap.PeriodCostUSD
	if monthly.Limit != wantGrant {
		t.Errorf("monthly grant = %v, want %v", monthly.Limit, wantGrant)
	}
	if monthly.Remaining != snap.RemainingCredits {
		t.Errorf("monthly remaining = %v, want %v", monthly.Remaining, snap.RemainingCredits)
	}
	if monthly.Used != snap.PeriodCostUSD {
		t.Errorf("monthly used = %v, want billed cost %v", monthly.Used, snap.PeriodCostUSD)
	}
	wantMonthlyPct := snap.PeriodCostUSD / wantGrant * 100
	if diff := monthly.Utilization - wantMonthlyPct; diff > 0.01 || diff < -0.01 {
		t.Errorf("monthly util = %v, want %v", monthly.Utilization, wantMonthlyPct)
	}
	if monthly.ResetsAt == nil || !monthly.ResetsAt.Equal(*snap.PeriodEnd) {
		t.Errorf("monthly reset = %v, want period end", monthly.ResetsAt)
	}
}

func TestBuildCommandCodeSnapshotSkipsCappedFreeWindows(t *testing.T) {
	// A window with used == cap == 0 has no meaningful percentage: the
	// reference provider drops it, and so must we, or the card shows a
	// permanent 0% bar for a window the account does not have.
	data := []byte(`{"credits":{"monthlyCredits":10},"windowLimits":{"fiveHour":{"used":0,"cap":0,"resetAt":null},"weekly":{"used":0,"cap":0,"resetAt":null}}}`)
	credits, err := ParseCommandCodeCredits(data)
	if err != nil {
		t.Fatal(err)
	}
	snap := BuildCommandCodeSnapshot(nil, credits, nil, nil, "{}", time.Now())
	for _, q := range snap.Quotas {
		if q.Name == CommandCodeQuotaFiveHour || q.Name == CommandCodeQuotaWeekly {
			t.Fatalf("window %s must be dropped when it has no cap", q.Name)
		}
	}
	if len(snap.Quotas) != 1 || snap.Quotas[0].Name != CommandCodeQuotaMonthly {
		t.Fatalf("quotas = %+v", snap.Quotas)
	}
}

func TestBuildCommandCodeSnapshotMonthlyWithoutSummary(t *testing.T) {
	// Without a usage summary the granted credits cannot be derived. The card
	// must fall back to the balance rather than reporting 0% of 0.
	data := []byte(`{"credits":{"monthlyCredits":12,"purchasedCredits":3,"freeCredits":1}}`)
	credits, err := ParseCommandCodeCredits(data)
	if err != nil {
		t.Fatal(err)
	}
	snap := BuildCommandCodeSnapshot(nil, credits, nil, nil, "{}", time.Now())
	if snap.RemainingCredits != 16 {
		t.Fatalf("remaining = %v, want 16", snap.RemainingCredits)
	}
	monthly := snap.Quotas[0]
	if monthly.Limit != 0 || monthly.Utilization != 0 {
		t.Errorf("monthly must have no cap/percent without a summary: %+v", monthly)
	}
	if monthly.Remaining != 16 {
		t.Errorf("monthly remaining = %v, want 16", monthly.Remaining)
	}
}

func TestBuildCommandCodeSnapshotNoCreditsSection(t *testing.T) {
	sub, err := ParseCommandCodeSubscription([]byte(commandCodeSubscriptionFixture))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := ParseCommandCodeSummary([]byte(commandCodeSummaryFixture))
	if err != nil {
		t.Fatal(err)
	}
	snap := BuildCommandCodeSnapshot(nil, nil, sub, summary, "{}", time.Now())
	if len(snap.Quotas) != 0 {
		t.Fatalf("expected no quotas without a credits section, got %+v", snap.Quotas)
	}
	if snap.Plan != "individual-goat" {
		t.Errorf("plan should still be captured: %q", snap.Plan)
	}
}

func TestBuildCommandCodeSnapshotClampsUtilization(t *testing.T) {
	data := []byte(`{"credits":{"monthlyCredits":5},"windowLimits":{"fiveHour":{"used":30,"cap":14,"resetAt":null}}}`)
	credits, err := ParseCommandCodeCredits(data)
	if err != nil {
		t.Fatal(err)
	}
	snap := BuildCommandCodeSnapshot(nil, credits, nil, nil, "{}", time.Now())
	if snap.Quotas[0].Utilization != 100 {
		t.Fatalf("util = %v, want clamped to 100", snap.Quotas[0].Utilization)
	}
}

func TestCommandCodeAccountNamePrecedence(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"username wins", `{"user":{"userName":"pk","name":"Prakersh Maheshwari"},"org":{"login":"acme"}}`, "pk"},
		{"full name fallback", `{"user":{"name":"Prakersh Maheshwari"},"org":{"login":"acme"}}`, "Prakersh Maheshwari"},
		{"org login last resort", `{"user":{},"org":{"login":"acme"}}`, "acme"},
		{"nothing", `{"user":{}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ParseCommandCodeWhoami([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if got := CommandCodeAccountName(resp); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := CommandCodeAccountName(nil); got != "" {
		t.Fatalf("nil response = %q", got)
	}
}

func TestCommandCodeDisplayPlan(t *testing.T) {
	cases := map[string]string{
		"individual-goat": "Individual Goat",
		"team_plan":       "Team Plan",
		"pro":             "Pro",
		"":                "",
		"  spaced-out  ":  "Spaced Out",
		"multi-part-name": "Multi Part Name",
		"UPPER":           "UPPER",
	}
	for in, want := range cases {
		if got := CommandCodeDisplayPlan(in); got != want {
			t.Errorf("CommandCodeDisplayPlan(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCommandCodeBodyErrors(t *testing.T) {
	if _, err := ParseCommandCodeCredits([]byte("{not json")); err == nil {
		t.Error("credits: expected an error")
	}
	if _, err := ParseCommandCodeSubscription([]byte("{not json")); err == nil {
		t.Error("subscription: expected an error")
	}
	if _, err := ParseCommandCodeSummary([]byte("{not json")); err == nil {
		t.Error("summary: expected an error")
	}
	if _, err := ParseCommandCodeWhoami([]byte("{not json")); err == nil {
		t.Error("whoami: expected an error")
	}
}
