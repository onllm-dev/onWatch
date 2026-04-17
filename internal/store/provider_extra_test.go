package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// --- Codex extra tests ---

func TestCodexStore_UpdateCodexCycleResetsAt(t *testing.T) {
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

	// Update resets_at
	newResetsAt := now.Add(10 * time.Hour)
	err = s.UpdateCodexCycleResetsAt(DefaultCodexAccountID, "five_hour", &newResetsAt)
	if err != nil {
		t.Fatalf("UpdateCodexCycleResetsAt: %v", err)
	}

	cycle, err := s.QueryActiveCodexCycle(DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryActiveCodexCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.ResetsAt == nil {
		t.Fatal("Expected ResetsAt to be set")
	}
	if cycle.ResetsAt.Unix() != newResetsAt.Unix() {
		t.Errorf("ResetsAt = %v, want %v", cycle.ResetsAt, newResetsAt)
	}
}

func TestCodexStore_UpdateCodexCycleResetsAt_Nil(t *testing.T) {
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

	// Set resets_at to nil
	err = s.UpdateCodexCycleResetsAt(DefaultCodexAccountID, "five_hour", nil)
	if err != nil {
		t.Fatalf("UpdateCodexCycleResetsAt nil: %v", err)
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

func TestCodexStore_QueryCodexCyclesSince(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// Create and close 3 cycles
	for i := 0; i < 3; i++ {
		start := base.Add(time.Duration(i) * 24 * time.Hour)
		ra := resetsAt.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", start, &ra)
		if err != nil {
			t.Fatalf("CreateCodexCycle %d: %v", i, err)
		}
		err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", start.Add(5*time.Hour), float64(i)*0.2+0.3, float64(i)*0.1)
		if err != nil {
			t.Fatalf("CloseCodexCycle %d: %v", i, err)
		}
	}

	// Query since day 1
	since := base.Add(24 * time.Hour)
	cycles, err := s.QueryCodexCyclesSince(DefaultCodexAccountID, "five_hour", since)
	if err != nil {
		t.Fatalf("QueryCodexCyclesSince: %v", err)
	}
	if len(cycles) != 2 {
		t.Errorf("Expected 2 cycles since day 1, got %d", len(cycles))
	}

	// Verify descending order
	if len(cycles) >= 2 && cycles[0].CycleStart.Before(cycles[1].CycleStart) {
		t.Error("Expected cycles in descending order")
	}
}

func TestCodexStore_QueryCodexCyclesSince_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	cycles, err := s.QueryCodexCyclesSince(DefaultCodexAccountID, "five_hour", time.Now())
	if err != nil {
		t.Fatalf("QueryCodexCyclesSince: %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("Expected 0 cycles, got %d", len(cycles))
	}
}

// --- Copilot extra tests ---

func TestCopilotStore_QueryCopilotCycleOverview_NoCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryCopilotCycleOverview("premium_interactions", 10)
	if err != nil {
		t.Fatalf("QueryCopilotCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

func TestCopilotStore_QueryCopilotCycleOverview_WithData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Create a cycle
	_, err = s.CreateCopilotCycle("premium_interactions", base, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}

	// Insert snapshots with increasing usage
	for i := 0; i < 5; i++ {
		snap := &api.CopilotSnapshot{
			CapturedAt:  base.Add(time.Duration(i) * time.Hour),
			CopilotPlan: "individual_pro",
			ResetDate:   &resetDate,
			RawJSON:     "{}",
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1500, Remaining: 1500 - i*100, PercentRemaining: float64(100 - i*7), Unlimited: false},
				{Name: "chat", Entitlement: 0, Remaining: 0, PercentRemaining: 100.0, Unlimited: true},
			},
		}
		_, err := s.InsertCopilotSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCopilotSnapshot: %v", err)
		}
	}

	// Close the cycle
	err = s.CloseCopilotCycle("premium_interactions", base.Add(5*time.Hour), 400, 400)
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

	row := rows[0]
	if row.QuotaType != "premium_interactions" {
		t.Errorf("QuotaType = %q, want 'premium_interactions'", row.QuotaType)
	}
	if len(row.CrossQuotas) < 1 {
		t.Error("Expected cross-quotas to be populated")
	}
}

func TestCopilotStore_QueryCopilotCycleOverview_NoSnapshots(t *testing.T) {
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
	err = s.CloseCopilotCycle("premium_interactions", base.Add(5*time.Hour), 0, 0)
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
	if len(rows[0].CrossQuotas) != 0 {
		t.Errorf("Expected 0 cross-quotas without snapshots, got %d", len(rows[0].CrossQuotas))
	}
}

// --- Z.ai extra tests ---

func TestZaiStore_QueryZaiCycleOverview_NoCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.QueryZaiCycleOverview("tokens", 10)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Expected 0 rows, got %d", len(rows))
	}
}

