package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseCopilotResponse(t *testing.T) {
	raw := `{
		"login": "iota31",
		"copilot_plan": "individual_pro",
		"access_type_sku": "plus_monthly_subscriber_quota",
		"quota_reset_date": "2026-03-01",
		"quota_reset_date_utc": "2026-03-01T00:00:00.000Z",
		"quota_snapshots": {
			"chat": {
				"entitlement": 0, "remaining": 0, "percent_remaining": 100.0,
				"quota_remaining": 0.0, "unlimited": true,
				"overage_count": 0, "overage_permitted": false,
				"timestamp_utc": "2026-02-15T08:56:03.095Z"
			},
			"completions": {
				"entitlement": 0, "remaining": 0, "percent_remaining": 100.0,
				"quota_remaining": 0.0, "unlimited": true,
				"overage_count": 0, "overage_permitted": false,
				"timestamp_utc": "2026-02-15T08:56:03.095Z"
			},
			"premium_interactions": {
				"entitlement": 1500, "remaining": 473,
				"percent_remaining": 31.578, "quota_remaining": 473.67,
				"unlimited": false,
				"overage_count": 0, "overage_permitted": false,
				"timestamp_utc": "2026-02-15T08:56:03.095Z"
			}
		}
	}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	if resp.Login != "iota31" {
		t.Errorf("Login = %q, want %q", resp.Login, "iota31")
	}
	if resp.CopilotPlan != "individual_pro" {
		t.Errorf("CopilotPlan = %q, want %q", resp.CopilotPlan, "individual_pro")
	}
	if resp.QuotaResetDate != "2026-03-01" {
		t.Errorf("QuotaResetDate = %q, want %q", resp.QuotaResetDate, "2026-03-01")
	}
	if resp.QuotaResetDateUTC != "2026-03-01T00:00:00.000Z" {
		t.Errorf("QuotaResetDateUTC = %q", resp.QuotaResetDateUTC)
	}

	if len(resp.QuotaSnapshots) != 3 {
		t.Fatalf("QuotaSnapshots len = %d, want 3", len(resp.QuotaSnapshots))
	}

	premium := resp.QuotaSnapshots["premium_interactions"]
	if premium == nil {
		t.Fatal("premium_interactions not found")
	}
	if premium.Entitlement != 1500 {
		t.Errorf("premium.Entitlement = %d, want 1500", premium.Entitlement)
	}
	if premium.Remaining != 473 {
		t.Errorf("premium.Remaining = %d, want 473", premium.Remaining)
	}
	if premium.PercentRemaining != 31.578 {
		t.Errorf("premium.PercentRemaining = %f, want 31.578", premium.PercentRemaining)
	}
	if premium.Unlimited {
		t.Error("premium.Unlimited should be false")
	}

	chat := resp.QuotaSnapshots["chat"]
	if chat == nil {
		t.Fatal("chat not found")
	}
	if !chat.Unlimited {
		t.Error("chat.Unlimited should be true")
	}
}

func TestCopilotActiveQuotaNames(t *testing.T) {
	resp := CopilotUserResponse{
		QuotaSnapshots: map[string]*CopilotQuotaSnapshot{
			"premium_interactions": {Entitlement: 1500},
			"chat":                 {Unlimited: true},
			"completions":          {Unlimited: true},
		},
	}

	names := resp.ActiveQuotaNames()
	expected := []string{"chat", "completions", "premium_interactions"}
	if len(names) != len(expected) {
		t.Fatalf("ActiveQuotaNames len = %d, want %d", len(names), len(expected))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("ActiveQuotaNames[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestCopilotActiveQuotaNames_NilEntry(t *testing.T) {
	resp := CopilotUserResponse{
		QuotaSnapshots: map[string]*CopilotQuotaSnapshot{
			"chat":        {Unlimited: true},
			"completions": nil,
		},
	}

	names := resp.ActiveQuotaNames()
	if len(names) != 1 {
		t.Fatalf("ActiveQuotaNames len = %d, want 1", len(names))
	}
	if names[0] != "chat" {
		t.Errorf("ActiveQuotaNames[0] = %q, want %q", names[0], "chat")
	}
}

func TestCopilotToSnapshot(t *testing.T) {
	now := time.Now().UTC()
	resp := CopilotUserResponse{
		Login:             "testuser",
		CopilotPlan:       "individual_pro",
		QuotaResetDateUTC: "2026-03-01T00:00:00.000Z",
		QuotaSnapshots: map[string]*CopilotQuotaSnapshot{
			"premium_interactions": {
				Entitlement:      1500,
				Remaining:        473,
				PercentRemaining: 31.578,
				Unlimited:        false,
				OverageCount:     0,
			},
			"chat": {
				Entitlement:      0,
				Remaining:        0,
				PercentRemaining: 100.0,
				Unlimited:        true,
			},
		},
	}

	snapshot := resp.ToSnapshot(now)
	if snapshot == nil {
		t.Fatal("ToSnapshot returned nil")
	}
	if snapshot.CapturedAt != now {
		t.Errorf("CapturedAt = %v, want %v", snapshot.CapturedAt, now)
	}
	if snapshot.CopilotPlan != "individual_pro" {
		t.Errorf("CopilotPlan = %q, want %q", snapshot.CopilotPlan, "individual_pro")
	}
	if snapshot.ResetDate == nil {
		t.Fatal("ResetDate should not be nil")
	}
	expectedReset := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !snapshot.ResetDate.Equal(expectedReset) {
		t.Errorf("ResetDate = %v, want %v", snapshot.ResetDate, expectedReset)
	}
	if len(snapshot.Quotas) != 2 {
		t.Fatalf("Quotas len = %d, want 2", len(snapshot.Quotas))
	}

	// Quotas should be sorted by name
	if snapshot.Quotas[0].Name != "chat" {
		t.Errorf("Quotas[0].Name = %q, want %q", snapshot.Quotas[0].Name, "chat")
	}
	if snapshot.Quotas[1].Name != "premium_interactions" {
		t.Errorf("Quotas[1].Name = %q, want %q", snapshot.Quotas[1].Name, "premium_interactions")
	}

	premium := snapshot.Quotas[1]
	if premium.Entitlement != 1500 {
		t.Errorf("premium.Entitlement = %d, want 1500", premium.Entitlement)
	}
	if premium.Remaining != 473 {
		t.Errorf("premium.Remaining = %d, want 473", premium.Remaining)
	}

	if snapshot.RawJSON == "" {
		t.Error("RawJSON should not be empty")
	}
}

func TestCopilotToSnapshot_NoResetDate(t *testing.T) {
	now := time.Now().UTC()
	resp := CopilotUserResponse{
		QuotaSnapshots: map[string]*CopilotQuotaSnapshot{
			"chat": {Unlimited: true},
		},
	}

	snapshot := resp.ToSnapshot(now)
	if snapshot.ResetDate != nil {
		t.Error("ResetDate should be nil when no QuotaResetDateUTC")
	}
}

func TestCopilotDisplayName(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"premium_interactions", "Premium Requests"},
		{"chat", "Chat"},
		{"completions", "Completions"},
		{"unknown_quota", "unknown_quota"},
	}

	for _, tt := range tests {
		got := CopilotDisplayName(tt.key)
		if got != tt.expected {
			t.Errorf("CopilotDisplayName(%q) = %q, want %q", tt.key, got, tt.expected)
		}
	}
}

func TestCopilotRoundTrip(t *testing.T) {
	// Test JSON round-trip: parse → ToSnapshot → verify raw JSON re-parses
	raw := `{"login":"test","copilot_plan":"pro","quota_reset_date_utc":"2026-03-01T00:00:00.000Z","quota_snapshots":{"premium_interactions":{"entitlement":1500,"remaining":1000,"percent_remaining":66.667,"unlimited":false,"overage_count":0,"overage_permitted":false}}}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	snapshot := resp.ToSnapshot(time.Now().UTC())
	if snapshot.RawJSON == "" {
		t.Fatal("RawJSON should not be empty")
	}

	// Re-parse the stored raw JSON
	var roundTripped CopilotUserResponse
	if err := json.Unmarshal([]byte(snapshot.RawJSON), &roundTripped); err != nil {
		t.Fatalf("Failed to re-parse RawJSON: %v", err)
	}
	if roundTripped.Login != "test" {
		t.Errorf("Round-trip Login = %q, want %q", roundTripped.Login, "test")
	}
}

