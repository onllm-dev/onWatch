package api

import (
	"testing"
	"time"
)

func TestAnthropicDisplayName_KnownKeys(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"five_hour", "5-Hour Limit"},
		{"seven_day", "Weekly All-Model"},
		{"seven_day_sonnet", "Weekly Sonnet"},
		{"seven_day_opus", "Weekly Opus"},
		{"seven_day_design", "Claude Design"},
		{"monthly_limit", "Monthly Limit"},
		{"extra_usage", "Extra Usage"},
		{"unknown_key", "unknown_key"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := AnthropicDisplayName(tt.key)
			if got != tt.want {
				t.Errorf("AnthropicDisplayName(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestAnthropicQuotaResponse_ToSnapshot(t *testing.T) {
	now := time.Now().UTC()
	fiveHourReset := now.Add(3 * time.Hour)
	sevenDayReset := now.Add(5 * 24 * time.Hour)

	fiveHour := 45.2
	sevenDay := 12.8
	extraUsage := 15.0
	extraUsed := 11.25
	extraLimit := 75.0
	boolTrue := true
	fiveHourResetStr := fiveHourReset.Format(time.RFC3339)
	sevenDayResetStr := sevenDayReset.Format(time.RFC3339)

	resp := AnthropicQuotaResponse{
		"five_hour": &AnthropicQuotaEntry{
			Utilization: &fiveHour,
			ResetsAt:    &fiveHourResetStr,
			IsEnabled:   &boolTrue,
		},
		"seven_day": &AnthropicQuotaEntry{
			Utilization: &sevenDay,
			ResetsAt:    &sevenDayResetStr,
			IsEnabled:   &boolTrue,
		},
		"extra_usage": &AnthropicQuotaEntry{
			Utilization:  &extraUsage,
			UsedCredits:  &extraUsed,
			MonthlyLimit: &extraLimit,
			IsEnabled:    &boolTrue,
		},
	}

	snapshot := resp.ToSnapshot(now)
	if snapshot.CapturedAt != now {
		t.Errorf("CapturedAt = %v, want %v", snapshot.CapturedAt, now)
	}
	if len(snapshot.Quotas) != 3 {
		t.Fatalf("expected 3 quotas, got %d", len(snapshot.Quotas))
	}
	if snapshot.RawJSON == "" {
		t.Error("RawJSON should not be empty")
	}

	// Quotas should be sorted by name: extra_usage, five_hour, seven_day
	if snapshot.Quotas[0].Name != "extra_usage" {
		t.Errorf("first quota = %q, want extra_usage", snapshot.Quotas[0].Name)
	}
	if snapshot.Quotas[0].UsedCredits != 11.25 {
		t.Errorf("extra_usage usedCredits = %f, want 11.25", snapshot.Quotas[0].UsedCredits)
	}
	if snapshot.Quotas[0].MonthlyLimit != 75.0 {
		t.Errorf("extra_usage monthlyLimit = %f, want 75.0", snapshot.Quotas[0].MonthlyLimit)
	}

	if snapshot.Quotas[1].Name != "five_hour" {
		t.Errorf("second quota = %q, want five_hour", snapshot.Quotas[1].Name)
	}
	if snapshot.Quotas[1].Utilization != 45.2 {
		t.Errorf("five_hour utilization = %f, want 45.2", snapshot.Quotas[1].Utilization)
	}
}

func TestAnthropicQuotaResponse_ToSnapshot_EmptyResetsAt(t *testing.T) {
	now := time.Now().UTC()
	fiveHour := 45.2
	boolTrue := true
	emptyStr := ""

	resp := AnthropicQuotaResponse{
		"five_hour": &AnthropicQuotaEntry{
			Utilization: &fiveHour,
			ResetsAt:    &emptyStr,
			IsEnabled:   &boolTrue,
		},
	}

	snapshot := resp.ToSnapshot(now)
	if len(snapshot.Quotas) != 1 {
		t.Fatalf("expected 1 quota, got %d", len(snapshot.Quotas))
	}
	// Empty resets_at string should result in nil ResetsAt
	if snapshot.Quotas[0].ResetsAt != nil {
		t.Error("ResetsAt should be nil for empty string")
	}
}

func TestParseAnthropicResponse_Valid(t *testing.T) {
	data := []byte(`{
		"five_hour": {
			"utilization": 45.2,
			"resets_at": "2026-03-04T10:00:00Z",
			"is_enabled": true
		}
	}`)

	resp, err := ParseAnthropicResponse(data)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse failed: %v", err)
	}
	if resp == nil {
		t.Fatal("response should not be nil")
	}
	entry := (*resp)["five_hour"]
	if entry == nil {
		t.Fatal("five_hour entry should not be nil")
	}
	if *entry.Utilization != 45.2 {
		t.Errorf("utilization = %f, want 45.2", *entry.Utilization)
	}
}

func TestParseAnthropicResponse_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid}`)
	_, err := ParseAnthropicResponse(data)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRedactAnthropicToken(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "empty", key: "", want: "(empty)"},
		{name: "short", key: "abc", want: "***...***"},
		{name: "len8", key: "abcdefgh", want: "abcd***...***fgh"},
		{name: "normal", key: "my_secret_token", want: "my_s***...***ken"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactAnthropicToken(tt.key)
			if got != tt.want {
				t.Fatalf("redactAnthropicToken(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
