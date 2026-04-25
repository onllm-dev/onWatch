package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestMoonshotStore(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()

	snap1 := &api.MoonshotSnapshot{
		CapturedAt:       now.Add(-time.Hour),
		AvailableBalance: 100.0,
		VoucherBalance:   20.0,
		CashBalance:      80.0,
	}

	snap2 := &api.MoonshotSnapshot{
		CapturedAt:       now,
		AvailableBalance: 90.0,
		VoucherBalance:   15.0,
		CashBalance:      75.0,
	}

	id1, err := s.InsertMoonshotSnapshot(snap1)
	if err != nil {
		t.Fatalf("failed to insert snapshot 1: %v", err)
	}
	if id1 == 0 {
		t.Error("expected non-zero id for snapshot 1")
	}

	id2, err := s.InsertMoonshotSnapshot(snap2)
	if err != nil {
		t.Fatalf("failed to insert snapshot 2: %v", err)
	}
	if id2 == 0 {
		t.Error("expected non-zero id for snapshot 2")
	}

	latest, err := s.QueryLatestMoonshot()
	if err != nil {
		t.Fatalf("failed to query latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest snapshot, got nil")
	}
	if latest.AvailableBalance != snap2.AvailableBalance {
		t.Errorf("expected available balance %v, got %v", snap2.AvailableBalance, latest.AvailableBalance)
	}

	rangeSnaps, err := s.QueryMoonshotRange(now.Add(-2*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("failed to query range: %v", err)
	}
	if len(rangeSnaps) != 2 {
		t.Errorf("expected 2 snapshots in range, got %d", len(rangeSnaps))
	}

	// Cycles
	cycleID, err := s.CreateMoonshotCycle("balance", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("failed to create cycle: %v", err)
	}

	activeCycle, err := s.QueryActiveMoonshotCycle("balance")
	if err != nil {
		t.Fatalf("failed to query active cycle: %v", err)
	}
	if activeCycle == nil {
		t.Fatal("expected active cycle, got nil")
	}
	if activeCycle.ID != cycleID {
		t.Errorf("expected cycle ID %d, got %d", cycleID, activeCycle.ID)
	}

	err = s.UpdateMoonshotCycle("balance", 100.0, 10.0)
	if err != nil {
		t.Fatalf("failed to update cycle: %v", err)
	}

	err = s.CloseMoonshotCycle("balance", now, 100.0, 10.0)
	if err != nil {
		t.Fatalf("failed to close cycle: %v", err)
	}

	history, err := s.QueryMoonshotCycleHistory("balance")
	if err != nil {
		t.Fatalf("failed to query cycle history: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 completed cycle, got %d", len(history))
	}
}
