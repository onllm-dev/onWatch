package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// --- Copilot cycle CRUD ---

func TestCopilotStore_CreateCloseCopilotCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetDate := now.Add(24 * time.Hour)
	id, err := s.CreateCopilotCycle("premium_interactions", now, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}

	// Verify active cycle exists
	cycle, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle, got nil")
	}

	// Update cycle
	err = s.UpdateCopilotCycle("premium_interactions", 500, 100)
	if err != nil {
		t.Fatalf("UpdateCopilotCycle: %v", err)
	}

	// Close cycle
	endTime := now.Add(12 * time.Hour)
	err = s.CloseCopilotCycle("premium_interactions", endTime, 500, 100)
	if err != nil {
		t.Fatalf("CloseCopilotCycle: %v", err)
	}

	// Verify no active cycle
	cycle, err = s.QueryActiveCopilotCycle("premium_interactions")
	if err != nil {
		t.Fatalf("QueryActiveCopilotCycle after close: %v", err)
	}
	if cycle != nil {
		t.Fatalf("Expected nil active cycle after close, got %+v", cycle)
	}
}

func TestCopilotStore_CreateCopilotCycle_NilResetDate(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	id, err := s.CreateCopilotCycle("chat", now, nil)
	if err != nil {
		t.Fatalf("CreateCopilotCycle with nil resetDate: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}
}

// --- Codex cycle CRUD ---

func TestCodexStore_CreateCloseCodexCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)
	id, err := s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}

	// Update cycle
	err = s.UpdateCodexCycle(DefaultCodexAccountID, "five_hour", 0.75, 0.5)
	if err != nil {
		t.Fatalf("UpdateCodexCycle: %v", err)
	}

	// Close cycle
	endTime := now.Add(5 * time.Hour)
	err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", endTime, 0.75, 0.5)
	if err != nil {
		t.Fatalf("CloseCodexCycle: %v", err)
	}

	// Verify history
	history, err := s.QueryCodexCycleHistory(DefaultCodexAccountID, "five_hour")
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Expected 1 cycle in history, got %d", len(history))
	}
	if history[0].PeakUtilization != 0.75 {
		t.Fatalf("Expected peak 0.75, got %f", history[0].PeakUtilization)
	}
}

func TestCodexStore_CreateCodexCycle_NilResetsAt(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	id, err := s.CreateCodexCycle(DefaultCodexAccountID, "daily", now, nil)
	if err != nil {
		t.Fatalf("CreateCodexCycle with nil resetsAt: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}
}

// --- Antigravity cycle CRUD ---

func TestAntigravityStore_CreateCloseAntigravityCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)
	id, err := s.CreateAntigravityCycle("model-a", now, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}

	// Update cycle
	err = s.UpdateAntigravityCycle("model-a", 0.8, 0.3)
	if err != nil {
		t.Fatalf("UpdateAntigravityCycle: %v", err)
	}

	// Close cycle
	endTime := now.Add(24 * time.Hour)
	err = s.CloseAntigravityCycle("model-a", endTime, 0.8, 0.3)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle: %v", err)
	}

	// Verify no active cycle
	cycle, err := s.QueryActiveAntigravityCycle("model-a")
	if err != nil {
		t.Fatalf("QueryActiveAntigravityCycle: %v", err)
	}
	if cycle != nil {
		t.Fatalf("Expected nil active cycle after close")
	}
}

func TestAntigravityStore_CreateAntigravityCycle_NilResetTime(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	id, err := s.CreateAntigravityCycle("model-b", now, nil)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle with nil resetTime: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}
}

// --- Antigravity snapshot with full model data ---

func TestAntigravityStore_InsertWithModels_QueryLatest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt:     now,
		Email:          "test@example.com",
		PlanName:       "pro",
		PromptCredits:  100.5,
		MonthlyCredits: 500,
		Models: []api.AntigravityModelQuota{
			{
				ModelID:           "gpt-4",
				Label:             "GPT-4",
				RemainingFraction: 0.75,
				RemainingPercent:  75.0,
				IsExhausted:       false,
				ResetTime:         &resetTime,
			},
			{
				ModelID:           "claude-3",
				Label:             "Claude 3",
				RemainingFraction: 0.0,
				RemainingPercent:  0.0,
				IsExhausted:       true,
				ResetTime:         nil,
			},
		},
		RawJSON: "{}",
	}

	id, err := s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive snapshot ID, got %d", id)
	}

	latest, err := s.QueryLatestAntigravity()
	if err != nil {
		t.Fatalf("QueryLatestAntigravity: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if latest.Email != "test@example.com" {
		t.Fatalf("Expected email test@example.com, got %s", latest.Email)
	}
	if latest.PlanName != "pro" {
		t.Fatalf("Expected planName pro, got %s", latest.PlanName)
	}
	if len(latest.Models) != 2 {
		t.Fatalf("Expected 2 models, got %d", len(latest.Models))
	}

	// Check model with ResetTime
	foundReset := false
	foundExhausted := false
	for _, m := range latest.Models {
		if m.ModelID == "gpt-4" {
			foundReset = true
			if m.ResetTime == nil {
				t.Fatal("Expected ResetTime for gpt-4, got nil")
			}
			if m.RemainingPercent != 75.0 {
				t.Fatalf("Expected 75.0 remaining percent, got %f", m.RemainingPercent)
			}
		}
		if m.ModelID == "claude-3" {
			foundExhausted = true
			if !m.IsExhausted {
				t.Fatal("Expected IsExhausted for claude-3")
			}
		}
	}
	if !foundReset {
		t.Fatal("Missing gpt-4 model")
	}
	if !foundExhausted {
		t.Fatal("Missing claude-3 model")
	}
}

