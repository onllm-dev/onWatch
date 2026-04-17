package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// ---------------------------------------------------------------------------
// Tracker.Process – reset via time-based and api-based paths with onReset
// ---------------------------------------------------------------------------

// TestTracker_Process_TimeBasedReset_WithHasLastValues exercises the
// time-based reset path (capturedAt > RenewsAt+2min) when hasLastValues=true,
// including the positive delta accumulation branch before closing the cycle.
func TestTracker_Process_TimeBasedReset_WithHasLastValues(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := New(s, nil)
	baseTime := time.Now()
	renewsAt := baseTime.Add(1 * time.Hour)

	// Snapshot 1 – creates cycles
	snap1 := &api.Snapshot{
		CapturedAt: baseTime,
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 200, RenewsAt: renewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 50, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 100, RenewsAt: baseTime.Add(100 * time.Hour)},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Snapshot 2 – same cycle, usage increases (hasLastValues becomes true)
	snap2 := &api.Snapshot{
		CapturedAt: baseTime.Add(30 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 350, RenewsAt: renewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 60, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 110, RenewsAt: baseTime.Add(100 * time.Hour)},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Snapshot 3 – capturedAt is past renewsAt+2min, triggering time-based reset.
	// Sub delta from snap2 to snap3 is positive → exercises delta accumulation
	// in the reset path.
	snap3 := &api.Snapshot{
		CapturedAt: baseTime.Add(2*time.Hour + 5*time.Minute),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 400, RenewsAt: baseTime.Add(7 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 70, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 120, RenewsAt: baseTime.Add(100 * time.Hour)},
	}
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3: %v", err)
	}

	history, err := s.QueryCycleHistory("subscription")
	if err != nil {
		t.Fatalf("QueryCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed subscription cycle, got %d", len(history))
	}

	// New active cycle should exist
	active, err := s.QueryActiveCycle("subscription")
	if err != nil {
		t.Fatalf("QueryActiveCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected new active subscription cycle after time-based reset")
	}
}

// TestTracker_Process_APIBasedReset_WithHasLastValues exercises the
// api-based reset path (renewsAt hour changed) when hasLastValues=true and
// delta is positive so it gets accumulated before closing.
func TestTracker_Process_APIBasedReset_WithHasLastValues_PositiveDelta(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := New(s, nil)
	var resetCount int
	tr.SetOnReset(func(_ string) { resetCount++ })

	baseTime := time.Now().Truncate(time.Hour)
	renewsAt := baseTime.Add(5 * time.Hour)

	// Snapshot 1
	if err := tr.Process(&api.Snapshot{
		CapturedAt: baseTime,
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 100, RenewsAt: renewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 20, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 50, RenewsAt: baseTime.Add(100 * time.Hour)},
	}); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Snapshot 2 – increases requests so hasLastValues is true and delta > 0
	if err := tr.Process(&api.Snapshot{
		CapturedAt: baseTime.Add(2 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 200, RenewsAt: renewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 30, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 60, RenewsAt: baseTime.Add(100 * time.Hour)},
	}); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Snapshot 3 – RenewsAt shifts by a full hour (api-based reset), positive delta
	newRenewsAt := renewsAt.Add(5 * time.Hour) // different hour bucket
	if err := tr.Process(&api.Snapshot{
		CapturedAt: baseTime.Add(3 * time.Minute),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 250, RenewsAt: newRenewsAt},
		Search:     api.QuotaInfo{Limit: 250, Requests: 35, RenewsAt: baseTime.Add(100 * time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 70, RenewsAt: baseTime.Add(100 * time.Hour)},
	}); err != nil {
		t.Fatalf("Process snap3: %v", err)
	}

	if resetCount == 0 {
		t.Error("expected at least one reset callback for subscription")
	}

	history, err := s.QueryCycleHistory("subscription")
	if err != nil {
		t.Fatalf("QueryCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle, got %d", len(history))
	}
}

// TestTracker_Process_Error exercises the error-return path in Process when
// no store is available (simulate by passing a nil store to trigger panic –
// instead, use a closed store).
func TestTracker_Process_ClosedStore_ReturnsError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close() // close immediately – subsequent queries will error

	tr := New(s, nil)
	snap := &api.Snapshot{
		CapturedAt: time.Now(),
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 10, RenewsAt: time.Now().Add(time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 5, RenewsAt: time.Now().Add(time.Hour)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 50, RenewsAt: time.Now().Add(time.Hour)},
	}

	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// ---------------------------------------------------------------------------
// AnthropicTracker.processQuota – time-based reset, nil ResetsAt paths
// ---------------------------------------------------------------------------

// TestAnthropicTracker_ProcessQuota_TimeBasedReset exercises the time-based
// reset detection branch (cycle.ResetsAt != nil && capturedAt > ResetsAt+2min).
func TestAnthropicTracker_ProcessQuota_TimeBasedReset(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()
	resetsAt1 := baseTime.Add(1 * time.Hour)

	// First snapshot
	snap1 := makeAnthropicSnapshot(baseTime, "five_hour", 30.0, &resetsAt1)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – same cycle, raise utilization (hasLast becomes true)
	snap2 := makeAnthropicSnapshot(baseTime.Add(30*time.Minute), "five_hour", 45.0, &resetsAt1)
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Third snapshot – capturedAt is past resetsAt1+2min → time-based reset
	// ResetsAt in API response is new (same as old so no api-based, but time-based fires)
	capturedAt3 := resetsAt1.Add(3 * time.Minute)
	snap3 := makeAnthropicSnapshot(capturedAt3, "five_hour", 5.0, &resetsAt1)
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3 (time-based reset): %v", err)
	}

	history, err := s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle after time-based reset, got %d", len(history))
	}

	active, err := s.QueryActiveAnthropicCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	if active == nil {
		t.Fatal("expected new active cycle after time-based reset")
	}
}

// TestAnthropicTracker_ProcessQuota_NilResetsAt_FirstSnapshot exercises the
// nil ResetsAt case when creating first cycle.
func TestAnthropicTracker_ProcessQuota_NilResetsAt_FirstSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()

	// ResetsAt is nil
	snap := makeAnthropicSnapshot(baseTime, "five_hour", 20.0, nil)
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process snap (nil ResetsAt): %v", err)
	}

	cycle, err := s.QueryActiveAnthropicCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle even with nil ResetsAt")
	}
	if cycle.PeakUtilization != 20.0 {
		t.Errorf("PeakUtilization = %v, want 20.0", cycle.PeakUtilization)
	}
	// ResetsAt should be nil in the DB
	if cycle.ResetsAt != nil {
		t.Errorf("expected nil ResetsAt in cycle, got %v", cycle.ResetsAt)
	}
}

// TestAnthropicTracker_ProcessQuota_NilResetsAt_SecondSnapshot exercises the
// "new ResetsAt appeared" api-based reset branch (quota.ResetsAt != nil &&
// cycle.ResetsAt == nil).
func TestAnthropicTracker_ProcessQuota_NilResetsAt_ThenResetsAtAppears(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()

	// First snapshot with nil ResetsAt
	snap1 := makeAnthropicSnapshot(baseTime, "five_hour", 15.0, nil)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot with ResetsAt now set → "new ResetsAt appeared" branch
	newResetsAt := baseTime.Add(5 * time.Hour)
	snap2 := makeAnthropicSnapshot(baseTime.Add(time.Minute), "five_hour", 20.0, &newResetsAt)
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Should have triggered reset
	history, err := s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle (new ResetsAt appeared), got %d", len(history))
	}
}

// TestAnthropicTracker_ProcessQuota_ExistingCycle_NoLastForThisQuota exercises
// the branch inside the same-cycle update where hasLast=true but the specific
// quota is not yet in lastValues map (first time seeing quota after restart).
func TestAnthropicTracker_ProcessQuota_NewQuotaSeenAfterOtherQuotaProcessed(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()
	resetsAt := baseTime.Add(5 * time.Hour)

	// Process a snapshot with only "five_hour" to set hasLast=true
	snap1 := makeAnthropicSnapshot(baseTime, "five_hour", 10.0, &resetsAt)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Now process a snapshot that has BOTH quotas but "seven_day" was never seen before.
	// hasLast is now true, but lastValues["seven_day"] doesn't exist.
	sevenDayReset := baseTime.Add(7 * 24 * time.Hour)
	snap2 := makeMultiQuotaSnapshot(baseTime.Add(time.Minute), []api.AnthropicQuota{
		{Name: "five_hour", Utilization: 20.0, ResetsAt: &resetsAt},
		{Name: "seven_day", Utilization: 15.0, ResetsAt: &sevenDayReset},
	})
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// "seven_day" should now have an active cycle created with initial peak
	cycle, err := s.QueryActiveAnthropicCycle("seven_day")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle(seven_day): %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle for seven_day")
	}

	// Now process a third snapshot for seven_day where util > peak → exercises
	// the "new quota after restart" update-peak-if-higher branch
	snap3 := makeMultiQuotaSnapshot(baseTime.Add(2*time.Minute), []api.AnthropicQuota{
		{Name: "five_hour", Utilization: 25.0, ResetsAt: &resetsAt},
		{Name: "seven_day", Utilization: 25.0, ResetsAt: &sevenDayReset},
	})
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3: %v", err)
	}

	updatedCycle, err := s.QueryActiveAnthropicCycle("seven_day")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle(seven_day) after snap3: %v", err)
	}
	if updatedCycle.PeakUtilization != 25.0 {
		t.Errorf("PeakUtilization = %v, want 25.0", updatedCycle.PeakUtilization)
	}
}

// ---------------------------------------------------------------------------
// NewCodexTracker / NewCopilotTracker – nil logger branch
// ---------------------------------------------------------------------------