func TestZaiStore_QueryZaiCycleOverview_WithData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	// Create a cycle
	_, err = s.CreateZaiCycle("tokens", base, &nextReset)
	if err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}

	// Insert snapshots
	for i := 0; i < 5; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:         base.Add(time.Duration(i) * time.Hour),
			TimeLimit:          100,
			TimeUnit:           1,
			TimeNumber:         100,
			TimeUsage:          float64(100),
			TimeCurrentValue:   float64(i * 10),
			TimeRemaining:      float64(100 - i*10),
			TimePercentage:     i * 10,
			TokensLimit:        1000000,
			TokensUnit:         1,
			TokensNumber:       1000000,
			TokensUsage:        float64(1000000),
			TokensCurrentValue: float64(i * 100000),
			TokensRemaining:    float64(1000000 - i*100000),
			TokensPercentage:   i * 10,
		}
		_, err := s.InsertZaiSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertZaiSnapshot: %v", err)
		}
	}

	// Close the cycle
	err = s.CloseZaiCycle("tokens", base.Add(5*time.Hour), 400000, 400000)
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

	row := rows[0]
	if row.QuotaType != "tokens" {
		t.Errorf("QuotaType = %q, want 'tokens'", row.QuotaType)
	}
	if len(row.CrossQuotas) != 2 {
		t.Errorf("Expected 2 cross-quotas, got %d", len(row.CrossQuotas))
	}
}

func TestZaiStore_QueryZaiCycleOverview_TimePeakCol(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	_, err = s.CreateZaiCycle("time", base, &nextReset)
	if err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	err = s.CloseZaiCycle("time", base.Add(5*time.Hour), 50, 50)
	if err != nil {
		t.Fatalf("CloseZaiCycle: %v", err)
	}

	rows, err := s.QueryZaiCycleOverview("time", 10)
	if err != nil {
		t.Fatalf("QueryZaiCycleOverview: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	// No snapshots means no cross-quotas
	if len(rows[0].CrossQuotas) != 0 {
		t.Errorf("Expected 0 cross-quotas without snapshots, got %d", len(rows[0].CrossQuotas))
	}
}

func TestZaiStore_QueryZaiCyclesSince(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	// Create and close 3 cycles
	for i := 0; i < 3; i++ {
		start := base.Add(time.Duration(i) * 24 * time.Hour)
		nr := nextReset.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateZaiCycle("tokens", start, &nr)
		if err != nil {
			t.Fatalf("CreateZaiCycle %d: %v", i, err)
		}
		err = s.CloseZaiCycle("tokens", start.Add(12*time.Hour), int64(i*1000), int64(i*500))
		if err != nil {
			t.Fatalf("CloseZaiCycle %d: %v", i, err)
		}
	}

	// Query since day 1
	since := base.Add(24 * time.Hour)
	cycles, err := s.QueryZaiCyclesSince("tokens", since)
	if err != nil {
		t.Fatalf("QueryZaiCyclesSince: %v", err)
	}
	if len(cycles) != 2 {
		t.Errorf("Expected 2 cycles since day 1, got %d", len(cycles))
	}

	// Verify descending order
	if len(cycles) >= 2 && cycles[0].CycleStart.Before(cycles[1].CycleStart) {
		t.Error("Expected cycles in descending order")
	}
}

func TestZaiStore_QueryZaiCyclesSince_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	cycles, err := s.QueryZaiCyclesSince("tokens", time.Now())
	if err != nil {
		t.Fatalf("QueryZaiCyclesSince: %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("Expected 0 cycles, got %d", len(cycles))
	}
}

// --- Antigravity extra tests ---

func TestAntigravityStore_QueryAntigravityUsageSeries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert snapshots with model values
	for i := 0; i < 5; i++ {
		resetTime := base.Add(24 * time.Hour)
		snapshot := &api.AntigravitySnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			Models: []api.AntigravityModelQuota{
				{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 1.0 - float64(i)*0.1, RemainingPercent: float64(100 - i*10), ResetTime: &resetTime},
			},
		}
		_, err := s.InsertAntigravitySnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAntigravitySnapshot: %v", err)
		}
	}

	points, err := s.QueryAntigravityUsageSeries("claude-4-5-sonnet", base)
	if err != nil {
		t.Fatalf("QueryAntigravityUsageSeries: %v", err)
	}
	if len(points) != 5 {
		t.Errorf("Expected 5 points, got %d", len(points))
	}

	// Verify ascending order and values
	if len(points) >= 2 {
		if points[0].RemainingFraction != 1.0 {
			t.Errorf("First point remaining = %v, want 1.0", points[0].RemainingFraction)
		}
		if points[4].RemainingFraction != 0.6 {
			t.Errorf("Last point remaining = %v, want 0.6", points[4].RemainingFraction)
		}
	}
}

