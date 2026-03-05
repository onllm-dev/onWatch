package testutil

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSyntheticResponseJSON_UsesProvidedValues(t *testing.T) {
	renewsAt := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	body := SyntheticResponseJSON(123.5, 7, 456.25, renewsAt)

	var result struct {
		Subscription struct {
			Requests float64 `json:"requests"`
			RenewsAt string  `json:"renewsAt"`
		} `json:"subscription"`
		Search struct {
			Hourly struct {
				Requests float64 `json:"requests"`
				RenewsAt string  `json:"renewsAt"`
			} `json:"hourly"`
		} `json:"search"`
		ToolCallDiscounts struct {
			Requests float64 `json:"requests"`
			RenewsAt string  `json:"renewsAt"`
		} `json:"toolCallDiscounts"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal synthetic response: %v", err)
	}

	if result.Subscription.Requests != 123.5 {
		t.Fatalf("expected subscription requests 123.5, got %v", result.Subscription.Requests)
	}
	if result.Search.Hourly.Requests != 7 {
		t.Fatalf("expected search requests 7, got %v", result.Search.Hourly.Requests)
	}
	if result.ToolCallDiscounts.Requests != 456.25 {
		t.Fatalf("expected tool requests 456.25, got %v", result.ToolCallDiscounts.Requests)
	}
	if result.Subscription.RenewsAt != renewsAt.Format(time.RFC3339) {
		t.Fatalf("expected renewsAt %q, got %q", renewsAt.Format(time.RFC3339), result.Subscription.RenewsAt)
	}
}

func TestResponseSequences_IncrementDeterministically(t *testing.T) {
	synthetic := SyntheticResponseSequence(3)
	if len(synthetic) != 3 {
		t.Fatalf("expected 3 synthetic responses, got %d", len(synthetic))
	}

	var syntheticSecond struct {
		Subscription struct {
			Requests float64 `json:"requests"`
		} `json:"subscription"`
		Search struct {
			Hourly struct {
				Requests float64 `json:"requests"`
			} `json:"hourly"`
		} `json:"search"`
		ToolCallDiscounts struct {
			Requests float64 `json:"requests"`
		} `json:"toolCallDiscounts"`
	}
	if err := json.Unmarshal([]byte(synthetic[1]), &syntheticSecond); err != nil {
		t.Fatalf("unmarshal synthetic sequence: %v", err)
	}
	if syntheticSecond.Subscription.Requests != 110 {
		t.Fatalf("expected second synthetic sub requests 110, got %v", syntheticSecond.Subscription.Requests)
	}
	if syntheticSecond.Search.Hourly.Requests != 5 {
		t.Fatalf("expected second synthetic search requests 5, got %v", syntheticSecond.Search.Hourly.Requests)
	}
	if syntheticSecond.ToolCallDiscounts.Requests != 5100 {
		t.Fatalf("expected second synthetic tool requests 5100, got %v", syntheticSecond.ToolCallDiscounts.Requests)
	}

	zai := ZaiResponseSequence(3)
	if len(zai) != 3 {
		t.Fatalf("expected 3 zai responses, got %d", len(zai))
	}

	var zaiThird struct {
		Data struct {
			Limits []struct {
				Type         string  `json:"type"`
				CurrentValue float64 `json:"currentValue"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(zai[2]), &zaiThird); err != nil {
		t.Fatalf("unmarshal zai sequence: %v", err)
	}
	if len(zaiThird.Data.Limits) != 2 {
		t.Fatalf("expected 2 zai limits, got %d", len(zaiThird.Data.Limits))
	}
	if zaiThird.Data.Limits[0].CurrentValue != 16 {
		t.Fatalf("expected third zai time current value 16, got %v", zaiThird.Data.Limits[0].CurrentValue)
	}
	if zaiThird.Data.Limits[1].CurrentValue != 20000000 {
		t.Fatalf("expected third zai token current value 20000000, got %v", zaiThird.Data.Limits[1].CurrentValue)
	}

	anthropic := AnthropicResponseSequence(2)
	if len(anthropic) != 2 {
		t.Fatalf("expected 2 anthropic responses, got %d", len(anthropic))
	}

	var anthropicSecond map[string]struct {
		Utilization float64 `json:"utilization"`
	}
	if err := json.Unmarshal([]byte(anthropic[1]), &anthropicSecond); err != nil {
		t.Fatalf("unmarshal anthropic sequence: %v", err)
	}
	if anthropicSecond["five_hour"].Utilization != 18 {
		t.Fatalf("expected second anthropic five_hour utilization 18, got %v", anthropicSecond["five_hour"].Utilization)
	}
	if anthropicSecond["seven_day_sonnet"].Utilization != 3.5 {
		t.Fatalf("expected second anthropic sonnet utilization 3.5, got %v", anthropicSecond["seven_day_sonnet"].Utilization)
	}

	copilot := CopilotResponseSequence(3)
	if len(copilot) != 3 {
		t.Fatalf("expected 3 copilot responses, got %d", len(copilot))
	}

	var copilotThird struct {
		QuotaSnapshots map[string]struct {
			Remaining int `json:"remaining"`
		} `json:"quota_snapshots"`
	}
	if err := json.Unmarshal([]byte(copilot[2]), &copilotThird); err != nil {
		t.Fatalf("unmarshal copilot sequence: %v", err)
	}
	if copilotThird.QuotaSnapshots["premium_interactions"].Remaining != 900 {
		t.Fatalf("expected third copilot remaining 900, got %d", copilotThird.QuotaSnapshots["premium_interactions"].Remaining)
	}
}