// TestNewCodexTracker_NilLogger verifies nil logger is handled gracefully.
func TestNewCodexTracker_NilLogger(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, nil) // nil logger → should use slog.Default()
	if tr == nil {
		t.Fatal("expected non-nil CodexTracker")
	}
	if tr.logger == nil {
		t.Fatal("expected non-nil logger after nil logger construction")
	}
}

// TestNewCopilotTracker_NilLogger verifies nil logger is handled gracefully.
func TestNewCopilotTracker_NilLogger(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, nil) // nil logger → should use slog.Default()
	if tr == nil {
		t.Fatal("expected non-nil CopilotTracker")
	}
	if tr.logger == nil {
		t.Fatal("expected non-nil logger after nil logger construction")
	}
}

// TestNewCodexTracker_WithLogger verifies explicit logger is kept.
func TestNewCodexTracker_WithLogger(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	logger := slog.Default()
	tr := NewCodexTracker(s, logger)
	if tr.logger != logger {
		t.Error("expected logger to be the one passed in")
	}
}

// TestNewCopilotTracker_WithLogger verifies explicit logger is kept.
func TestNewCopilotTracker_WithLogger(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	logger := slog.Default()
	tr := NewCopilotTracker(s, logger)
	if tr.logger != logger {
		t.Error("expected logger to be the one passed in")
	}
}

// ---------------------------------------------------------------------------
// CodexTracker.processQuota – reset via utilization drop (diff>threshold)
// ---------------------------------------------------------------------------

// TestCodexTracker_processQuota_ResetViaUtilizationDrop exercises the branch:
// diff > codexResetShiftThreshold && hasLast && currentUtil+2 < lastUtil
func TestCodexTracker_processQuota_ResetViaUtilizationDrop(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	now := time.Now().UTC()
	reset1 := now.Add(5 * time.Hour)

	// First snapshot
	snap1 := &api.CodexSnapshot{
		CapturedAt: now,
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 80, ResetsAt: &reset1, Status: "warning"},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – reset timestamp shifts by >60min AND util drops materially
	// (80 -> 5, so 5+2 < 80 → reset detected)
	reset2 := now.Add(12 * time.Hour) // shift > 60 min
	snap2 := &api.CodexSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 5, ResetsAt: &reset2, Status: "healthy"},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryCodexCycleHistory(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle (util-drop reset), got %d", len(history))
	}
}

// TestCodexTracker_processQuota_LargeShift_NoUtilDrop_NoReset exercises the
// branch: diff > codexResetShiftThreshold but currentUtil+2 >= lastUtil, so NO reset.
func TestCodexTracker_processQuota_LargeShift_NoUtilDrop_NoReset(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	now := time.Now().UTC()
	reset1 := now.Add(5 * time.Hour)

	// First snapshot
	snap1 := &api.CodexSnapshot{
		CapturedAt: now,
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 40, ResetsAt: &reset1, Status: "warning"},
		},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – reset timestamp shifts by >60min but utilization stays HIGH
	// (40 -> 42, so 42+2=44 >= 40 → no reset detected, just cycle reset-at update)
	reset2 := now.Add(12 * time.Hour)
	snap2 := &api.CodexSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 42, ResetsAt: &reset2, Status: "warning"},
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryCodexCycleHistory(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 closed cycles (no util-drop), got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// CodexTracker.UsageSummary – active cycle with rate calculation
// ---------------------------------------------------------------------------

// TestCodexTracker_UsageSummary_ActiveCycleWithRate exercises the rate &
// projected util path inside UsageSummary when the cycle is old enough.
func TestCodexTracker_UsageSummary_ActiveCycleWithRate(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create a cycle with start 2 hours ago and delta > 0
	cycleStart := time.Now().UTC().Add(-2 * time.Hour)
	resetAt := time.Now().UTC().Add(3 * time.Hour)
	if _, err := s.CreateCodexCycle(store.DefaultCodexAccountID, "daily", cycleStart, &resetAt); err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	if err := s.UpdateCodexCycle(store.DefaultCodexAccountID, "daily", 60, 30); err != nil {
		t.Fatalf("UpdateCodexCycle: %v", err)
	}

	snap := &api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.CodexQuota{
			{Name: "daily", Utilization: 55, ResetsAt: &resetAt, Status: "warning"},
		},
	}
	if _, err := s.InsertCodexSnapshot(snap); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	tr := NewCodexTracker(s, slog.Default())
	summary, err := tr.UsageSummary(store.DefaultCodexAccountID, "daily")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CurrentRate <= 0 {
		t.Errorf("expected CurrentRate > 0, got %v", summary.CurrentRate)
	}
	if summary.ProjectedUtil <= 0 {
		t.Errorf("expected ProjectedUtil > 0, got %v", summary.ProjectedUtil)
	}
}

// TestCodexTracker_UsageSummary_NoCycles verifies clean state.
func TestCodexTracker_UsageSummary_NoCycles(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	summary, err := tr.UsageSummary(store.DefaultCodexAccountID, "unknown_quota")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.QuotaName != "unknown_quota" {
		t.Errorf("QuotaName = %q, want 'unknown_quota'", summary.QuotaName)
	}
	if summary.CompletedCycles != 0 {
		t.Errorf("CompletedCycles = %d, want 0", summary.CompletedCycles)
	}
}

// ---------------------------------------------------------------------------
// CopilotTracker.processQuota – time-based reset (reset date passed + remaining up)
// ---------------------------------------------------------------------------

// TestCopilotTracker_processQuota_TimeBasedReset exercises the time-based reset
// path: cycle.ResetDate != nil && capturedAt after ResetDate && remaining increased.
func TestCopilotTracker_processQuota_TimeBasedReset(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()
	// Set reset date in the past
	resetDate := now.Add(-1 * time.Hour)

	// First snapshot
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now.Add(-2 * time.Hour),
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 500}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – capturedAt is after resetDate, remaining increased (reset)
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate, // same reset date string so not detected by string compare
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1400}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	history, err := s.QueryCopilotCycleHistory("premium_interactions")
	if err != nil {
		t.Fatalf("QueryCopilotCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle (time-based reset), got %d", len(history))
	}
}

// TestCopilotTracker_Process_NilResetDate tests Process when snapshot.ResetDate is nil.
func TestCopilotTracker_Process_NilResetDate(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()

	snap := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  nil, // nil reset date
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000}},
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process with nil ResetDate: %v", err)
	}

	cycle, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle with nil reset date")
	}
}

// ---------------------------------------------------------------------------
// ZaiTracker.processTokensQuota – time-based and api-based reset paths
// ---------------------------------------------------------------------------

// TestZaiTracker_processTokensQuota_TimeBasedReset exercises the time-based
// reset path for tokens (cycle.NextReset != nil && capturedAt > NextReset+2min).
func TestZaiTracker_processTokensQuota_TimeBasedReset(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewZaiTracker(s, nil)
	baseTime := time.Now()
	nextReset := baseTime.Add(1 * time.Hour)

	// Snapshot 1
	s1 := makeZaiSnapshot(baseTime, 50000, 100, &nextReset)
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Snapshot 2 – same cycle, increase to set hasLastValues
	s2 := makeZaiSnapshot(baseTime.Add(30*time.Minute), 80000, 150, &nextReset)
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	// Snapshot 3 – capturedAt is past nextReset+2min → time-based reset
	capturedAt3 := nextReset.Add(3 * time.Minute)
	s3 := makeZaiSnapshot(capturedAt3, 5000, 160, &nextReset)
	if err := tr.Process(s3); err != nil {
		t.Fatalf("Process s3 (time-based reset): %v", err)
	}

	history, err := s.QueryZaiCycleHistory("tokens")
	if err != nil {
		t.Fatalf("QueryZaiCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed tokens cycle (time-based), got %d", len(history))
	}
}

// TestZaiTracker_processTokensQuota_APIBasedReset_NewResetAppeared exercises
// the "new NextReset appeared" branch (snapshot has NextReset, cycle had nil).
func TestZaiTracker_processTokensQuota_APIBasedReset_NewResetAppeared(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewZaiTracker(s, nil)
	baseTime := time.Now()

	// First snapshot with nil nextReset
	s1 := makeZaiSnapshot(baseTime, 50000, 100, nil)
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Second snapshot with non-nil nextReset → api-based "appeared" reset
	newReset := baseTime.Add(24 * time.Hour)
	s2 := makeZaiSnapshot(baseTime.Add(time.Minute), 1000, 110, &newReset)
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2 (new reset appeared): %v", err)
	}

	history, err := s.QueryZaiCycleHistory("tokens")
	if err != nil {
		t.Fatalf("QueryZaiCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed tokens cycle (new reset appeared), got %d", len(history))
	}
}

// TestZaiTracker_Process_ClosedStore_ReturnsError verifies error propagation
// from processTokensQuota.
func TestZaiTracker_Process_ClosedStore_ReturnsError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewZaiTracker(s, nil)
	snap := makeZaiSnapshot(time.Now(), 1000, 100, nil)
	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// ---------------------------------------------------------------------------
// ZaiTracker.processTimeQuota – zero value and reset path
// ---------------------------------------------------------------------------

// TestZaiTracker_processTimeQuota_ZeroLastValue verifies that when lastTimeValue
// is 0, no reset is detected (avoids divide-by-zero / false positive).
func TestZaiTracker_processTimeQuota_ZeroLastValue(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewZaiTracker(s, nil)
	baseTime := time.Now()
	resetTime := baseTime.Add(24 * time.Hour)

	// Snapshot 1 – timeValue = 0 (initial zero value)
	s1 := makeZaiSnapshot(baseTime, 1000, 0, &resetTime)
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Snapshot 2 – timeValue goes up from 0; since lastTimeValue=0 no reset
	s2 := makeZaiSnapshot(baseTime.Add(time.Minute), 2000, 100, &resetTime)
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	history, err := s.QueryZaiCycleHistory("time")
	if err != nil {
		t.Fatalf("QueryZaiCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 closed time cycles (zero last value), got %d", len(history))
	}
}

