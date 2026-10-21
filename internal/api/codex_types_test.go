package api

import (
	"testing"
	"time"
)

func TestParseCodexUsageResponse_ToSnapshot_PrimaryAndSecondary(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "pro",
	  "rate_limit": {
	    "primary_window": {"used_percent": 22.5, "reset_at": 1766000000, "limit_window_seconds": 18000},
	    "secondary_window": {"used_percent": 41.0, "reset_at": 1766400000, "limit_window_seconds": 604800}
	  },
	  "code_review_rate_limit": {
	    "primary_window": {"used_percent": 38.0, "reset_at": 1766000000, "limit_window_seconds": 18000}
	  },
	  "credits": {"balance": 123.4}
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}

	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if len(snap.Quotas) != 3 {
		t.Fatalf("quota len = %d, want 3", len(snap.Quotas))
	}

	if snap.Quotas[0].Name != "five_hour" {
		t.Fatalf("first quota name = %q, want five_hour", snap.Quotas[0].Name)
	}
	if snap.Quotas[1].Name != "seven_day" {
		t.Fatalf("second quota name = %q, want seven_day", snap.Quotas[1].Name)
	}
	if snap.Quotas[2].Name != "code_review" {
		t.Fatalf("third quota name = %q, want code_review", snap.Quotas[2].Name)
	}
	if snap.Quotas[0].Utilization != 22.5 {
		t.Fatalf("five_hour utilization = %f, want 22.5", snap.Quotas[0].Utilization)
	}
	if snap.Quotas[2].Utilization != 38.0 {
		t.Fatalf("code_review utilization = %f, want 38.0", snap.Quotas[2].Utilization)
	}
	if snap.Quotas[0].Status != "healthy" {
		t.Fatalf("five_hour status = %q, want healthy", snap.Quotas[0].Status)
	}
	if snap.Quotas[1].Status != "healthy" {
		t.Fatalf("seven_day status = %q, want healthy", snap.Quotas[1].Status)
	}
	if snap.Quotas[2].Status != "healthy" {
		t.Fatalf("code_review status = %q, want healthy", snap.Quotas[2].Status)
	}
	if snap.CreditsBalance == nil || *snap.CreditsBalance != 123.4 {
		t.Fatalf("credits balance missing or incorrect")
	}
	if snap.PlanType != "pro" {
		t.Fatalf("plan type = %q, want pro", snap.PlanType)
	}
	if snap.RawJSON == "" {
		t.Fatal("RawJSON should not be empty")
	}
}

func TestCodexStatusFromUtilization(t *testing.T) {
	tests := []struct {
		name        string
		utilization float64
		want        string
	}{
		{name: "healthy", utilization: 10, want: "healthy"},
		{name: "warning", utilization: 60, want: "warning"},
		{name: "danger", utilization: 85, want: "danger"},
		{name: "critical", utilization: 99, want: "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexStatusFromUtilization(tt.utilization); got != tt.want {
				t.Fatalf("codexStatusFromUtilization(%v) = %q, want %q", tt.utilization, got, tt.want)
			}
		})
	}
}

func TestParseCodexUsageResponse_CreditsBalanceString(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "pro",
	  "rate_limit": {
	    "primary_window": {"used_percent": 22.5, "reset_at": 1766000000, "limit_window_seconds": 18000}
	  },
	  "credits": {"balance": "123.4"}
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}

	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if snap.CreditsBalance == nil || *snap.CreditsBalance != 123.4 {
		t.Fatalf("credits balance missing or incorrect, got %#v", snap.CreditsBalance)
	}
}

