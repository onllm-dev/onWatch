package tracker

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func commandCodeSnapshotAt(now time.Time, util float64, reset *time.Time) *api.CommandCodeSnapshot {
	quota := api.CommandCodeQuota{
		Name:        api.CommandCodeQuotaFiveHour,
		Used:        util / 100 * 14,
		Limit:       14,
		Utilization: util,
		Format:      api.CommandCodeQuotaFormatCredits,
		ResetsAt:    reset,
	}
	return &api.CommandCodeSnapshot{CapturedAt: now, Quotas: []api.CommandCodeQuota{quota}}
}

func openCodeSnapshotAt(now time.Time, util float64, reset *time.Time) *api.OpenCodeSnapshot {
	return &api.OpenCodeSnapshot{CapturedAt: now, Quotas: []api.OpenCodeQuota{
		{Name: "weekly", Utilization: util, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: reset},
	}}
}

// TestWindowResetSampleBelongsToNewCycle guards the live Command Code defect
// where the poll that revealed a reset was credited to the cycle it closed:
// a completed cycle was stored with the peak of the first sample of the next
// window.
func TestWindowResetSampleBelongsToNewCycle(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, now time.Time, oldReset, newReset *time.Time)
	}{
		{
			name: "commandcode",
			run: func(t *testing.T, now time.Time, oldReset, newReset *time.Time) {
				s := newCommandCodeTrackerStore(t)
				tr := NewCommandCodeTracker(s, nil)
				if err := tr.Process(commandCodeSnapshotAt(now, 10, oldReset)); err != nil {
					t.Fatal(err)
				}
				if err := tr.Process(commandCodeSnapshotAt(now.Add(time.Minute), 20, newReset)); err != nil {
					t.Fatal(err)
				}
				history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
				if err != nil || len(history) != 1 {
					t.Fatalf("history = %d, err %v; want 1", len(history), err)
				}
				if math.Abs(history[0].PeakUtilization-10) > 0.01 || math.Abs(history[0].TotalDelta) > 0.01 {
					t.Fatalf("closed cycle peak/delta = %.2f/%.2f, want 10/0; the reset sample belongs to the new cycle", history[0].PeakUtilization, history[0].TotalDelta)
				}
				active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
				if err != nil || active == nil || math.Abs(active.PeakUtilization-20) > 0.01 {
					t.Fatalf("active = %+v, err %v; want peak 20", active, err)
				}
			},
		},
		{
			name: "opencode",
			run: func(t *testing.T, now time.Time, oldReset, newReset *time.Time) {
				s := newTestOpenCodeStore(t)
				tr := NewOpenCodeTracker(s, nil)
				if err := tr.Process(openCodeSnapshotAt(now, 10, oldReset)); err != nil {
					t.Fatal(err)
				}
				if err := tr.Process(openCodeSnapshotAt(now.Add(time.Minute), 20, newReset)); err != nil {
					t.Fatal(err)
				}
				history, err := s.QueryOpenCodeCycleHistory("weekly")
				if err != nil || len(history) != 1 {
					t.Fatalf("history = %d, err %v; want 1", len(history), err)
				}
				if math.Abs(history[0].PeakUtilization-10) > 0.01 || math.Abs(history[0].TotalDelta) > 0.01 {
					t.Fatalf("closed cycle peak/delta = %.2f/%.2f, want 10/0; the reset sample belongs to the new cycle", history[0].PeakUtilization, history[0].TotalDelta)
				}
				active, err := s.QueryActiveOpenCodeCycle("weekly")
				if err != nil || active == nil || math.Abs(active.PeakUtilization-20) > 0.01 {
					t.Fatalf("active = %+v, err %v; want peak 20", active, err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			oldReset := now.Add(time.Hour)
			newReset := now.Add(6 * time.Hour)
			tc.run(t, now, &oldReset, &newReset)
		})
	}
}

// TestWindowStaleStaleStaleFreshFresh exercises the churn reproduced by both
// reviewers: a cycle created while the API still reported the old reset stored
// no reset, so every later sample carrying that stale reset was read as a fresh
// window. The sequence must close exactly one cycle and fire one notification.
func TestWindowStaleStaleStaleFreshFresh(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	stale := now.Add(time.Hour)
	fresh := now.Add(3 * time.Hour)
	steps := []struct {
		at time.Time
		u  float64
		r  *time.Time
	}{
		{now, 10, &stale},
		{now.Add(2 * time.Hour), 1, &stale},
		{now.Add(2*time.Hour + time.Minute), 2, &stale},
		{now.Add(2*time.Hour + 2*time.Minute), 3, &fresh},
		{now.Add(2*time.Hour + 3*time.Minute), 4, &fresh},
	}

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		for _, st := range steps {
			if err := tr.Process(commandCodeSnapshotAt(st.at, st.u, st.r)); err != nil {
				t.Fatal(err)
			}
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		assertOneClosedCycle(t, "commandcode", len(history), resets, history)
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active resets_at = %v, want the fresh reset %v attached", active.ResetsAt, fresh)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		for _, st := range steps {
			if err := tr.Process(openCodeSnapshotAt(st.at, st.u, st.r)); err != nil {
				t.Fatal(err)
			}
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		assertOneClosedCycleGeneric(t, "opencode", len(history), resets, openCodeCycleBounds(history))
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active resets_at = %v, want the fresh reset %v attached", active.ResetsAt, fresh)
		}
	})
}