// TestZaiTracker_processTimeQuota_ResetCallback verifies the onReset callback
// is called when a time-quota reset is detected.
func TestZaiTracker_processTimeQuota_ResetCallback(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewZaiTracker(s, nil)
	var resetQuota string
	tr.SetOnReset(func(q string) { resetQuota = q })

	baseTime := time.Now()
	resetTime := baseTime.Add(24 * time.Hour)

	// Snapshot 1 – high time value
	s1 := makeZaiSnapshot(baseTime, 50000, 900, &resetTime)
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Snapshot 2 – time value drops >50% → reset detected
	s2 := makeZaiSnapshot(baseTime.Add(time.Minute), 55000, 100, &resetTime)
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	if resetQuota != "time" {
		t.Errorf("onReset called with %q, want 'time'", resetQuota)
	}
}

// TestZaiTracker_processTimeQuota_ExistingCycleAfterRestart_UpdatesPeak
// exercises the "first snapshot after restart, existing cycle in DB" path
// in processTimeQuota (no hasLastValues, update peak if higher).
func TestZaiTracker_processTimeQuota_ExistingCycleAfterRestart_UpdatesPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create a "time" cycle in DB directly (simulating an existing cycle from
	// a previous run).
	base := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	if _, err := s.CreateZaiCycle("time", base, nil); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("time", 200, 50); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	// New tracker – hasLastValues = false
	tr := NewZaiTracker(s, nil)
	resetTime := base.Add(24 * time.Hour)
	snap := makeZaiSnapshot(base.Add(10*time.Minute), 5000, 400, &resetTime)

	// Process only the time quota part directly
	if err := tr.processTimeQuota(snap); err != nil {
		t.Fatalf("processTimeQuota: %v", err)
	}

	cycle, err := s.QueryActiveZaiCycle("time")
	if err != nil {
		t.Fatalf("QueryActiveZaiCycle: %v", err)
	}
	// 400 > 200 so peak should have been updated
	if cycle.PeakValue != 400 {
		t.Errorf("PeakValue = %d, want 400", cycle.PeakValue)
	}
	// TotalDelta preserved
	if cycle.TotalDelta != 50 {
		t.Errorf("TotalDelta = %d, want 50", cycle.TotalDelta)
	}
}

// ---------------------------------------------------------------------------
// ZaiTracker.UsageSummary – active/no cycles, time quota
// ---------------------------------------------------------------------------

// TestZaiTracker_UsageSummary_ActiveCycleWithRate exercises the rate calculation
// path when there is an active cycle and a recent snapshot.
func TestZaiTracker_UsageSummary_ActiveCycleWithRate(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().UTC().Add(-2 * time.Hour)
	nextReset := time.Now().UTC().Add(22 * time.Hour)
	if _, err := s.CreateZaiCycle("tokens", cycleStart, &nextReset); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("tokens", 100000, 50000); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	snap := makeZaiSnapshot(time.Now().UTC(), 80000, 500, &nextReset)
	if _, err := s.InsertZaiSnapshot(snap); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	tr := NewZaiTracker(s, nil)
	summary, err := tr.UsageSummary("tokens")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CurrentUsage != 80000 {
		t.Errorf("CurrentUsage = %v, want 80000", summary.CurrentUsage)
	}
	if summary.CurrentRate <= 0 {
		t.Errorf("expected CurrentRate > 0, got %v", summary.CurrentRate)
	}
	if summary.ProjectedUsage <= 0 {
		t.Errorf("expected ProjectedUsage > 0, got %v", summary.ProjectedUsage)
	}
	if summary.RenewsAt == nil {
		t.Error("expected RenewsAt to be set")
	}
}

// TestZaiTracker_UsageSummary_TimeQuotaActive exercises the "time" branch in
// UsageSummary and the RenewsAt fallback from TokensNextResetTime.
func TestZaiTracker_UsageSummary_TimeQuotaActive(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().UTC().Add(-1 * time.Hour)
	// time quota has no NextReset (nil)
	if _, err := s.CreateZaiCycle("time", cycleStart, nil); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("time", 800, 200); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	snap := makeZaiSnapshot(time.Now().UTC(), 5000, 600, nil)
	if _, err := s.InsertZaiSnapshot(snap); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	tr := NewZaiTracker(s, nil)
	summary, err := tr.UsageSummary("time")
	if err != nil {
		t.Fatalf("UsageSummary(time): %v", err)
	}
	if summary.CurrentUsage != 600 {
		t.Errorf("CurrentUsage = %v, want 600", summary.CurrentUsage)
	}
	if summary.CurrentLimit != 1000 {
		t.Errorf("CurrentLimit = %v, want 1000", summary.CurrentLimit)
	}
	if summary.UsagePercent == 0 {
		t.Error("expected non-zero UsagePercent")
	}
}

// TestZaiTracker_UsageSummary_WithCompletedCycles tests history aggregation.
func TestZaiTracker_UsageSummary_WithCompletedCycles(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewZaiTracker(s, nil)
	baseTime := time.Now()
	resetTime1 := baseTime.Add(24 * time.Hour)

	// Cycle 1: 50000 → 100000 (delta = 50000)
	s1 := makeZaiSnapshot(baseTime, 50000, 100, &resetTime1)
	tr.Process(s1)
	s2 := makeZaiSnapshot(baseTime.Add(time.Minute), 100000, 200, &resetTime1)
	tr.Process(s2)

	// Trigger reset → cycle 1 closed
	resetTime2 := baseTime.Add(48 * time.Hour)
	s3 := makeZaiSnapshot(baseTime.Add(2*time.Minute), 10000, 210, &resetTime2)
	s.InsertZaiSnapshot(s3)
	tr.Process(s3)

	summary, err := tr.UsageSummary("tokens")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CompletedCycles != 1 {
		t.Errorf("CompletedCycles = %d, want 1", summary.CompletedCycles)
	}
	if summary.AvgPerCycle <= 0 {
		t.Errorf("AvgPerCycle = %v, want > 0", summary.AvgPerCycle)
	}
	if summary.TrackingSince.IsZero() {
		t.Error("expected TrackingSince to be set")
	}
}

// TestZaiTracker_UsageSummary_TokensRenewsAtFallback exercises the branch
// where RenewsAt is nil in the active cycle but set in the latest snapshot.
func TestZaiTracker_UsageSummary_TokensRenewsAtFallback(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().UTC().Add(-30 * time.Minute)
	// Create cycle with nil NextReset
	if _, err := s.CreateZaiCycle("tokens", cycleStart, nil); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("tokens", 50000, 10000); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	// The snapshot has TokensNextResetTime set
	nextReset := time.Now().UTC().Add(24 * time.Hour)
	snap := makeZaiSnapshot(time.Now().UTC(), 40000, 200, &nextReset)
	if _, err := s.InsertZaiSnapshot(snap); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	tr := NewZaiTracker(s, nil)
	summary, err := tr.UsageSummary("tokens")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	// RenewsAt should fall back to the snapshot's TokensNextResetTime
	if summary.RenewsAt == nil {
		t.Error("expected RenewsAt to fall back from snapshot TokensNextResetTime")
	}
}

// ---------------------------------------------------------------------------
// CodexTracker.UsageSummary – history with completed cycles
// ---------------------------------------------------------------------------

// TestCodexTracker_UsageSummary_WithHistory exercises the completed cycles
// aggregation path (history > 0) and verifies TrackingSince, AvgPerCycle, etc.
func TestCodexTracker_UsageSummary_WithHistory(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	now := time.Now().UTC()
	reset1 := now.Add(5 * time.Hour)

	// Cycle 1: 20 → 45 (delta = 25)
	snap1 := &api.CodexSnapshot{
		CapturedAt: now,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 20, ResetsAt: &reset1}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}
	snap2 := &api.CodexSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 45, ResetsAt: &reset1}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Trigger reset (utilization drops + reset time shifts >60min)
	reset2 := now.Add(12 * time.Hour)
	snap3 := &api.CodexSnapshot{
		CapturedAt: now.Add(2 * time.Minute),
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 3, ResetsAt: &reset2}},
	}
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3 (reset): %v", err)
	}

	// Also insert snap3 so QueryLatestCodex has data
	if _, err := s.InsertCodexSnapshot(snap3); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	summary, err := tr.UsageSummary(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CompletedCycles != 1 {
		t.Errorf("CompletedCycles = %d, want 1", summary.CompletedCycles)
	}
	if summary.AvgPerCycle <= 0 {
		t.Errorf("AvgPerCycle = %v, want > 0", summary.AvgPerCycle)
	}
	if summary.TrackingSince.IsZero() {
		t.Error("expected TrackingSince to be set")
	}
}

// TestCodexTracker_UsageSummary_ActiveCycleNoReset exercises UsageSummary
// when there is an active cycle but ResetsAt is nil, and no snapshot available.
func TestCodexTracker_UsageSummary_ActiveCycleNoLatestSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().UTC().Add(-1 * time.Hour)
	if _, err := s.CreateCodexCycle(store.DefaultCodexAccountID, "five_hour", cycleStart, nil); err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	if err := s.UpdateCodexCycle(store.DefaultCodexAccountID, "five_hour", 50, 20); err != nil {
		t.Fatalf("UpdateCodexCycle: %v", err)
	}

	// No snapshot inserted so QueryLatestCodex returns nil
	tr := NewCodexTracker(s, slog.Default())
	summary, err := tr.UsageSummary(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	// CurrentUtil should be 0 (no latest snapshot)
	if summary.CurrentUtil != 0 {
		t.Errorf("CurrentUtil = %v, want 0 (no snapshot)", summary.CurrentUtil)
	}
	// ResetsAt should be nil (cycle has nil ResetsAt)
	if summary.ResetsAt != nil {
		t.Errorf("expected nil ResetsAt, got %v", summary.ResetsAt)
	}
}

