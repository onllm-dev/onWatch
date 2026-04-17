package store

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStore_RunCycleMigrationIfNeeded_NoBadCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.RunCycleMigrationIfNeeded(logger)
	if err != nil {
		t.Fatalf("RunCycleMigrationIfNeeded failed: %v", err)
	}
	if results != nil {
		t.Errorf("Expected nil results for no bad cycles, got %v", results)
	}

	// Verify it marked migration complete
	val, err := s.GetSetting("cycle_migration_v2")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if val != "completed" {
		t.Errorf("Expected migration marked as 'completed', got %q", val)
	}
}

func TestStore_RunCycleMigrationIfNeeded_AlreadyCompleted(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	// Mark as already completed
	if err := s.SetSetting("cycle_migration_v2", "completed"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}

	results, err := s.RunCycleMigrationIfNeeded(logger)
	if err != nil {
		t.Fatalf("RunCycleMigrationIfNeeded failed: %v", err)
	}
	if results != nil {
		t.Errorf("Expected nil results when already completed, got %v", results)
	}
}

func TestStore_RunCycleMigrationIfNeeded_WithBadAnthropicCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	// Create a bad five_hour cycle (7 hours instead of 5)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	badEnd := base.Add(7 * time.Hour)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", badEnd, 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	// Insert snapshots with different resets_at to create a boundary
	for i := 0; i < 4; i++ {
		rat := base.Add(time.Duration(i+1) * time.Hour)
		if i >= 2 {
			rat = base.Add(8 * time.Hour) // Different resets_at = boundary
		}
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 20, ResetsAt: &rat},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	results, err := s.RunCycleMigrationIfNeeded(logger)
	if err != nil {
		t.Fatalf("RunCycleMigrationIfNeeded failed: %v", err)
	}

	// Should have processed something
	if results == nil {
		t.Fatal("Expected non-nil results for bad cycles")
	}

	// Verify migration marked complete
	val, err := s.GetSetting("cycle_migration_v2")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if val != "completed" {
		t.Errorf("Expected migration marked as 'completed', got %q", val)
	}
}

func TestStore_CountBadCycles_AllZero(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	count, err := s.countBadCycles()
	if err != nil {
		t.Fatalf("countBadCycles failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 bad cycles on empty DB, got %d", count)
	}
}

func TestStore_CountBadAnthropicCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Create a normal 5-hour cycle
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(5*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	count, err := s.countBadAnthropicCycles()
	if err != nil {
		t.Fatalf("countBadAnthropicCycles failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 bad cycles, got %d", count)
	}
}

func TestStore_CountBadAnthropicCycles_BadFiveHour(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Create a bad five_hour cycle (> 6 hours)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	count, err := s.countBadAnthropicCycles()
	if err != nil {
		t.Fatalf("countBadAnthropicCycles failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 bad cycle, got %d", count)
	}
}

func TestStore_CountBadAnthropicCycles_BadSevenDay(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(7 * 24 * time.Hour)
	_, err = s.CreateAnthropicCycle("seven_day", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	// Close at 9 days (> 8 day threshold)
	err = s.CloseAnthropicCycle("seven_day", base.Add(9*24*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	count, err := s.countBadAnthropicCycles()
	if err != nil {
		t.Fatalf("countBadAnthropicCycles failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 bad seven_day cycle, got %d", count)
	}
}

func TestStore_CountBadSyntheticCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	count, err := s.countBadSyntheticCycles()
	if err != nil {
		t.Fatalf("countBadSyntheticCycles failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 bad cycles, got %d", count)
	}
}

func TestStore_CountBadSyntheticCycles_BadSearchCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.CreateCycle("search", base, base.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}
	// Close at 3 hours (> 2 hour threshold for search)
	err = s.CloseCycle("search", base.Add(3*time.Hour), 100, 50)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	count, err := s.countBadSyntheticCycles()
	if err != nil {
		t.Fatalf("countBadSyntheticCycles failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 bad search cycle, got %d", count)
	}
}

func TestStore_CountBadZaiCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	count, err := s.countBadZaiCycles()
	if err != nil {
		t.Fatalf("countBadZaiCycles failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 bad cycles, got %d", count)
	}
}

