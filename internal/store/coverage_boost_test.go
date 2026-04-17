package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// --- recalculateAnthropicCycle with boundaries ---

func TestStore_RecalculateAnthropicCycle_WithBoundaries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	badEnd := base.Add(12 * time.Hour)

	// Insert snapshots with a clear reset boundary at +6h (resets_at changes)
	r1 := base.Add(5 * time.Hour)
	r2 := base.Add(11 * time.Hour) // Different resets_at => boundary

	for i := 0; i < 4; i++ {
		var rat *time.Time
		if i < 2 {
			rat = &r1
		} else {
			rat = &r2
		}
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * 2 * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i%2+1) * 20, ResetsAt: rat},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	// Create bad cycle in the DB
	resetsAt := base.Add(5 * time.Hour)
	cycleID, err := s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", badEnd, 40, 20)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	cycle := &AnthropicResetCycle{
		ID:         cycleID,
		QuotaName:  "five_hour",
		CycleStart: base,
		CycleEnd:   &badEnd,
	}

	fixed, created, snapshotCount, err := s.recalculateAnthropicCycle(cycle, "five_hour", logger)
	if err != nil {
		t.Fatalf("recalculateAnthropicCycle failed: %v", err)
	}
	if !fixed {
		t.Error("Expected cycle to be fixed")
	}
	if created < 2 {
		t.Errorf("Expected at least 2 sub-cycles created, got %d", created)
	}
	if snapshotCount != 4 {
		t.Errorf("Expected 4 snapshots used, got %d", snapshotCount)
	}

	// Verify the original bad cycle was replaced
	history, err := s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory failed: %v", err)
	}
	// Should have at least 2 sub-cycles replacing the 1 bad cycle
	if len(history) < 2 {
		t.Errorf("Expected at least 2 cycles in history after fix, got %d", len(history))
	}
}

// --- QueryAntigravityCycleOverview with data ---

func TestAntigravityStore_QueryAntigravityCycleOverview_InvalidGroup(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	_, err = s.QueryAntigravityCycleOverview("invalid_group", 10)
	if err == nil {
		t.Error("Expected error for invalid group")
	}
}

func TestAntigravityStore_QueryAntigravityCycleOverview_EmptyGroup(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Default group with no data
	rows, err := s.QueryAntigravityCycleOverview("", 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}
	if rows != nil {
		t.Errorf("Expected nil rows for empty DB, got %v", rows)
	}
}

func TestAntigravityStore_QueryAntigravityCycleOverview_WithCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetTime := base.Add(24 * time.Hour)

	// Insert snapshot so model IDs are registered
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Email:      "test@example.com",
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

	// Create and close cycles for claude-4-5-sonnet
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	err = s.CloseAntigravityCycle("claude-4-5-sonnet", base.Add(12*time.Hour), 0.2, 0.15)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle: %v", err)
	}

	// Create and close cycles for gpt-4o (same start time)
	_, err = s.CreateAntigravityCycle("gpt-4o", base, &resetTime)
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
	if len(rows) == 0 {
		t.Fatal("Expected at least 1 grouped cycle row")
	}

	// Verify it's grouped (should be 1 merged row, not 2)
	if len(rows) != 1 {
		t.Errorf("Expected 1 merged cycle row, got %d", len(rows))
	}
}

func TestAntigravityStore_QueryAntigravityCycleOverview_WithActiveCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetTime := base.Add(24 * time.Hour)

	// Insert snapshot
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Create active cycle (no close)
	_, err = s.CreateAntigravityCycle("claude-4-5-sonnet", base, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}

	rows, err := s.QueryAntigravityCycleOverview(api.AntigravityQuotaGroupClaudeGPT, 10)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleOverview: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Expected at least 1 row with active cycle")
	}

	// Active cycle should have nil CycleEnd
	hasActive := false
	for _, row := range rows {
		if row.CycleEnd == nil {
			hasActive = true
			break
		}
	}
	if !hasActive {
		t.Error("Expected to find an active cycle row")
	}
}

// --- Copilot CycleOverview with default limit ---