// ---------------------------------------------------------------------------
// CopilotTracker.UsageSummary – completed cycles path
// ---------------------------------------------------------------------------

// TestCopilotTracker_UsageSummary_WithHistory exercises the completed cycles
// aggregation path (history > 0) for CopilotTracker.
func TestCopilotTracker_UsageSummary_WithHistory(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()
	resetDate1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Cycle 1: 1500 entitlement, start at 1500 → usage increases to 500
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate1,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1500}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate1,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Trigger reset with different reset date
	resetDate2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	snap3 := &api.CopilotSnapshot{
		CapturedAt: now.Add(2 * time.Minute),
		ResetDate:  &resetDate2,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1500}},
	}
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3 (reset): %v", err)
	}

	// Insert latest snapshot for QueryLatestCopilot
	if _, err := s.InsertCopilotSnapshot(snap3); err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CompletedCycles != 1 {
		t.Errorf("CompletedCycles = %d, want 1", summary.CompletedCycles)
	}
	if summary.AvgPerCycle <= 0 {
		t.Errorf("AvgPerCycle = %v, want > 0", summary.AvgPerCycle)
	}
	if summary.TrackingSince.IsZero() {
		t.Error("expected TrackingSince to be set")
	}
}

// TestCopilotTracker_UsageSummary_ActiveCycleNoLatestSnapshot exercises the
// active-cycle path when no snapshot is in the store (latest = nil).
func TestCopilotTracker_UsageSummary_ActiveCycleNoLatestSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Add(-1 * time.Hour)
	resetDate := base.Add(30 * 24 * time.Hour)
	if _, err := s.CreateCopilotCycle("premium_interactions", base, &resetDate); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("premium_interactions", 300, 100); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	// No snapshot inserted so QueryLatestCopilot returns nil
	tr := NewCopilotTracker(s, slog.Default())
	summary, err := tr.UsageSummary("premium_interactions")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	// CurrentUsed should be 0 (no snapshot)
	if summary.CurrentUsed != 0 {
		t.Errorf("CurrentUsed = %d, want 0 (no snapshot)", summary.CurrentUsed)
	}
	// ResetDate should come from the active cycle
	if summary.ResetDate == nil {
		t.Error("expected ResetDate from active cycle")
	}
}

// ---------------------------------------------------------------------------
// Tracker.Process – toolcall and search error paths (via error injection)
// ---------------------------------------------------------------------------

// TestTracker_Process_SearchError exercises the "tracker: search: ..." error
// path by using a store that succeeds for subscription but errors for search.
// We simulate this by running subscription first (creating its cycle) then
// closing the store – but that would error on subscription too. Instead we
// directly call processQuota with a broken quotaType that comes second.
// The simplest approach: create a fresh store, process one snapshot to seed
// cycles for subscription, then close the store; the NEXT process call
// (which tries subscription again) would fail. We want search to fail.
//
// Since we cannot easily make individual quota queries fail independently,
// we test through Process with a fresh closed store to ensure at minimum
// the error propagation from each first-failing quota is exercised.
//
// A more targeted approach: call processQuota directly for "search" with a
// closed store to hit the search error path.
func TestTracker_Process_DirectSearchError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	tr := New(s, nil)
	// Create subscription cycle so it won't trigger an error in processQuota
	// (we need QueryActiveCycle to succeed for subscription but fail for search).
	// We can't do this with a single SQLite store, so instead test processQuota
	// directly for "search" with a closed store.
	s.Close()

	capturedAt := time.Now()
	searchInfo := api.QuotaInfo{Limit: 250, Requests: 10, RenewsAt: capturedAt.Add(time.Hour)}

	err = tr.processQuota("search", capturedAt, searchInfo, &tr.lastSearchRequests)
	if err == nil {
		t.Error("expected error from processQuota with closed store, got nil")
	}
}

// TestTracker_Process_ToolcallError similarly tests processQuota for "toolcall"
// with a closed store.
func TestTracker_Process_DirectToolcallError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	tr := New(s, nil)
	s.Close()

	capturedAt := time.Now()
	toolInfo := api.QuotaInfo{Limit: 5000, Requests: 100, RenewsAt: capturedAt.Add(time.Hour)}

	err = tr.processQuota("toolcall", capturedAt, toolInfo, &tr.lastToolRequests)
	if err == nil {
		t.Error("expected error from processQuota with closed store, got nil")
	}
}

// ---------------------------------------------------------------------------
// AnthropicTracker.Process – error path
// ---------------------------------------------------------------------------

// TestAnthropicTracker_Process_Error exercises the error return path from Process
// when the store is closed.
func TestAnthropicTracker_Process_Error(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()
	resetsAt := baseTime.Add(time.Hour)
	snap := makeAnthropicSnapshot(baseTime, "five_hour", 30.0, &resetsAt)

	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// ---------------------------------------------------------------------------
// CopilotTracker.processQuota – hasLastValues=true but quota not in lastValues
// ---------------------------------------------------------------------------

// TestCopilotTracker_processQuota_HasLastButNoLastForThisQuota exercises the
// branch inside same-cycle update where hasLastValues=true but the specific
// quota name is NOT in lastValues map. This happens when a new quota appears
// after at least one other quota has been processed.
func TestCopilotTracker_processQuota_HasLastButNoLastForQuota(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()
	resetDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// First snapshot with only "premium_interactions" → sets hasLastValues=true
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1200}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot adds "chat" quota for first time (not in lastValues map)
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1100},
			{Name: "chat", Entitlement: 500, Remaining: 480}, // new quota, higher used than initial
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// "chat" should have an active cycle with peak updated
	chatCycle, err := s.QueryActiveCopilotCycle("chat")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle(chat): %v", err)
	}
	if chatCycle == nil {
		t.Fatal("expected active cycle for chat after second snapshot")
	}

	// Third snapshot: "chat" now has higher usage → exercises the
	// "hasLastValues && quota not in lastValues" branch that updates peak
	snap3 := &api.CopilotSnapshot{
		CapturedAt: now.Add(2 * time.Minute),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000},
			{Name: "chat", Entitlement: 500, Remaining: 400},
		},
	}
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3: %v", err)
	}

	// "chat" peak should reflect the higher usage
	updatedCycle, err := s.QueryActiveCopilotCycle("chat")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle(chat) after snap3: %v", err)
	}
	if updatedCycle.PeakUsed != 100 { // 500 - 400
		t.Errorf("chat PeakUsed = %d, want 100", updatedCycle.PeakUsed)
	}
}

// ---------------------------------------------------------------------------
// Process error paths for each tracker
// ---------------------------------------------------------------------------

// TestAntigravityTracker_Process_Error tests error propagation from Process.
func TestAntigravityTracker_Process_Error(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewAntigravityTracker(s, nil)
	resetTime := time.Now().Add(24 * time.Hour)
	snap := makeAntigravitySnapshot(time.Now(), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &resetTime),
	})

	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// TestCodexTracker_Process_Error tests error propagation from Process.
func TestCodexTracker_Process_Error(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewCodexTracker(s, nil)
	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	snap := &api.CodexSnapshot{
		CapturedAt: now,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 30, ResetsAt: &reset}},
	}

	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// TestCopilotTracker_Process_Error tests error propagation from Process.
func TestCopilotTracker_Process_Error(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewCopilotTracker(s, nil)
	now := time.Now().UTC()
	resetDate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	snap := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1000}},
	}

	if err := tr.Process(snap); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

// TestZaiTracker_processTimeQuota_ClosedStore_ReturnsError calls processTimeQuota
// directly with a closed store.
func TestZaiTracker_processTimeQuota_ClosedStore_ReturnsError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewZaiTracker(s, nil)
	snap := makeZaiSnapshot(time.Now(), 1000, 100, nil)

	if err := tr.processTimeQuota(snap); err == nil {
		t.Error("expected error from processTimeQuota with closed store, got nil")
	}
}

// TestZaiTracker_processTokensQuota_ClosedStore_ReturnsError calls processTokensQuota
// directly with a closed store.
func TestZaiTracker_processTokensQuota_ClosedStore_ReturnsError(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	tr := NewZaiTracker(s, nil)
	snap := makeZaiSnapshot(time.Now(), 1000, 100, nil)

	if err := tr.processTokensQuota(snap); err == nil {
		t.Error("expected error from processTokensQuota with closed store, got nil")
	}
}

// ---------------------------------------------------------------------------
// ZaiTracker.processTimeQuota - NoReset because !hasLastValues and value not higher
// ---------------------------------------------------------------------------

// TestZaiTracker_processTimeQuota_ExistingCycle_PeakNotUpdated exercises the
// "first snapshot after restart" path where currentValue <= cycle.PeakValue
// (no update needed).
func TestZaiTracker_processTimeQuota_ExistingCycle_PeakNotUpdated(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	if _, err := s.CreateZaiCycle("time", base, nil); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("time", 900, 300); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	// New tracker – hasLastValues = false
	tr := NewZaiTracker(s, nil)
	resetTime := base.Add(24 * time.Hour)
	// currentValue (100) < cycle.PeakValue (900) → no update
	snap := makeZaiSnapshot(base.Add(5*time.Minute), 5000, 100, &resetTime)

	if err := tr.processTimeQuota(snap); err != nil {
		t.Fatalf("processTimeQuota: %v", err)
	}

	cycle, err := s.QueryActiveZaiCycle("time")
	if err != nil {
		t.Fatalf("QueryActiveZaiCycle: %v", err)
	}
	// PeakValue should NOT have changed (100 < 900)
	if cycle.PeakValue != 900 {
		t.Errorf("PeakValue = %d, want 900 (no update)", cycle.PeakValue)
	}
	if cycle.TotalDelta != 300 {
		t.Errorf("TotalDelta = %d, want 300 (preserved)", cycle.TotalDelta)
	}
}

