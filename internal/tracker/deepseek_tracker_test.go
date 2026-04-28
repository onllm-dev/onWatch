package tracker

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestDeepSeekTracker(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer db.Close()

	tracker := NewDeepSeekTracker(db, nil)
	now := time.Now().UTC()

	// Initial balance: 100 CNY
	snap1 := &api.DeepSeekSnapshot{
		CapturedAt:   now.Add(-2 * time.Hour),
		IsAvailable:  true,
		Currency:     "CNY",
		TotalBalance: 100.0,
	}
	db.InsertDeepSeekSnapshot(snap1)
	err = tracker.Process(snap1)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}

	// Balance drops to 80 (spend 20)
	snap2 := &api.DeepSeekSnapshot{
		CapturedAt:   now.Add(-1 * time.Hour),
		IsAvailable:  true,
		Currency:     "CNY",
		TotalBalance: 80.0,
	}
	db.InsertDeepSeekSnapshot(snap2)
	err = tracker.Process(snap2)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}

	summary, err := tracker.UsageSummary("CNY")
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}
	if summary.CurrentBalance != 80.0 {
		t.Errorf("expected balance 80, got %v", summary.CurrentBalance)
	}
	if summary.TotalTracked != 20.0 {
		t.Errorf("expected total tracked 20, got %v", summary.TotalTracked)
	}
	if summary.CompletedCycles != 0 {
		t.Errorf("expected 0 completed cycles, got %v", summary.CompletedCycles)
	}

	// Balance recharges to 200 (reset)
	resetCalled := false
	tracker.SetOnReset(func(q string) { resetCalled = true })
	snap3 := &api.DeepSeekSnapshot{
		CapturedAt:   now,
		IsAvailable:  true,
		Currency:     "CNY",
		TotalBalance: 200.0,
	}
	db.InsertDeepSeekSnapshot(snap3)
	err = tracker.Process(snap3)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}

	if !resetCalled {
		t.Error("expected reset callback to be called")
	}

	summary, err = tracker.UsageSummary("CNY")
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}
	if summary.CurrentBalance != 200.0 {
		t.Errorf("expected balance 200, got %v", summary.CurrentBalance)
	}
	if summary.CompletedCycles != 1 {
		t.Errorf("expected 1 completed cycle, got %v", summary.CompletedCycles)
	}
	// Active cycle has 0 tracked, completed cycle had 20
	if summary.TotalTracked != 20.0 {
		t.Errorf("expected total tracked 20, got %v", summary.TotalTracked)
	}
}
