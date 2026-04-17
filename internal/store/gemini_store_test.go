package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestInsertAndQueryGeminiSnapshot(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	resetTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	snapshot := &api.GeminiSnapshot{
		CapturedAt: time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC),
		Tier:       "standard",
		ProjectID:  "gen-lang-client-12345",
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 1.0, UsagePercent: 0, ResetTime: &resetTime},
			{ModelID: "gemini-2.5-flash", RemainingFraction: 0.993, UsagePercent: 0.7, ResetTime: &resetTime},
		},
		RawJSON: `{"test": true}`,
	}

	id, err := s.InsertGeminiSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertGeminiSnapshot() error = %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero snapshot ID")
	}

	latest, err := s.QueryLatestGemini()
	if err != nil {
		t.Fatalf("QueryLatestGemini() error = %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil snapshot")
	}

	if latest.Tier != "standard" {
		t.Errorf("expected tier 'standard', got %q", latest.Tier)
	}
	if latest.ProjectID != "gen-lang-client-12345" {
		t.Errorf("expected project ID, got %q", latest.ProjectID)
	}
	if len(latest.Quotas) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(latest.Quotas))
	}
}

func TestGeminiResetCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	resetTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	modelID := "gemini-2.5-pro"
	cycleStart := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)

	// Create cycle
	id, err := s.CreateGeminiCycle(modelID, cycleStart, &resetTime)
	if err != nil {
		t.Fatalf("CreateGeminiCycle() error = %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero cycle ID")
	}

	// Query active cycle
	active, err := s.QueryActiveGeminiCycle(modelID)
	if err != nil {
		t.Fatalf("QueryActiveGeminiCycle() error = %v", err)
	}
	if active == nil {
		t.Fatal("expected non-nil active cycle")
	}
	if active.ModelID != modelID {
		t.Errorf("expected model %q, got %q", modelID, active.ModelID)
	}

	// Update cycle
	if err := s.UpdateGeminiCycle(modelID, 0.15, 0.05); err != nil {
		t.Fatalf("UpdateGeminiCycle() error = %v", err)
	}

	// Close cycle
	cycleEnd := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	if err := s.CloseGeminiCycle(modelID, cycleEnd, 0.15, 0.05); err != nil {
		t.Fatalf("CloseGeminiCycle() error = %v", err)
	}

	// Query history
	history, err := s.QueryGeminiCycleHistory(modelID)
	if err != nil {
		t.Fatalf("QueryGeminiCycleHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 completed cycle, got %d", len(history))
	}
	if history[0].PeakUsage != 0.15 {
		t.Errorf("expected peak 0.15, got %f", history[0].PeakUsage)
	}

	// No active cycle after close
	active, err = s.QueryActiveGeminiCycle(modelID)
	if err != nil {
		t.Fatalf("QueryActiveGeminiCycle() error = %v", err)
	}
	if active != nil {
		t.Error("expected nil active cycle after close")
	}
}

func TestQueryGeminiRange(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	resetTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		snapshot := &api.GeminiSnapshot{
			CapturedAt: time.Date(2026, 3, 17, 10+i, 0, 0, 0, time.UTC),
			Tier:       "standard",
			Quotas: []api.GeminiQuota{
				{ModelID: "gemini-2.5-pro", RemainingFraction: 1.0 - float64(i)*0.1, UsagePercent: float64(i) * 10, ResetTime: &resetTime},
			},
		}
		if _, err := s.InsertGeminiSnapshot(snapshot); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	start := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 17, 13, 0, 0, 0, time.UTC)

	snapshots, err := s.QueryGeminiRange(start, end)
	if err != nil {
		t.Fatalf("QueryGeminiRange() error = %v", err)
	}
	if len(snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(snapshots))
	}

	// Test with limit
	limited, err := s.QueryGeminiRange(start, end, 2)
	if err != nil {
		t.Fatalf("QueryGeminiRange(limit=2) error = %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 snapshots with limit, got %d", len(limited))
	}
}

func TestQueryAllGeminiModelIDs(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	snapshot := &api.GeminiSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 1.0, UsagePercent: 0},
			{ModelID: "gemini-2.5-flash", RemainingFraction: 0.9, UsagePercent: 10},
		},
	}
	if _, err := s.InsertGeminiSnapshot(snapshot); err != nil {
		t.Fatalf("insert: %v", err)
	}

	ids, err := s.QueryAllGeminiModelIDs()
	if err != nil {
		t.Fatalf("QueryAllGeminiModelIDs() error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 model IDs, got %d", len(ids))
	}
}