// ---------------------------------------------------------------------------
// ZaiTracker.processTokensQuota – first snapshot after restart with existing cycle
// ---------------------------------------------------------------------------

// TestZaiTracker_processTokensQuota_ExistingCycleAfterRestart_PeakUpdated exercises
// the "first snapshot after restart" path where currentValue > cycle.PeakValue.
func TestZaiTracker_processTokensQuota_ExistingCycleAfterRestart_PeakUpdated(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)
	if _, err := s.CreateZaiCycle("tokens", base, &nextReset); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("tokens", 1000, 500); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	// New tracker – hasLastValues = false
	tr := NewZaiTracker(s, nil)
	// currentValue (5000) > cycle.PeakValue (1000) → peak should update
	snap := makeZaiSnapshot(base.Add(10*time.Minute), 5000, 100, &nextReset)

	if err := tr.processTokensQuota(snap); err != nil {
		t.Fatalf("processTokensQuota: %v", err)
	}

	cycle, err := s.QueryActiveZaiCycle("tokens")
	if err != nil {
		t.Fatalf("QueryActiveZaiCycle: %v", err)
	}
	if cycle.PeakValue != 5000 {
		t.Errorf("PeakValue = %d, want 5000 (updated)", cycle.PeakValue)
	}
	if cycle.TotalDelta != 500 {
		t.Errorf("TotalDelta = %d, want 500 (preserved)", cycle.TotalDelta)
	}
}

// TestZaiTracker_processTokensQuota_ExistingCycleAfterRestart_PeakNotUpdated exercises
// the "first snapshot after restart" path where currentValue <= cycle.PeakValue.
func TestZaiTracker_processTokensQuota_ExistingCycleAfterRestart_PeakNotUpdated(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	nextReset := base.Add(24 * time.Hour)
	if _, err := s.CreateZaiCycle("tokens", base, &nextReset); err != nil {
		t.Fatalf("CreateZaiCycle: %v", err)
	}
	if err := s.UpdateZaiCycle("tokens", 90000, 40000); err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	tr := NewZaiTracker(s, nil)
	// currentValue (500) < cycle.PeakValue (90000) → no update
	snap := makeZaiSnapshot(base.Add(5*time.Minute), 500, 200, &nextReset)

	if err := tr.processTokensQuota(snap); err != nil {
		t.Fatalf("processTokensQuota: %v", err)
	}

	cycle, err := s.QueryActiveZaiCycle("tokens")
	if err != nil {
		t.Fatalf("QueryActiveZaiCycle: %v", err)
	}
	if cycle.PeakValue != 90000 {
		t.Errorf("PeakValue = %d, want 90000 (no update)", cycle.PeakValue)
	}
}

// ---------------------------------------------------------------------------
// AnthropicTracker.UsageSummary – rate calculation path
// ---------------------------------------------------------------------------

// TestAnthropicTracker_UsageSummary_WithRateAndProjection exercises the rate
// and projection calculation when enough time has passed and delta > 0.
func TestAnthropicTracker_UsageSummary_WithRateAndProjection(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create a cycle that started 2 hours ago with meaningful delta
	cycleStart := time.Now().Add(-2 * time.Hour)
	resetsAt := time.Now().Add(3 * time.Hour)
	if _, err := s.CreateAnthropicCycle("five_hour", cycleStart, &resetsAt); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("five_hour", 50, 30); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// Insert a snapshot so QueryLatestAnthropic has data
	snap := &api.AnthropicSnapshot{
		CapturedAt: time.Now(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 45.0, ResetsAt: &resetsAt},
		},
	}
	if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	tr := NewAnthropicTracker(s, nil)
	summary, err := tr.UsageSummary("five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.CurrentRate <= 0 {
		t.Errorf("expected CurrentRate > 0, got %v", summary.CurrentRate)
	}
	if summary.CurrentUtil != 45.0 {
		t.Errorf("CurrentUtil = %v, want 45.0", summary.CurrentUtil)
	}
}

// TestAnthropicTracker_UsageSummary_ActiveCycleNilResetsAt exercises the branch
// where activeCycle.ResetsAt is nil (no TimeUntilReset set from cycle).
func TestAnthropicTracker_UsageSummary_ActiveCycleNilResetsAt(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().Add(-30 * time.Minute)
	// Create cycle with nil ResetsAt
	if _, err := s.CreateAnthropicCycle("five_hour", cycleStart, nil); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("five_hour", 25, 10); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// Snapshot also has nil ResetsAt
	snap := &api.AnthropicSnapshot{
		CapturedAt: time.Now(),
		Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: 20.0, ResetsAt: nil}},
	}
	if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	tr := NewAnthropicTracker(s, nil)
	summary, err := tr.UsageSummary("five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.ResetsAt != nil {
		t.Errorf("expected nil ResetsAt when both cycle and snapshot have nil, got %v", summary.ResetsAt)
	}
	if summary.CurrentUtil != 20.0 {
		t.Errorf("CurrentUtil = %v, want 20.0", summary.CurrentUtil)
	}
}

// ---------------------------------------------------------------------------
// Tracker.UsageSummary – active cycle path (subscription, search, toolcall)
// ---------------------------------------------------------------------------

// TestTracker_UsageSummary_ActiveCycle_Toolcall exercises the "toolcall" branch
// in UsageSummary's switch statement.
func TestTracker_UsageSummary_ActiveCycle_Toolcall(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := New(s, nil)
	baseTime := time.Now()

	snap := &api.Snapshot{
		CapturedAt: baseTime,
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 100, RenewsAt: baseTime.Add(6 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 50, RenewsAt: baseTime.Add(90 * time.Minute)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 750, RenewsAt: baseTime.Add(4 * time.Hour)},
	}
	if _, err := s.InsertSnapshot(snap); err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Test toolcall summary (covers the "toolcall" case in the switch)
	summary, err := tr.UsageSummary("toolcall")
	if err != nil {
		t.Fatalf("UsageSummary(toolcall): %v", err)
	}
	if summary.CurrentUsage != 750 {
		t.Errorf("CurrentUsage = %v, want 750", summary.CurrentUsage)
	}
	if summary.CurrentLimit != 5000 {
		t.Errorf("CurrentLimit = %v, want 5000", summary.CurrentLimit)
	}
	if summary.UsagePercent != 15 {
		t.Errorf("UsagePercent = %v, want 15", summary.UsagePercent)
	}
}

// ---------------------------------------------------------------------------
// Codex processQuota – "cycle.ResetsAt == nil and quota.ResetsAt != nil" path
// (updateCycleResetAt without a prior reset)
// ---------------------------------------------------------------------------

// TestCodexTracker_processQuota_NilCycleResetAt_QuotaHasResetAt exercises the
// branch where the active cycle has nil ResetsAt but the new quota has one.
func TestCodexTracker_processQuota_NilCycleResetAt_QuotaHasResetAt(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create cycle with nil ResetsAt
	base := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	if _, err := s.CreateCodexCycle(store.DefaultCodexAccountID, "five_hour", base, nil); err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	if err := s.UpdateCodexCycle(store.DefaultCodexAccountID, "five_hour", 20, 5); err != nil {
		t.Fatalf("UpdateCodexCycle: %v", err)
	}

	tr := NewCodexTracker(s, slog.Default())
	// Process quota with non-nil ResetsAt → triggers updateCycleResetAt branch
	newReset := base.Add(5 * time.Hour)
	quota := api.CodexQuota{Name: "five_hour", Utilization: 25, ResetsAt: &newReset, Status: "healthy"}
	if err := tr.processQuota(store.DefaultCodexAccountID, quota, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("processQuota: %v", err)
	}

	// Cycle should now have a ResetsAt set
	cycle, err := s.QueryActiveCodexCycle(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryActiveCodexCycle: %v", err)
	}
	if cycle.ResetsAt == nil {
		t.Error("expected ResetsAt to be set after updateCycleResetAt")
	}
	// Peak should have updated (25 > 20)
	if cycle.PeakUtilization != 25 {
		t.Errorf("PeakUtilization = %v, want 25", cycle.PeakUtilization)
	}
}

// ---------------------------------------------------------------------------
// AntigravityTracker.processModel – uncovered branches
// ---------------------------------------------------------------------------

// TestAntigravityTracker_processModel_ExistingCycle_PeakNotUpdated exercises
// the "first snapshot after restart" path where currentUsage <= cycle.PeakUsage.
func TestAntigravityTracker_processModel_ExistingCycle_PeakNotUpdated(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	reset := base.Add(24 * time.Hour)
	if _, err := s.CreateAntigravityCycle("model-z", base, &reset); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	// Set high peak usage
	if err := s.UpdateAntigravityCycle("model-z", 0.9, 0.5); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	tr := NewAntigravityTracker(s, nil)
	// remaining=0.95 → currentUsage=0.05, which is < cycle.PeakUsage (0.9)
	model := makeModel("model-z", "Model Z", 0.95, &reset)
	if err := tr.processModel(model, base.Add(5*time.Minute)); err != nil {
		t.Fatalf("processModel: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-z")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle: %v", err)
	}
	// PeakUsage should NOT have changed (0.05 < 0.9)
	if cycle.PeakUsage < 0.89 || cycle.PeakUsage > 0.91 {
		t.Errorf("PeakUsage = %v, want ~0.9 (no update)", cycle.PeakUsage)
	}
}