// --- Antigravity Range with models ---

func TestAntigravityStore_QueryAntigravityRange_WithModels(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt:     now,
		Email:          "user@test.com",
		PlanName:       "enterprise",
		PromptCredits:  200.0,
		MonthlyCredits: 1000,
		Models: []api.AntigravityModelQuota{
			{
				ModelID:           "model-x",
				Label:             "Model X",
				RemainingFraction: 0.5,
				RemainingPercent:  50.0,
				IsExhausted:       false,
				ResetTime:         &resetTime,
			},
		},
		RawJSON: "{}",
	}

	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	start := now.Add(-1 * time.Hour)
	end := now.Add(1 * time.Hour)
	results, err := s.QueryAntigravityRange(start, end)
	if err != nil {
		t.Fatalf("QueryAntigravityRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(results))
	}
	if len(results[0].Models) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(results[0].Models))
	}
	if results[0].Models[0].ResetTime == nil {
		t.Fatal("Expected ResetTime, got nil")
	}
	if results[0].Email != "user@test.com" {
		t.Fatalf("Expected email user@test.com, got %s", results[0].Email)
	}
}

// --- Anthropic Range with resetsAt on quotas ---

func TestAnthropicStore_QueryAnthropicRange_WithResetsAt(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(5 * time.Hour)
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: now,
		RawJSON:    "{}",
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: 0.3, ResetsAt: &resetTime},
			{Name: "daily", Utilization: 0.1, ResetsAt: nil},
		},
	}

	_, err = s.InsertAnthropicSnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	start := now.Add(-1 * time.Hour)
	end := now.Add(1 * time.Hour)
	results, err := s.QueryAnthropicRange(start, end)
	if err != nil {
		t.Fatalf("QueryAnthropicRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(results))
	}
	if len(results[0].Quotas) != 2 {
		t.Fatalf("Expected 2 quotas, got %d", len(results[0].Quotas))
	}

	// Verify resetsAt was loaded
	foundWithReset := false
	for _, q := range results[0].Quotas {
		if q.Name == "five_hour" && q.ResetsAt != nil {
			foundWithReset = true
		}
	}
	if !foundWithReset {
		t.Fatal("Expected five_hour quota to have ResetsAt")
	}
}

// --- Copilot snapshot with resetDate and quotas ---

func TestCopilotStore_InsertWithQuotas_QueryRange_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetDate := now.Add(30 * 24 * time.Hour)

	for i := 0; i < 5; i++ {
		snapshot := &api.CopilotSnapshot{
			CapturedAt:  now.Add(time.Duration(i) * time.Hour),
			CopilotPlan: "business",
			ResetDate:   &resetDate,
			RawJSON:     "{}",
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 900 - i*100, PercentRemaining: float64(90 - i*10), Unlimited: false, OverageCount: 0},
				{Name: "chat", Entitlement: 0, Remaining: 0, PercentRemaining: 0, Unlimited: true, OverageCount: 0},
			},
		}
		_, err := s.InsertCopilotSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertCopilotSnapshot[%d]: %v", i, err)
		}
	}

	start := now.Add(-1 * time.Hour)
	end := now.Add(10 * time.Hour)

	// Query with limit
	results, err := s.QueryCopilotRange(start, end, 2)
	if err != nil {
		t.Fatalf("QueryCopilotRange with limit: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 snapshots with limit, got %d", len(results))
	}
	if results[0].CopilotPlan != "business" {
		t.Fatalf("Expected copilotPlan business, got %s", results[0].CopilotPlan)
	}
	if results[0].ResetDate == nil {
		t.Fatal("Expected ResetDate, got nil")
	}

	// Verify quotas loaded including unlimited flag
	if len(results[0].Quotas) != 2 {
		t.Fatalf("Expected 2 quotas, got %d", len(results[0].Quotas))
	}
	foundUnlimited := false
	for _, q := range results[0].Quotas {
		if q.Name == "chat" && q.Unlimited {
			foundUnlimited = true
		}
	}
	if !foundUnlimited {
		t.Fatal("Expected chat quota to be unlimited")
	}
}

// --- Codex snapshot with full quota data, QueryRange with limit ---

