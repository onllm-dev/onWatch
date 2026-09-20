package tracker

import (
	"strconv"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// museTrackerSubscription builds a subscription payload with reset timestamps
// relative to now. Pinned epochs would silently start reporting elapsed cycles
// once real time passed them, turning these into time bombs.
func museTrackerSubscription(windowReset, weeklyReset time.Time, windowUtil, weeklyUtil float64) *api.MuseSubscription {
	sub, err := api.ParseMuseSubscriptionEvents([]string{
		`{"subscription":{"tier":"pro","weekly":{"resets_at":"` + itoa(weeklyReset) + `","used_percent":"` + ftoa(weeklyUtil) + `"},"window":{"resets_at":"` + itoa(windowReset) + `","used_percent":"` + ftoa(windowUtil) + `","window_duration_mins":"300"}}}`,
	})
	if err != nil {
		panic(err)
	}
	return sub
}

func museTrackerSnapshot(now time.Time, windowUtil, weeklyUtil float64) *api.MuseSnapshot {
	sub := museTrackerSubscription(now.Add(time.Hour), now.Add(72*time.Hour), windowUtil, weeklyUtil)
	return api.BuildMuseSnapshot(sub, "muse-spark-1.3", "{}", now)
}

func itoa(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func ftoa(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func TestMuseTracker_CreatesCycles(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMuseTracker(s, nil)
	now := time.Now().UTC()
	if err := tr.Process(museTrackerSnapshot(now, 20, 10)); err != nil {
		t.Fatalf("process: %v", err)
	}

	for _, name := range []string{api.MuseQuotaWindow5H, api.MuseQuotaWeekly} {
		cycle, err := s.QueryActiveMuseCycle(name)
		if err != nil {
			t.Fatalf("active %s: %v", name, err)
		}
		if cycle == nil {
			t.Fatalf("no active cycle for %s", name)
		}
	}
}

func TestMuseTracker_DetectsResetOnNewWindow(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMuseTracker(s, nil)
	var resets []string
	tr.SetOnReset(func(name string) { resets = append(resets, name) })

	now := time.Now().UTC()
	if err := tr.Process(museTrackerSnapshot(now, 80, 50)); err != nil {
		t.Fatalf("process: %v", err)
	}
	// Usage drops to near zero with a new reset timestamp: next window. The
	// weekly reset is unchanged and still in the future, so only the 5h window
	// may report a reset.
	later := now.Add(6 * time.Hour)
	sub := museTrackerSubscription(now.Add(7*time.Hour), now.Add(72*time.Hour), 2, 55)
	if err := tr.Process(api.BuildMuseSnapshot(sub, "m", "{}", later)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(resets) != 1 || resets[0] != api.MuseQuotaWindow5H {
		t.Fatalf("resets = %v, want [window_5h]", resets)
	}

	history, err := s.QueryMuseCycleHistory(api.MuseQuotaWindow5H)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1 closed cycle", len(history))
	}
}

func TestMuseTracker_UsageSummary(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tr := NewMuseTracker(s, nil)
	now := time.Now().UTC()
	snap := museTrackerSnapshot(now, 30, 15)
	if _, err := s.InsertMuseSnapshot(snap); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("process: %v", err)
	}
	summary, err := tr.UsageSummary(api.MuseQuotaWeekly)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.CurrentUtil != 15 {
		t.Fatalf("current = %v, want 15", summary.CurrentUtil)
	}
	if summary.ResetsAt == nil {
		t.Fatal("expected ResetsAt")
	}
}
