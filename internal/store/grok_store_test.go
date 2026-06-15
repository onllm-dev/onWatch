package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestGrokStore_InsertAndQueryLatest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()
	defer os.Remove(dbPath)

	now := time.Now().UTC()
	snap := &api.GrokSnapshot{
		CapturedAt:  now,
		AccountID:   1,
		Email:       "g@x.ai",
		TeamID:      "t1",
		LoginMethod: "SuperGrok",
		Quotas: []api.GrokQuota{
			{Name: "credits", Utilization: 37.5, Status: "healthy", ResetsAt: nil},
		},
		RawJSON: "{}",
	}
	id, err := s.InsertGrokSnapshot(snap)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Error("expected id > 0")
	}

	latest, err := s.QueryLatestGrok(1)
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest == nil || latest.Email != "g@x.ai" || len(latest.Quotas) != 1 || latest.Quotas[0].Utilization != 37.5 {
		t.Errorf("latest mismatch: %+v", latest)
	}
}

func TestGrokStore_QueryRangeLoadsQuotas(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()
	defer os.Remove(dbPath)

	base := time.Now().UTC().Add(-time.Hour)
	const n = 5
	for i := 0; i < n; i++ {
		snap := &api.GrokSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			AccountID:  1,
			Quotas: []api.GrokQuota{
				{Name: "credits", Utilization: float64(i) + 1, Status: "healthy"},
			},
			RawJSON: "{}",
		}
		if _, err := s.InsertGrokSnapshot(snap); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	snaps, err := s.QueryGrokRange(1, base.Add(-time.Minute), time.Now().UTC())
	if err != nil {
		t.Fatalf("query range: %v", err)
	}
	if len(snaps) != n {
		t.Fatalf("expected %d snapshots, got %d", n, len(snaps))
	}
	// Each snapshot must carry its credits quota (regression: range query dropped quotas).
	for i, sn := range snaps {
		if len(sn.Quotas) != 1 || sn.Quotas[0].Name != "credits" {
			t.Fatalf("snapshot %d missing quota: %+v", i, sn.Quotas)
		}
		if sn.Quotas[0].Utilization != float64(i)+1 {
			t.Errorf("snapshot %d util = %v, want %v", i, sn.Quotas[0].Utilization, float64(i)+1)
		}
	}
}

func TestGrokStore_Cycles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer s.Close()
	defer os.Remove(dbPath)

	c := &GrokResetCycle{
		AccountID:       1,
		QuotaName:       "credits",
		CycleStart:      time.Now().Add(-24 * time.Hour),
		PeakUtilization: 10,
		TotalDelta:      2,
	}
	id, err := s.InsertGrokResetCycle(c)
	if err != nil || id == 0 {
		t.Fatalf("insert cycle: %v", err)
	}

	active, err := s.QueryActiveGrokResetCycle(1, "credits")
	if err != nil || active == nil {
		t.Fatalf("active cycle: %v", err)
	}
	if active.QuotaName != "credits" {
		t.Error("wrong quota")
	}

	end := time.Now()
	if err := s.UpdateGrokResetCycleEnd(id, end, 12, 3); err != nil {
		t.Fatalf("update end: %v", err)
	}
}