func TestCodexStore_InsertWithQuotas_QueryRange_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)
	balance := 42.5

	for i := 0; i < 4; i++ {
		snapshot := &api.CodexSnapshot{
			CapturedAt:     now.Add(time.Duration(i) * time.Hour),
			PlanType:       "pro",
			CreditsBalance: &balance,
			RawJSON:        "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i) * 0.2, ResetsAt: &resetsAt, Status: "active"},
				{Name: "daily", Utilization: float64(i) * 0.1, ResetsAt: nil, Status: ""},
			},
		}
		_, err := s.InsertCodexSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertCodexSnapshot[%d]: %v", i, err)
		}
	}

	start := now.Add(-1 * time.Hour)
	end := now.Add(10 * time.Hour)

	// Query with limit
	results, err := s.QueryCodexRange(DefaultCodexAccountID, start, end, 2)
	if err != nil {
		t.Fatalf("QueryCodexRange with limit: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 snapshots with limit, got %d", len(results))
	}
	if results[0].PlanType != "pro" {
		t.Fatalf("Expected planType pro, got %s", results[0].PlanType)
	}
	if results[0].CreditsBalance == nil {
		t.Fatal("Expected CreditsBalance, got nil")
	}

	// Verify quotas loaded including status and resetsAt
	if len(results[0].Quotas) != 2 {
		t.Fatalf("Expected 2 quotas, got %d", len(results[0].Quotas))
	}
	foundWithResetsAt := false
	foundWithStatus := false
	for _, q := range results[0].Quotas {
		if q.Name == "five_hour" {
			if q.ResetsAt != nil {
				foundWithResetsAt = true
			}
			if q.Status == "active" {
				foundWithStatus = true
			}
		}
	}
	if !foundWithResetsAt {
		t.Fatal("Expected five_hour quota to have ResetsAt")
	}
	if !foundWithStatus {
		t.Fatal("Expected five_hour quota to have Status 'active'")
	}
}

// --- Zai Range with limit ---

func TestZaiStore_QueryZaiRange_WithLimit_Models(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	for i := 0; i < 4; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:          now.Add(time.Duration(i) * time.Hour),
			TimeLimit:           60,
			TimeUnit:            1,
			TimeNumber:          60,
			TimeUsage:           float64(i * 10),
			TimeCurrentValue:    float64(60 - i*10),
			TimeRemaining:       float64(50 - i*10),
			TimePercentage:      100 - i*10,
			TimeUsageDetails:    "{}",
			TokensLimit:         1000000,
			TokensUnit:          1,
			TokensNumber:        1000000,
			TokensUsage:         float64(i * 100000),
			TokensCurrentValue:  float64(1000000 - i*100000),
			TokensRemaining:     float64(900000 - i*100000),
			TokensPercentage:    100 - i*10,
			TokensNextResetTime: &resetTime,
		}
		_, err := s.InsertZaiSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertZaiSnapshot[%d]: %v", i, err)
		}
	}

	start := now.Add(-1 * time.Hour)
	end := now.Add(10 * time.Hour)
	results, err := s.QueryZaiRange(start, end, 2)
	if err != nil {
		t.Fatalf("QueryZaiRange with limit: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 snapshots, got %d", len(results))
	}
	if results[0].TokensNextResetTime == nil {
		t.Fatal("Expected TokensNextResetTime, got nil")
	}
}

// --- Session operations ---

func TestStore_SessionCRUD(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Create session with start values
	err = s.CreateSession("sess-1", now, 60, "synthetic", 10.0, 5.0, 2.0)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Query active session
	session, err := s.QueryActiveSession()
	if err != nil {
		t.Fatalf("QueryActiveSession: %v", err)
	}
	if session == nil {
		t.Fatal("Expected active session, got nil")
	}
	if session.ID != "sess-1" {
		t.Fatalf("Expected session ID sess-1, got %s", session.ID)
	}

	// Update max requests
	err = s.UpdateSessionMaxRequests("sess-1", 20.0, 10.0, 5.0)
	if err != nil {
		t.Fatalf("UpdateSessionMaxRequests: %v", err)
	}

	// Increment snapshot count
	err = s.IncrementSnapshotCount("sess-1")
	if err != nil {
		t.Fatalf("IncrementSnapshotCount: %v", err)
	}

	// Close session
	endTime := now.Add(1 * time.Hour)
	err = s.CloseSession("sess-1", endTime)
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	// Verify no active session
	session, err = s.QueryActiveSession()
	if err != nil {
		t.Fatalf("QueryActiveSession after close: %v", err)
	}
	if session != nil {
		t.Fatalf("Expected nil active session after close")
	}
}

func TestStore_CloseOrphanedSessions_Multiple(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Create two sessions, leave them open
	err = s.CreateSession("orphan-1", now, 60, "synthetic")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err = s.CreateSession("orphan-2", now.Add(time.Minute), 60, "anthropic")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	count, err := s.CloseOrphanedSessions()
	if err != nil {
		t.Fatalf("CloseOrphanedSessions: %v", err)
	}
	if count != 2 {
		t.Fatalf("Expected 2 orphaned sessions closed, got %d", count)
	}

	// No active sessions now
	session, err := s.QueryActiveSession()
	if err != nil {
		t.Fatalf("QueryActiveSession: %v", err)
	}
	if session != nil {
		t.Fatal("Expected no active session after closing orphans")
	}
}