func TestParseCodexUsageResponse_ToSnapshot_FreePrimaryMapsToWeekly(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "free",
	  "rate_limit": {
	    "primary_window": {"used_percent": 14.0, "reset_at": 1766400000, "limit_window_seconds": 604800},
	    "secondary_window": null
	  },
	  "code_review_rate_limit": {
	    "primary_window": {"used_percent": 0.0, "reset_at": 1766400000, "limit_window_seconds": 604800}
	  }
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}

	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if len(snap.Quotas) != 2 {
		t.Fatalf("quota len = %d, want 2", len(snap.Quotas))
	}
	if snap.Quotas[0].Name != "seven_day" {
		t.Fatalf("first quota name = %q, want seven_day", snap.Quotas[0].Name)
	}
	if snap.Quotas[1].Name != "code_review" {
		t.Fatalf("second quota name = %q, want code_review", snap.Quotas[1].Name)
	}
}

func TestCodexDisplayName(t *testing.T) {
	if got := CodexDisplayName("five_hour"); got != "5-Hour Limit" {
		t.Fatalf("CodexDisplayName(five_hour) = %q", got)
	}
	if got := CodexDisplayName("seven_day"); got != "Weekly All-Model" {
		t.Fatalf("CodexDisplayName(seven_day) = %q", got)
	}
	if got := CodexDisplayName("code_review"); got != "Review Requests" {
		t.Fatalf("CodexDisplayName(code_review) = %q", got)
	}
	if got := CodexDisplayName("unknown"); got != "unknown" {
		t.Fatalf("CodexDisplayName(unknown) = %q", got)
	}
}

// The reset-credit count sits on the main usage payload, so one oddly typed
// value must not fail the whole decode and stale Codex usage for every
// account. Numbers, quoted numbers and integral floats are accepted; anything
// else degrades that count to unknown while usage still parses.
func TestParseCodexUsageResponse_ResetCreditsCountOddTypes(t *testing.T) {
	const usagePrefix = `{
	  "plan_type": "pro",
	  "rate_limit": {
	    "primary_window": {"used_percent": 25, "reset_at": 1766000000, "limit_window_seconds": 18000}
	  },
	  "rate_limit_reset_credits": {"available_count": `

	cases := []struct {
		name      string
		raw       string
		wantValid bool
		wantValue int64
	}{
		{name: "quoted number", raw: `"2"}}`, wantValid: true, wantValue: 2},
		{name: "integral float", raw: `2.0}}`, wantValid: true, wantValue: 2},
		{name: "quoted integral float", raw: `"2.0"}}`, wantValid: true, wantValue: 2},
		{name: "integer", raw: `2}}`, wantValid: true, wantValue: 2},
		{name: "empty string", raw: `""}}`, wantValid: false},
		{name: "object", raw: `{}}}`, wantValid: false},
		{name: "non-numeric string", raw: `"two"}}`, wantValid: false},
		{name: "fractional value", raw: `2.5}}`, wantValid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(usagePrefix + tc.raw)
			resp, err := ParseCodexUsageResponse(payload)
			if err != nil {
				t.Fatalf("usage decode failed on an odd count %s: %v", tc.raw, err)
			}
			snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
			if len(snap.Quotas) == 0 {
				t.Fatal("usage quotas were dropped; the poll would go stale")
			}
			if snap.Quotas[0].Utilization != 25 {
				t.Fatalf("five_hour utilization = %v, want 25", snap.Quotas[0].Utilization)
			}
			got := snap.ResetCreditsAvailableCount
			if got.Valid != tc.wantValid {
				t.Fatalf("count valid = %v, want %v (value=%d)", got.Valid, tc.wantValid, got.Value)
			}
			if tc.wantValid && got.Value != tc.wantValue {
				t.Fatalf("count = %d, want %d", got.Value, tc.wantValue)
			}
		})
	}
}

// applicable_available_count shares the lenient decoding.
func TestParseCodexUsageResponse_ApplicableCountOddType(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "pro",
	  "rate_limit": {"primary_window": {"used_percent": 10, "reset_at": 1766000000, "limit_window_seconds": 18000}},
	  "rate_limit_reset_credits": {"available_count": 3, "applicable_available_count": "1"}
	}`)
	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}
	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if !snap.ResetCreditsApplicableCount.Valid || snap.ResetCreditsApplicableCount.Value != 1 {
		t.Fatalf("applicable count = %#v, want known 1", snap.ResetCreditsApplicableCount)
	}
}