func TestResetResponses_MoveQuotaWindowsForward(t *testing.T) {
	synBefore, synAfter := SyntheticResponseWithReset()
	var synA, synB struct {
		Subscription struct {
			Requests float64 `json:"requests"`
			RenewsAt string  `json:"renewsAt"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal([]byte(synBefore), &synA); err != nil {
		t.Fatalf("unmarshal synthetic before: %v", err)
	}
	if err := json.Unmarshal([]byte(synAfter), &synB); err != nil {
		t.Fatalf("unmarshal synthetic after: %v", err)
	}
	if synB.Subscription.Requests >= synA.Subscription.Requests {
		t.Fatalf("expected synthetic reset to lower requests, before=%v after=%v", synA.Subscription.Requests, synB.Subscription.Requests)
	}
	if synB.Subscription.RenewsAt == synA.Subscription.RenewsAt {
		t.Fatal("expected synthetic reset renew time to change")
	}

	zaiBefore, zaiAfter := ZaiResponseWithReset()
	var zaiA, zaiB struct {
		Data struct {
			Limits []map[string]interface{} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(zaiBefore), &zaiA); err != nil {
		t.Fatalf("unmarshal zai before: %v", err)
	}
	if err := json.Unmarshal([]byte(zaiAfter), &zaiB); err != nil {
		t.Fatalf("unmarshal zai after: %v", err)
	}
	beforeReset := zaiA.Data.Limits[1]["nextResetTime"]
	afterReset := zaiB.Data.Limits[1]["nextResetTime"]
	if afterReset == beforeReset {
		t.Fatal("expected zai reset time to change")
	}

	anthBefore, anthAfter := AnthropicResponseWithReset()
	var anthA, anthB map[string]struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	}
	if err := json.Unmarshal([]byte(anthBefore), &anthA); err != nil {
		t.Fatalf("unmarshal anthropic before: %v", err)
	}
	if err := json.Unmarshal([]byte(anthAfter), &anthB); err != nil {
		t.Fatalf("unmarshal anthropic after: %v", err)
	}
	if anthB["five_hour"].Utilization >= anthA["five_hour"].Utilization {
		t.Fatalf("expected anthropic five_hour utilization to drop after reset, before=%v after=%v", anthA["five_hour"].Utilization, anthB["five_hour"].Utilization)
	}
	if anthB["five_hour"].ResetsAt == anthA["five_hour"].ResetsAt {
		t.Fatal("expected anthropic reset time to change")
	}

	copilotBefore, copilotAfter := CopilotResponseWithReset()
	var copA, copB struct {
		QuotaResetDateUTC string `json:"quota_reset_date_utc"`
		QuotaSnapshots    map[string]struct {
			Remaining int `json:"remaining"`
		} `json:"quota_snapshots"`
	}
	if err := json.Unmarshal([]byte(copilotBefore), &copA); err != nil {
		t.Fatalf("unmarshal copilot before: %v", err)
	}
	if err := json.Unmarshal([]byte(copilotAfter), &copB); err != nil {
		t.Fatalf("unmarshal copilot after: %v", err)
	}
	if copB.QuotaSnapshots["premium_interactions"].Remaining <= copA.QuotaSnapshots["premium_interactions"].Remaining {
		t.Fatalf("expected copilot remaining to increase after reset, before=%d after=%d", copA.QuotaSnapshots["premium_interactions"].Remaining, copB.QuotaSnapshots["premium_interactions"].Remaining)
	}
	if copB.QuotaResetDateUTC == copA.QuotaResetDateUTC {
		t.Fatal("expected copilot reset date to change")
	}
}

func TestAnthropicAndZaiSpecialResponses(t *testing.T) {
	var zaiAuth map[string]interface{}
	if err := json.Unmarshal([]byte(ZaiAuthErrorResponse()), &zaiAuth); err != nil {
		t.Fatalf("unmarshal zai auth error: %v", err)
	}
	if int(zaiAuth["code"].(float64)) != 401 {
		t.Fatalf("expected zai auth error code 401, got %v", zaiAuth["code"])
	}
	if zaiAuth["success"].(bool) {
		t.Fatal("expected zai auth error success=false")
	}

	var anthropic map[string]*struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    *string  `json:"resets_at"`
		IsEnabled   *bool    `json:"is_enabled"`
	}
	if err := json.Unmarshal([]byte(AnthropicResponseNullQuotas()), &anthropic); err != nil {
		t.Fatalf("unmarshal anthropic null quotas: %v", err)
	}
	if anthropic["extra_usage"] != nil {
		t.Fatal("expected extra_usage quota to be null")
	}
	if anthropic["five_hour"] == nil || anthropic["five_hour"].Utilization == nil {
		t.Fatal("expected five_hour quota to remain populated")
	}
}