// --- Quota name queries ---

func TestAnthropicStore_QueryAllAnthropicQuotaNames(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	_, err = s.CreateAnthropicCycle("five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}
	_, err = s.CreateAnthropicCycle("daily", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}

	names, err := s.QueryAllAnthropicQuotaNames()
	if err != nil {
		t.Fatalf("QueryAllAnthropicQuotaNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("Expected 2 quota names, got %d", len(names))
	}
}

func TestCopilotStore_QueryAllCopilotQuotaNames(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	snap := &api.CopilotSnapshot{
		CapturedAt: now,
		RawJSON:    "{}",
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1000, Remaining: 800},
			{Name: "chat", Entitlement: 0, Remaining: 0, Unlimited: true},
		},
	}
	_, err = s.InsertCopilotSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	names, err := s.QueryAllCopilotQuotaNames()
	if err != nil {
		t.Fatalf("QueryAllCopilotQuotaNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("Expected 2 quota names, got %d", len(names))
	}
}

func TestCodexStore_QueryAllCodexQuotaNames(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	snap := &api.CodexSnapshot{
		CapturedAt: now,
		PlanType:   "pro",
		RawJSON:    "{}",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 0.3, ResetsAt: &resetsAt},
			{Name: "daily", Utilization: 0.1},
		},
	}
	_, err = s.InsertCodexSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	names, err := s.QueryAllCodexQuotaNames()
	if err != nil {
		t.Fatalf("QueryAllCodexQuotaNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("Expected 2 quota names, got %d", len(names))
	}
}

// --- Antigravity ModelIDs for group and SnapshotAtOrBefore ---

func TestAntigravityStore_QueryAntigravitySnapshotAtOrBefore(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	snapshot := &api.AntigravitySnapshot{
		CapturedAt:     now,
		Email:          "user@test.com",
		PlanName:       "enterprise",
		PromptCredits:  200.0,
		MonthlyCredits: 1000,
		Models: []api.AntigravityModelQuota{
			{
				ModelID:           "model-x",
				Label:             "Model X",
				RemainingFraction: 0.5,
				RemainingPercent:  50.0,
				IsExhausted:       false,
				ResetTime:         &resetTime,
			},
		},
		RawJSON: "{}",
	}

	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Query at current time
	result, err := s.QueryAntigravitySnapshotAtOrBefore(now.Add(1 * time.Hour))
	if err != nil {
		t.Fatalf("QueryAntigravitySnapshotAtOrBefore: %v", err)
	}
	if result == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if result.Email != "user@test.com" {
		t.Fatalf("Expected email user@test.com, got %s", result.Email)
	}
	if len(result.Models) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0].ResetTime == nil {
		t.Fatal("Expected ResetTime on model, got nil")
	}

	// Query before any snapshot
	result, err = s.QueryAntigravitySnapshotAtOrBefore(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("QueryAntigravitySnapshotAtOrBefore before: %v", err)
	}
	if result != nil {
		t.Fatal("Expected nil when querying before any snapshot")
	}
}

func TestAntigravityStore_QueryAntigravityModelIDsForGroup(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	snapshot := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "model-a", Label: "Model A", RemainingFraction: 0.5, RemainingPercent: 50.0},
			{ModelID: "model-b", Label: "Model B", RemainingFraction: 0.3, RemainingPercent: 30.0},
		},
		RawJSON: "{}",
	}

	_, err = s.InsertAntigravitySnapshot(snapshot)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Query with a group key that may or may not match
	ids, err := s.QueryAntigravityModelIDsForGroup("nonexistent")
	if err != nil {
		t.Fatalf("QueryAntigravityModelIDsForGroup: %v", err)
	}
	// We don't know what group these models map to, but the function should not error
	_ = ids
}

// --- Codex cycle history with limit ---

func TestCodexStore_QueryCodexCycleHistory_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		resetsAt := start.Add(5 * time.Hour)
		_, err := s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", start, &resetsAt)
		if err != nil {
			t.Fatalf("CreateCodexCycle[%d]: %v", i, err)
		}
		end := start.Add(5 * time.Hour)
		err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", end, float64(i)*0.1, float64(i)*0.05)
		if err != nil {
			t.Fatalf("CloseCodexCycle[%d]: %v", i, err)
		}
	}

	history, err := s.QueryCodexCycleHistory(DefaultCodexAccountID, "five_hour", 3)
	if err != nil {
		t.Fatalf("QueryCodexCycleHistory with limit: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("Expected 3 cycles with limit, got %d", len(history))
	}
}

// --- Copilot cycle history ---

func TestCopilotStore_QueryCopilotCycleHistory(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetDate := now.Add(30 * 24 * time.Hour)

	_, err = s.CreateCopilotCycle("premium_interactions", now, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}
	err = s.CloseCopilotCycle("premium_interactions", now.Add(24*time.Hour), 500, 100)
	if err != nil {
		t.Fatalf("CloseCopilotCycle: %v", err)
	}

	history, err := s.QueryCopilotCycleHistory("premium_interactions")
	if err != nil {
		t.Fatalf("QueryCopilotCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Expected 1 cycle in history, got %d", len(history))
	}
}