func TestCopilotStore_QueryCopilotCycleOverview_DefaultLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Test that limit <= 0 defaults to 50
	rows, err := s.QueryCopilotCycleOverview("chat", 0)
	if err != nil {
		t.Fatalf("QueryCopilotCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- Z.ai CycleOverview with default groupBy ---

func TestZaiStore_QueryZaiCycleOverview_DefaultGroupBy(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Using default groupBy (not "tokens" or "time")
	rows, err := s.QueryZaiCycleOverview("other", 10)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- SyntheticCycleOverview with different peakCol values ---

func TestStore_QuerySyntheticCycleOverview_SearchGroupBy(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)

	_, err = s.CreateCycle("search", base, base.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}

	// Insert a snapshot
	snapshot := &api.Snapshot{
		CapturedAt: base.Add(10 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 1350, Requests: 100, RenewsAt: base.Add(24 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 50, RenewsAt: base.Add(time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 500, RenewsAt: base.Add(24 * time.Hour)},
	}
	_, err = s.InsertSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertSnapshot failed: %v", err)
	}

	err = s.CloseCycle("search", base.Add(30*time.Minute), 50, 40)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	rows, err := s.QuerySyntheticCycleOverview("search", 10)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	if len(rows[0].CrossQuotas) != 3 {
		t.Errorf("Expected 3 cross-quotas, got %d", len(rows[0].CrossQuotas))
	}
}

func TestStore_QuerySyntheticCycleOverview_ToolcallGroupBy(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)

	_, err = s.CreateCycle("toolcall", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}
	err = s.CloseCycle("toolcall", base.Add(5*time.Hour), 500, 450)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	rows, err := s.QuerySyntheticCycleOverview("toolcall", 10)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
}

// --- migrateAnthropicSessions with idle gap ---

func TestStore_MigrateAnthropicSessions_IdleGap(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_anthropic_idle.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// First session: changing values
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
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	// Idle gap: same values for a long time
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
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	// Second session: values change again
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
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	err = s.MigrateSessionsToUsageBased(5 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	sessions, err := s.QuerySessionHistory("anthropic")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) < 2 {
		t.Errorf("Expected at least 2 anthropic sessions with idle gap, got %d", len(sessions))
	}
}

// --- QueryCycleHistory with limit ---

func TestStore_QueryCycleHistory_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		start := base.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateCycle("subscription", start, start.Add(12*time.Hour))
		if err != nil {
			t.Fatalf("CreateCycle %d: %v", i, err)
		}
		err = s.CloseCycle("subscription", start.Add(6*time.Hour), float64(i*100), float64(i*50))
		if err != nil {
			t.Fatalf("CloseCycle %d: %v", i, err)
		}
	}

	// Without limit
	all, err := s.QueryCycleHistory("subscription")
	if err != nil {
		t.Fatalf("QueryCycleHistory failed: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("Expected 5 cycles, got %d", len(all))
	}

	// With limit
	limited, err := s.QueryCycleHistory("subscription", 3)
	if err != nil {
		t.Fatalf("QueryCycleHistory with limit failed: %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("Expected 3 cycles with limit, got %d", len(limited))
	}
}

// --- QuerySyntheticCycleOverview with active cycle ---

func TestStore_QuerySyntheticCycleOverview_WithActiveCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)

	// Create an active cycle (not closed)
	_, err = s.CreateCycle("subscription", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}

	// Insert a snapshot within the active cycle
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

	rows, err := s.QuerySyntheticCycleOverview("subscription", 10)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row (active cycle), got %d", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Error("Expected nil CycleEnd for active cycle")
	}
}

// --- QueryAnthropicCycleOverview with active cycle ---

func TestStore_QueryAnthropicCycleOverview_WithActiveCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// Create an active cycle (not closed)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}

	// Insert a snapshot within the cycle
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: base.Add(10 * time.Minute),
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 50, ResetsAt: &resetsAt},
			{Name: "seven_day", Utilization: 10},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
	}

	rows, err := s.QueryAnthropicCycleOverview("five_hour", 10)
	if err != nil {
		t.Fatalf("QueryAnthropicCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row (active cycle), got %d", len(rows))
	}
	if rows[0].CycleEnd != nil {
		t.Error("Expected nil CycleEnd for active cycle")
	}
	if len(rows[0].CrossQuotas) < 1 {
		t.Error("Expected cross-quotas to be populated")
	}
}