func TestParseCopilotResponse_InvalidJSON(t *testing.T) {
	_, err := ParseCopilotResponse([]byte(`{invalid`))
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestParseCopilotResponse_EmptySnapshots(t *testing.T) {
	raw := `{"login":"test","quota_snapshots":{}}`
	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}
	names := resp.ActiveQuotaNames()
	if len(names) != 0 {
		t.Errorf("ActiveQuotaNames should be empty, got %v", names)
	}
}

func TestParseCopilotResponse_LimitedUserFormat(t *testing.T) {
	raw := `{
		"login": "testuser",
		"copilot_plan": "individual",
		"access_type_sku": "free_limited_copilot",
		"limited_user_quotas": {"chat": 260, "completions": 3327},
		"monthly_quotas": {"chat": 500, "completions": 4000},
		"limited_user_subscribed_day": 24,
		"limited_user_reset_date": "2026-04-24"
	}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	if resp.Login != "testuser" {
		t.Errorf("Login = %q, want %q", resp.Login, "testuser")
	}
	if resp.CopilotPlan != "individual" {
		t.Errorf("CopilotPlan = %q, want %q", resp.CopilotPlan, "individual")
	}
	if resp.AccessTypeSKU != "free_limited_copilot" {
		t.Errorf("AccessTypeSKU = %q, want %q", resp.AccessTypeSKU, "free_limited_copilot")
	}

	// normalize() should have synthesized QuotaSnapshots
	if len(resp.QuotaSnapshots) != 2 {
		t.Fatalf("QuotaSnapshots len = %d, want 2", len(resp.QuotaSnapshots))
	}

	chat := resp.QuotaSnapshots["chat"]
	if chat == nil {
		t.Fatal("chat quota not found")
	}
	if chat.Entitlement != 500 {
		t.Errorf("chat.Entitlement = %d, want 500", chat.Entitlement)
	}
	if chat.Remaining != 240 {
		t.Errorf("chat.Remaining = %d, want 240 (500-260)", chat.Remaining)
	}
	if chat.Unlimited {
		t.Error("chat.Unlimited should be false")
	}
	if chat.PercentRemaining != 48.0 {
		t.Errorf("chat.PercentRemaining = %f, want 48.0", chat.PercentRemaining)
	}

	completions := resp.QuotaSnapshots["completions"]
	if completions == nil {
		t.Fatal("completions quota not found")
	}
	if completions.Entitlement != 4000 {
		t.Errorf("completions.Entitlement = %d, want 4000", completions.Entitlement)
	}
	if completions.Remaining != 673 {
		t.Errorf("completions.Remaining = %d, want 673 (4000-3327)", completions.Remaining)
	}

	// Reset date should be synthesized from limited_user_reset_date
	if resp.QuotaResetDateUTC != "2026-04-24T00:00:00.000Z" {
		t.Errorf("QuotaResetDateUTC = %q, want %q", resp.QuotaResetDateUTC, "2026-04-24T00:00:00.000Z")
	}
}

func TestParseCopilotResponse_LimitedUserToSnapshot(t *testing.T) {
	raw := `{
		"login": "testuser",
		"copilot_plan": "individual",
		"access_type_sku": "free_limited_copilot",
		"limited_user_quotas": {"chat": 260, "completions": 3327},
		"monthly_quotas": {"chat": 500, "completions": 4000},
		"limited_user_reset_date": "2026-04-24"
	}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	if snapshot == nil {
		t.Fatal("ToSnapshot returned nil")
	}
	if len(snapshot.Quotas) != 2 {
		t.Fatalf("Quotas len = %d, want 2", len(snapshot.Quotas))
	}

	// Quotas should be sorted by name
	if snapshot.Quotas[0].Name != "chat" {
		t.Errorf("Quotas[0].Name = %q, want %q", snapshot.Quotas[0].Name, "chat")
	}
	if snapshot.Quotas[1].Name != "completions" {
		t.Errorf("Quotas[1].Name = %q, want %q", snapshot.Quotas[1].Name, "completions")
	}

	// Verify reset date parsed
	if snapshot.ResetDate == nil {
		t.Fatal("ResetDate should not be nil")
	}
	expectedReset := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	if !snapshot.ResetDate.Equal(expectedReset) {
		t.Errorf("ResetDate = %v, want %v", snapshot.ResetDate, expectedReset)
	}

	// Verify non-unlimited quotas have correct values
	chat := snapshot.Quotas[0]
	if chat.Entitlement != 500 {
		t.Errorf("chat.Entitlement = %d, want 500", chat.Entitlement)
	}
	if chat.Remaining != 240 {
		t.Errorf("chat.Remaining = %d, want 240", chat.Remaining)
	}
	if chat.Unlimited {
		t.Error("chat should not be unlimited")
	}
}