func TestStore_CountBadZaiCycles_BadTokensCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)

	_, err = s.db.Exec(
		`INSERT INTO zai_reset_cycles (quota_type, cycle_start, cycle_end, next_reset, peak_value, total_delta)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"tokens",
		base.Format(time.RFC3339Nano),
		base.Add(72*time.Hour).Format(time.RFC3339Nano), // 72h > 48h threshold
		nextReset.Format(time.RFC3339Nano),
		1000, 500,
	)
	if err != nil {
		t.Fatalf("Insert zai cycle failed: %v", err)
	}

	count, err := s.countBadZaiCycles()
	if err != nil {
		t.Fatalf("countBadZaiCycles failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 bad zai cycle, got %d", count)
	}
}

func TestStore_CountBadCopilotCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	count, err := s.countBadCopilotCycles()
	if err != nil {
		t.Fatalf("countBadCopilotCycles failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 bad copilot cycles, got %d", count)
	}
}

func TestStore_CountBadCopilotCycles_BadCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.db.Exec(
		`INSERT INTO copilot_reset_cycles (quota_name, cycle_start, cycle_end, peak_used, total_delta)
		VALUES (?, ?, ?, ?, ?)`,
		"premium_interactions",
		base.Format(time.RFC3339Nano),
		base.Add(40*24*time.Hour).Format(time.RFC3339Nano), // 40 days > 35 threshold
		100, 50,
	)
	if err != nil {
		t.Fatalf("Insert copilot cycle failed: %v", err)
	}

	count, err := s.countBadCopilotCycles()
	if err != nil {
		t.Fatalf("countBadCopilotCycles failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 bad copilot cycle, got %d", count)
	}
}

func TestStore_FixBadCycles_NoBadCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.fixBadCycles(logger)
	if err != nil {
		t.Fatalf("fixBadCycles failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

func TestStore_FixBadSyntheticCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.fixBadSyntheticCycles(logger)
	if err != nil {
		t.Fatalf("fixBadSyntheticCycles failed: %v", err)
	}
	if results != nil {
		t.Errorf("Expected nil results, got %v", results)
	}
}

func TestStore_FixBadSyntheticCycles_WithBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	// Create a bad search cycle (> 2 hours)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.CreateCycle("search", base, base.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}
	err = s.CloseCycle("search", base.Add(3*time.Hour), 100, 50)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	results, err := s.fixBadSyntheticCycles(logger)
	if err != nil {
		t.Fatalf("fixBadSyntheticCycles failed: %v", err)
	}
	// The function logs but doesn't actually fix them (placeholder)
	if results != nil {
		t.Errorf("Expected nil results from placeholder fix, got %v", results)
	}
}

func TestStore_FixBadZaiCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.fixBadZaiCycles(logger)
	if err != nil {
		t.Fatalf("fixBadZaiCycles failed: %v", err)
	}
	if results != nil {
		t.Errorf("Expected nil results, got %v", results)
	}
}

func TestStore_FixBadZaiCycles_WithBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.db.Exec(
		`INSERT INTO zai_reset_cycles (quota_type, cycle_start, cycle_end, next_reset, peak_value, total_delta)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"tokens",
		base.Format(time.RFC3339Nano),
		base.Add(72*time.Hour).Format(time.RFC3339Nano),
		base.Add(24*time.Hour).Format(time.RFC3339Nano),
		1000, 500,
	)
	if err != nil {
		t.Fatalf("Insert zai cycle failed: %v", err)
	}

	results, err := s.fixBadZaiCycles(logger)
	if err != nil {
		t.Fatalf("fixBadZaiCycles failed: %v", err)
	}
	// Placeholder - just logs
	if results != nil {
		t.Errorf("Expected nil results from placeholder fix, got %v", results)
	}
}

func TestStore_FixBadCopilotCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.fixBadCopilotCycles(logger)
	if err != nil {
		t.Fatalf("fixBadCopilotCycles failed: %v", err)
	}
	if results != nil {
		t.Errorf("Expected nil results, got %v", results)
	}
}

func TestStore_FixBadCopilotCycles_WithBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.db.Exec(
		`INSERT INTO copilot_reset_cycles (quota_name, cycle_start, cycle_end, peak_used, total_delta)
		VALUES (?, ?, ?, ?, ?)`,
		"premium_interactions",
		base.Format(time.RFC3339Nano),
		base.Add(40*24*time.Hour).Format(time.RFC3339Nano),
		100, 50,
	)
	if err != nil {
		t.Fatalf("Insert copilot cycle failed: %v", err)
	}

	results, err := s.fixBadCopilotCycles(logger)
	if err != nil {
		t.Fatalf("fixBadCopilotCycles failed: %v", err)
	}
	// Placeholder - just logs
	if results != nil {
		t.Errorf("Expected nil results from placeholder fix, got %v", results)
	}
}