// --- QueryAnthropicCycleOverview default limit ---

func TestStore_QueryAnthropicCycleOverview_DefaultLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryAnthropicCycleOverview("five_hour", 0)
	if err != nil {
		t.Fatalf("QueryAnthropicCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- SyntheticCycleOverview default limit ---

func TestStore_QuerySyntheticCycleOverview_DefaultLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QuerySyntheticCycleOverview("subscription", 0)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- ZaiCycleOverview with zero limit ---

func TestZaiStore_QueryZaiCycleOverview_ZeroLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryZaiCycleOverview("tokens", 0)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

// --- QuerySyntheticCycleOverview percent with zero limit ---

func TestStore_QuerySyntheticCycleOverview_ZeroLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)

	_, err = s.CreateCycle("subscription", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}

	// Insert snapshot with zero limit to test pct function
	snapshot := &api.Snapshot{
		CapturedAt: base.Add(10 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 0, Requests: 100, RenewsAt: base},
		Search:     api.QuotaInfo{Limit: 250, Requests: 50, RenewsAt: base},
		ToolCall:   api.QuotaInfo{Limit: 16200, Requests: 500, RenewsAt: base},
	}
	_, err = s.InsertSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertSnapshot failed: %v", err)
	}

	err = s.CloseCycle("subscription", base.Add(5*time.Hour), 100, 50)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	rows, err := s.QuerySyntheticCycleOverview("subscription", 10)
	if err != nil {
		t.Fatalf("QuerySyntheticCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	// The subscription quota should have 0% because limit is 0
	subEntry := rows[0].CrossQuotas[0]
	if subEntry.Percent != 0 {
		t.Errorf("Expected 0%% for zero limit, got %v", subEntry.Percent)
	}
}

// --- Notification log with empty provider ---

func TestStore_UpsertNotificationLog_EmptyProvider(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	err = s.UpsertNotificationLog("", "five_hour", "warning", 80.0)
	if err != nil {
		t.Fatalf("UpsertNotificationLog failed: %v", err)
	}

	// Should default to "legacy"
	sentAt, util, err := s.GetLastNotification("", "five_hour", "warning")
	if err != nil {
		t.Fatalf("GetLastNotification failed: %v", err)
	}
	if sentAt.IsZero() {
		t.Error("Expected non-zero sentAt")
	}
	if util != 80.0 {
		t.Errorf("util = %v, want 80.0", util)
	}
}

func TestStore_ClearNotificationLog_EmptyProvider(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	err = s.UpsertNotificationLog("", "five_hour", "warning", 80.0)
	if err != nil {
		t.Fatalf("UpsertNotificationLog failed: %v", err)
	}

	err = s.ClearNotificationLog("", "five_hour")
	if err != nil {
		t.Fatalf("ClearNotificationLog failed: %v", err)
	}

	sentAt, _, err := s.GetLastNotification("", "five_hour", "warning")
	if err != nil {
		t.Fatalf("GetLastNotification failed: %v", err)
	}
	if !sentAt.IsZero() {
		t.Errorf("Expected zero sentAt after clear, got %v", sentAt)
	}
}

// --- QuerySessionHistory no filter ---

func TestStore_QuerySessionHistory_AllProviders(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	err = s.CreateSession("s1", now, 60, "synthetic")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	err = s.CreateSession("s2", now.Add(time.Minute), 60, "zai")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// No provider filter
	sessions, err := s.QuerySessionHistory()
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions (all providers), got %d", len(sessions))
	}
}

// --- CreateSession with empty provider ---

func TestStore_CreateSession_EmptyProvider(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	err = s.CreateSession("s1", time.Now().UTC(), 60, "")
	if err != nil {
		t.Fatalf("CreateSession with empty provider failed: %v", err)
	}

	// Should default to "synthetic"
	sessions, err := s.QuerySessionHistory("synthetic")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session with default provider, got %d", len(sessions))
	}
}
