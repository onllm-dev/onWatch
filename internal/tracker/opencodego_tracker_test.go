package tracker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newOpenCodeGoTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCodeGoTracker_ProcessAndReset(t *testing.T) {
	t.Parallel()
	s := newOpenCodeGoTestStore(t)
	tr := NewOpenCodeGoTracker(s, slog.Default())

	now := time.Now().UTC()
	first := &api.OpenCodeGoSnapshot{CapturedAt: now, Windows: []api.OpenCodeGoWindowValue{{WindowName: "rolling", UsagePercent: 70, ResetInSec: 300}}}
	if err := tr.Process(first); err != nil {
		t.Fatalf("Process first: %v", err)
	}

	resetCalled := false
	tr.SetOnReset(func(string) { resetCalled = true })
	second := &api.OpenCodeGoSnapshot{CapturedAt: now.Add(time.Minute), Windows: []api.OpenCodeGoWindowValue{{WindowName: "rolling", UsagePercent: 5, ResetInSec: 7200}}}
	if err := tr.Process(second); err != nil {
		t.Fatalf("Process second: %v", err)
	}
	if !resetCalled {
		t.Fatal("expected reset callback")
	}
}

func TestOpenCodeGoTracker_UsageSummary(t *testing.T) {
	t.Parallel()
	s := newOpenCodeGoTestStore(t)
	tr := NewOpenCodeGoTracker(s, slog.Default())

	now := time.Now().UTC().Add(-5 * time.Minute)
	snap := &api.OpenCodeGoSnapshot{
		CapturedAt: now,
		Windows:    []api.OpenCodeGoWindowValue{{WindowName: "rolling", UsagePercent: 30, ResetInSec: 3600, Status: "ok"}},
	}
	if _, err := s.InsertOpenCodeGoSnapshot(snap); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("process: %v", err)
	}

	snap2 := &api.OpenCodeGoSnapshot{
		CapturedAt: now.Add(2 * time.Minute),
		Windows:    []api.OpenCodeGoWindowValue{{WindowName: "rolling", UsagePercent: 40, ResetInSec: 3000, Status: "ok"}},
	}
	if _, err := s.InsertOpenCodeGoSnapshot(snap2); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	if err := tr.Process(snap2); err != nil {
		t.Fatalf("process2: %v", err)
	}

	summary, err := tr.UsageSummary("rolling")
	if err != nil {
		t.Fatalf("UsageSummary: %v", err)
	}
	if summary.UsagePercent != 40 {
		t.Fatalf("UsagePercent = %.1f", summary.UsagePercent)
	}
	if summary.CurrentRate <= 0 {
		t.Fatalf("CurrentRate = %.2f, want > 0", summary.CurrentRate)
	}
}
