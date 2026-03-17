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

func TestGeminiModelFamily(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"gemini-2.5-pro", GeminiFamilyPro},
		{"gemini-3-pro-preview", GeminiFamilyPro},
		{"gemini-2.5-flash", GeminiFamilyFlash},
		{"gemini-3-flash-preview", GeminiFamilyFlash},
		{"gemini-2.5-flash-lite", GeminiFamilyFlashLite},
		{"gemini-3.1-flash-lite-preview", GeminiFamilyFlashLite},
		{"unknown-model", "unknown-model"}, // fallback to own ID
	}

	for _, tt := range tests {
		got := GeminiModelFamily(tt.modelID)
		if got != tt.expected {
			t.Errorf("GeminiModelFamily(%q) = %q, want %q", tt.modelID, got, tt.expected)
		}
	}
}

func TestGeminiFamilyDisplayName(t *testing.T) {
	tests := []struct {
		family   string
		expected string
	}{
		{GeminiFamilyPro, "Gemini Pro"},
		{GeminiFamilyFlash, "Gemini Flash"},
		{GeminiFamilyFlashLite, "Gemini Flash Lite"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := GeminiFamilyDisplayName(tt.family)
		if got != tt.expected {
			t.Errorf("GeminiFamilyDisplayName(%q) = %q, want %q", tt.family, got, tt.expected)
		}
	}
}

func TestAggregateGeminiByFamily(t *testing.T) {
	resetEarly := time.Date(2026, 3, 18, 9, 0, 0, 0, time.UTC)
	resetLate := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)

	quotas := []GeminiQuota{
		{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20, ResetTime: &resetLate},
		{ModelID: "gemini-3-pro-preview", RemainingFraction: 0.8, UsagePercent: 20, ResetTime: &resetEarly},
		{ModelID: "gemini-2.5-flash", RemainingFraction: 0.5, UsagePercent: 50, ResetTime: &resetLate},
		{ModelID: "gemini-3-flash-preview", RemainingFraction: 0.5, UsagePercent: 50, ResetTime: &resetLate},
		{ModelID: "gemini-2.5-flash-lite", RemainingFraction: 0.95, UsagePercent: 5, ResetTime: &resetLate},
		{ModelID: "gemini-3.1-flash-lite-preview", RemainingFraction: 0.95, UsagePercent: 5, ResetTime: &resetLate},
	}

	families := AggregateGeminiByFamily(quotas)

	if len(families) != 3 {
		t.Fatalf("expected 3 families, got %d", len(families))
	}

	// Sorted: Pro, Flash, Flash Lite
	if families[0].FamilyID != GeminiFamilyPro {
		t.Errorf("expected first family to be pro, got %s", families[0].FamilyID)
	}
	if families[1].FamilyID != GeminiFamilyFlash {
		t.Errorf("expected second family to be flash, got %s", families[1].FamilyID)
	}
	if families[2].FamilyID != GeminiFamilyFlashLite {
		t.Errorf("expected third family to be flash_lite, got %s", families[2].FamilyID)
	}

	// Pro family should have 2 members
	if len(families[0].Members) != 2 {
		t.Errorf("expected 2 pro members, got %d", len(families[0].Members))
	}

	// Pro family should pick earliest reset time
	if families[0].ResetTime == nil || !families[0].ResetTime.Equal(resetEarly) {
		t.Errorf("expected pro reset time %v, got %v", resetEarly, families[0].ResetTime)
	}

	// Usage should come from first member
	if families[0].UsagePercent != 20 {
		t.Errorf("expected pro usage 20%%, got %f", families[0].UsagePercent)
	}

	// Display names
	if families[0].DisplayName != "Gemini Pro" {
		t.Errorf("expected 'Gemini Pro', got %q", families[0].DisplayName)
	}
}

func TestAggregateGeminiByFamily_UnknownModel(t *testing.T) {
	quotas := []GeminiQuota{
		{ModelID: "some-new-model", RemainingFraction: 0.9, UsagePercent: 10},
	}

	families := AggregateGeminiByFamily(quotas)
	if len(families) != 1 {
		t.Fatalf("expected 1 family, got %d", len(families))
	}
	if families[0].FamilyID != "some-new-model" {
		t.Errorf("expected singleton family 'some-new-model', got %q", families[0].FamilyID)
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