func TestStore_FixBadAnthropicCycles_NoBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	results, err := s.fixBadAnthropicCycles(logger)
	if err != nil {
		t.Fatalf("fixBadAnthropicCycles failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

func TestStore_GetAnthropicQuotasWithBadCycles_None(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	names, err := s.getAnthropicQuotasWithBadCycles()
	if err != nil {
		t.Fatalf("getAnthropicQuotasWithBadCycles failed: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("Expected 0 names, got %d", len(names))
	}
}

func TestStore_GetAnthropicQuotasWithBadCycles_WithBad(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	names, err := s.getAnthropicQuotasWithBadCycles()
	if err != nil {
		t.Fatalf("getAnthropicQuotasWithBadCycles failed: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("Expected 1 name, got %d", len(names))
	}
	if names[0] != "five_hour" {
		t.Errorf("Expected 'five_hour', got %q", names[0])
	}
}

func TestStore_GetBadAnthropicCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	cycles, err := s.getBadAnthropicCycles("five_hour")
	if err != nil {
		t.Fatalf("getBadAnthropicCycles failed: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("Expected 1 bad cycle, got %d", len(cycles))
	}
	if cycles[0].QuotaName != "five_hour" {
		t.Errorf("Expected quota 'five_hour', got %q", cycles[0].QuotaName)
	}
}

func TestStore_GetBadAnthropicCycles_SevenDayVariant(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(7 * 24 * time.Hour)
	_, err = s.CreateAnthropicCycle("seven_day_sonnet", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("seven_day_sonnet", base.Add(9*24*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	cycles, err := s.getBadAnthropicCycles("seven_day_sonnet")
	if err != nil {
		t.Fatalf("getBadAnthropicCycles failed: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("Expected 1 bad cycle, got %d", len(cycles))
	}
}

func TestStore_GetBadAnthropicCycles_MonthlyLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(30 * 24 * time.Hour)
	_, err = s.CreateAnthropicCycle("monthly_limit", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("monthly_limit", base.Add(33*24*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	cycles, err := s.getBadAnthropicCycles("monthly_limit")
	if err != nil {
		t.Fatalf("getBadAnthropicCycles failed: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("Expected 1 bad monthly_limit cycle, got %d", len(cycles))
	}
}

func TestStore_GetBadAnthropicCycles_DefaultQuota(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Unknown quota name defaults to 6h threshold
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("unknown_quota", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("unknown_quota", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	// unknown_quota won't be in the SQL WHERE clause of countBadAnthropicCycles,
	// but getBadAnthropicCycles defaults to maxHours=6
	cycles, err := s.getBadAnthropicCycles("unknown_quota")
	if err != nil {
		t.Fatalf("getBadAnthropicCycles failed: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("Expected 1 bad cycle for unknown quota, got %d", len(cycles))
	}
}

func TestStore_FixAnthropicQuotaCycles_NoSnapshots(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	// Create a bad cycle with no snapshots
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	result, err := s.fixAnthropicQuotaCycles("five_hour", logger)
	if err != nil {
		t.Fatalf("fixAnthropicQuotaCycles failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.Provider != "anthropic" {
		t.Errorf("Provider = %q, want 'anthropic'", result.Provider)
	}
	if result.QuotaType != "five_hour" {
		t.Errorf("QuotaType = %q, want 'five_hour'", result.QuotaType)
	}
}

func TestStore_RecalculateAnthropicCycle_NotEnoughSnapshots(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	badEnd := base.Add(7 * time.Hour)

	// Insert only 1 snapshot
	resetsAt := base.Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: base.Add(1 * time.Hour),
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 20, ResetsAt: &resetsAt},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
	}

	cycle := &AnthropicResetCycle{
		ID:         1,
		QuotaName:  "five_hour",
		CycleStart: base,
		CycleEnd:   &badEnd,
	}

	fixed, created, snapshots, err := s.recalculateAnthropicCycle(cycle, "five_hour", logger)
	if err != nil {
		t.Fatalf("recalculateAnthropicCycle failed: %v", err)
	}
	if fixed {
		t.Error("Expected not fixed with only 1 snapshot")
	}
	if created != 0 {
		t.Errorf("Expected 0 created, got %d", created)
	}
	if snapshots != 0 {
		t.Errorf("Expected 0 snapshots, got %d", snapshots)
	}
}

func TestStore_RecalculateAnthropicCycle_NoBoundaries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	logger := testLogger()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	badEnd := base.Add(7 * time.Hour)
	resetsAt := base.Add(5 * time.Hour)

	// Insert 3 snapshots with same resets_at and increasing utilization
	for i := 0; i < 3; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * 30 * time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 10, ResetsAt: &resetsAt},
			},
		}
		_, err = s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	cycle := &AnthropicResetCycle{
		ID:         1,
		QuotaName:  "five_hour",
		CycleStart: base,
		CycleEnd:   &badEnd,
	}

	fixed, _, _, err := s.recalculateAnthropicCycle(cycle, "five_hour", logger)
	if err != nil {
		t.Fatalf("recalculateAnthropicCycle failed: %v", err)
	}
	if fixed {
		t.Error("Expected not fixed when no boundaries detected")
	}
}

func TestStore_GetAnthropicSnapshotsInRange(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	for i := 0; i < 5; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Hour),
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

	// Get snapshots in range [+1h, +3h]
	points, err := s.getAnthropicSnapshotsInRange("five_hour", base.Add(1*time.Hour), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("getAnthropicSnapshotsInRange failed: %v", err)
	}
	if len(points) != 3 {
		t.Errorf("Expected 3 points, got %d", len(points))
	}

	// Check ordering
	if len(points) > 0 && points[0].Utilization != 10 {
		t.Errorf("First point utilization = %v, want 10", points[0].Utilization)
	}
}

func TestStore_GetAnthropicSnapshotsInRange_NilResetsAt(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshot := &api.AnthropicSnapshot{
		CapturedAt: base,
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 10, ResetsAt: nil},
		},
	}
	_, err = s.InsertAnthropicSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
	}

	points, err := s.getAnthropicSnapshotsInRange("five_hour", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("getAnthropicSnapshotsInRange failed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("Expected 1 point, got %d", len(points))
	}
	if points[0].ResetsAt != nil {
		t.Error("Expected nil ResetsAt")
	}
}

// --- Pure function tests ---

func TestFindResetBoundaries_Empty(t *testing.T) {
	t.Parallel()
	boundaries := findResetBoundaries(nil)
	if len(boundaries) != 0 {
		t.Errorf("Expected 0 boundaries for nil input, got %d", len(boundaries))
	}

	boundaries = findResetBoundaries([]snapshotPoint{})
	if len(boundaries) != 0 {
		t.Errorf("Expected 0 boundaries for empty input, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_SingleSnapshot(t *testing.T) {
	t.Parallel()
	boundaries := findResetBoundaries([]snapshotPoint{
		{CapturedAt: time.Now(), Utilization: 10},
	})
	if len(boundaries) != 0 {
		t.Errorf("Expected 0 boundaries for single snapshot, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_ResetsAtChanged(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r1 := base.Add(5 * time.Hour)
	r2 := base.Add(10 * time.Hour) // Different by 5 hours > 10 min threshold

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10, ResetsAt: &r1},
		{CapturedAt: base.Add(30 * time.Minute), Utilization: 20, ResetsAt: &r1},
		{CapturedAt: base.Add(1 * time.Hour), Utilization: 5, ResetsAt: &r2}, // Boundary here
		{CapturedAt: base.Add(90 * time.Minute), Utilization: 15, ResetsAt: &r2},
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 1 {
		t.Fatalf("Expected 1 boundary, got %d", len(boundaries))
	}
	if !boundaries[0].Time.Equal(base.Add(1 * time.Hour)) {
		t.Errorf("Boundary time = %v, want %v", boundaries[0].Time, base.Add(1*time.Hour))
	}
}

func TestFindResetBoundaries_ResetsAtAppeared(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r1 := base.Add(5 * time.Hour)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10, ResetsAt: nil},
		{CapturedAt: base.Add(30 * time.Minute), Utilization: 20, ResetsAt: &r1}, // Appeared
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 1 {
		t.Fatalf("Expected 1 boundary when ResetsAt appeared, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_ResetsAtDisappeared(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r1 := base.Add(5 * time.Hour)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10, ResetsAt: &r1},
		{CapturedAt: base.Add(30 * time.Minute), Utilization: 20, ResetsAt: nil}, // Disappeared
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 1 {
		t.Fatalf("Expected 1 boundary when ResetsAt disappeared, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_UtilizationDrop(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 50, ResetsAt: nil},
		{CapturedAt: base.Add(10 * time.Minute), Utilization: 10, ResetsAt: nil}, // Drop > 20
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 1 {
		t.Fatalf("Expected 1 boundary on utilization drop, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_TimeGapWithDrop(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10, ResetsAt: nil},
		{CapturedAt: base.Add(2 * time.Hour), Utilization: 5, ResetsAt: nil}, // Gap > 1h with drop
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 1 {
		t.Fatalf("Expected 1 boundary on time gap + drop, got %d", len(boundaries))
	}
}

func TestFindResetBoundaries_NoChange(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r1 := base.Add(5 * time.Hour)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10, ResetsAt: &r1},
		{CapturedAt: base.Add(10 * time.Minute), Utilization: 20, ResetsAt: &r1},
		{CapturedAt: base.Add(20 * time.Minute), Utilization: 30, ResetsAt: &r1},
	}

	boundaries := findResetBoundaries(snapshots)
	if len(boundaries) != 0 {
		t.Errorf("Expected 0 boundaries with no reset, got %d", len(boundaries))
	}
}

func TestFilterSnapshotsByRange(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10},
		{CapturedAt: base.Add(1 * time.Hour), Utilization: 20},
		{CapturedAt: base.Add(2 * time.Hour), Utilization: 30},
		{CapturedAt: base.Add(3 * time.Hour), Utilization: 40},
	}

	// Filter [+30min, +2.5h) should include +1h and +2h
	filtered := filterSnapshotsByRange(snapshots, base.Add(30*time.Minute), base.Add(150*time.Minute))
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered snapshots, got %d", len(filtered))
	}
}

func TestFilterSnapshotsByRange_IncludesStart(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10},
		{CapturedAt: base.Add(1 * time.Hour), Utilization: 20},
	}

	// Start exactly equals first snapshot
	filtered := filterSnapshotsByRange(snapshots, base, base.Add(2*time.Hour))
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered snapshots (start inclusive), got %d", len(filtered))
	}
}

func TestFilterSnapshotsByRange_ExcludesEnd(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	snapshots := []snapshotPoint{
		{CapturedAt: base, Utilization: 10},
		{CapturedAt: base.Add(1 * time.Hour), Utilization: 20},
	}

	// End exactly equals second snapshot
	filtered := filterSnapshotsByRange(snapshots, base, base.Add(1*time.Hour))
	if len(filtered) != 1 {
		t.Errorf("Expected 1 filtered snapshot (end exclusive), got %d", len(filtered))
	}
}

func TestCalculateCycleStats_Empty(t *testing.T) {
	t.Parallel()
	peak, delta := calculateCycleStats(nil)
	if peak != 0 || delta != 0 {
		t.Errorf("Expected (0, 0) for empty input, got (%v, %v)", peak, delta)
	}
}

func TestCalculateCycleStats_SingleSnapshot(t *testing.T) {
	t.Parallel()
	snapshots := []snapshotPoint{
		{Utilization: 42},
	}
	peak, delta := calculateCycleStats(snapshots)
	if peak != 42 {
		t.Errorf("peak = %v, want 42", peak)
	}
	if delta != 0 {
		t.Errorf("delta = %v, want 0", delta)
	}
}

func TestCalculateCycleStats_MultipleSnapshots(t *testing.T) {
	t.Parallel()
	snapshots := []snapshotPoint{
		{Utilization: 10},
		{Utilization: 30},
		{Utilization: 20}, // Drop - should not add to delta
		{Utilization: 50},
	}
	peak, delta := calculateCycleStats(snapshots)
	if peak != 50 {
		t.Errorf("peak = %v, want 50", peak)
	}
	// Delta: (30-10) + (50-20) = 20 + 30 = 50
	if delta != 50 {
		t.Errorf("delta = %v, want 50", delta)
	}
}

func TestStore_CountBadCycles_Combined(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Add a bad anthropic cycle
	resetsAt := base.Add(5 * time.Hour)
	_, err = s.CreateAnthropicCycle("five_hour", base, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle failed: %v", err)
	}
	err = s.CloseAnthropicCycle("five_hour", base.Add(7*time.Hour), 50, 30)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle failed: %v", err)
	}

	// Add a bad synthetic cycle
	_, err = s.CreateCycle("search", base, base.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}
	err = s.CloseCycle("search", base.Add(3*time.Hour), 100, 50)
	if err != nil {
		t.Fatalf("CloseCycle failed: %v", err)
	}

	count, err := s.countBadCycles()
	if err != nil {
		t.Fatalf("countBadCycles failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 bad cycles total, got %d", count)
	}
}