func TestWindowDelayedRollover(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%t", restart), func(t *testing.T) {
			for _, provider := range []string{"commandcode", "opencode"} {
				t.Run(provider, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "cycles.db")
					s, err := store.New(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { s.Close() })
					resets := 0
					var process func(time.Time, float64, *time.Time) error
					newTracker := func() {
						if provider == "commandcode" {
							tr := NewCommandCodeTracker(s, nil)
							tr.SetOnReset(func(string) { resets++ })
							process = func(at time.Time, u float64, r *time.Time) error { return tr.Process(commandCodeSnapshotAt(at, u, r)) }
						} else {
							tr := NewOpenCodeTracker(s, nil)
							tr.SetOnReset(func(string) { resets++ })
							process = func(at time.Time, u float64, r *time.Time) error { return tr.Process(openCodeSnapshotAt(at, u, r)) }
						}
					}
					newTracker()
					now := time.Now().UTC().Truncate(time.Second)
					r := now.Add(time.Hour)
					future := r.Add(5 * time.Hour)
					for _, at := range []time.Time{now, r.Add(3 * time.Minute)} {
						if err := process(at, 100, &r); err != nil {
							t.Fatal(err)
						}
					}
					if provider == "commandcode" {
						a, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
						if err != nil || a == nil || a.PeakUtilization != 0 || a.ResetsAt != nil || !a.CycleStart.Equal(r) {
							t.Fatalf("awaiting cycle=%+v err=%v", a, err)
						}
					} else {
						a, err := s.QueryActiveOpenCodeCycle("weekly")
						if err != nil || a == nil || a.PeakUtilization != 0 || a.ResetsAt != nil || !a.CycleStart.Equal(r) {
							t.Fatalf("awaiting cycle=%+v err=%v", a, err)
						}
					}
					reopen := func() {
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						s, err = store.New(path)
						if err != nil {
							t.Fatal(err)
						}
						newTracker()
					}
					if restart {
						reopen()
					}
					if err := process(r.Add(4*time.Minute), 100, &r); err != nil {
						t.Fatal(err)
					}
					if restart {
						reopen()
					}
					if err := process(r.Add(5*time.Minute), 0, &future); err != nil {
						t.Fatal(err)
					}
					if err := process(r.Add(6*time.Minute), 4, &future); err != nil {
						t.Fatal(err)
					}
					if provider == "commandcode" {
						h, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
						if err != nil {
							t.Fatal(err)
						}
						a, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
						if err != nil {
							t.Fatal(err)
						}
						if len(h) != 1 || resets != 1 || h[0].PeakUtilization != 100 || a == nil || a.PeakUtilization != 4 || a.TotalDelta != 4 || a.ResetsAt == nil || !a.ResetsAt.Equal(future) {
							t.Fatalf("closed=%+v resets=%d active=%+v", h, resets, a)
						}
					} else {
						h, err := s.QueryOpenCodeCycleHistory("weekly")
						if err != nil {
							t.Fatal(err)
						}
						a, err := s.QueryActiveOpenCodeCycle("weekly")
						if err != nil {
							t.Fatal(err)
						}
						if len(h) != 1 || resets != 1 || h[0].PeakUtilization != 100 || a == nil || a.PeakUtilization != 4 || a.TotalDelta != 4 || a.ResetsAt == nil || !a.ResetsAt.Equal(future) {
							t.Fatalf("closed=%+v resets=%d active=%+v", h, resets, a)
						}
					}
				})
			}
		})
	}
}

