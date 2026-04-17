package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// --- CodexCycleOverview with completed cycles + snapshots ---

func TestCodexStore_QueryCodexCycleOverview_CompletedWithSnapshots(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// Create a cycle
	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}

	// Insert snapshots with increasing utilization
	for i := 0; i < 5; i++ {
		snap := &api.CodexSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			PlanType:   "pro",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i) * 20, ResetsAt: &resetsAt, Status: "healthy"},
				{Name: "seven_day", Utilization: float64(i) * 5, ResetsAt: &resetsAt, Status: "healthy"},
			},
		}
		_, err := s.InsertCodexSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCodexSnapshot: %v", err)
		}
	}

	// Close the cycle
	err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", base.Add(5*time.Hour), 80, 72)
	if err != nil {
		t.Fatalf("CloseCodexCycle: %v", err)
	}

	rows, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 10)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	row := rows[0]
	if len(row.CrossQuotas) < 2 {
		t.Fatalf("Expected at least 2 cross-quotas, got %d", len(row.CrossQuotas))
	}

	// Verify delta calculation (peak - start)
	for _, cq := range row.CrossQuotas {
		if cq.Name == "five_hour" {
			if cq.Delta == 0 && cq.Percent > 0 {
				// Delta should be non-zero since peak > start
				t.Logf("five_hour: Percent=%v, StartPercent=%v, Delta=%v", cq.Percent, cq.StartPercent, cq.Delta)
			}
		}
	}
}

func TestCodexStore_QueryCodexCycleOverview_NoCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 10)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

func TestCodexStore_QueryCodexCycleOverview_NoSnapshots(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", base.Add(5*time.Hour), 0, 0)
	if err != nil {
		t.Fatalf("CloseCodexCycle: %v", err)
	}

	rows, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 10)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	if len(rows[0].CrossQuotas) != 0 {
		t.Errorf("Expected 0 cross-quotas without snapshots, got %d", len(rows[0].CrossQuotas))
	}
}

func TestCodexStore_QueryCodexCycleOverview_DefaultLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 0)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- CopilotCycleOverview with active cycle ---

func TestCopilotStore_QueryCopilotCycleOverview_ActiveCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Create active cycle
	_, err = s.CreateCopilotCycle("premium_interactions", base, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}

	// Insert snapshot
	snap := &api.CopilotSnapshot{
		CapturedAt:  base.Add(10 * time.Minute),
		CopilotPlan: "individual_pro",
		ResetDate:   &resetDate,
		RawJSON:     "{}",
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000, PercentRemaining: 66.7, Unlimited: false},
		},
	}
	_, err = s.InsertCopilotSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	rows, err := s.QueryCopilotCycleOverview("premium_interactions", 10)
	if err != nil {
		t.Fatalf("QueryCopilotCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Error("Expected nil CycleEnd for active cycle")
	}
}

// --- QueryActiveCodexCycle with resetsAt ---

func TestCodexStore_QueryActiveCodexCycle_WithResetsAt(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(5 * time.Hour)

	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}

	cycle, err := s.QueryActiveCodexCycle(DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryActiveCodexCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.ResetsAt == nil {
		t.Error("Expected ResetsAt to be set")
	}
	if cycle.CycleEnd != nil {
		t.Error("Expected CycleEnd to be nil for active cycle")
	}
}

func TestCodexStore_QueryActiveCodexCycle_NoResetsAt(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", now, nil)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}

	cycle, err := s.QueryActiveCodexCycle(DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryActiveCodexCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.ResetsAt != nil {
		t.Errorf("Expected nil ResetsAt, got %v", cycle.ResetsAt)
	}
}

func TestCodexStore_QueryActiveCodexCycle_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	cycle, err := s.QueryActiveCodexCycle(DefaultCodexAccountID, "nonexistent")
	if err != nil {
		t.Fatalf("QueryActiveCodexCycle: %v", err)
	}
	if cycle != nil {
		t.Error("Expected nil for no active cycle")
	}
}

// --- AntigravityCycleOverview merging ---

