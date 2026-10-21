package api

import (
	"testing"
	"time"
)

func TestParseCodexUsageResponse_ResetCreditsCountPresent(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "plus",
	  "rate_limit": {"primary_window": {"used_percent": 10, "reset_at": 1766000000, "limit_window_seconds": 18000}},
	  "rate_limit_reset_credits": {"available_count": 3}
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}
	if resp.RateLimitResetCredits == nil {
		t.Fatal("RateLimitResetCredits = nil, want a parsed summary")
	}
	if !resp.RateLimitResetCredits.AvailableCount.Valid {
		t.Fatal("AvailableCount invalid, want known")
	}
	if got := resp.RateLimitResetCredits.AvailableCount.Value; got != 3 {
		t.Fatalf("AvailableCount = %d, want 3", got)
	}

	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if !snap.ResetCreditsAvailableCount.Valid || snap.ResetCreditsAvailableCount.Value != 3 {
		t.Fatalf("snapshot count = %#v, want known 3", snap.ResetCreditsAvailableCount)
	}
	// The persisted raw JSON is re-parsed for history; it must carry the count.
	reparsed, err := ParseCodexUsageResponse([]byte(snap.RawJSON))
	if err != nil {
		t.Fatalf("re-parse snapshot raw JSON: %v (raw=%q)", err, snap.RawJSON)
	}
	if reparsed.RateLimitResetCredits == nil ||
		!reparsed.RateLimitResetCredits.AvailableCount.Valid ||
		reparsed.RateLimitResetCredits.AvailableCount.Value != 3 {
		t.Fatalf("raw JSON lost the reset-credit count: %q", snap.RawJSON)
	}
}

func TestParseCodexUsageResponse_ResetCreditsCountAbsentIsUnknownNotZero(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "plus",
	  "rate_limit": {"primary_window": {"used_percent": 10, "reset_at": 1766000000, "limit_window_seconds": 18000}}
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}
	if resp.RateLimitResetCredits != nil {
		t.Fatalf("RateLimitResetCredits = %#v, want nil for an absent field", resp.RateLimitResetCredits)
	}

	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if snap.ResetCreditsAvailableCount.Valid {
		t.Fatalf("snapshot count = %#v, want unknown", snap.ResetCreditsAvailableCount)
	}
}