type windowHarness struct {
	provider string
	path     string
	s        *store.Store
	resets   int
	process  func(time.Time, float64, *time.Time) error
}

func newWindowHarness(t *testing.T, provider string) *windowHarness {
	t.Helper()
	h := &windowHarness{provider: provider, path: filepath.Join(t.TempDir(), "cycles.db")}
	h.open(t)
	t.Cleanup(func() { h.s.Close() })
	return h
}

func (h *windowHarness) open(t *testing.T) {
	t.Helper()
	if h.s != nil {
		if err := h.s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.New(h.path)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	if h.provider == "commandcode" {
		tr := NewCommandCodeTracker(s, nil)
		tr.SetOnReset(func(string) { h.resets++ })
		h.process = func(at time.Time, u float64, r *time.Time) error {
			return tr.Process(commandCodeSnapshotAt(at, u, r))
		}
		return
	}
	tr := NewOpenCodeTracker(s, nil)
	tr.SetOnReset(func(string) { h.resets++ })
	h.process = func(at time.Time, u float64, r *time.Time) error {
		return tr.Process(openCodeSnapshotAt(at, u, r))
	}
}

func (h *windowHarness) state(t *testing.T) (time.Time, *time.Time, float64, float64) {
	t.Helper()
	if h.provider == "commandcode" {
		c, err := h.s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || c == nil {
			t.Fatalf("active commandcode cycle: %+v, err %v", c, err)
		}
		return c.CycleStart, c.ResetsAt, c.PeakUtilization, c.TotalDelta
	}
	c, err := h.s.QueryActiveOpenCodeCycle("weekly")
	if err != nil || c == nil {
		t.Fatalf("active opencode cycle: %+v, err %v", c, err)
	}
	return c.CycleStart, c.ResetsAt, c.PeakUtilization, c.TotalDelta
}

func (h *windowHarness) historyLen(t *testing.T) int {
	t.Helper()
	if h.provider == "commandcode" {
		hist, err := h.s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		return len(hist)
	}
	hist, err := h.s.QueryOpenCodeCycleHistory("weekly")
	if err != nil {
		t.Fatal(err)
	}
	return len(hist)
}

// TestWindowIdleRestartKeepsPublishedReset covers an idle window that already
// learned its real reset during the grace period: the boundary it opened at is
// genuine, so the first post-grace poll must not move the start forward.
func TestWindowIdleRestartKeepsPublishedReset(t *testing.T) {
	h := newWindowHarness(t, "commandcode")
	now := time.Now().UTC().Truncate(time.Second)
	r := now.Add(time.Hour)
	r2 := r.Add(7 * 24 * time.Hour)
	h.run(t,
		resetStep{now, 10, &r},
		resetStep{r.Add(3 * time.Minute), 10, &r},
		resetStep{r.Add(5 * time.Minute), 0, nil},
		resetStep{r.Add(6 * time.Minute), 0, &r2},
		resetStep{r.Add(20 * time.Minute), 0, &r2},
		resetStep{r.Add(21 * time.Minute), 3, &r2},
	)
	start, reset, peak, delta := h.state(t)
	if !start.Equal(r) || reset == nil || !reset.Equal(r2) || peak != 3 || delta != 3 {
		t.Fatalf("start=%v reset=%v peak=%v delta=%v, want start %v, reset %v, peak 3, delta 3", start, reset, peak, delta, r, r2)
	}
}

// TestWindowIdleRestartSeedsFirstSample keeps the utilization the restarted
// Command Code window opened with, without counting it twice on the next poll.
func TestWindowIdleRestartSeedsFirstSample(t *testing.T) {
	h := newWindowHarness(t, "commandcode")
	now := time.Now().UTC().Truncate(time.Second)
	r := now.Add(time.Hour)
	h.run(t, resetStep{now, 10, &r}, resetStep{r.Add(5 * time.Minute), 0, nil})
	later := r.Add(48 * time.Hour)
	r2 := later.Add(5 * time.Hour)
	h.run(t, resetStep{later, 2, &r2}, resetStep{later.Add(2 * time.Minute), 5, &r2})
	start, _, peak, delta := h.state(t)
	if !start.Equal(later) || peak != 5 || delta != 5 {
		t.Fatalf("start=%v peak=%v delta=%v, want start %v, peak 5, delta 5", start, peak, delta, later)
	}
}

type resetStep struct {
	at time.Time
	u  float64
	r  *time.Time
}

func (h *windowHarness) run(t *testing.T, steps ...resetStep) {
	t.Helper()
	for _, st := range steps {
		if err := h.process(st.at, st.u, st.r); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWindowAwaitingRolloverExpires covers a reset the API never acknowledges
// (for Command Code, a subscription lookup that keeps failing): the awaiting
// state must stop discarding samples after its grace period.
func TestWindowAwaitingRolloverExpires(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			h.run(t, resetStep{now, 100, &r}, resetStep{r.Add(3 * time.Minute), 100, &r}, resetStep{r.Add(5 * time.Minute), 0, nil})
			start, reset, peak, delta := h.state(t)
			if !start.Equal(r) || reset != nil || peak != 0 || delta != 0 {
				t.Fatalf("during grace: start=%v reset=%v peak=%v delta=%v", start, reset, peak, delta)
			}
			h.run(t, resetStep{r.Add(20 * time.Minute), 5, nil}, resetStep{r.Add(21 * time.Minute), 12, nil})
			_, reset, peak, delta = h.state(t)
			if reset != nil || peak != 12 || delta != 12 {
				t.Fatalf("after grace: reset=%v peak=%v delta=%v, want accumulation", reset, peak, delta)
			}
			if h.resets != 1 || h.historyLen(t) != 1 {
				t.Fatalf("resets=%d history=%d, want one rollover only", h.resets, h.historyLen(t))
			}
		})
	}
}

