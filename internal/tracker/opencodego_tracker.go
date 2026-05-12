package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// OpenCodeGoTracker manages reset cycle detection and usage calculation for OpenCode Go windows.
type OpenCodeGoTracker struct {
	store          *store.Store
	logger         *slog.Logger
	lastPercents   map[string]float64   // window_name -> last usage percent
	lastResetSecs  map[string]int       // window_name -> last reset seconds
	hasLastValues  bool

	onReset func(windowName string)
}

// NewOpenCodeGoTracker creates a new OpenCodeGoTracker.
func NewOpenCodeGoTracker(st *store.Store, logger *slog.Logger) *OpenCodeGoTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenCodeGoTracker{
		store:         st,
		logger:        logger,
		lastPercents:  make(map[string]float64),
		lastResetSecs: make(map[string]int),
	}
}

// SetOnReset registers a callback invoked when a window reset is detected.
func (t *OpenCodeGoTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// Process processes a snapshot, detecting resets and updating cycles.
func (t *OpenCodeGoTracker) Process(snapshot *api.OpenCodeGoSnapshot) error {
	for _, w := range snapshot.Windows {
		if err := t.processWindow(w, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("opencodego tracker: %s: %w", w.WindowName, err)
		}
	}

	t.hasLastValues = true
	return nil
}

func (t *OpenCodeGoTracker) processWindow(w api.OpenCodeGoWindowValue, capturedAt time.Time) error {
	windowName := w.WindowName
	currentUsage := w.UsagePercent

	// Check if we have an active cycle
	cycle, err := t.store.QueryActiveOpenCodeGoCycle(windowName)
	if err != nil {
		return fmt.Errorf("query active cycle: %w", err)
	}

	if cycle == nil {
		// Create new cycle
		now := time.Now().UTC()
		resetTime := now
		if w.ResetInSec > 0 {
			resetTime = now.Add(time.Duration(w.ResetInSec) * time.Second)
		} else {
			resetTime = now.Add(24 * time.Hour)
		}

		_, err := t.store.CreateOpenCodeGoCycle(windowName, capturedAt, resetTime)
		if err != nil {
			return fmt.Errorf("create cycle: %w", err)
		}

		if err := t.store.UpdateOpenCodeGoCycle(windowName, currentUsage, 0); err != nil {
			return fmt.Errorf("set initial peak: %w", err)
		}

		t.lastPercents[windowName] = currentUsage
		t.lastResetSecs[windowName] = w.ResetInSec
		return nil
	}

	lastPercent, hasLast := t.lastPercents[windowName]
	lastResetSec := t.lastResetSecs[windowName]

	if !hasLast {
		// First time seeing this window with an active cycle - skip delta
		t.lastPercents[windowName] = currentUsage
		t.lastResetSecs[windowName] = w.ResetInSec
		return nil
	}

	// Detect reset: if rename_sec increased significantly (went up instead of down by more than interval)
	// or if usage dropped significantly
	now := time.Now().UTC()
	resetDetected := false

	if w.ResetInSec > lastResetSec+300 {
		// reset_in_sec jumped up by 5+ minutes - reset likely occurred
		resetDetected = true
	} else if currentUsage < lastPercent-50 && lastPercent > 50 {
		// Usage dropped by more than 50% from a high value - reset likely occurred
		resetDetected = true
	}

	delta := currentUsage - lastPercent

	if resetDetected {
		// Close current cycle
		if err := t.store.CloseOpenCodeGoCycle(windowName, now, cycle.PeakUsage, cycle.TotalDelta+delta); err != nil {
			return fmt.Errorf("close cycle: %w", err)
		}

		// Create new cycle
		resetTime := now
		if w.ResetInSec > 0 {
			resetTime = now.Add(time.Duration(w.ResetInSec) * time.Second)
		} else {
			resetTime = now.Add(24 * time.Hour)
		}

		if _, err := t.store.CreateOpenCodeGoCycle(windowName, capturedAt, resetTime); err != nil {
			return fmt.Errorf("create new cycle: %w", err)
		}

		if err := t.store.UpdateOpenCodeGoCycle(windowName, currentUsage, 0); err != nil {
			return fmt.Errorf("set new cycle peak: %w", err)
		}

		if t.onReset != nil {
			t.onReset(windowName)
		}

		t.logger.Info("OpenCode Go reset detected", "window", windowName,
			"prev_percent", lastPercent, "cur_percent", currentUsage)
	} else {
		// Update peak and delta
		existingPeak := cycle.PeakUsage
		existingDelta := cycle.TotalDelta

		peak := existingPeak
		if currentUsage > peak {
			peak = currentUsage
		}

		if err := t.store.UpdateOpenCodeGoCycle(windowName, peak, existingDelta+delta); err != nil {
			return fmt.Errorf("update cycle: %w", err)
		}
	}

	t.lastPercents[windowName] = currentUsage
	t.lastResetSecs[windowName] = w.ResetInSec

	return nil
}

// UsageSummary returns computed usage statistics for a window.
func (t *OpenCodeGoTracker) UsageSummary(windowName string) (*api.OpenCodeGoSummary, error) {
	// Get active cycle
	cycle, err := t.store.QueryActiveOpenCodeGoCycle(windowName)
	if err != nil {
		return nil, fmt.Errorf("query active cycle: %w", err)
	}

	// Get latest snapshot for current values
	latest, err := t.store.QueryLatestOpenCodeGo()
	if err != nil {
		return nil, fmt.Errorf("query latest snapshot: %w", err)
	}

	summary := &api.OpenCodeGoSummary{
		WindowName: windowName,
	}

	if latest != nil {
		if w := latest.GetWindow(windowName); w != nil {
			summary.UsagePercent = w.UsagePercent
			summary.ResetInSec = w.ResetInSec
			summary.TimeUntilReset = time.Duration(w.ResetInSec) * time.Second
		}
	}

	// Get completed cycles
	completed, err := t.store.QueryOpenCodeGoCycleHistory(windowName, 0)
	if err != nil {
		t.logger.Debug("opencodego: failed to query cycle history", "window", windowName, "error", err)
	}

	summary.CompletedCycles = len(completed)
	if len(completed) > 0 {
		var totalDelta float64
		for _, c := range completed {
			totalDelta += c.TotalDelta
			if c.PeakUsage > summary.PeakCycle {
				summary.PeakCycle = c.PeakUsage
			}
			if summary.TrackingSince.IsZero() || c.CycleStart.Before(summary.TrackingSince) {
				summary.TrackingSince = c.CycleStart
			}
		}
		summary.TotalTracked = totalDelta
	}

	// Calculate current rate
	if cycle != nil {
		elapsed := time.Since(cycle.CycleStart).Hours()
		if elapsed > 0 && latest != nil {
			if w := latest.GetWindow(windowName); w != nil {
				// Current rate: accumulated delta / elapsed hours (change over time, not absolute usage)
				delta := cycle.TotalDelta
				if delta < 0 {
					delta = 0
				}
				summary.CurrentRate = delta / elapsed

				// Projected usage at reset
				if summary.CurrentRate > 0 && w.ResetInSec > 0 {
					remainingHours := float64(w.ResetInSec) / 3600.0
					summary.ProjectedUsage = w.UsagePercent + summary.CurrentRate*remainingHours
				}
			}
		}
	}

	return summary, nil
}
