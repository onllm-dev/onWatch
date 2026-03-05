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
	}

	snapshot := resp.ToSnapshot(now)
	if snapshot.CapturedAt != now {
		t.Errorf("CapturedAt = %v, want %v", snapshot.CapturedAt, now)
	}
	if len(snapshot.Quotas) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(snapshot.Quotas))
	}
	if snapshot.RawJSON == "" {
		t.Error("RawJSON should not be empty")
	}

	// Quotas should be sorted by name
	if snapshot.Quotas[0].Name != "five_hour" {
		t.Errorf("first quota = %q, want five_hour", snapshot.Quotas[0].Name)
	}
	if snapshot.Quotas[0].Utilization != 45.2 {
		t.Errorf("five_hour utilization = %f, want 45.2", snapshot.Quotas[0].Utilization)
	}
	if snapshot.Quotas[0].ResetsAt == nil {
		t.Error("five_hour ResetsAt should not be nil")
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
