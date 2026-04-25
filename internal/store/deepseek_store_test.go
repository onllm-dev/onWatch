package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestDeepSeekStore(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()

	snap1 := &api.DeepSeekSnapshot{
		CapturedAt:      now.Add(-time.Hour),
		IsAvailable:     true,
		Currency:        "CNY",
		TotalBalance:    125.0,
		GrantedBalance:  25.0,
		ToppedUpBalance: 100.0,
	}

	snap2 := &api.DeepSeekSnapshot{
		CapturedAt:      now,
		IsAvailable:     true,
		Currency:        "CNY",
		TotalBalance:    120.0,
		GrantedBalance:  20.0,
		ToppedUpBalance: 100.0,
	}

	id1, err := s.InsertDeepSeekSnapshot(snap1)
	if err != nil {
		t.Fatalf("failed to insert snapshot 1: %v", err)
	}
	if id1 == 0 {
		t.Error("expected non-zero id for snapshot 1")
	}

	id2, err := s.InsertDeepSeekSnapshot(snap2)
	if err != nil {
		t.Fatalf("failed to insert snapshot 2: %v", err)
	}
	if id2 == 0 {
		t.Error("expected non-zero id for snapshot 2")
	}

	latest, err := s.QueryLatestDeepSeek()
	if err != nil {
		t.Fatalf("failed to query latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest snapshot, got nil")
	}
	if latest.TotalBalance != snap2.TotalBalance {
		t.Errorf("expected total balance %v, got %v", snap2.TotalBalance, latest.TotalBalance)
	}
	if latest.Currency != "CNY" {
		t.Errorf("expected currency CNY, got %s", latest.Currency)
	}

	rangeSnaps, err := s.QueryDeepSeekRange(now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("failed to query range: %v", err)
	}
	if len(rangeSnaps) != 2 {
		t.Errorf("expected 2 snapshots in range, got %d", len(rangeSnaps))
	}

	// Cycles
	cycleID, err := s.CreateDeepSeekCycle("balance", "CNY", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("failed to create cycle: %v", err)
	}

	activeCycle, err := s.QueryActiveDeepSeekCycle("balance", "CNY")
	if err != nil {
		t.Fatalf("failed to query active cycle: %v", err)
	}
	if activeCycle == nil {
		t.Fatal("expected active cycle, got nil")
	}
	if activeCycle.ID != cycleID {
		t.Errorf("expected cycle ID %d, got %d", cycleID, activeCycle.ID)
	}

	err = s.UpdateDeepSeekCycle("balance", "CNY", 125.0, 5.0)
	if err != nil {
		t.Fatalf("failed to update cycle: %v", err)
	}

	err = s.CloseDeepSeekCycle("balance", "CNY", now, 125.0, 5.0)
	if err != nil {
		t.Fatalf("failed to close cycle: %v", err)
	}

	history, err := s.QueryDeepSeekCycleHistory("balance", "CNY")
	if err != nil {
		t.Fatalf("failed to query cycle history: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 completed cycle, got %d", len(history))
	}
}