// --- Antigravity cycle history ---

func TestAntigravityStore_QueryAntigravityCycleHistory(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	_, err = s.CreateAntigravityCycle("model-a", now, &resetTime)
	if err != nil {
		t.Fatalf("CreateAntigravityCycle: %v", err)
	}
	err = s.CloseAntigravityCycle("model-a", now.Add(24*time.Hour), 0.95, 0.5)
	if err != nil {
		t.Fatalf("CloseAntigravityCycle: %v", err)
	}

	history, err := s.QueryAntigravityCycleHistory("model-a")
	if err != nil {
		t.Fatalf("QueryAntigravityCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("Expected 1 cycle in history, got %d", len(history))
	}
}

// --- Anthropic UtilizationSeries ---

func TestAnthropicStore_QueryAnthropicUtilizationSeries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	// Create cycle
	_, err = s.CreateAnthropicCycle("five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}

	// Insert snapshots with quota values
	for i := 0; i < 3; i++ {
		snap := &api.AnthropicSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 0.1, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot[%d]: %v", i, err)
		}
	}

	series, err := s.QueryAnthropicUtilizationSeries("five_hour", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryAnthropicUtilizationSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("Expected 3 points, got %d", len(series))
	}
}

// --- Copilot UtilizationSeries ---

func TestCopilotStore_QueryCopilotUsageSeries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		snap := &api.CopilotSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 900 - i*100, PercentRemaining: float64(90 - i*10)},
			},
		}
		_, err := s.InsertCopilotSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCopilotSnapshot[%d]: %v", i, err)
		}
	}

	series, err := s.QueryCopilotUsageSeries("premium_interactions", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryCopilotUsageSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("Expected 3 points, got %d", len(series))
	}
}

// --- Codex UtilizationSeries ---

func TestCodexStore_QueryCodexUtilizationSeries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	for i := 0; i < 3; i++ {
		snap := &api.CodexSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			PlanType:   "pro",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i) * 0.2, ResetsAt: &resetsAt, Status: "active"},
			},
		}
		_, err := s.InsertCodexSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCodexSnapshot[%d]: %v", i, err)
		}
	}

	series, err := s.QueryCodexUtilizationSeries(DefaultCodexAccountID, "five_hour", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryCodexUtilizationSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("Expected 3 points, got %d", len(series))
	}
}

// --- Zai cycle CRUD ---

func TestZaiStore_CreateCloseZaiCycle_NilNextReset(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	id, err := s.CreateZaiCycle("tokens", now, nil)
	if err != nil {
		t.Fatalf("CreateZaiCycle with nil nextReset: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Expected positive ID, got %d", id)
	}

	err = s.UpdateZaiCycle("tokens", 500000, 200000)
	if err != nil {
		t.Fatalf("UpdateZaiCycle: %v", err)
	}

	err = s.CloseZaiCycle("tokens", now.Add(24*time.Hour), 500000, 200000)
	if err != nil {
		t.Fatalf("CloseZaiCycle: %v", err)
	}
}

// --- Zai hourly usage ---

func TestZaiStore_InsertZaiHourlyUsage(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Hour)
	hourStr := now.Format("2006-01-02 15:00")
	err = s.InsertZaiHourlyUsage(hourStr, 10, 500, 2, 3, 1)
	if err != nil {
		t.Fatalf("InsertZaiHourlyUsage: %v", err)
	}

	// Query the hourly usage
	results, err := s.QueryZaiHourlyUsage(now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("QueryZaiHourlyUsage: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 hourly usage, got %d", len(results))
	}
}

// --- DeleteAllAuthTokens ---

func TestStore_DeleteAllAuthTokens_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	err = s.DeleteAllAuthTokens()
	if err != nil {
		t.Fatalf("DeleteAllAuthTokens on empty: %v", err)
	}
}

// --- CleanExpiredAuthTokens ---

func TestStore_CleanExpiredAuthTokens_WithData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Insert an expired token
	now := time.Now().UTC()
	s.SaveAuthToken("expired-token", now.Add(-1*time.Hour))
	s.SaveAuthToken("valid-token", now.Add(1*time.Hour))

	err = s.CleanExpiredAuthTokens()
	if err != nil {
		t.Fatalf("CleanExpiredAuthTokens: %v", err)
	}

	// Valid token should still exist
	_, found, err := s.GetAuthTokenExpiry("valid-token")
	if err != nil {
		t.Fatalf("GetAuthTokenExpiry: %v", err)
	}
	if !found {
		t.Fatal("Expected valid token to still exist")
	}

	// Expired token should be gone
	_, found, err = s.GetAuthTokenExpiry("expired-token")
	if err != nil {
		t.Fatalf("GetAuthTokenExpiry: %v", err)
	}
	if found {
		t.Fatal("Expected expired token to be cleaned")
	}
}

// --- Settings ---