func TestWindowAwaitingRolloverRestart(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider+"/during-grace", func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			h.run(t, resetStep{now, 100, &r}, resetStep{r.Add(3 * time.Minute), 100, &r})
			h.open(t)
			h.run(t, resetStep{r.Add(5 * time.Minute), 100, nil}, resetStep{r.Add(6 * time.Minute), 0, nil}, resetStep{r.Add(7 * time.Minute), 9, nil})
			start, _, peak, delta := h.state(t)
			if !start.Equal(r) || peak != 9 || delta != 9 {
				t.Fatalf("restart lost the awaiting state: start=%v peak=%v delta=%v", start, peak, delta)
			}
			if h.resets != 1 {
				t.Fatalf("resets = %d, want 1", h.resets)
			}
		})
		t.Run(provider+"/after-grace", func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			h.run(t, resetStep{now, 100, &r}, resetStep{r.Add(3 * time.Minute), 100, &r}, resetStep{r.Add(20 * time.Minute), 5, nil})
			h.open(t)
			h.run(t, resetStep{r.Add(21 * time.Minute), 12, nil})
			_, _, peak, _ := h.state(t)
			if peak != 12 {
				t.Fatalf("peak after restart = %v, want 12", peak)
			}
			if h.resets != 1 {
				t.Fatalf("resets = %d, want 1", h.resets)
			}
		})
	}
}

// TestWindowOpenCodeIdleExpiryDoesNotSpanDays covers an idle 5-hour window
// that would otherwise stretch to the next session two days later.
func TestWindowOpenCodeIdleExpiryDoesNotSpanDays(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			sessionEnd := now.Add(5 * time.Minute)
			// Idle after the reset: used 0 with no reset published at all.
			h.run(t, resetStep{now, 10, &sessionEnd}, resetStep{sessionEnd.Add(10 * time.Minute), 0, nil})
			later := sessionEnd.Add(48 * time.Hour)
			r2 := later.Add(5 * time.Hour)
			h.run(t, resetStep{later, 1, &r2})
			start, reset, peak, _ := h.state(t)
			if !start.Equal(later) || reset == nil || !reset.Equal(r2) || peak != 1 {
				t.Fatalf("new session start=%v reset=%v peak=%v, want start %v, reset %v, peak 1", start, reset, peak, later, r2)
			}
			if h.resets != 1 {
				t.Fatalf("resets = %d, want 1", h.resets)
			}
		})
	}
}

// TestWindowLowPeakRolloverDelta keeps usage spent shortly after a boundary
// when the previous window barely registered any usage.
func TestWindowLowPeakRolloverDelta(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			h.run(t,
				resetStep{now, 1.5, &r},
				resetStep{r.Add(3 * time.Minute), 1.5, &r},
				resetStep{r.Add(4 * time.Minute), 0, nil},
				resetStep{r.Add(5 * time.Minute), 30, nil},
			)
			_, _, peak, delta := h.state(t)
			if peak != 30 || delta != 30 {
				t.Fatalf("peak=%v delta=%v, want 30/30 after a low-peak predecessor", peak, delta)
			}
		})
	}
}

