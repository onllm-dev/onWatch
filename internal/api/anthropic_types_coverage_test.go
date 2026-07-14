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

// TestParseAnthropicResponse_MixedNonObjectValues reproduces the current shape
// of the /api/oauth/usage response, where the top-level object mixes quota
// objects with non-object values (a "limits" array and null placeholders).
// These non-object values must be tolerated instead of causing a hard
// "cannot unmarshal array/bool into AnthropicQuotaEntry" error.
func TestParseAnthropicResponse_MixedNonObjectValues(t *testing.T) {
	data := []byte(`{
		"five_hour": {"utilization": 10.0, "resets_at": "2026-07-14T11:10:00Z"},
		"seven_day": {"utilization": 9.0, "resets_at": "2026-07-19T21:00:00Z"},
		"seven_day_sonnet": null,
		"extra_usage": {"is_enabled": true, "utilization": 37.4},
		"limits": [
			{"kind": "session", "percent": 10, "is_active": true},
			{"kind": "weekly_all", "percent": 9, "is_active": false}
		]
	}`)

	resp, err := ParseAnthropicResponse(data)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse should tolerate mixed values, got: %v", err)
	}
	if resp == nil {
		t.Fatal("response should not be nil")
	}

	if entry := (*resp)["five_hour"]; entry == nil || entry.Utilization == nil || *entry.Utilization != 10.0 {
		t.Errorf("five_hour utilization not parsed correctly: %+v", entry)
	}
	if entry := (*resp)["seven_day"]; entry == nil || entry.Utilization == nil || *entry.Utilization != 9.0 {
		t.Errorf("seven_day utilization not parsed correctly: %+v", entry)
	}

	// The "limits" array value must be ignored (not surfaced as an active quota).
	for _, name := range resp.ActiveQuotaNames() {
		if name == "limits" {
			t.Error("limits array should not be treated as an active quota")
		}
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