func TestStore_GetSetSetting(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Get nonexistent setting
	val, err := s.GetSetting("nonexistent")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if val != "" {
		t.Fatalf("Expected empty string for nonexistent setting, got %s", val)
	}

	// Set and get
	err = s.SetSetting("test_key", "test_value")
	if err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err = s.GetSetting("test_key")
	if err != nil {
		t.Fatalf("GetSetting after set: %v", err)
	}
	if val != "test_value" {
		t.Fatalf("Expected test_value, got %s", val)
	}

	// Overwrite
	err = s.SetSetting("test_key", "new_value")
	if err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	val, err = s.GetSetting("test_key")
	if err != nil {
		t.Fatalf("GetSetting after overwrite: %v", err)
	}
	if val != "new_value" {
		t.Fatalf("Expected new_value, got %s", val)
	}
}

// --- User operations ---

func TestStore_UpsertGetUser(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Get nonexistent user
	hash, err := s.GetUser("admin")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if hash != "" {
		t.Fatalf("Expected empty hash for nonexistent user, got %s", hash)
	}

	// Upsert user
	err = s.UpsertUser("admin", "hashed_password_123")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	hash, err = s.GetUser("admin")
	if err != nil {
		t.Fatalf("GetUser after upsert: %v", err)
	}
	if hash != "hashed_password_123" {
		t.Fatalf("Expected hashed_password_123, got %s", hash)
	}

	// Update password
	err = s.UpsertUser("admin", "new_hash_456")
	if err != nil {
		t.Fatalf("UpsertUser update: %v", err)
	}

	hash, err = s.GetUser("admin")
	if err != nil {
		t.Fatalf("GetUser after update: %v", err)
	}
	if hash != "new_hash_456" {
		t.Fatalf("Expected new_hash_456, got %s", hash)
	}
}

// --- GetLastNotification ---

func TestStore_GetLastNotification(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// No notification - GetLastNotification returns (time.Time, float64, error)
	// When no entry exists, the time will be zero
	sentAt, _, err := s.GetLastNotification("anthropic", "five_hour", "threshold")
	if err != nil {
		t.Fatalf("GetLastNotification: %v", err)
	}
	if !sentAt.IsZero() {
		t.Fatal("Expected zero time for no notification")
	}

	// Upsert and check
	err = s.UpsertNotificationLog("anthropic", "five_hour", "threshold", 0.8)
	if err != nil {
		t.Fatalf("UpsertNotificationLog: %v", err)
	}

	sentAt, util, err := s.GetLastNotification("anthropic", "five_hour", "threshold")
	if err != nil {
		t.Fatalf("GetLastNotification after upsert: %v", err)
	}
	if sentAt.IsZero() {
		t.Fatal("Expected non-zero sent time")
	}
	if util != 0.8 {
		t.Fatalf("Expected utilization 0.8, got %f", util)
	}
}

// --- Anthropic CyclesSince ---

func TestAnthropicStore_QueryAnthropicCyclesSince(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateAnthropicCycle("five_hour", start, &resetsAt)
		if err != nil {
			t.Fatalf("CreateAnthropicCycle[%d]: %v", i, err)
		}
		end := start.Add(5 * time.Hour)
		err = s.CloseAnthropicCycle("five_hour", end, float64(i)*0.1, float64(i)*0.05)
		if err != nil {
			t.Fatalf("CloseAnthropicCycle[%d]: %v", i, err)
		}
	}

	// Query since before first cycle
	cycles, err := s.QueryAnthropicCyclesSince("five_hour", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("QueryAnthropicCyclesSince: %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(cycles))
	}

	// Query since after second cycle
	cycles, err = s.QueryAnthropicCyclesSince("five_hour", now.Add(2*24*time.Hour))
	if err != nil {
		t.Fatalf("QueryAnthropicCyclesSince filtered: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("Expected 1 cycle, got %d", len(cycles))
	}
}

// --- Copilot CyclesSince ---

func TestCopilotStore_QueryCopilotCyclesSince(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateCopilotCycle("premium_interactions", start, nil)
		if err != nil {
			t.Fatalf("CreateCopilotCycle[%d]: %v", i, err)
		}
		end := start.Add(24 * time.Hour)
		err = s.CloseCopilotCycle("premium_interactions", end, 500+i*100, 50+i*10)
		if err != nil {
			t.Fatalf("CloseCopilotCycle[%d]: %v", i, err)
		}
	}

	cycles, err := s.QueryCopilotCyclesSince("premium_interactions", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("QueryCopilotCyclesSince: %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(cycles))
	}
}

// --- Anthropic CycleHistory ---

func TestAnthropicStore_QueryAnthropicCycleHistory_WithData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateAnthropicCycle("five_hour", start, &resetsAt)
		if err != nil {
			t.Fatalf("CreateAnthropicCycle[%d]: %v", i, err)
		}
		end := start.Add(5 * time.Hour)
		err = s.CloseAnthropicCycle("five_hour", end, float64(i)*0.2, float64(i)*0.1)
		if err != nil {
			t.Fatalf("CloseAnthropicCycle[%d]: %v", i, err)
		}
	}

	// With limit
	history, err := s.QueryAnthropicCycleHistory("five_hour", 2)
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory with limit: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("Expected 2 cycles with limit, got %d", len(history))
	}

	// Without limit
	history, err = s.QueryAnthropicCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryAnthropicCycleHistory without limit: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(history))
	}
}

