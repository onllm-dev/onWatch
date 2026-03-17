package tracker

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newTestGeminiStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGeminiTracker_Process(t *testing.T) {
	s := newTestGeminiStore(t)
	tr := NewGeminiTracker(s, nil)

	resetTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	snapshot := &api.GeminiSnapshot{
		CapturedAt: time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC),
		Tier:       "standard",
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 1.0, UsagePercent: 0, ResetTime: &resetTime},
			{ModelID: "gemini-2.5-flash", RemainingFraction: 0.993, UsagePercent: 0.7, ResetTime: &resetTime},
		},
	}

	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	// Check cycles were created
	cycle, err := s.QueryActiveGeminiCycle("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("QueryActiveGeminiCycle() error = %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle for gemini-2.5-pro")
	}
}

func TestGeminiTracker_ResetDetection(t *testing.T) {
	s := newTestGeminiStore(t)
	tr := NewGeminiTracker(s, nil)

	var resetModelID string
	tr.SetOnReset(func(modelID string) {
		resetModelID = modelID
	})

	resetTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)

	// First snapshot: some usage
	snap1 := &api.GeminiSnapshot{
		CapturedAt: time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC),
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 0.5, UsagePercent: 50, ResetTime: &resetTime},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot: after reset time, usage back to fresh
	newResetTime := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	snap2 := &api.GeminiSnapshot{
		CapturedAt: time.Date(2026, 3, 18, 10, 5, 0, 0, time.UTC), // past reset time
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 1.0, UsagePercent: 0, ResetTime: &newResetTime},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	if resetModelID != "gemini-2.5-pro" {
		t.Errorf("expected reset callback for gemini-2.5-pro, got %q", resetModelID)
	}

	// Check completed cycle
	history, err := s.QueryGeminiCycleHistory("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("QueryGeminiCycleHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 completed cycle, got %d", len(history))
	}
}

func TestGeminiTracker_UsageSummary(t *testing.T) {
	s := newTestGeminiStore(t)
	tr := NewGeminiTracker(s, nil)

	resetTime := time.Now().Add(12 * time.Hour).UTC()
	snapshot := &api.GeminiSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.GeminiQuota{
			{ModelID: "gemini-2.5-pro", RemainingFraction: 0.8, UsagePercent: 20, ResetTime: &resetTime},
		},
	}

	// Insert snapshot and process
	if _, err := s.InsertGeminiSnapshot(snapshot); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tr.Process(snapshot); err != nil {
		t.Fatalf("Process: %v", err)
	}

	summary, err := tr.UsageSummary("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.RemainingFraction != 0.8 {
		t.Errorf("expected remaining 0.8, got %f", summary.RemainingFraction)
	}
	if summary.UsagePercent != 20 {
		t.Errorf("expected usage 20%%, got %f", summary.UsagePercent)
	}
}