// TestAntigravityTracker_processModel_HasLastButNoLastForThisModel exercises
// the "hasLastValues=true but this specific model not in lastFractions" branch.
func TestAntigravityTracker_processModel_HasLastButNoLastForThisModel(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Now()
	reset := base.Add(24 * time.Hour)

	// First snapshot with "model-a" only → sets hasLastValues=true
	s1 := makeAntigravitySnapshot(base, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.9, &reset),
	})
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Second snapshot adds "model-b" for the first time. hasLastValues=true
	// but lastFractions doesn't have "model-b".
	s2 := makeAntigravitySnapshot(base.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.8, &reset),
		makeModel("model-b", "Model B", 0.7, &reset), // new model
	})
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-b")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle(model-b): %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle for model-b")
	}

	// Third snapshot: model-b usage increases → exercises the
	// "hasLastValues && !ok" branch where currentUsage > cycle.PeakUsage
	s3 := makeAntigravitySnapshot(base.Add(2*time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.7, &reset),
		makeModel("model-b", "Model B", 0.4, &reset), // usage=0.6
	})
	if err := tr.Process(s3); err != nil {
		t.Fatalf("Process s3: %v", err)
	}

	updatedCycle, err := s.QueryActiveAntigravityCycle("model-b")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle(model-b) after s3: %v", err)
	}
	if updatedCycle.PeakUsage < 0.59 || updatedCycle.PeakUsage > 0.61 {
		t.Errorf("model-b PeakUsage = %v, want ~0.6", updatedCycle.PeakUsage)
	}
}

// TestAntigravityTracker_processModel_HasLastButNoLastForThisModel_NotHigher exercises
// the "hasLastValues=true, quota not in lastFractions, currentUsage <= cycle.PeakUsage" branch.
func TestAntigravityTracker_processModel_HasLast_NewModel_NotHigherThanPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Now()
	reset := base.Add(24 * time.Hour)

	// First snapshot with "model-a" only → sets hasLastValues=true
	s1 := makeAntigravitySnapshot(base, []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.5, &reset),
	})
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Create model-c with a high existing peak directly in the DB
	if _, err := s.CreateAntigravityCycle("model-c", base, &reset); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("model-c", 0.95, 0.3); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	// Second snapshot adds "model-c" for first time, but usage (0.1) < existing peak (0.95)
	// hasLastValues=true, !ok for model-c, currentUsage=0.1 < 0.95 → no update
	s2 := makeAntigravitySnapshot(base.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-a", "Model A", 0.45, &reset),
		makeModel("model-c", "Model C", 0.9, &reset), // usage=0.1
	})
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-c")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle(model-c): %v", err)
	}
	// PeakUsage should NOT have changed (0.1 < 0.95)
	if cycle.PeakUsage < 0.94 || cycle.PeakUsage > 0.96 {
		t.Errorf("model-c PeakUsage = %v, want ~0.95 (no update since usage<peak)", cycle.PeakUsage)
	}
}

// ---------------------------------------------------------------------------
// Copilot processQuota – hasLastValues but no lastValues for this quota
// ---------------------------------------------------------------------------

// TestCopilotTracker_processQuota_HasLast_NoLastForQuota_PeakNotHigher exercises
// the branch: hasLastValues=true, quota not in lastValues, currentUsed <= cycle.PeakUsed.
func TestCopilotTracker_processQuota_HasLast_NewQuota_PeakNotHigher(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()
	resetDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// First snapshot – set hasLastValues=true
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1200}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Create "chat" cycle in DB with high existing peak
	if _, err := s.CreateCopilotCycle("chat", now, &resetDate); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("chat", 400, 100); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	// Second snapshot: "chat" appears for first time, low usage (50) < existing peak (400)
	// hasLastValues=true, chat not in lastValues → exercises "else { if currentUsed > cycle.PeakUsed }" NO-OP
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1100},
			{Name: "chat", Entitlement: 500, Remaining: 450}, // used=50 < peak=400
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	chatCycle, err := s.QueryActiveCopilotCycle("chat")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle(chat): %v", err)
	}
	// PeakUsed should still be 400 (50 < 400)
	if chatCycle.PeakUsed != 400 {
		t.Errorf("chat PeakUsed = %d, want 400 (no update)", chatCycle.PeakUsed)
	}
}

// ---------------------------------------------------------------------------
// Tracker.processQuota – "first after restart, existing cycle, peak not higher"
// ---------------------------------------------------------------------------

// TestTracker_processQuota_ExistingCycle_RestartPath_PeakNotHigher exercises
// the !hasLastValues branch where existing cycle peak >= current requests.
func TestTracker_processQuota_ExistingCycle_RestartPath_PeakNotHigher(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	// Create existing cycle with high peak
	if _, err := s.CreateCycle("subscription", base, base.Add(5*time.Hour)); err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if err := s.UpdateCycle("subscription", 900, 400); err != nil {
		t.Fatalf("UpdateCycle: %v", err)
	}

	tr := New(s, nil)
	// info.Requests (100) < cycle.PeakRequests (900) → no update to peak
	info := api.QuotaInfo{Limit: 1000, Requests: 100, RenewsAt: base.Add(5 * time.Hour)}
	if err := tr.processQuota("subscription", base.Add(10*time.Minute), info, &tr.lastSubRequests); err != nil {
		t.Fatalf("processQuota: %v", err)
	}

	cycle, err := s.QueryActiveCycle("subscription")
	if err != nil {
		t.Fatalf("QueryActiveCycle: %v", err)
	}
	// PeakRequests should NOT have changed (100 < 900)
	if cycle.PeakRequests != 900 {
		t.Errorf("PeakRequests = %v, want 900 (no update)", cycle.PeakRequests)
	}
}

// ---------------------------------------------------------------------------
// AntigravityTracker.UsageSummary – cycle.ResetTime in the past (TimeUntilReset capped)
// ---------------------------------------------------------------------------

// TestAntigravityTracker_UsageSummary_CycleResetTimeInPast exercises the
// `summary.TimeUntilReset < 0` branch when the active cycle's ResetTime
// is in the past.
func TestAntigravityTracker_UsageSummary_CycleResetTimeInPast(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Add(-3 * time.Hour)
	// Set ResetTime to 2 hours ago (past)
	pastReset := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := s.CreateAntigravityCycle("model-p", base, &pastReset); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("model-p", 0.5, 0.2); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	tr := NewAntigravityTracker(s, nil)
	summary, err := tr.UsageSummary("model-p")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	// TimeUntilReset should be capped at 0 (not negative)
	if summary.TimeUntilReset != 0 {
		t.Errorf("TimeUntilReset = %v, want 0 (capped for past reset time)", summary.TimeUntilReset)
	}
	if summary.ResetTime == nil {
		t.Error("expected ResetTime to be set from active cycle")
	}
}

// ---------------------------------------------------------------------------
// Anthropic processQuota – diff < 0 (resetsAt went backward)
// ---------------------------------------------------------------------------

// TestAnthropicTracker_processQuota_ResetsAtWentBackward exercises the
// `if diff < 0 { diff = -diff }` branch where the new ResetsAt is BEFORE
// the stored one (backward shift).
func TestAnthropicTracker_processQuota_ResetsAtWentBackward(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()
	resetsAt1 := baseTime.Add(5 * time.Hour)

	// First snapshot
	snap1 := makeAnthropicSnapshot(baseTime, "five_hour", 30.0, &resetsAt1)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – ResetsAt went backward by 2 seconds (jitter, not a reset)
	// But the backward diff is < 10 min so no reset detected, just exercises diff < 0 path
	resetsAtBackward := resetsAt1.Add(-2 * time.Second)
	snap2 := makeAnthropicSnapshot(baseTime.Add(time.Minute), "five_hour", 35.0, &resetsAtBackward)
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2 (backward resetsAt): %v", err)
	}

	// Should still be in the same cycle (diff = 2s, well under 10min threshold)
	history, err := s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 closed cycles for backward shift < 10min, got %d", len(history))
	}
}

// TestAnthropicTracker_processQuota_ResetsAtWentBackwardBig exercises the diff < 0
// case where the backward shift is large (> 10 min), triggering a reset.
func TestAnthropicTracker_processQuota_ResetsAtWentBackwardBig(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	baseTime := time.Now()
	resetsAt1 := baseTime.Add(5 * time.Hour)

	// First snapshot
	snap1 := makeAnthropicSnapshot(baseTime, "five_hour", 30.0, &resetsAt1)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – ResetsAt went backward by 1 hour (>10 min → reset)
	resetsAtBigBackward := resetsAt1.Add(-1 * time.Hour)
	snap2 := makeAnthropicSnapshot(baseTime.Add(time.Minute), "five_hour", 5.0, &resetsAtBigBackward)
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2 (big backward resetsAt): %v", err)
	}

	history, err := s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle for big backward shift, got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// Antigravity processModel – diff < 0 (resetTime went backward)
// ---------------------------------------------------------------------------