func TestParseCopilotResponse_LimitedUserOverage(t *testing.T) {
	// Test that used > monthly clamps remaining to 0
	raw := `{
		"limited_user_quotas": {"chat": 600},
		"monthly_quotas": {"chat": 500}
	}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	chat := resp.QuotaSnapshots["chat"]
	if chat == nil {
		t.Fatal("chat quota not found")
	}
	if chat.Remaining != 0 {
		t.Errorf("chat.Remaining = %d, want 0 (clamped)", chat.Remaining)
	}
	if chat.PercentRemaining != 0 {
		t.Errorf("chat.PercentRemaining = %f, want 0", chat.PercentRemaining)
	}
}

func TestParseCopilotResponse_LegacyFormatUnchanged(t *testing.T) {
	// Ensure the legacy quota_snapshots format still works and is not
	// overwritten by normalize() when both formats are present.
	raw := `{
		"login": "legacyuser",
		"copilot_plan": "individual_pro",
		"quota_snapshots": {
			"premium_interactions": {
				"entitlement": 1500, "remaining": 473,
				"percent_remaining": 31.578, "unlimited": false
			}
		},
		"limited_user_quotas": {"chat": 100},
		"monthly_quotas": {"chat": 500}
	}`

	resp, err := ParseCopilotResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCopilotResponse: %v", err)
	}

	// Legacy format should be preserved, not overwritten
	if len(resp.QuotaSnapshots) != 1 {
		t.Fatalf("QuotaSnapshots len = %d, want 1 (legacy preserved)", len(resp.QuotaSnapshots))
	}
	premium := resp.QuotaSnapshots["premium_interactions"]
	if premium == nil {
		t.Fatal("premium_interactions not found")
	}
	if premium.Entitlement != 1500 {
		t.Errorf("premium.Entitlement = %d, want 1500", premium.Entitlement)
	}
}