func TestParseCodexUsageResponse_ResetCreditsNullCountIsUnknown(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "plus",
	  "rate_limit": {"primary_window": {"used_percent": 10, "reset_at": 1766000000, "limit_window_seconds": 18000}},
	  "rate_limit_reset_credits": {"available_count": null}
	}`)

	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}
	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if snap.ResetCreditsAvailableCount.Valid {
		t.Fatalf("snapshot count = %#v, want unknown for a null count", snap.ResetCreditsAvailableCount)
	}
}

func TestParseCodexResetCreditsResponse_AvailableAndUnknownValues(t *testing.T) {
	payload := []byte(`{
	  "credits": [
	    {
	      "id": "credit-1",
	      "reset_type": "codex_rate_limits",
	      "status": "available",
	      "granted_at": "2026-06-17T00:00:00Z",
	      "expires_at": "2026-07-17T00:00:00Z",
	      "title": "Full reset (Weekly + 5 hr)",
	      "description": "Ready to redeem"
	    },
	    {
	      "id": "credit-2",
	      "reset_type": "codex_rate_limits",
	      "status": "available",
	      "granted_at": "2026-06-18T00:00:00Z",
	      "expires_at": null
	    },
	    {
	      "id": "credit-3",
	      "reset_type": "gift_card",
	      "status": "mystery_status",
	      "granted_at": "2026-06-19T00:00:00Z",
	      "expires_at": "2026-08-19T00:00:00Z"
	    }
	  ],
	  "available_count": 2,
	  "total_earned_count": 4
	}`)

	// Parse at grant time so the fixture's expiries are not "already past".
	resp, err := ParseCodexResetCreditsResponseAt(payload, time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponseAt: %v", err)
	}
	if resp.AvailableCount == nil || *resp.AvailableCount != 2 || resp.TotalEarnedCount != 4 {
		t.Fatalf("counts = %v/%d, want 2/4", resp.AvailableCount, resp.TotalEarnedCount)
	}
	if len(resp.Credits) != 3 {
		t.Fatalf("credits len = %d, want 3", len(resp.Credits))
	}

	first := resp.Credits[0]
	if first.ID != "credit-1" || first.Status != CodexResetCreditStatusAvailable || first.ResetType != "codex_rate_limits" {
		t.Fatalf("first credit = %#v", first)
	}
	if first.ExpiresAt == nil || !first.ExpiresAt.Equal(time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first expires_at = %v", first.ExpiresAt)
	}
	if first.Title != "Full reset (Weekly + 5 hr)" || first.Description != "Ready to redeem" {
		t.Fatalf("first text = %q / %q", first.Title, first.Description)
	}

	if resp.Credits[1].ExpiresAt != nil {
		t.Fatalf("second expires_at = %v, want nil (never expires)", resp.Credits[1].ExpiresAt)
	}
	if resp.Credits[1].Title != "" || resp.Credits[1].Description != "" {
		t.Fatalf("optional text should stay empty, got %q / %q", resp.Credits[1].Title, resp.Credits[1].Description)
	}

	// Unknown enum values must be tolerated, not rejected.
	if resp.Credits[2].Status != CodexResetCreditStatusUnknown {
		t.Fatalf("third status = %q, want unknown", resp.Credits[2].Status)
	}
	if resp.Credits[2].ResetType != CodexResetTypeUnknown {
		t.Fatalf("third reset_type = %q, want unknown", resp.Credits[2].ResetType)
	}
}

func TestParseCodexResetCreditsResponse_EqualGrantAndExpiryIsUnknown(t *testing.T) {
	// Real backend shape: granted_at and expires_at identical to the
	// microsecond while the credit is available. Taken literally it expired at
	// grant time, so the expiry must be treated as unknown.
	payload := []byte(`{
	  "credits": [
	    {
	      "id": "credit-real",
	      "reset_type": "codex_rate_limits",
	      "is_supported_by_plan": true,
	      "status": "available",
	      "granted_at": "2026-10-18T10:00:00.897669Z",
	      "expires_at": "2026-10-18T10:00:00.897669Z",
	      "redeem_started_at": null,
	      "redeemed_at": null,
	      "title": "Full reset",
	      "description": "Ready to redeem"
	    }
	  ],
	  "available_count": 1,
	  "total_earned_count": 0,
	  "immediate_reset_purchase_eligible": false,
	  "history_enabled": true
	}`)

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	resp, err := ParseCodexResetCreditsResponseAt(payload, now)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponseAt: %v", err)
	}
	if len(resp.Credits) != 1 {
		t.Fatalf("credits len = %d, want 1", len(resp.Credits))
	}
	credit := resp.Credits[0]
	if credit.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil (unknown)", credit.ExpiresAt)
	}
	if !credit.ExpiryUnknown {
		t.Fatal("ExpiryUnknown = false, want true for expires_at == granted_at")
	}
	if resp.AvailableCount == nil || *resp.AvailableCount != 1 {
		t.Fatalf("AvailableCount = %v, want 1", resp.AvailableCount)
	}
	// Never list an unknown-expiry credit as expiring in lead-window queries.
	if credits := AvailableCodexResetCredits(resp.Credits); len(credits) != 1 || credits[0].ExpiresAt != nil {
		t.Fatalf("available credits = %#v, want one with no expiry", credits)
	}
}

func TestParseCodexResetCreditsResponse_PastExpiryAvailableIsUnknown(t *testing.T) {
	payload := []byte(`{
	  "credits": [
	    {
	      "id": "credit-past",
	      "reset_type": "codex_rate_limits",
	      "status": "available",
	      "granted_at": "2026-10-01T00:00:00Z",
	      "expires_at": "2026-10-02T00:00:00Z"
	    }
	  ],
	  "available_count": 1
	}`)

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	resp, err := ParseCodexResetCreditsResponseAt(payload, now)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponseAt: %v", err)
	}
	if resp.Credits[0].ExpiresAt != nil || !resp.Credits[0].ExpiryUnknown {
		t.Fatalf("credit = %#v, want unknown expiry for an available credit already past expiry", resp.Credits[0])
	}
}

func TestParseCodexResetCreditsResponse_PastExpiryRedeemedIsKept(t *testing.T) {
	// Only available credits are sanitized: a redeemed credit legitimately has
	// a past expiry and is never shown or notified anyway.
	payload := []byte(`{
	  "credits": [
	    {
	      "id": "credit-redeemed",
	      "reset_type": "codex_rate_limits",
	      "status": "redeemed",
	      "granted_at": "2026-10-01T00:00:00Z",
	      "expires_at": "2026-10-02T00:00:00Z"
	    }
	  ],
	  "available_count": 0
	}`)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	resp, err := ParseCodexResetCreditsResponseAt(payload, now)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponseAt: %v", err)
	}
	if resp.Credits[0].ExpiresAt == nil || resp.Credits[0].ExpiryUnknown {
		t.Fatalf("credit = %#v, want the real past expiry kept for a redeemed credit", resp.Credits[0])
	}
}

func TestParseCodexResetCreditsResponse_ApplicableCountAndSupportedFlag(t *testing.T) {
	payload := []byte(`{
	  "credits": [
	    {
	      "id": "credit-supported",
	      "reset_type": "codex_rate_limits",
	      "is_supported_by_plan": true,
	      "status": "available",
	      "granted_at": "2026-10-01T00:00:00Z",
	      "expires_at": "2026-11-01T00:00:00Z"
	    },
	    {
	      "id": "credit-unsupported",
	      "reset_type": "codex_rate_limits",
	      "is_supported_by_plan": false,
	      "status": "available",
	      "granted_at": "2026-10-02T00:00:00Z",
	      "expires_at": "2026-11-02T00:00:00Z"
	    },
	    {
	      "id": "credit-unspecified",
	      "reset_type": "codex_rate_limits",
	      "status": "available",
	      "granted_at": "2026-10-03T00:00:00Z",
	      "expires_at": "2026-11-03T00:00:00Z"
	    }
	  ],
	  "available_count": 3,
	  "applicable_available_count": 2,
	  "total_earned_count": 0,
	  "immediate_reset_purchase_eligible": false,
	  "history_enabled": true
	}`)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	resp, err := ParseCodexResetCreditsResponseAt(payload, now)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponseAt: %v", err)
	}
	if resp.ApplicableAvailableCount == nil || *resp.ApplicableAvailableCount != 2 {
		t.Fatalf("ApplicableAvailableCount = %v, want 2", resp.ApplicableAvailableCount)
	}
	if resp.Credits[0].UnsupportedByPlan {
		t.Fatal("first credit should be supported")
	}
	if !resp.Credits[1].UnsupportedByPlan {
		t.Fatal("second credit should be unsupported")
	}
	// A missing flag defaults to supported.
	if resp.Credits[2].UnsupportedByPlan {
		t.Fatal("third credit (flag absent) should default to supported")
	}
}

func TestParseCodexResetCreditsResponse_MissingAvailableCountIsNil(t *testing.T) {
	payload := []byte(`{"credits": [
	  {"id":"credit-1","reset_type":"codex_rate_limits","status":"available","expires_at":null}
	]}`)
	resp, err := ParseCodexResetCreditsResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsResponse: %v", err)
	}
	if resp.AvailableCount != nil {
		t.Fatalf("AvailableCount = %v, want nil when absent", *resp.AvailableCount)
	}
	if resp.ApplicableAvailableCount != nil {
		t.Fatalf("ApplicableAvailableCount = %v, want nil when absent", *resp.ApplicableAvailableCount)
	}
}

func TestParseCodexUsageResponse_ApplicableCount(t *testing.T) {
	payload := []byte(`{
	  "plan_type": "plus",
	  "rate_limit": {"primary_window": {"used_percent": 10, "reset_at": 1766000000, "limit_window_seconds": 18000}},
	  "rate_limit_reset_credits": {"available_count": 3, "applicable_available_count": 1}
	}`)
	resp, err := ParseCodexUsageResponse(payload)
	if err != nil {
		t.Fatalf("ParseCodexUsageResponse: %v", err)
	}
	if resp.RateLimitResetCredits == nil {
		t.Fatal("RateLimitResetCredits = nil")
	}
	if !resp.RateLimitResetCredits.ApplicableAvailableCount.Valid ||
		resp.RateLimitResetCredits.ApplicableAvailableCount.Value != 1 {
		t.Fatalf("ApplicableAvailableCount = %#v, want known 1", resp.RateLimitResetCredits.ApplicableAvailableCount)
	}
	snap := resp.ToSnapshot(time.Unix(1765900000, 0).UTC())
	if !snap.ResetCreditsApplicableCount.Valid || snap.ResetCreditsApplicableCount.Value != 1 {
		t.Fatalf("snapshot applicable = %#v, want known 1", snap.ResetCreditsApplicableCount)
	}
}

func TestParseCodexResetCreditsResponse_RejectsMissingCredits(t *testing.T) {
	if _, err := ParseCodexResetCreditsResponse([]byte(`{"available_count":1}`)); err == nil {
		t.Fatal("expected an error when credits[] is missing")
	}
}

func TestCodexResetCredits_AvailableSortedByExpiryNeverExpiringLast(t *testing.T) {
	day := func(d int) *time.Time {
		t := time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC)
		return &t
	}
	credits := []CodexResetCredit{
		{ID: "never", Status: CodexResetCreditStatusAvailable},
		{ID: "later", Status: CodexResetCreditStatusAvailable, ExpiresAt: day(20)},
		{ID: "redeemed", Status: CodexResetCreditStatusRedeemed, ExpiresAt: day(1)},
		{ID: "sooner", Status: CodexResetCreditStatusAvailable, ExpiresAt: day(10)},
	}

	available := AvailableCodexResetCredits(credits)
	if len(available) != 3 {
		t.Fatalf("available len = %d, want 3", len(available))
	}
	want := []string{"sooner", "later", "never"}
	for i, id := range want {
		if available[i].ID != id {
			t.Fatalf("available[%d] = %q, want %q", i, available[i].ID, id)
		}
	}
}

func TestCodexResetCreditStatus_StringValues(t *testing.T) {
	cases := map[CodexResetCreditStatus]string{
		CodexResetCreditStatusAvailable: "available",
		CodexResetCreditStatusRedeeming: "redeeming",
		CodexResetCreditStatusRedeemed:  "redeemed",
		CodexResetCreditStatusUnknown:   "unknown",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("status %d = %q, want %q", status, got, want)
		}
	}
}