// TestAntigravityTracker_processModel_ResetTimeWentBackward exercises the
// `if diff < 0 { diff = -diff }` branch where the model's ResetTime
// goes backward (which is a large shift → reset).
func TestAntigravityTracker_processModel_ResetTimeWentBackward(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Now()
	reset1 := base.Add(24 * time.Hour)

	// First snapshot
	s1 := makeAntigravitySnapshot(base, []api.AntigravityModelQuota{
		makeModel("model-q", "Model Q", 0.5, &reset1),
	})
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Second snapshot – reset time went backward by 1 hour (|diff| > 10min → reset)
	resetBackward := reset1.Add(-1 * time.Hour)
	s2 := makeAntigravitySnapshot(base.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-q", "Model Q", 0.9, &resetBackward),
	})
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	history, err := s.QueryAntigravityCycleHistory("model-q")
	if err != nil {
		t.Fatalf("QueryAntigravityCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle for backward reset time, got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// Anthropic processQuota – !hasLast, existing cycle, util not higher
// ---------------------------------------------------------------------------

// TestAnthropicTracker_processQuota_NotHasLast_PeakNotUpdated exercises the
// `!hasLast → else { if currentUtil > cycle.PeakUtilization }` branch where
// the current utilization is NOT higher than the existing peak.
func TestAnthropicTracker_processQuota_NotHasLast_PeakNotUpdated(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create cycle with high peak directly in DB
	base := time.Now()
	resetsAt := base.Add(5 * time.Hour)
	if _, err := s.CreateAnthropicCycle("five_hour", base, &resetsAt); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("five_hour", 80.0, 50.0); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// New tracker – hasLast = false
	tr := NewAnthropicTracker(s, nil)
	// util (30) < cycle peak (80) → no update
	snap := makeAnthropicSnapshot(base.Add(time.Minute), "five_hour", 30.0, &resetsAt)
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveAnthropicCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	// Peak should NOT have changed (30 < 80)
	if cycle.PeakUtilization != 80.0 {
		t.Errorf("PeakUtilization = %v, want 80.0 (no update)", cycle.PeakUtilization)
	}
}

// ---------------------------------------------------------------------------
// Codex processQuota – time-based reset when capturedAt > cycle.ResetsAt+2min
// ---------------------------------------------------------------------------

// TestCodexTracker_processQuota_TimeBasedReset exercises the time-based reset
// path in codex (cycle.ResetsAt != nil && capturedAt.After(cycle.ResetsAt.Add(2min))).
func TestCodexTracker_processQuota_TimeBasedReset(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	base := time.Now().UTC()
	reset1 := base.Add(1 * time.Hour)

	// First snapshot
	snap1 := &api.CodexSnapshot{
		CapturedAt: base,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 40, ResetsAt: &reset1}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot – trigger time-based reset (capturedAt > reset1 + 2min)
	capturedAt2 := reset1.Add(3 * time.Minute)
	reset2 := capturedAt2.Add(5 * time.Hour)
	snap2 := &api.CodexSnapshot{
		CapturedAt: capturedAt2,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 10, ResetsAt: &reset2}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2 (time-based reset): %v", err)
	}

	history, err := s.QueryCodexCycleHistory(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle (codex time-based reset), got %d", len(history))
	}
}

// TestCodexTracker_processQuota_TimeBasedReset_WithHasLast_PositiveDelta exercises
// the time-based reset path when hasLast=true and delta > 0 and util > peak.
func TestCodexTracker_processQuota_TimeBasedReset_WithHasLast_PositiveDelta(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCodexTracker(s, slog.Default())
	base := time.Now().UTC()
	reset1 := base.Add(1 * time.Hour)

	// First snapshot
	snap1 := &api.CodexSnapshot{
		CapturedAt: base,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 40, ResetsAt: &reset1}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second snapshot: same cycle, usage increases (sets hasLast=true)
	snap2 := &api.CodexSnapshot{
		CapturedAt: base.Add(30 * time.Minute),
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 60, ResetsAt: &reset1}},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	// Third snapshot: time-based reset, util=70 > lastUtil=60, and util=70 > cycle.Peak=60
	capturedAt3 := reset1.Add(3 * time.Minute)
	reset3 := capturedAt3.Add(5 * time.Hour)
	snap3 := &api.CodexSnapshot{
		CapturedAt: capturedAt3,
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 70, ResetsAt: &reset3}},
	}
	if err := tr.Process(snap3); err != nil {
		t.Fatalf("Process snap3 (time-based reset with positive delta): %v", err)
	}

	history, err := s.QueryCodexCycleHistory(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 closed cycle, got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// Anthropic UsageSummary – projected util > 100 (clamped)
// ---------------------------------------------------------------------------

// TestAnthropicTracker_UsageSummary_ProjectedUtilClamped exercises the
// `if projected > 100 { projected = 100 }` branch.
func TestAnthropicTracker_UsageSummary_ProjectedUtilClamped(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create cycle that started 2 hours ago, resetsAt in 1 hour, high delta
	cycleStart := time.Now().Add(-2 * time.Hour)
	resetsAt := time.Now().Add(1 * time.Hour)
	if _, err := s.CreateAnthropicCycle("five_hour", cycleStart, &resetsAt); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	// Set high delta (80) so rate is 40/hr, projected = 90 + 40*1 = 130 > 100
	if err := s.UpdateAnthropicCycle("five_hour", 90, 80); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	snap := &api.AnthropicSnapshot{
		CapturedAt: time.Now(),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 90.0, ResetsAt: &resetsAt},
		},
	}
	if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	tr := NewAnthropicTracker(s, nil)
	summary, err := tr.UsageSummary("five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.ProjectedUtil != 100 {
		t.Errorf("ProjectedUtil = %v, want 100 (clamped)", summary.ProjectedUtil)
	}
}

// ---------------------------------------------------------------------------
// Tracker.UsageSummary – subscription case in switch (latest != nil)
// ---------------------------------------------------------------------------

// TestTracker_UsageSummary_SubscriptionWithSnapshot exercises the "subscription"
// case in the switch statement inside UsageSummary when there's a stored snapshot.
func TestTracker_UsageSummary_SubscriptionWithSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := New(s, nil)
	baseTime := time.Now()

	snap := &api.Snapshot{
		CapturedAt: baseTime,
		Sub:        api.QuotaInfo{Limit: 1000, Requests: 300, RenewsAt: baseTime.Add(6 * time.Hour)},
		Search:     api.QuotaInfo{Limit: 250, Requests: 75, RenewsAt: baseTime.Add(90 * time.Minute)},
		ToolCall:   api.QuotaInfo{Limit: 5000, Requests: 1500, RenewsAt: baseTime.Add(4 * time.Hour)},
	}
	// Insert BEFORE Process so QueryLatest() returns a snapshot
	if _, err := s.InsertSnapshot(snap); err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Call UsageSummary for "subscription" to cover that switch case
	summary, err := tr.UsageSummary("subscription")
	if err != nil {
		t.Fatalf("UsageSummary(subscription): %v", err)
	}
	if summary.CurrentUsage != 300 {
		t.Errorf("CurrentUsage = %v, want 300", summary.CurrentUsage)
	}
	if summary.CurrentLimit != 1000 {
		t.Errorf("CurrentLimit = %v, want 1000", summary.CurrentLimit)
	}
	if summary.UsagePercent != 30 {
		t.Errorf("UsagePercent = %v, want 30", summary.UsagePercent)
	}
}

// ---------------------------------------------------------------------------
// Copilot processQuota – hasLastValues=true, quota in lastValues, no delta, peak higher
// The "else { if currentUsed > cycle.PeakUsed }" branch with high usage
// ---------------------------------------------------------------------------

// TestCopilotTracker_processQuota_HasLast_NewQuota_PeakHigher exercises:
// hasLastValues=true, quota NOT in lastValues, currentUsed > cycle.PeakUsed
func TestCopilotTracker_processQuota_HasLast_NewQuota_PeakHigher(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewCopilotTracker(s, slog.Default())
	now := time.Now().UTC()
	resetDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// First snapshot – sets hasLastValues=true for "premium_interactions"
	snap1 := &api.CopilotSnapshot{
		CapturedAt: now,
		ResetDate:  &resetDate,
		Quotas:     []api.CopilotQuota{{Name: "premium_interactions", Entitlement: 1500, Remaining: 1200}},
	}
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Create "chat" cycle with low existing peak
	if _, err := s.CreateCopilotCycle("chat", now, &resetDate); err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if err := s.UpdateCopilotCycle("chat", 5, 2); err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	// Second snapshot: "chat" appears for first time, HIGH usage (200) > existing peak (5)
	// hasLastValues=true, chat not in lastValues → exercises "else { if currentUsed > cycle.PeakUsed }" YES branch
	snap2 := &api.CopilotSnapshot{
		CapturedAt: now.Add(time.Minute),
		ResetDate:  &resetDate,
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1500, Remaining: 1100},
			{Name: "chat", Entitlement: 500, Remaining: 300}, // used=200 > peak=5
		},
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	chatCycle, err := s.QueryActiveCopilotCycle("chat")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle(chat): %v", err)
	}
	// PeakUsed should have been updated to 200 (200 > 5)
	if chatCycle.PeakUsed != 200 {
		t.Errorf("chat PeakUsed = %d, want 200", chatCycle.PeakUsed)
	}
}

// ---------------------------------------------------------------------------
// Antigravity processModel – hasLastValues=true, model in lastFractions, but peak IS higher
// ---------------------------------------------------------------------------

// TestAntigravityTracker_processModel_HasLast_NewModel_PeakHigher already tested
// via TestAntigravityTracker_processModel_HasLastButNoLastForThisModel. But let's
// also ensure the same scenario for the "else if currentUsage > cycle.PeakUsage" branch
// specifically by creating a model with no lastFractions entry and testing the update.

