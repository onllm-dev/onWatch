package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func newTestCursorSnapshot(capturedAt time.Time, accountType api.CursorAccountType, quotas []api.CursorQuota) *api.CursorSnapshot {
	return &api.CursorSnapshot{
		CapturedAt:  capturedAt,
		AccountType: accountType,
		PlanName:    "Pro",
		RawJSON:     `{"test": true}`,
		Quotas:      quotas,
	}
}

func TestCursorStore_InsertAndQueryLatest(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)
	snap := newTestCursorSnapshot(now, api.CursorAccountIndividual, []api.CursorQuota{
		{Name: "total_usage", Used: 50.0, Limit: 400.0, Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		{Name: "auto_usage", Utilization: 3.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
	})

	id, err := s.InsertCursorSnapshot(snap)
	if err != nil {
		t.Fatalf("InsertCursorSnapshot: %v", err)
	}
	if id <= 0 {
		t.Errorf("Expected positive ID, got %d", id)
	}

	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest == nil {
		t.Fatal("QueryLatestCursor returned nil")
	}
	if latest.AccountType != api.CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", latest.AccountType, api.CursorAccountIndividual)
	}
	if latest.PlanName != "Pro" {
		t.Errorf("PlanName = %q, want %q", latest.PlanName, "Pro")
	}
	if len(latest.Quotas) != 2 {
		t.Fatalf("Quotas len = %d, want 2", len(latest.Quotas))
	}
	if latest.Quotas[0].Name != "auto_usage" {
		t.Errorf("Quotas[0].Name = %q, want auto_usage (sorted)", latest.Quotas[0].Name)
	}
	if latest.Quotas[1].Name != "total_usage" {
		t.Errorf("Quotas[1].Name = %q, want total_usage (sorted)", latest.Quotas[1].Name)
	}
	if latest.Quotas[1].Used != 50.0 {
		t.Errorf("total_usage Used = %f, want 50.0", latest.Quotas[1].Used)
	}
	if latest.Quotas[1].Format != api.CursorFormatPercent {
		t.Errorf("total_usage Format = %q, want percent", latest.Quotas[1].Format)
	}
}

func TestCursorStore_QueryLatest_Empty(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest != nil {
		t.Error("Expected nil for empty store")
	}
}