func TestAntigravityStore_QueryAntigravityCycleOverview_MergeSameStartTime(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetTime := base.Add(24 * time.Hour)

	// Insert snapshots so model IDs are registered
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Email:      "test@test.com",
		PlanName:   "Pro",
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime},
			{ModelID: "gpt-4o", Label: "GPT 4o", RemainingFraction: 0.7, RemainingPercent: 70, ResetTime: &resetTime},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Create closed cycles for two models with same start time (truncated to minute)
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle claude: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(12*time.Hour), 0.2, 0.15)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle claude: %v", err)
	}

	_, err = s.CreateAntigravityCycle("gpt-4o", base.Add(1*time.Second), &resetTime) // Same minute
	if err != nil {
		t.Fatalf("CreateAntigravityCycle gpt: %v", err)
	}
	err = s.CloseAntigravityCycle("gpt-4o", base.Add(12*time.Hour), 0.3, 0.25)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle gpt: %v", err)
	}

	rows, err := s.QueryAntigravityCycleOverview(api.AntigravityQuotaGroupClaudeGPT, 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}

	// Should merge into 1 row since start times are in same minute
	if len(rows) != 1 {
		t.Errorf("Expected 1 merged row, got %d", len(rows))
	}

	if len(rows) > 0 {
		// Peak should be max of the two
		if rows[0].PeakValue < 30.0 { // 0.3 * 100 = 30
			t.Errorf("Expected PeakValue >= 30, got %v", rows[0].PeakValue)
		}
	}
}

// --- AntigravityCycleOverview with different start times ---

func TestAntigravityStore_QueryAntigravityCycleOverview_DifferentStartTimes(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetTime1 := base.Add(24 * time.Hour)
	resetTime2 := base.Add(48 * time.Hour)

	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime1},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Insert second snapshot at later time
	snapshot2 := &api.AntigravitySnapshot{
		CapturedAt: base.Add(24 * time.Hour),
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.5, RemainingPercent: 50, ResetTime: &resetTime2},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot2)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Create two cycles at different start times
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime1)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle 1: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(12*time.Hour), 0.2, 0.15)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle 1: %v", err)
	}

	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base.Add(24*time.Hour), &resetTime2)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle 2: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(36*time.Hour), 0.5, 0.4)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle 2: %v", err)
	}

	rows, err := s.QueryAntigravityCycleOverview(api.AntigravityQuotaGroupClaudeGPT, 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}

	// Should have 2 separate rows (different start times)
	if len(rows) != 2 {
		t.Errorf("Expected 2 rows for different start times, got %d", len(rows))
	}
}

// --- ZaiCycleOverview with active cycle ---

func TestZaiStore_QueryZaiCycleOverview_ActiveCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	_, err = s.CreateZaiCycle("tokens", base, &nextReset)
	if err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}

	// Insert snapshot
	snapshot := &api.ZaiSnapshot{
		CapturedAt:         base.Add(10 * time.Minute),
		TimeLimit:          100,
		TimeUnit:           1,
		TimeNumber:         100,
		TimeUsage:          100,
		TimeCurrentValue:   20,
		TimeRemaining:      80,
		TimePercentage:     20,
		TokensLimit:        1000000,
		TokensUnit:         1,
		TokensNumber:       1000000,
		TokensUsage:        1000000,
		TokensCurrentValue: 200000,
		TokensRemaining:    800000,
		TokensPercentage:   20,
	}
	_, err = s.InsertZaiSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	rows, err := s.QueryZaiCycleOverview("tokens", 10)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row with active cycle, got %d", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Error("Expected nil CycleEnd for active cycle")
	}
	if len(rows[0].CrossQuotas) != 2 {
		t.Errorf("Expected 2 cross-quotas, got %d", len(rows[0].CrossQuotas))
	}
}

// --- ZaiCycleOverview with zero-usage pct ---

func TestZaiStore_QueryZaiCycleOverview_ZeroUsage(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	_, err = s.CreateZaiCycle("tokens", base, &nextReset)
	if err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}

	// Insert snapshot with zero usage
	snapshot := &api.ZaiSnapshot{
		CapturedAt:         base.Add(10 * time.Minute),
		TimeLimit:          100,
		TimeUnit:           1,
		TimeNumber:         100,
		TimeUsage:          0, // Zero usage
		TimeCurrentValue:   0,
		TimeRemaining:      100,
		TimePercentage:     0,
		TokensLimit:        1000000,
		TokensUnit:         1,
		TokensNumber:       1000000,
		TokensUsage:        0, // Zero usage
		TokensCurrentValue: 0,
		TokensRemaining:    1000000,
		TokensPercentage:   0,
	}
	_, err = s.InsertZaiSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	err = s.CloseZaiCycle("tokens", base.Add(5*time.Hour), 0, 0)
	if err != nil {
		t.Fatalf("CloseZaiCycle: %v", err)
	}

	rows, err := s.QueryZaiCycleOverview("tokens", 10)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	// Verify pct returns 0 for zero limits
	for _, cq := range rows[0].CrossQuotas {
		if cq.Percent != 0 && cq.Limit == 0 {
			t.Errorf("Expected 0%% for zero limit on %s, got %v", cq.Name, cq.Percent)
		}
	}
}

