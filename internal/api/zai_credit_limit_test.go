package api

import (
	"testing"
	"time"
)

// liteCreditPayload is the upstream response reported in issue #122: a GLM
// Coding Plan Lite account whose quotas arrive as CREDIT_LIMIT entries with no
// TIME_LIMIT / TOKENS_LIMIT alongside them.
const liteCreditPayload = `{
	"code": 200,
	"msg": "Operation successful",
	"data": {
		"limits": [
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586},
			{"type": "CREDIT_LIMIT", "unit": 6, "number": 1, "usage": 10000, "currentValue": 5207, "remaining": 4792, "percentage": 52, "nextResetTime": 1788784466996}
		],
		"level": "lite"
	},
	"success": true
}`

// TestZai_CreditLimit_LitePlan covers the reported symptom: the poll succeeds
// but every quota card reads 0 because CREDIT_LIMIT entries are dropped.
func TestZai_CreditLimit_LitePlan(t *testing.T) {
	resp, err := ParseZaiResponse([]byte(liteCreditPayload))
	if err != nil {
		t.Fatalf("ParseZaiResponse() error = %v", err)
	}
	if resp.Level != "lite" {
		t.Errorf("Level = %q, want %q", resp.Level, "lite")
	}

	snapshot := resp.ToSnapshot(time.Now().UTC())

	// Shorter window (unit=3, number=5) fills the time slot.
	if snapshot.TimePercentage != 20 {
		t.Errorf("TimePercentage = %d, want 20", snapshot.TimePercentage)
	}
	if snapshot.TimeCurrentValue != 402 {
		t.Errorf("TimeCurrentValue = %v, want 402", snapshot.TimeCurrentValue)
	}
	if snapshot.TimeUsage != 2000 {
		t.Errorf("TimeUsage (budget) = %v, want 2000", snapshot.TimeUsage)
	}
	if snapshot.TimeRemaining != 1597 {
		t.Errorf("TimeRemaining = %v, want 1597", snapshot.TimeRemaining)
	}
	if snapshot.TimeLimitType != ZaiLimitTypeCredit {
		t.Errorf("TimeLimitType = %q, want %q", snapshot.TimeLimitType, ZaiLimitTypeCredit)
	}
	if snapshot.TimeNextResetTime == nil {
		t.Error("TimeNextResetTime is nil, want the credit window reset")
	} else if got := snapshot.TimeNextResetTime.UnixMilli(); got != 1788351145586 {
		t.Errorf("TimeNextResetTime = %d, want 1788351145586", got)
	}

	// Longer window (unit=6, number=1) fills the tokens slot.
	if snapshot.TokensPercentage != 52 {
		t.Errorf("TokensPercentage = %d, want 52", snapshot.TokensPercentage)
	}
	if snapshot.TokensCurrentValue != 5207 {
		t.Errorf("TokensCurrentValue = %v, want 5207", snapshot.TokensCurrentValue)
	}
	if snapshot.TokensUsage != 10000 {
		t.Errorf("TokensUsage (budget) = %v, want 10000", snapshot.TokensUsage)
	}
	if snapshot.TokensLimitType != ZaiLimitTypeCredit {
		t.Errorf("TokensLimitType = %q, want %q", snapshot.TokensLimitType, ZaiLimitTypeCredit)
	}
	if snapshot.TokensNextResetTime == nil {
		t.Error("TokensNextResetTime is nil, want the credit window reset")
	} else if got := snapshot.TokensNextResetTime.UnixMilli(); got != 1788784466996 {
		t.Errorf("TokensNextResetTime = %d, want 1788784466996", got)
	}

	if !snapshot.IsCreditBased() {
		t.Error("IsCreditBased() = false, want true")
	}
}

// TestZai_CreditLimit_SlotAssignmentIsStable guards cycle detection: the
// shortest window must land in the same slot on every poll regardless of the
// order the API happens to list the entries in.
func TestZai_CreditLimit_SlotAssignmentIsStable(t *testing.T) {
	reversed := `{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "CREDIT_LIMIT", "unit": 6, "number": 1, "usage": 10000, "currentValue": 5207, "remaining": 4792, "percentage": 52, "nextResetTime": 1788784466996},
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586}
		], "level": "lite"}
	}`

	forward, err := ParseZaiResponse([]byte(liteCreditPayload))
	if err != nil {
		t.Fatalf("ParseZaiResponse(forward) error = %v", err)
	}
	backward, err := ParseZaiResponse([]byte(reversed))
	if err != nil {
		t.Fatalf("ParseZaiResponse(reversed) error = %v", err)
	}

	now := time.Now().UTC()
	a, b := forward.ToSnapshot(now), backward.ToSnapshot(now)

	if a.TimePercentage != b.TimePercentage || a.TokensPercentage != b.TokensPercentage {
		t.Errorf("slot assignment depends on array order: forward time/tokens = %d/%d, reversed = %d/%d",
			a.TimePercentage, a.TokensPercentage, b.TimePercentage, b.TokensPercentage)
	}
}