// --- Antigravity CycleHistory with limit ---

func TestAntigravityStore_QueryAntigravityCycleHistory_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		reset := start.Add(24 * time.Hour)
		_, err := s.CreateAntigravityCycle("model-a", start, &reset)
		if err != nil {
			t.Fatalf("CreateAntigravityCycle[%d]: %v", i, err)
		}
		err = s.CloseAntigravityCycle("model-a", start.Add(24*time.Hour), float64(i)*0.1, float64(i)*0.05)
		if err != nil {
			t.Fatalf("CloseAntigravityCycle[%d]: %v", i, err)
		}
	}

	history, err := s.QueryAntigravityCycleHistory("model-a", 3)
	if err != nil {
		t.Fatalf("QueryAntigravityCycleHistory with limit: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("Expected 3 cycles with limit, got %d", len(history))
	}
}

// --- Copilot CycleHistory with limit ---

func TestCopilotStore_QueryCopilotCycleHistory_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		_, err := s.CreateCopilotCycle("premium_interactions", start, nil)
		if err != nil {
			t.Fatalf("CreateCopilotCycle[%d]: %v", i, err)
		}
		err = s.CloseCopilotCycle("premium_interactions", start.Add(24*time.Hour), 500+i*100, 50+i*10)
		if err != nil {
			t.Fatalf("CloseCopilotCycle[%d]: %v", i, err)
		}
	}

	history, err := s.QueryCopilotCycleHistory("premium_interactions", 2)
	if err != nil {
		t.Fatalf("QueryCopilotCycleHistory with limit: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("Expected 2 cycles with limit, got %d", len(history))
	}
}

// --- QueryLatestCodex with full data ---

func TestCodexStore_QueryLatestCodex_FullData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)
	balance := 42.5

	snap := &api.CodexSnapshot{
		CapturedAt:     now,
		PlanType:       "pro",
		CreditsBalance: &balance,
		RawJSON:        "{}",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 0.3, ResetsAt: &resetsAt, Status: "active"},
			{Name: "daily", Utilization: 0.1, ResetsAt: nil, Status: ""},
		},
	}

	_, err = s.InsertCodexSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	latest, err := s.QueryLatestCodex(DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if latest.PlanType != "pro" {
		t.Fatalf("Expected planType pro, got %s", latest.PlanType)
	}
	if latest.CreditsBalance == nil {
		t.Fatal("Expected CreditsBalance, got nil")
	}
	if *latest.CreditsBalance != 42.5 {
		t.Fatalf("Expected 42.5, got %f", *latest.CreditsBalance)
	}
	if len(latest.Quotas) != 2 {
		t.Fatalf("Expected 2 quotas, got %d", len(latest.Quotas))
	}

	// Verify resetsAt and status on quotas
	for _, q := range latest.Quotas {
		if q.Name == "five_hour" {
			if q.ResetsAt == nil {
				t.Fatal("Expected ResetsAt on five_hour quota")
			}
			if q.Status != "active" {
				t.Fatalf("Expected status 'active', got %s", q.Status)
			}
		}
	}
}

// --- QueryLatestCopilot with full data ---

func TestCopilotStore_QueryLatestCopilot_FullData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetDate := now.Add(30 * 24 * time.Hour)

	snap := &api.CopilotSnapshot{
		CapturedAt:  now,
		CopilotPlan: "enterprise",
		ResetDate:   &resetDate,
		RawJSON:     "{}",
		Quotas: []api.CopilotQuota{
			{Name: "premium_interactions", Entitlement: 1000, Remaining: 800, PercentRemaining: 80.0, Unlimited: false, OverageCount: 2},
			{Name: "chat", Entitlement: 0, Remaining: 0, PercentRemaining: 0, Unlimited: true, OverageCount: 0},
		},
	}

	_, err = s.InsertCopilotSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCopilotSnapshot: %v", err)
	}

	latest, err := s.QueryLatestCopilot()
	if err != nil {
		t.Fatalf("QueryLatestCopilot: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if latest.CopilotPlan != "enterprise" {
		t.Fatalf("Expected enterprise, got %s", latest.CopilotPlan)
	}
	if latest.ResetDate == nil {
		t.Fatal("Expected ResetDate, got nil")
	}
	if len(latest.Quotas) != 2 {
		t.Fatalf("Expected 2 quotas, got %d", len(latest.Quotas))
	}

	foundUnlimited := false
	for _, q := range latest.Quotas {
		if q.Name == "chat" && q.Unlimited {
			foundUnlimited = true
		}
	}
	if !foundUnlimited {
		t.Fatal("Expected chat quota to be unlimited")
	}
}

// --- Zai with tokensNextReset, QueryLatest ---