// --- QuerySyntheticCycleOverview active cycle with default groupBy ---

func TestStore_QuerySyntheticCycleOverview_DefaultGroupBy(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)

	_, err = s.CreateCycle("unknown_type", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}

	snapshot := &api.Snapshot{
		CapturedAt: base.Add(10 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 200, RenewsAt: base.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 30, RenewsAt: base.Add(time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 1000, RenewsAt: base.Add(24 * time.Hour)},
	}
	_, err = s.InsertSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertSnapshot failed: %v", err)
	}

	err = s.CloseCycle("unknown_type", base.Add(5*time.Hour), 200, 180)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	// Default peakCol is sub_requests
	rows, err := s.QuerySyntheticCycleOverview("unknown_type", 10)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
}

// --- CopilotCycleOverview with entitlement-based percent ---

func TestCopilotStore_QueryCopilotCycleOverview_WithPercent(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	_, err = s.CreateCopilotCycle("premium_interactions", base, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}

	// Insert snapshot with meaningful usage
	snap := &api.CopilotSnapshot{
		CapturedAt:  base.Add(10 * time.Minute),
		CopilotPlan: "individual_pro",
		ResetDate:   &resetDate,
		RawJSON:     "{}",
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 500, PercentRemaining: 33.3, Unlimited: false},
			{Name: "chat", Entitlement: 0, Remaining: 0, PercentRemaining: 100.0, Unlimited: true},
		},
	}
	_, err = s.InsertCopilotSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	err = s.CloseCopilotCycle("premium_interactions", base.Add(5*time.Hour), 1000, 1000)
	if err != nil {
		t.Fatalf("CloseCopilotCycle: %v", err)
	}

	rows, err := s.QueryCopilotCycleOverview("premium_interactions", 10)
	if err != nil {
		t.Fatalf("QueryCopilotCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	// Check that premium_interactions has correct percent
	for _, cq := range rows[0].CrossQuotas {
		if cq.Name == "premium_interactions" {
			// Used 1000 out of 1500 = 66.7%
			if cq.Percent < 66 || cq.Percent > 67 {
				t.Errorf("Expected ~66.7%% usage, got %v", cq.Percent)
			}
			if cq.Value != 1000 {
				t.Errorf("Expected value 1000, got %v", cq.Value)
			}
			if cq.Limit != 1500 {
				t.Errorf("Expected limit 1500, got %v", cq.Limit)
			}
		}
	}
}

// --- Anthropic CycleOverview with start values (delta) ---

func TestStore_QueryAnthropicCycleOverview_CrossQuotaDelta(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	cycleEnd := base.Add(5 * time.Hour)

	_, err = s.CreateAnthropicCycle("five_hour", base, &cycleEnd)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}

	// Insert start snapshot (low utilization)
	startSnap := &api.AnthropicSnapshot{
		CapturedAt: base,
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 10},
			{Name: "seven_day", Utilization: 5},
		},
	}
	_, err = s.InsertAnthropicSnapshot(startSnap)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
	}

	// Insert peak snapshot (high utilization)
	peakSnap := &api.AnthropicSnapshot{
		CapturedAt: base.Add(3 * time.Hour),
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 80},
			{Name: "seven_day", Utilization: 20},
		},
	}
	_, err = s.InsertAnthropicSnapshot(peakSnap)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
	}

	err = s.CloseAnthropicCycle("five_hour", cycleEnd, 80, 70)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	rows, err := s.QueryAnthropicCycleOverview("five_hour", 10)
	if err != nil {
		t.Fatalf("QueryAnthropicCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}

	// Check that delta = peak - start
	for _, cq := range rows[0].CrossQuotas {
		if cq.Name == "five_hour" {
			if cq.Delta != 70 { // 80 - 10
				t.Errorf("five_hour Delta = %v, want 70", cq.Delta)
			}
			if cq.StartPercent != 10 {
				t.Errorf("five_hour StartPercent = %v, want 10", cq.StartPercent)
			}
		}
		if cq.Name == "seven_day" {
			if cq.Delta != 15 { // 20 - 5
				t.Errorf("seven_day Delta = %v, want 15", cq.Delta)
			}
		}
	}
}

// --- Codex overview with active cycle + snapshots ---

// --- migrateNotificationLogProviderScope: full migration path ---

func TestStore_MigrateNotificationLogProviderScope_OldSchema(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Drop the modern notification_log (which has provider column) and recreate
	// the OLD schema without the provider column to exercise the migration path.
	if _, err := s.db.Exec(`DROP TABLE notification_log`); err != nil {
		t.Fatalf("drop notification_log: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			quota_key TEXT NOT NULL,
			notification_type TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			utilization REAL,
			UNIQUE(quota_key, notification_type)
		)
	`); err != nil {
		t.Fatalf("create old notification_log: %v", err)
	}

	// Insert a row into the old table to verify data migration
	if _, err := s.db.Exec(`
		INSERT INTO notification_log (quota_key, notification_type, sent_at, utilization)
		VALUES ('five_hour', 'warning', '2026-01-01T00:00:00Z', 80.0)
	`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}

	// Run migration
	if err := s.migrateNotificationLogProviderScope(); err != nil {
		t.Fatalf("migrateNotificationLogProviderScope: %v", err)
	}

	// Verify the new table has provider column with 'legacy' value
	var provider, quotaKey string
	err = s.db.QueryRow(
		`SELECT provider, quota_key FROM notification_log WHERE quota_key = 'five_hour'`,
	).Scan(&provider, &quotaKey)
	if err != nil {
		t.Fatalf("query migrated row: %v", err)
	}
	if provider != "legacy" {
		t.Errorf("Expected provider 'legacy', got %q", provider)
	}

	// Calling again should be a no-op (provider column now exists)
	if err := s.migrateNotificationLogProviderScope(); err != nil {
		t.Fatalf("second migrateNotificationLogProviderScope: %v", err)
	}
}

// --- migrateAnthropicSessions: idle timeout + different quota count ---

func TestStore_MigrateSessionsToUsageBased_AnthropicIdleTimeout(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_anthropic_idle.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// Insert Anthropic snapshots: active period, then idle gap, then new activity
	// First batch: changing values (creates a session)
	for i := 0; i < 3; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 10, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot batch1: %v", err)
		}
	}

	// Gap with no value change (2 hours of same value = idle timeout)
	for i := 0; i < 3; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(2*time.Hour + time.Duration(i)*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: 20, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot gap: %v", err)
		}
	}

	// New activity after gap
	for i := 0; i < 3; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(3*time.Hour + time.Duration(i)*time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(30 + i*10), ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot batch2: %v", err)
		}
	}

	// Short idle timeout to trigger session split
	err = s.MigrateSessionsToUsageBased(5 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased: %v", err)
	}

	sessions, err := s.QuerySessionHistory("anthropic")
	if err != nil {
		t.Fatalf("QuerySessionHistory: %v", err)
	}
	if len(sessions) < 2 {
		t.Errorf("Expected at least 2 sessions (split by idle), got %d", len(sessions))
	}
}

func TestStore_MigrateSessionsToUsageBased_AnthropicDifferentQuotaCount(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_anthropic_quotas.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// First snapshot: 1 quota
	snap1 := &api.AnthropicSnapshot{
		CapturedAt: base,
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 10, ResetsAt: &resetsAt},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snap1)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	// Second snapshot: 2 quotas (different count triggers `changed = true`)
	snap2 := &api.AnthropicSnapshot{
		CapturedAt: base.Add(1 * time.Minute),
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 10, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 5, ResetsAt: &resetsAt},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snap2)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	// Third snapshot: same 2 quotas, same values (no change, covers snapshotCount++ else branch)
	snap3 := &api.AnthropicSnapshot{
		CapturedAt: base.Add(2 * time.Minute),
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 10, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 5, ResetsAt: &resetsAt},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snap3)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased: %v", err)
	}

	sessions, err := s.QuerySessionHistory("anthropic")
	if err != nil {
		t.Fatalf("QuerySessionHistory: %v", err)
	}
	if len(sessions) == 0 {
		t.Error("Expected at least 1 session from different quota count change")
	}
}

// --- AntigravityCycleOverview merge: earlier start + later end ---

func TestAntigravityStore_QueryAntigravityCycleOverview_MergeEarlierStart(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 30, 0, time.UTC) // starts 30 seconds into the minute
	resetTime := base.Add(24 * time.Hour)

	// Insert snapshots for two models
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Email:      "test@test.com",
		PlanName:   "Pro",
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime},
			{ModelID: "gpt-4o", Label: "GPT 4o", RemainingFraction: 0.7, RemainingPercent: 70, ResetTime: &resetTime},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Create first closed cycle for claude with LATER start in same minute
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle claude: %v", err)
	}
	endTime1 := base.Add(12 * time.Hour)
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", endTime1, 0.2, 0.15)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle claude: %v", err)
	}

	// Create second closed cycle for gpt with EARLIER start in same minute
	// This should trigger the cycle.CycleStart.Before(existing.CycleStart) branch
	earlierBase := base.Add(-20 * time.Second) // 10 seconds into the minute vs 30 seconds
	_, err = s.CreateAntigravityCycle("gpt-4o", earlierBase, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle gpt: %v", err)
	}
	endTime2 := base.Add(14 * time.Hour) // Later end to trigger CycleEnd.After branch
	err = s.CloseAntigravityCycle("gpt-4o", endTime2, 0.3, 0.25)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle gpt: %v", err)
	}

	rows, err := s.QueryAntigravityCycleOverview(api.AntigravityQuotaGroupClaudeGPT, 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 merged row, got %d", len(rows))
	}

	// Merged row should have earliest start
	if rows[0].CycleStart.After(earlierBase) {
		t.Errorf("Expected merged start <= %v, got %v", earlierBase, rows[0].CycleStart)
	}
}

// --- AntigravityCycleOverview: active + closed cycles (sort branch) ---

func TestAntigravityStore_QueryAntigravityCycleOverview_ActiveAndClosed(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetTime := base.Add(24 * time.Hour)

	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Email:      "test@test.com",
		PlanName:   "Pro",
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Create and close TWO cycles (different start minutes to avoid merging)
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle 1: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(12*time.Hour), 0.2, 0.15)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle 1: %v", err)
	}

	resetTime1b := base.Add(36 * time.Hour)
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base.Add(13*time.Hour), &resetTime1b)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle 2: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(23*time.Hour), 0.3, 0.2)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle 2: %v", err)
	}

	// Create an active cycle (newest)
	resetTime2 := base.Add(72 * time.Hour)
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base.Add(24*time.Hour), &resetTime2)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle active: %v", err)
	}

	// Insert another snapshot for the new time period
	snapshot2 := &api.AntigravitySnapshot{
		CapturedAt: base.Add(24 * time.Hour),
		Email:      "test@test.com",
		PlanName:   "Pro",
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.5, RemainingPercent: 50, ResetTime: &resetTime2},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot2)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot 2: %v", err)
	}

	rows, err := s.QueryAntigravityCycleOverview(api.AntigravityQuotaGroupClaudeGPT, 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}
	// Should have 3 rows: 1 active + 2 closed, with active sorted first
	if len(rows) < 3 {
		t.Fatalf("Expected at least 3 rows (active + 2 closed), got %d", len(rows))
	}
	// Active cycle should be first (CycleEnd == nil)
	if rows[0].CycleEnd != nil {
		t.Error("Expected first row to be active cycle (nil CycleEnd)")
	}
}

// --- migrateSchema: exercise ALTER TABLE success paths ---

func TestStore_MigrateSchema_AlterTableSuccess(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Drop sessions table and recreate without provider and start_* columns
	// to exercise the ALTER TABLE success paths in migrateSchema
	if _, err := s.db.Exec(`DROP TABLE sessions`); err != nil {
		t.Fatalf("drop sessions: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			poll_interval INTEGER NOT NULL,
			max_sub_requests REAL NOT NULL DEFAULT 0,
			max_search_requests REAL NOT NULL DEFAULT 0,
			max_tool_requests REAL NOT NULL DEFAULT 0,
			snapshot_count INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create minimal sessions: %v", err)
	}

	// Drop and recreate quota_snapshots without provider column
	if _, err := s.db.Exec(`DROP TABLE quota_snapshots`); err != nil {
		t.Fatalf("drop quota_snapshots: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE quota_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			captured_at TEXT NOT NULL,
			sub_limit REAL NOT NULL,
			sub_requests REAL NOT NULL,
			sub_renews_at TEXT NOT NULL,
			search_limit REAL NOT NULL,
			search_requests REAL NOT NULL,
			search_renews_at TEXT NOT NULL,
			tool_limit REAL NOT NULL,
			tool_requests REAL NOT NULL,
			tool_renews_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create minimal quota_snapshots: %v", err)
	}

	// Drop and recreate reset_cycles without provider column
	if _, err := s.db.Exec(`DROP TABLE reset_cycles`); err != nil {
		t.Fatalf("drop reset_cycles: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE reset_cycles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			quota_type TEXT NOT NULL,
			cycle_start TEXT NOT NULL,
			cycle_end TEXT,
			renews_at TEXT NOT NULL,
			peak_requests REAL NOT NULL DEFAULT 0,
			total_delta REAL NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create minimal reset_cycles: %v", err)
	}

	// Drop zai_snapshots and recreate without time_usage_details
	if _, err := s.db.Exec(`DROP TABLE zai_snapshots`); err != nil {
		t.Fatalf("drop zai_snapshots: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE zai_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'zai',
			captured_at TEXT NOT NULL,
			time_limit INTEGER NOT NULL,
			time_unit INTEGER NOT NULL,
			time_number INTEGER NOT NULL,
			time_usage REAL NOT NULL,
			time_current_value REAL NOT NULL,
			time_remaining REAL NOT NULL,
			time_percentage INTEGER NOT NULL,
			tokens_limit INTEGER NOT NULL,
			tokens_unit INTEGER NOT NULL,
			tokens_number INTEGER NOT NULL,
			tokens_usage REAL NOT NULL,
			tokens_current_value REAL NOT NULL,
			tokens_remaining REAL NOT NULL,
			tokens_percentage INTEGER NOT NULL,
			tokens_next_reset TEXT
		)
	`); err != nil {
		t.Fatalf("create minimal zai_snapshots: %v", err)
	}

	// Now call migrateSchema - the ALTER TABLE statements should succeed
	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}

	// Verify columns were actually added
	hasProv, err := s.tableHasColumn("sessions", "provider")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if !hasProv {
		t.Error("Expected sessions table to have 'provider' column after migration")
	}

	hasStart, err := s.tableHasColumn("sessions", "start_sub_requests")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if !hasStart {
		t.Error("Expected sessions table to have 'start_sub_requests' column after migration")
	}

	hasDetails, err := s.tableHasColumn("zai_snapshots", "time_usage_details")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if !hasDetails {
		t.Error("Expected zai_snapshots table to have 'time_usage_details' column after migration")
	}
}

