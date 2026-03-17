package api

import (
	"testing"
	"time"
)

func TestParseGeminiQuotaResponse(t *testing.T) {
	raw := `{"buckets":[
		{"remainingFraction":0.993,"resetTime":"2026-03-18T10:00:00Z","modelId":"gemini-2.5-flash"},
		{"remainingFraction":1.0,"resetTime":"2026-03-18T10:00:00Z","modelId":"gemini-2.5-pro"},
		{"remainingFraction":0.999,"resetTime":"2026-03-18T10:00:00Z","modelId":"gemini-2.5-flash-lite"}
	]}`

	resp, err := ParseGeminiQuotaResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseGeminiQuotaResponse() error = %v", err)
	}

	if len(resp.Quotas) != 3 {
		t.Fatalf("expected 3 quotas, got %d", len(resp.Quotas))
	}

	// Verify first quota
	if resp.Quotas[0].ModelID != "gemini-2.5-flash" {
		t.Errorf("expected gemini-2.5-flash, got %s", resp.Quotas[0].ModelID)
	}
	if resp.Quotas[0].RemainingFraction != 0.993 {
		t.Errorf("expected 0.993, got %f", resp.Quotas[0].RemainingFraction)
	}
}

func TestGeminiQuotaResponseToSnapshot(t *testing.T) {
	resp := GeminiQuotaResponse{
		Quotas: []GeminiQuotaBucket{
			{RemainingFraction: 0.5, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-pro"},
			{RemainingFraction: 0.75, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-flash"},
		},
	}

	now := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	snapshot := resp.ToSnapshot(now)

	if snapshot.CapturedAt != now {
		t.Errorf("expected CapturedAt %v, got %v", now, snapshot.CapturedAt)
	}

	if len(snapshot.Quotas) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(snapshot.Quotas))
	}

	// Pro should sort first
	if snapshot.Quotas[0].ModelID != "gemini-2.5-pro" {
		t.Errorf("expected pro first, got %s", snapshot.Quotas[0].ModelID)
	}

	// Verify usage percent calculation
	if snapshot.Quotas[0].UsagePercent != 50.0 {
		t.Errorf("expected 50%% usage, got %f", snapshot.Quotas[0].UsagePercent)
	}

	if snapshot.RawJSON == "" {
		t.Error("expected non-empty RawJSON")
	}
}

func TestGeminiDisplayName(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"gemini-2.5-pro", "Gemini 2.5 Pro"},
		{"gemini-2.5-flash", "Gemini 2.5 Flash"},
		{"unknown-model", "unknown-model"},
	}

	for _, tt := range tests {
		got := GeminiDisplayName(tt.modelID)
		if got != tt.expected {
			t.Errorf("GeminiDisplayName(%q) = %q, want %q", tt.modelID, got, tt.expected)
		}
	}
}

func TestGeminiActiveModelIDs(t *testing.T) {
	resp := GeminiQuotaResponse{
		Quotas: []GeminiQuotaBucket{
			{ModelID: "gemini-2.5-flash"},
			{ModelID: "gemini-2.5-pro"},
			{ModelID: ""},
		},
	}

	ids := resp.ActiveModelIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	// Should be sorted
	if ids[0] != "gemini-2.5-flash" || ids[1] != "gemini-2.5-pro" {
		t.Errorf("unexpected IDs: %v", ids)
	}
}

func TestGeminiStatusFromUsage(t *testing.T) {
	tests := []struct {
		usage    float64
		expected string
	}{
		{0, "healthy"},
		{49, "healthy"},
		{50, "warning"},
		{79, "warning"},
		{80, "danger"},
		{94, "danger"},
		{95, "critical"},
		{100, "critical"},
	}

	for _, tt := range tests {
		got := geminiStatusFromUsage(tt.usage)
		if got != tt.expected {
			t.Errorf("geminiStatusFromUsage(%f) = %q, want %q", tt.usage, got, tt.expected)
		}
	}
}
