package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func museTestSnapshot(now time.Time, windowUtil, weeklyUtil float64) *api.MuseSnapshot {
	reset := now.Add(5 * time.Hour)
	weeklyReset := now.Add(7 * 24 * time.Hour)
	return &api.MuseSnapshot{
		CapturedAt:         now,
		Tier:               "pro",
		Model:              "muse-spark-1.3",
		WindowUsedPct:      windowUtil,
		WindowResetsAt:     &reset,
		WindowDurationMins: 300,
		WeeklyUsedPct:      weeklyUtil,
		WeeklyResetsAt:     &weeklyReset,
		Quotas: []api.MuseQuota{
			{Name: api.MuseQuotaWindow5H, Used: windowUtil, Limit: 100, Utilization: windowUtil, Format: api.MuseQuotaFormatPercent, ResetsAt: &reset},
			{Name: api.MuseQuotaWeekly, Used: weeklyUtil, Limit: 100, Utilization: weeklyUtil, Format: api.MuseQuotaFormatPercent, ResetsAt: &weeklyReset},
		},
	}
}

func TestMuseStore_InsertAndQueryLatest(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	id, err := s.InsertMuseSnapshot(museTestSnapshot(now, 34, 12.5))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Error("expected id > 0")
	}

	latest, err := s.QueryLatestMuse()
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot")
	}
	if latest.Tier != "pro" || latest.Model != "muse-spark-1.3" {
		t.Fatalf("latest mismatch: %+v", latest)
	}
	if latest.WindowUsedPct != 34 || latest.WeeklyUsedPct != 12.5 || latest.WindowDurationMins != 300 {
		t.Fatalf("window fields mismatch: %+v", latest)
	}
	if len(latest.Quotas) != 2 {
		t.Fatalf("quotas = %d, want 2", len(latest.Quotas))
	}
	if latest.Quotas[0].ResetsAt == nil {
		t.Fatal("quota resets_at not loaded")
	}
}

func TestMuseStore_QueryLatestEmpty(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	latest, err := s.QueryLatestMuse()
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected nil, got %+v", latest)
	}
}

func TestMuseStore_QueryRange(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		snap := museTestSnapshot(base.Add(time.Duration(i)*time.Minute), float64(i*10), float64(i*5))
		if _, err := s.InsertMuseSnapshot(snap); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	snaps, err := s.QueryMuseRange(base.Add(-time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("range = %d, want 3", len(snaps))
	}
	if len(snaps[0].Quotas) != 2 {
		t.Fatalf("range snapshot quotas = %d, want 2", len(snaps[0].Quotas))
	}

	limited, err := s.QueryMuseRange(base.Add(-time.Minute), base.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("limited range: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited range = %d, want 2", len(limited))
	}
}

func TestMuseStore_Cycles(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if _, err := s.CreateMuseCycle(api.MuseQuotaWeekly, now, nil); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	active, err := s.QueryActiveMuseCycle(api.MuseQuotaWeekly)
	if err != nil {
		t.Fatalf("active cycle: %v", err)
	}
	if active == nil || active.QuotaName != api.MuseQuotaWeekly {
		t.Fatalf("active = %+v", active)
	}
	if err := s.UpdateMuseCycle(api.MuseQuotaWeekly, 40, 15); err != nil {
		t.Fatalf("update cycle: %v", err)
	}
	if err := s.QueryActiveMuseCycleCheck(api.MuseQuotaWeekly, 40, 15); err != nil {
		t.Fatalf("verify cycle: %v", err)
	}
	if err := s.CloseMuseCycle(api.MuseQuotaWeekly, now.Add(time.Hour), 40, 15); err != nil {
		t.Fatalf("close cycle: %v", err)
	}
	active, err = s.QueryActiveMuseCycle(api.MuseQuotaWeekly)
	if err != nil {
		t.Fatalf("active after close: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active cycle, got %+v", active)
	}
	history, err := s.QueryMuseCycleHistory(api.MuseQuotaWeekly)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
	names, err := s.QueryAllMuseQuotaNames()
	if err != nil {
		t.Fatalf("quota names: %v", err)
	}
	if len(names) != 1 || names[0] != api.MuseQuotaWeekly {
		t.Fatalf("names = %v", names)
	}
}

// QueryActiveMuseCycleCheck is a test-only helper asserting peak/delta.
func (s *Store) QueryActiveMuseCycleCheck(quotaName string, peak, delta float64) error {
	active, err := s.QueryActiveMuseCycle(quotaName)
	if err != nil {
		return err
	}
	if active == nil {
		return errors.New("no active muse cycle")
	}
	if active.PeakUtilization != peak || active.TotalDelta != delta {
		return fmt.Errorf("peak/delta = %v/%v, want %v/%v", active.PeakUtilization, active.TotalDelta, peak, delta)
	}
	return nil
}