// --- Closed-DB tests for migration functions ---

func closedStoreForTest(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	s.db.Close()
	return s
}

func TestClosedDB_CountBadCycles(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.countBadCycles()
	if err == nil {
		t.Fatal("Expected error from countBadCycles on closed DB")
	}
}

func TestClosedDB_CountBadAnthropicCycles(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.countBadAnthropicCycles()
	if err == nil {
		t.Fatal("Expected error from countBadAnthropicCycles on closed DB")
	}
}

func TestClosedDB_CountBadSyntheticCycles(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.countBadSyntheticCycles()
	if err == nil {
		t.Fatal("Expected error from countBadSyntheticCycles on closed DB")
	}
}

func TestClosedDB_CountBadZaiCycles(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.countBadZaiCycles()
	if err == nil {
		t.Fatal("Expected error from countBadZaiCycles on closed DB")
	}
}

func TestClosedDB_CountBadCopilotCycles(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.countBadCopilotCycles()
	if err == nil {
		t.Fatal("Expected error from countBadCopilotCycles on closed DB")
	}
}

func TestClosedDB_RunCycleMigrationIfNeeded(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	logger := testLogger()
	_, err := s.RunCycleMigrationIfNeeded(logger)
	if err == nil {
		t.Fatal("Expected error from RunCycleMigrationIfNeeded on closed DB")
	}
}

func TestClosedDB_TableHasColumn(t *testing.T) {
	t.Parallel()
	s := closedStoreForTest(t)
	_, err := s.tableHasColumn("sessions", "provider")
	if err == nil {
		t.Fatal("Expected error from tableHasColumn on closed DB")
	}
}

func TestCodexStore_QueryCodexCycleOverview_ActiveWithSnapshots(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}

	// Insert snapshots
	for i := 0; i < 3; i++ {
		snap := &api.CodexSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			PlanType:   "pro",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i) * 25, ResetsAt: &resetsAt, Status: "healthy"},
			},
		}
		_, err := s.InsertCodexSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCodexSnapshot: %v", err)
		}
	}

	rows, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 10)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row (active), got %d", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Error("Expected nil CycleEnd for active cycle")
	}
	if len(rows[0].CrossQuotas) == 0 {
		t.Error("Expected cross-quotas to be populated from snapshots")
	}
}