// TestWindowAwaitingRolloverPostResetDelta keeps usage recorded between the
// boundary and the API acknowledging it, without trusting a stale pre-reset
// sample when the reset advances first.
func TestWindowAwaitingRolloverPostResetDelta(t *testing.T) {
	for _, provider := range []string{"commandcode", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			r2 := r.Add(5 * time.Hour)
			h.run(t,
				resetStep{now, 100, &r},
				resetStep{r.Add(3 * time.Minute), 100, &r},
				resetStep{r.Add(4 * time.Minute), 0, nil},
				resetStep{r.Add(5 * time.Minute), 5, nil},
				resetStep{r.Add(6 * time.Minute), 12, nil},
				resetStep{r.Add(7 * time.Minute), 12, &r2},
				resetStep{r.Add(8 * time.Minute), 15, &r2},
			)
			_, _, peak, delta := h.state(t)
			if peak != 15 || delta != 15 {
				t.Fatalf("peak=%v delta=%v, want 15/15 including post-reset usage", peak, delta)
			}
			if h.resets != 1 {
				t.Fatalf("resets = %d, want 1", h.resets)
			}
		})
		t.Run(provider+"/reset-before-utilization", func(t *testing.T) {
			h := newWindowHarness(t, provider)
			now := time.Now().UTC().Truncate(time.Second)
			r := now.Add(time.Hour)
			r2 := r.Add(5 * time.Hour)
			h.run(t,
				resetStep{now, 100, &r},
				resetStep{r.Add(3 * time.Minute), 100, &r},
				resetStep{r.Add(4 * time.Minute), 100, &r2},
				resetStep{r.Add(5 * time.Minute), 100, &r2},
				resetStep{r.Add(6 * time.Minute), 0, &r2},
				resetStep{r.Add(7 * time.Minute), 5, &r2},
			)
			_, _, peak, delta := h.state(t)
			if peak != 5 || delta != 5 {
				t.Fatalf("peak=%v delta=%v, want 5/5 without the stale 100", peak, delta)
			}
		})
	}
}

type cycleBound struct {
	start time.Time
	end   *time.Time
}

func openCodeCycleBounds(cycles []*store.OpenCodeResetCycle) []cycleBound {
	bounds := make([]cycleBound, 0, len(cycles))
	for _, c := range cycles {
		bounds = append(bounds, cycleBound{start: c.CycleStart, end: c.CycleEnd})
	}
	return bounds
}

func assertOneClosedCycle(t *testing.T, provider string, closed, resets int, history []*store.CommandCodeResetCycle) {
	t.Helper()
	if closed != 1 || resets != 1 {
		t.Fatalf("%s closed cycles=%d onReset=%d, want 1 and 1", provider, closed, resets)
	}
	for _, c := range history {
		if c.CycleEnd != nil && c.CycleEnd.Before(c.CycleStart) {
			t.Fatalf("%s cycle %d ends %v before it starts %v", provider, c.ID, c.CycleEnd, c.CycleStart)
		}
	}
}

func assertOneClosedCycleGeneric(t *testing.T, provider string, closed, resets int, bounds []cycleBound) {
	t.Helper()
	if closed != 1 || resets != 1 {
		t.Fatalf("%s closed cycles=%d onReset=%d, want 1 and 1", provider, closed, resets)
	}
	for _, c := range bounds {
		if c.end != nil && c.end.Before(c.start) {
			t.Fatalf("%s cycle ends %v before it starts %v", provider, c.end, c.start)
		}
	}
}