// TestZai_CreditLimit_LegacyEntriesWin verifies accounts that still receive
// TIME_LIMIT / TOKENS_LIMIT are untouched, even if credit entries appear too.
func TestZai_CreditLimit_LegacyEntriesWin(t *testing.T) {
	payload := `{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "TIME_LIMIT", "unit": 5, "number": 1, "usage": 1000, "currentValue": 19, "remaining": 981, "percentage": 1},
			{"type": "TOKENS_LIMIT", "unit": 5, "number": 1, "usage": 300000000, "currentValue": 200112618, "remaining": 99887382, "percentage": 66, "nextResetTime": 1788784466996},
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586},
			{"type": "CREDIT_LIMIT", "unit": 6, "number": 1, "usage": 10000, "currentValue": 5207, "remaining": 4792, "percentage": 52, "nextResetTime": 1788784466996}
		]}
	}`

	resp, err := ParseZaiResponse([]byte(payload))
	if err != nil {
		t.Fatalf("ParseZaiResponse() error = %v", err)
	}
	snapshot := resp.ToSnapshot(time.Now().UTC())

	if snapshot.TimePercentage != 1 || snapshot.TimeCurrentValue != 19 {
		t.Errorf("TIME_LIMIT overwritten: percentage=%d currentValue=%v, want 1 / 19",
			snapshot.TimePercentage, snapshot.TimeCurrentValue)
	}
	if snapshot.TokensPercentage != 66 || snapshot.TokensCurrentValue != 200112618 {
		t.Errorf("TOKENS_LIMIT overwritten: percentage=%d currentValue=%v, want 66 / 200112618",
			snapshot.TokensPercentage, snapshot.TokensCurrentValue)
	}
	if snapshot.TimeLimitType != ZaiLimitTypeTime || snapshot.TokensLimitType != ZaiLimitTypeTokens {
		t.Errorf("limit types = %q / %q, want %q / %q",
			snapshot.TimeLimitType, snapshot.TokensLimitType, ZaiLimitTypeTime, ZaiLimitTypeTokens)
	}
	if snapshot.IsCreditBased() {
		t.Error("IsCreditBased() = true for a legacy account, want false")
	}
}

// TestZai_CreditLimit_FillsOnlyEmptySlots covers a mixed payload where one
// legacy entry is present and the other is not.
func TestZai_CreditLimit_FillsOnlyEmptySlots(t *testing.T) {
	payload := `{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "TOKENS_LIMIT", "unit": 5, "number": 1, "usage": 300000000, "currentValue": 200112618, "remaining": 99887382, "percentage": 66, "nextResetTime": 1788784466996},
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586},
			{"type": "CREDIT_LIMIT", "unit": 6, "number": 1, "usage": 10000, "currentValue": 5207, "remaining": 4792, "percentage": 52, "nextResetTime": 1788784466996}
		]}
	}`

	resp, err := ParseZaiResponse([]byte(payload))
	if err != nil {
		t.Fatalf("ParseZaiResponse() error = %v", err)
	}
	snapshot := resp.ToSnapshot(time.Now().UTC())

	if snapshot.TokensPercentage != 66 {
		t.Errorf("TokensPercentage = %d, want 66 (legacy entry kept)", snapshot.TokensPercentage)
	}
	if snapshot.TimePercentage != 20 || snapshot.TimeLimitType != ZaiLimitTypeCredit {
		t.Errorf("empty time slot not filled from credits: percentage=%d type=%q",
			snapshot.TimePercentage, snapshot.TimeLimitType)
	}
}

// TestZai_CreditLimit_SingleWindow verifies one credit window is not duplicated
// across both slots, which would double-count usage.
func TestZai_CreditLimit_SingleWindow(t *testing.T) {
	payload := `{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586}
		], "level": "lite"}
	}`

	resp, err := ParseZaiResponse([]byte(payload))
	if err != nil {
		t.Fatalf("ParseZaiResponse() error = %v", err)
	}
	snapshot := resp.ToSnapshot(time.Now().UTC())

	if snapshot.TimePercentage != 20 {
		t.Errorf("TimePercentage = %d, want 20", snapshot.TimePercentage)
	}
	if snapshot.TokensLimitType != "" || snapshot.TokensPercentage != 0 || snapshot.TokensCurrentValue != 0 {
		t.Errorf("single credit window leaked into the tokens slot: type=%q percentage=%d currentValue=%v",
			snapshot.TokensLimitType, snapshot.TokensPercentage, snapshot.TokensCurrentValue)
	}
}

// TestZai_CreditLimit_UnknownTypesIgnored keeps the parser forward-compatible
// with limit types onWatch does not model yet.
func TestZai_CreditLimit_UnknownTypesIgnored(t *testing.T) {
	payload := `{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "SOMETHING_NEW", "unit": 1, "number": 1, "usage": 5, "currentValue": 5, "percentage": 100},
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586}
		]}
	}`

	resp, err := ParseZaiResponse([]byte(payload))
	if err != nil {
		t.Fatalf("ParseZaiResponse() error = %v", err)
	}
	snapshot := resp.ToSnapshot(time.Now().UTC())

	if snapshot.TimePercentage != 20 || snapshot.TimeLimitType != ZaiLimitTypeCredit {
		t.Errorf("unknown limit type disturbed credit mapping: percentage=%d type=%q",
			snapshot.TimePercentage, snapshot.TimeLimitType)
	}
}