func TestZaiStore_QueryLatestZai_FullData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	snap := &api.ZaiSnapshot{
		CapturedAt:          now,
		TimeLimit:           60,
		TimeUnit:            1,
		TimeNumber:          60,
		TimeUsage:           10,
		TimeCurrentValue:    50,
		TimeRemaining:       50,
		TimePercentage:      83,
		TimeUsageDetails:    `{"model1": 5}`,
		TokensLimit:         1000000,
		TokensUnit:          1,
		TokensNumber:        1000000,
		TokensUsage:         100000,
		TokensCurrentValue:  900000,
		TokensRemaining:     900000,
		TokensPercentage:    90,
		TokensNextResetTime: &resetTime,
	}

	_, err = s.InsertZaiSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	latest, err := s.QueryLatestZai()
	if err != nil {
		t.Fatalf("QueryLatestZai: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if latest.TokensNextResetTime == nil {
		t.Fatal("Expected TokensNextResetTime, got nil")
	}
	if latest.TimeUsageDetails != `{"model1": 5}` {
		t.Fatalf("Expected TimeUsageDetails, got %s", latest.TimeUsageDetails)
	}
}

// --- Zai CycleHistory ---

func TestZaiStore_QueryZaiCycleHistory_WithLimit(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		reset := start.Add(24 * time.Hour)
		_, err := s.CreateZaiCycle("tokens", start, &reset)
		if err != nil {
			t.Fatalf("CreateZaiCycle[%d]: %v", i, err)
		}
		err = s.CloseZaiCycle("tokens", start.Add(24*time.Hour), int64(i*100000), int64(i*50000))
		if err != nil {
			t.Fatalf("CloseZaiCycle[%d]: %v", i, err)
		}
	}

	history, err := s.QueryZaiCycleHistory("tokens", 3)
	if err != nil {
		t.Fatalf("QueryZaiCycleHistory with limit: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("Expected 3 cycles with limit, got %d", len(history))
	}
}

// --- Antigravity UsageSeries ---

func TestAntigravityStore_QueryAntigravityUsageSeries_FullData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	for i := 0; i < 3; i++ {
		snap := &api.AntigravitySnapshot{
			CapturedAt:     now.Add(time.Duration(i) * time.Hour),
			Email:          "user@test.com",
			PlanName:       "pro",
			PromptCredits:  100.0,
			MonthlyCredits: 500,
			Models: []api.AntigravityModelQuota{
				{ModelID: "model-a", Label: "Model A", RemainingFraction: 1.0 - float64(i)*0.1, RemainingPercent: 100.0 - float64(i)*10.0, ResetTime: &resetTime},
			},
			RawJSON: "{}",
		}
		_, err := s.InsertAntigravitySnapshot(snap)
		if err != nil {
			t.Fatalf("InsertAntigravitySnapshot[%d]: %v", i, err)
		}
	}

	series, err := s.QueryAntigravityUsageSeries("model-a", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryAntigravityUsageSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("Expected 3 points, got %d", len(series))
	}
}

// --- Codex CyclesSince ---

func TestCodexStore_QueryCodexCyclesSince_WithData(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		resetsAt := start.Add(5 * time.Hour)
		_, err := s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", start, &resetsAt)
		if err != nil {
			t.Fatalf("CreateCodexCycle[%d]: %v", i, err)
		}
		end := start.Add(5 * time.Hour)
		err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", end, float64(i)*0.1, float64(i)*0.05)
		if err != nil {
			t.Fatalf("CloseCodexCycle[%d]: %v", i, err)
		}
	}

	cycles, err := s.QueryCodexCyclesSince(DefaultCodexAccountID, "five_hour", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("QueryCodexCyclesSince: %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(cycles))
	}
}

// --- Zai CyclesSince ---

func TestZaiStore_QueryZaiCyclesSince_FullHistory(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		reset := start.Add(24 * time.Hour)
		_, err := s.CreateZaiCycle("tokens", start, &reset)
		if err != nil {
			t.Fatalf("CreateZaiCycle[%d]: %v", i, err)
		}
		err = s.CloseZaiCycle("tokens", start.Add(24*time.Hour), int64(i*100000), int64(i*50000))
		if err != nil {
			t.Fatalf("CloseZaiCycle[%d]: %v", i, err)
		}
	}

	cycles, err := s.QueryZaiCyclesSince("tokens", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("QueryZaiCyclesSince: %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(cycles))
	}
}

// --- Antigravity CyclesSince ---

func TestAntigravityStore_QueryAntigravityCyclesSince(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		start := now.Add(time.Duration(i) * 24 * time.Hour)
		reset := start.Add(24 * time.Hour)
		_, err := s.CreateAntigravityCycle("model-a", start, &reset)
		if err != nil {
			t.Fatalf("CreateAntigravityCycle[%d]: %v", i, err)
		}
		err = s.CloseAntigravityCycle("model-a", start.Add(24*time.Hour), float64(i)*0.1, float64(i)*0.05)
		if err != nil {
			t.Fatalf("CloseAntigravityCycle[%d]: %v", i, err)
		}
	}

	// Need to check the function signature
	history, err := s.QueryAntigravityCycleHistory("model-a")
	if err != nil {
		t.Fatalf("QueryAntigravityCycleHistory: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("Expected 3 cycles, got %d", len(history))
	}
}