// TestWindowMissedPollsSpanningReset covers a daemon that was offline across a
// reset: the first sample after the gap is the only one that can reveal it.
func TestWindowMissedPollsSpanningReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	oldReset := now.Add(time.Hour)
	fresh := now.Add(6 * time.Hour)
	later := now.Add(4 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(commandCodeSnapshotAt(now, 10, &oldReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(later, 0.5, &fresh)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
		if history[0].CycleEnd == nil || !history[0].CycleEnd.Equal(oldReset) {
			t.Fatalf("cycle end = %v, want the missed reset %v", history[0].CycleEnd, oldReset)
		}
		if history[0].CycleEnd.Before(history[0].CycleStart) {
			t.Fatalf("cycle end %v before start %v", history[0].CycleEnd, history[0].CycleStart)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active = %+v, err %v", active, err)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 10, &oldReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(later, 0.5, &fresh)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
		if history[0].CycleEnd == nil || !history[0].CycleEnd.Equal(oldReset) {
			t.Fatalf("cycle end = %v, want the missed reset %v", history[0].CycleEnd, oldReset)
		}
		if history[0].CycleEnd.Before(history[0].CycleStart) {
			t.Fatalf("cycle end %v before start %v", history[0].CycleEnd, history[0].CycleStart)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(fresh) {
			t.Fatalf("active = %+v, err %v", active, err)
		}
	})
}

// TestWindowRestartMidWindowAttachesReset covers a tracker restart in the
// middle of an open window: the reset the API only starts reporting later is
// the same window, not a new one.
func TestWindowRestartMidWindowAttachesReset(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(3 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		// No reset reported yet, usage already climbing.
		if err := tr.Process(commandCodeSnapshotAt(now, 10, nil)); err != nil {
			t.Fatal(err)
		}
		// Restart mid-window: the same window now reports its reset.
		if err := tr.Process(commandCodeSnapshotAt(now.Add(10*time.Minute), 15, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 || resets != 0 {
			t.Fatalf("closed cycles=%d onReset=%d, want 0 and 0", len(history), resets)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
			t.Fatalf("active = %+v, err %v; want the reset attached", active, err)
		}
		if !active.CycleStart.Equal(now) {
			t.Fatalf("cycle start moved to %v, want the original %v", active.CycleStart, now)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 10, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(now.Add(10*time.Minute), 15, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 || resets != 0 {
			t.Fatalf("closed cycles=%d onReset=%d, want 0 and 0", len(history), resets)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil || active.ResetsAt == nil || !active.ResetsAt.Equal(reset) {
			t.Fatalf("active = %+v, err %v; want the reset attached", active, err)
		}
		if !active.CycleStart.Equal(now) {
			t.Fatalf("cycle start moved to %v, want the original %v", active.CycleStart, now)
		}
	})
}

// TestWindowResetDropClosesCycle keeps the genuine reset signal: a new reset
// alongside falling utilization really is a new window.
func TestWindowResetDropClosesCycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(4 * time.Hour)

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(commandCodeSnapshotAt(now, 60, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(now.Add(time.Minute), 2, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryCommandCodeCycleHistory(api.CommandCodeQuotaFiveHour)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
	})

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		resets := 0
		tr.SetOnReset(func(string) { resets++ })
		if err := tr.Process(openCodeSnapshotAt(now, 60, nil)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(now.Add(time.Minute), 2, &reset)); err != nil {
			t.Fatal(err)
		}
		history, err := s.QueryOpenCodeCycleHistory("weekly")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 1 || resets != 1 {
			t.Fatalf("closed cycles=%d onReset=%d, want 1 and 1", len(history), resets)
		}
	})
}

// TestWindowStaleResetIsNotStored covers the inverted live OpenCode cycle
// (cycle_start 03:38, cycle_end 03:36): a reset was detected while the API
// still reported the previous, already-passed reset, and that stale timestamp
// was written as the new cycle's reset.
func TestWindowStaleResetIsNotStored(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	staleReset := now.Add(time.Hour)
	later := now.Add(2 * time.Hour)

	t.Run("opencode", func(t *testing.T) {
		s := newTestOpenCodeStore(t)
		tr := NewOpenCodeTracker(s, nil)
		if err := tr.Process(openCodeSnapshotAt(now, 10, &staleReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(openCodeSnapshotAt(later, 0.2, &staleReset)); err != nil {
			t.Fatal(err)
		}
		active, err := s.QueryActiveOpenCodeCycle("weekly")
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt != nil {
			t.Fatalf("new cycle stored stale reset %v", active.ResetsAt)
		}
	})

	t.Run("commandcode", func(t *testing.T) {
		s := newCommandCodeTrackerStore(t)
		tr := NewCommandCodeTracker(s, nil)
		if err := tr.Process(commandCodeSnapshotAt(now, 10, &staleReset)); err != nil {
			t.Fatal(err)
		}
		if err := tr.Process(commandCodeSnapshotAt(later, 0.2, &staleReset)); err != nil {
			t.Fatal(err)
		}
		active, err := s.QueryActiveCommandCodeCycle(api.CommandCodeQuotaFiveHour)
		if err != nil || active == nil {
			t.Fatalf("active = %+v, err %v", active, err)
		}
		if active.ResetsAt != nil {
			t.Fatalf("new cycle stored stale reset %v", active.ResetsAt)
		}
	})
}