func TestAntigravityStore_QueryAntigravityUsageSeries_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	points, err := s.QueryAntigravityUsageSeries("nonexistent", time.Now())
	if err != nil {
		t.Fatalf("QueryAntigravityUsageSeries: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("Expected 0 points, got %d", len(points))
	}
}

func TestAntigravityStore_QueryAntigravityHistory(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		resetTime := base.Add(24 * time.Hour)
		snapshot := &api.AntigravitySnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			Models: []api.AntigravityModelQuota{
				{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80, ResetTime: &resetTime},
			},
		}
		_, err := s.InsertAntigravitySnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAntigravitySnapshot: %v", err)
		}
	}

	// QueryAntigravityHistory is an alias for QueryAntigravityRange
	snapshots, err := s.QueryAntigravityHistory(base.Add(-time.Hour), base.Add(4*time.Hour))
	if err != nil {
		t.Fatalf("QueryAntigravityHistory: %v", err)
	}
	if len(snapshots) != 3 {
		t.Errorf("Expected 3 snapshots, got %d", len(snapshots))
	}
}

func TestAntigravityStore_QueryAllAntigravityModelIDs(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Empty DB
	ids, err := s.QueryAllAntigravityModelIDs()
	if err != nil {
		t.Fatalf("QueryAllAntigravityModelIDs (empty): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("Expected 0 IDs for empty DB, got %d", len(ids))
	}

	// Insert snapshot with models
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: base,
		Models: []api.AntigravityModelQuota{
			{ModelID: "claude-4-5-sonnet", Label: "Claude 4.5 Sonnet", RemainingFraction: 0.8, RemainingPercent: 80},
			{ModelID: "gpt-4o", Label: "GPT 4o", RemainingFraction: 0.7, RemainingPercent: 70},
			{ModelID: "gemini-3-pro", Label: "Gemini 3 Pro", RemainingFraction: 0.5, RemainingPercent: 50},
		},
	}
	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	ids, err = s.QueryAllAntigravityModelIDs()
	if err != nil {
		t.Fatalf("QueryAllAntigravityModelIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("Expected 3 model IDs, got %d", len(ids))
	}

	// Verify sorted
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Errorf("Model IDs not sorted: %v", ids)
			break
		}
	}
}

func TestAntigravityStore_QueryAntigravityUsageSeries_Since(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		snapshot := &api.AntigravitySnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			Models: []api.AntigravityModelQuota{
				{ModelID: "claude-4-5-sonnet", RemainingFraction: 1.0 - float64(i)*0.1},
			},
		}
		_, err := s.InsertAntigravitySnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAntigravitySnapshot: %v", err)
		}
	}

	// Query since +2h should return 3 points
	points, err := s.QueryAntigravityUsageSeries("claude-4-5-sonnet", base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("QueryAntigravityUsageSeries: %v", err)
	}
	if len(points) != 3 {
		t.Errorf("Expected 3 points since +2h, got %d", len(points))
	}
}
