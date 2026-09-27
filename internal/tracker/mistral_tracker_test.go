package tracker

import (
	"context"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func mistralTrackerSnapshot(identity string, now time.Time, used, limit, utilization float64, resetsAt *time.Time) *api.MistralSnapshot {
	return &api.MistralSnapshot{
		Identity:   identity,
		CapturedAt: now,
		Status:     "ok",
		Quotas: []api.MistralQuota{{
			Name: "api_included", Used: used, Limit: limit, Utilization: utilization,
			Currency: "EUR", CapturedAt: now, ResetsAt: resetsAt,
		}},
	}
}

func TestMistralTracker_ProcessPersistsQuota(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMistralTracker(s)
	now := time.Now().UTC()
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", now, 5, 10, 50, nil)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if err := s.SetSetting("mistral_identity", "a"); err != nil {
		t.Fatal(err)
	}
	cycles, err := s.MistralCycles(context.Background(), "api_included", 10)
	if err != nil || len(cycles) != 1 || !cycles[0].IsActive {
		t.Fatalf("cycles=%+v err=%v", cycles, err)
	}
}

func TestMistralTracker_ProcessFiresOnReset(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMistralTracker(s)
	var resets []string
	tr.SetOnReset(func(name string) { resets = append(resets, name) })

	now := time.Now().UTC()
	firstReset := now.Add(24 * time.Hour)
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", now, 5, 10, 50, &firstReset)); err != nil {
		t.Fatalf("process: %v", err)
	}
	secondReset := firstReset.Add(30 * 24 * time.Hour)
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", firstReset.Add(time.Minute), 1, 10, 10, &secondReset)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(resets) != 1 || resets[0] != "api_included" {
		t.Fatalf("resets = %v, want [api_included]", resets)
	}
}

// A tracker with no onReset callback registered (the default before
// SetOnReset is called) must not panic when a reset actually occurs.
func TestMistralTracker_ProcessWithoutOnResetCallback(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMistralTracker(s)
	now := time.Now().UTC()
	firstReset := now.Add(24 * time.Hour)
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", now, 5, 10, 50, &firstReset)); err != nil {
		t.Fatalf("process: %v", err)
	}
	secondReset := firstReset.Add(30 * 24 * time.Hour)
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", firstReset.Add(time.Minute), 1, 10, 10, &secondReset)); err != nil {
		t.Fatalf("process: %v", err)
	}
}

func TestMistralTracker_ProcessPropagatesStoreError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	s.Close() // closed store: TrackMistral must fail, and Process must surface it

	tr := NewMistralTracker(s)
	if err := tr.Process(context.Background(), mistralTrackerSnapshot("a", time.Now().UTC(), 1, 10, 10, nil)); err == nil {
		t.Fatal("expected error from a closed store")
	}
}