func TestCursorStore_CycleLifecycle(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	// Create cycle
	cycleID, err := s.CreateCursorCycle("total_usage", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCursorCycle: %v", err)
	}
	if cycleID <= 0 {
		t.Errorf("Expected positive cycle ID, got %d", cycleID)
	}

	// Update cycle
	if err := s.UpdateCursorCycle("total_usage", 45.0, 10.0); err != nil {
		t.Fatalf("UpdateCursorCycle: %v", err)
	}

	// Query active cycle
	cycle, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.PeakUtilization != 45.0 {
		t.Errorf("PeakUtilization = %f, want 45.0", cycle.PeakUtilization)
	}
	if cycle.TotalDelta != 10.0 {
		t.Errorf("TotalDelta = %f, want 10.0", cycle.TotalDelta)
	}

	// Close cycle
	cycleEnd := now.Add(24 * time.Hour)
	if err := s.CloseCursorCycle("total_usage", cycleEnd, 45.0, 10.0); err != nil {
		t.Fatalf("CloseCursorCycle: %v", err)
	}

	// Verify no active cycle
	active, err := s.QueryActiveCursorCycle("total_usage")
	if err != nil {
		t.Fatalf("QueryActiveCursorCycle after close: %v", err)
	}
	if active != nil {
		t.Error("Expected nil after closing cycle")
	}

	// Query history
	history, err := s.QueryCursorCycleHistory("total_usage", 10)
	if err != nil {
		t.Fatalf("QueryCursorCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("History len = %d, want 1", len(history))
	}
	if history[0].PeakUtilization != 45.0 {
		t.Errorf("History PeakUtilization = %f, want 45.0", history[0].PeakUtilization)
	}
}

func TestCursorStore_QueryRange(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	for i := 0; i < 3; i++ {
		snap := newTestCursorSnapshot(now.Add(time.Duration(i)*time.Hour), api.CursorAccountIndividual, []api.CursorQuota{
			{Name: "total_usage", Used: float64(i) * 10, Limit: 400.0, Utilization: float64(i) * 2.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		})
		if _, err := s.InsertCursorSnapshot(snap); err != nil {
			t.Fatalf("InsertCursorSnapshot %d: %v", i, err)
		}
	}

	snapshots, err := s.QueryCursorRange(now.Add(-time.Hour), now.Add(4*time.Hour), 200)
	if err != nil {
		t.Fatalf("QueryCursorRange: %v", err)
	}
	if len(snapshots) != 3 {
		t.Errorf("Range len = %d, want 3", len(snapshots))
	}
}

func TestCursorStore_QueryAllQuotaNames(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()

	_, err = s.CreateCursorCycle("total_usage", now, nil)
	if err != nil {
		t.Fatalf("CreateCursorCycle: %v", err)
	}
	_, err = s.CreateCursorCycle("auto_usage", now, nil)
	if err != nil {
		t.Fatalf("CreateCursorCycle: %v", err)
	}

	names, err := s.QueryAllCursorQuotaNames()
	if err != nil {
		t.Fatalf("QueryAllCursorQuotaNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("Names len = %d, want 2", len(names))
	}
	if names[0] != "auto_usage" {
		t.Errorf("Names[0] = %q, want auto_usage", names[0])
	}
	if names[1] != "total_usage" {
		t.Errorf("Names[1] = %q, want total_usage", names[1])
	}
}

func TestCursorStore_LatestPerQuota(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	resetsAt := now.Add(30 * 24 * time.Hour)

	snap1 := newTestCursorSnapshot(now.Add(-2*time.Hour), api.CursorAccountIndividual, []api.CursorQuota{
		{Name: "total_usage", Used: 30.0, Limit: 400.0, Utilization: 7.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
	})
	snap2 := newTestCursorSnapshot(now.Add(-1*time.Hour), api.CursorAccountIndividual, []api.CursorQuota{
		{Name: "total_usage", Used: 50.0, Limit: 400.0, Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
		{Name: "auto_usage", Utilization: 3.0, Format: api.CursorFormatPercent, ResetsAt: &resetsAt},
	})

	if _, err := s.InsertCursorSnapshot(snap1); err != nil {
		t.Fatalf("Insert snap1: %v", err)
	}
	if _, err := s.InsertCursorSnapshot(snap2); err != nil {
		t.Fatalf("Insert snap2: %v", err)
	}

	latest, err := s.QueryCursorLatestPerQuota()
	if err != nil {
		t.Fatalf("QueryCursorLatestPerQuota: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("Latest len = %d, want 2", len(latest))
	}

	for _, q := range latest {
		if q.Name == "total_usage" {
			if q.Utilization != 12.5 {
				t.Errorf("total_usage Utilization = %f, want 12.5 (from newer snapshot)", q.Utilization)
			}
		}
	}
}

func TestCursorStore_LatestPerQuota_UsesOnlyLatestSnapshot(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	oldReset := now.Add(30 * 24 * time.Hour)

	individual := &api.CursorSnapshot{
		CapturedAt:  now.Add(-time.Hour),
		AccountType: api.CursorAccountIndividual,
		PlanName:    "Pro",
		Quotas: []api.CursorQuota{
			{Name: "total_usage", Used: 50, Limit: 400, Utilization: 12.5, Format: api.CursorFormatPercent, ResetsAt: &oldReset},
			{Name: "auto_usage", Utilization: 3, Format: api.CursorFormatPercent, ResetsAt: &oldReset},
		},
	}
	enterprise := &api.CursorSnapshot{
		CapturedAt:  now,
		AccountType: api.CursorAccountEnterprise,
		PlanName:    "Enterprise",
		Quotas: []api.CursorQuota{
			{Name: "requests_gpt-4.1", Used: 15, Limit: 100, Utilization: 15, Format: api.CursorFormatCount},
		},
	}

	if _, err := s.InsertCursorSnapshot(individual); err != nil {
		t.Fatalf("Insert individual snapshot: %v", err)
	}
	if _, err := s.InsertCursorSnapshot(enterprise); err != nil {
		t.Fatalf("Insert enterprise snapshot: %v", err)
	}

	latest, err := s.QueryCursorLatestPerQuota()
	if err != nil {
		t.Fatalf("QueryCursorLatestPerQuota: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("Latest len = %d, want 1", len(latest))
	}
	if latest[0].Name != "requests_gpt-4.1" {
		t.Fatalf("Latest[0].Name = %q, want requests_gpt-4.1", latest[0].Name)
	}
	if latest[0].AccountType != string(api.CursorAccountEnterprise) {
		t.Fatalf("Latest[0].AccountType = %q, want %q", latest[0].AccountType, api.CursorAccountEnterprise)
	}
}