// TestAntigravityTracker_processModel_HasLast_ExistingModel_NewCycleWithLowPeak exercises
// hasLastValues=true && !ok for lastFractions with high current usage > cycle peak.
func TestAntigravityTracker_processModel_HasLast_ExistingModel_HigherThanPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAntigravityTracker(s, nil)
	base := time.Now()
	reset := base.Add(24 * time.Hour)

	// Set hasLastValues=true by processing one snapshot with model-x
	s1 := makeAntigravitySnapshot(base, []api.AntigravityModelQuota{
		makeModel("model-x", "Model X", 0.8, &reset),
	})
	if err := tr.Process(s1); err != nil {
		t.Fatalf("Process s1: %v", err)
	}

	// Create model-y with LOW existing peak in DB
	if _, err := s.CreateAntigravityCycle("model-y", base, &reset); err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if err := s.UpdateAntigravityCycle("model-y", 0.05, 0.02); err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	// Second snapshot: model-y appears for first time with HIGH usage (0.7) > cycle peak (0.05)
	// hasLastValues=true, model-y not in lastFractions → exercises "else { if currentUsage > cycle.PeakUsage }" YES branch
	s2 := makeAntigravitySnapshot(base.Add(time.Minute), []api.AntigravityModelQuota{
		makeModel("model-x", "Model X", 0.7, &reset),
		makeModel("model-y", "Model Y", 0.3, &reset), // usage=0.7 > peak=0.05
	})
	if err := tr.Process(s2); err != nil {
		t.Fatalf("Process s2: %v", err)
	}

	cycle, err := s.QueryActiveAntigravityCycle("model-y")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle(model-y): %v", err)
	}
	if cycle.PeakUsage < 0.69 || cycle.PeakUsage > 0.71 {
		t.Errorf("model-y PeakUsage = %v, want ~0.7", cycle.PeakUsage)
	}
}

// ---------------------------------------------------------------------------
// Anthropic processQuota – hasLast=true, no lastValues for quota, util NOT higher
// AND hasLast=false, util NOT higher
// ---------------------------------------------------------------------------

// TestAnthropicTracker_processQuota_HasLast_NewQuota_NotHigherThanPeak exercises:
// hasLast=true, quota NOT in lastValues map, currentUtil <= cycle.PeakUtilization
func TestAnthropicTracker_processQuota_HasLast_NewQuota_NotHigherThanPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	base := time.Now()
	resetsAt := base.Add(5 * time.Hour)

	// First snapshot – sets hasLast=true for "five_hour"
	snap1 := makeAnthropicSnapshot(base, "five_hour", 20.0, &resetsAt)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Create "seven_day" cycle with HIGH existing peak
	sevenDayReset := base.Add(7 * 24 * time.Hour)
	if _, err := s.CreateAnthropicCycle("seven_day", base, &sevenDayReset); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("seven_day", 90.0, 40.0); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// Second snapshot: "seven_day" appears for first time with LOW util (10) < peak (90)
	// hasLast=true, seven_day not in lastValues → exercises "else { if currentUtil > cycle.PeakUtilization }" NO branch
	snap2 := makeMultiQuotaSnapshot(base.Add(time.Minute), []api.AnthropicQuota{
		{Name: "five_hour", Utilization: 25.0, ResetsAt: &resetsAt},
		{Name: "seven_day", Utilization: 10.0, ResetsAt: &sevenDayReset},
	})
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveAnthropicCycle("seven_day")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	// Peak should NOT have changed (10 < 90)
	if cycle.PeakUtilization != 90.0 {
		t.Errorf("PeakUtilization = %v, want 90.0 (no update)", cycle.PeakUtilization)
	}
}

// ---------------------------------------------------------------------------
// Codex UsageSummary – active cycle, ResetsAt nil in cycle but found in snapshot
// ---------------------------------------------------------------------------

// TestCodexTracker_UsageSummary_ActiveCycleNilResetsAt_SnapshotHasIt exercises
// the `if summary.ResetsAt == nil && q.ResetsAt != nil` branch in UsageSummary.
func TestCodexTracker_UsageSummary_ActiveCycleNilResetsAt_SnapshotHasIt(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cycleStart := time.Now().UTC().Add(-1 * time.Hour)
	// Create cycle with nil ResetsAt
	if _, err := s.CreateCodexCycle(store.DefaultCodexAccountID, "five_hour", cycleStart, nil); err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	if err := s.UpdateCodexCycle(store.DefaultCodexAccountID, "five_hour", 30, 10); err != nil {
		t.Fatalf("UpdateCodexCycle: %v", err)
	}

	// Snapshot has ResetsAt set for the quota
	snapReset := time.Now().UTC().Add(4 * time.Hour)
	snap := &api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 25, ResetsAt: &snapReset, Status: "healthy"},
		},
	}
	if _, err := s.InsertCodexSnapshot(snap); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	tr := NewCodexTracker(s, slog.Default())
	summary, err := tr.UsageSummary(store.DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	// ResetsAt should fall back from snapshot since cycle has nil
	if summary.ResetsAt == nil {
		t.Error("expected ResetsAt from snapshot fallback")
	}
	if !summary.ResetsAt.Equal(snapReset) {
		t.Errorf("ResetsAt = %v, want %v", summary.ResetsAt, snapReset)
	}
}

// ---------------------------------------------------------------------------
// Anthropic processQuota – hasLast=true, quota NOT in lastValues, cycle EXISTS,
// currentUtil IS higher (lines 204-206)
// ---------------------------------------------------------------------------

// TestAnthropicTracker_processQuota_HasLast_ExistingCycle_NewQuota_HigherThanPeak exercises:
// hasLast=true, quota not in lastValues, active cycle EXISTS in DB, currentUtil > cycle.PeakUtilization
func TestAnthropicTracker_processQuota_HasLast_ExistingCycle_NewQuota_HigherThanPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := NewAnthropicTracker(s, nil)
	base := time.Now()
	resetsAt := base.Add(5 * time.Hour)

	// Pre-create a "seven_day" cycle in the DB (simulating existing cycle from previous run)
	sevenDayReset := base.Add(7 * 24 * time.Hour)
	if _, err := s.CreateAnthropicCycle("seven_day", base, &sevenDayReset); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	// Set low peak so current util will be higher
	if err := s.UpdateAnthropicCycle("seven_day", 10.0, 5.0); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// First: Process five_hour snapshot → sets hasLast=true, lastValues["five_hour"]=10.0
	snap1 := makeAnthropicSnapshot(base, "five_hour", 10.0, &resetsAt)
	if err := tr.Process(snap1); err != nil {
		t.Fatalf("Process snap1: %v", err)
	}

	// Second: Process a snapshot with BOTH quotas.
	// hasLast=true, seven_day EXISTS in DB, but NOT in lastValues → hits "else { if > }" branch
	// currentUtil=40 > cycle.PeakUtilization=10 → updates peak (exercises lines 204-206)
	snap2 := makeMultiQuotaSnapshot(base.Add(time.Minute), []api.AnthropicQuota{
		{Name: "five_hour", Utilization: 15.0, ResetsAt: &resetsAt},
		{Name: "seven_day", Utilization: 40.0, ResetsAt: &sevenDayReset},
	})
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("Process snap2: %v", err)
	}

	cycle, err := s.QueryActiveAnthropicCycle("seven_day")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	if cycle.PeakUtilization != 40.0 {
		t.Errorf("PeakUtilization = %v, want 40.0 (updated from new quota with higher util)", cycle.PeakUtilization)
	}
}

// TestAnthropicTracker_processQuota_NotHasLast_ExistingCycle_HigherThanPeak exercises:
// hasLast=false (fresh tracker), active cycle EXISTS in DB, currentUtil > cycle.PeakUtilization
// (lines 213-215)
func TestAnthropicTracker_processQuota_NotHasLast_ExistingCycle_HigherThanPeak(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create cycle with LOW existing peak
	base := time.Now()
	resetsAt := base.Add(5 * time.Hour)
	if _, err := s.CreateAnthropicCycle("five_hour", base, &resetsAt); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("five_hour", 15.0, 10.0); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// Fresh tracker – hasLast = false
	tr := NewAnthropicTracker(s, nil)
	// util (60) > cycle peak (15) → SHOULD update peak
	snap := makeAnthropicSnapshot(base.Add(time.Minute), "five_hour", 60.0, &resetsAt)
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	cycle, err := s.QueryActiveAnthropicCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveAnthropicCycle: %v", err)
	}
	if cycle.PeakUtilization != 60.0 {
		t.Errorf("PeakUtilization = %v, want 60.0 (updated, util > peak)", cycle.PeakUtilization)
	}
}

// ---------------------------------------------------------------------------
// Anthropic UsageSummary – ResetsAt=nil in cycle, but snapshot quota has ResetsAt
// (lines 282-285 in UsageSummary)
// ---------------------------------------------------------------------------

// TestAnthropicTracker_UsageSummary_SnapshotResetsAtFallback exercises the
// `if summary.ResetsAt == nil && q.ResetsAt != nil` branch in UsageSummary.
func TestAnthropicTracker_UsageSummary_SnapshotResetsAtFallback(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Create cycle with nil ResetsAt
	base := time.Now()
	if _, err := s.CreateAnthropicCycle("five_hour", base, nil); err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	if err := s.UpdateAnthropicCycle("five_hour", 25.0, 10.0); err != nil {
		t.Fatalf("UpdateAnthropicCycle: %v", err)
	}

	// Insert a snapshot where the quota HAS a ResetsAt
	snapResetsAt := base.Add(5 * time.Hour)
	snap := &api.AnthropicSnapshot{
		CapturedAt: base.Add(time.Minute),
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 22.0, ResetsAt: &snapResetsAt},
		},
	}
	if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	tr := NewAnthropicTracker(s, nil)
	summary, err := tr.UsageSummary("five_hour")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	// summary.ResetsAt should have been filled from the snapshot quota's ResetsAt
	// (since cycle had nil ResetsAt, the `if summary.ResetsAt == nil && q.ResetsAt != nil` branch fires)
	if summary.ResetsAt == nil {
		t.Error("expected ResetsAt to be set from snapshot quota (fallback from nil cycle.ResetsAt)")
	}
	if !summary.ResetsAt.Equal(snapResetsAt) {
		t.Errorf("ResetsAt = %v, want %v", summary.ResetsAt, snapResetsAt)
	}
	if summary.CurrentUtil != 22.0 {
		t.Errorf("CurrentUtil = %v, want 22.0", summary.CurrentUtil)
	}
}
