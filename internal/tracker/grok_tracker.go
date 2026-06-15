package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// GrokTracker manages reset cycle detection and usage stats for the Grok "credits" quota.
type GrokTracker struct {
	store      *store.Store
	logger     *slog.Logger
	lastValues map[int64]map[string]float64   // account -> quota -> last util
	lastResets map[int64]map[string]time.Time
	hasLast    map[int64]bool

	onReset func(quotaName string)
}

const DefaultGrokAccountID int64 = 1

// GrokSummary holds computed stats for a Grok quota (primarily credits).
type GrokSummary struct {
	QuotaName       string
	CurrentUtil     float64
	ResetsAt        *time.Time
	TimeUntilReset  time.Duration
	CurrentRate     float64
	ProjectedUtil   float64
	CompletedCycles int
	AvgPerCycle     float64
	PeakCycle       float64
	TotalTracked    float64
	TrackingSince   time.Time
}

// NewGrokTracker creates a GrokTracker.
func NewGrokTracker(store *store.Store, logger *slog.Logger) *GrokTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrokTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[int64]map[string]float64),
		lastResets: make(map[int64]map[string]time.Time),
		hasLast:    make(map[int64]bool),
	}
}

func (t *GrokTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// Process runs reset detection and cycle updates for all quotas in the snapshot.
func (t *GrokTracker) Process(snapshot *api.GrokSnapshot) error {
	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	for _, q := range snapshot.Quotas {
		if err := t.processQuota(accountID, q, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("grok tracker: %s: %w", q.Name, err)
		}
	}
	t.hasLast[accountID] = true
	return nil
}

func (t *GrokTracker) processQuota(accountID int64, q api.GrokQuota, capturedAt time.Time) error {
	quotaName := q.Name
	current := q.Utilization

	cycle, err := t.store.QueryActiveGrokResetCycle(accountID, quotaName)
	if err != nil {
		return fmt.Errorf("query active cycle: %w", err)
	}

	if cycle == nil {
		// seed new cycle
		newC := &store.GrokResetCycle{
			AccountID:       accountID,
			QuotaName:       quotaName,
			CycleStart:      capturedAt,
			PeakUtilization: current,
		}
		if _, err := t.store.InsertGrokResetCycle(newC); err != nil {
			return fmt.Errorf("create cycle: %w", err)
		}
		t.initLast(accountID, quotaName, current, q.ResetsAt)
		t.logger.Info("Created new Grok credits cycle", "quota", quotaName, "initial", current)
		return nil
	}

	resetDetected := false
	if t.hasLastFor(accountID, quotaName) {
		last := t.lastValues[accountID][quotaName]
		if last > 0 && current < last*0.5 {
			resetDetected = true
		}
	}

	if resetDetected {
		// close previous
		if err := t.store.UpdateGrokResetCycleEnd(cycle.ID, capturedAt, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("close cycle: %w", err)
		}
		// new
		newC := &store.GrokResetCycle{
			AccountID:       accountID,
			QuotaName:       quotaName,
			CycleStart:      capturedAt,
			PeakUtilization: current,
		}
		if _, err := t.store.InsertGrokResetCycle(newC); err != nil {
			return fmt.Errorf("create after reset: %w", err)
		}
		t.initLast(accountID, quotaName, current, q.ResetsAt)
		t.logger.Info("Detected Grok credits reset", "quota", quotaName, "prevPeak", cycle.PeakUtilization)
		if t.onReset != nil {
			t.onReset(quotaName)
		}
		return nil
	}

	// ongoing cycle
	if t.hasLastFor(accountID, quotaName) {
		last := t.lastValues[accountID][quotaName]
		delta := current - last
		if delta > 0 {
			cycle.TotalDelta += delta
		}
		if current > cycle.PeakUtilization {
			cycle.PeakUtilization = current
		}
		// Note: our store currently only has Update on close; for ongoing we just keep in memory.
		// On next reset the final peak/delta will be written. For live rate we use in-mem.
	} else {
		if current > cycle.PeakUtilization {
			cycle.PeakUtilization = current
		}
	}

	t.setLast(accountID, quotaName, current, q.ResetsAt)
	return nil
}

func (t *GrokTracker) initLast(account int64, quota string, val float64, resets *time.Time) {
	if t.lastValues[account] == nil {
		t.lastValues[account] = map[string]float64{}
	}
	t.lastValues[account][quota] = val
	if resets != nil {
		if t.lastResets[account] == nil {
			t.lastResets[account] = map[string]time.Time{}
		}
		t.lastResets[account][quota] = *resets
	}
}

func (t *GrokTracker) setLast(account int64, quota string, val float64, resets *time.Time) {
	if t.lastValues[account] == nil {
		t.lastValues[account] = map[string]float64{}
	}
	t.lastValues[account][quota] = val
	if resets != nil {
		if t.lastResets[account] == nil {
			t.lastResets[account] = map[string]time.Time{}
		}
		t.lastResets[account][quota] = *resets
	}
}

func (t *GrokTracker) hasLastFor(account int64, quota string) bool {
	if !t.hasLast[account] {
		return false
	}
	m, ok := t.lastValues[account]
	return ok && m != nil
}

// GetGrokSummary computes live stats for the quota using latest snapshot + cycles.
func (t *GrokTracker) GetGrokSummary(accountID int64, quotaName string, latest *api.GrokSnapshot) *GrokSummary {
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	sum := &GrokSummary{QuotaName: quotaName, TrackingSince: time.Now()}
	if latest == nil || len(latest.Quotas) == 0 {
		return sum
	}
	var q api.GrokQuota
	for _, qq := range latest.Quotas {
		if qq.Name == quotaName {
			q = qq
			break
		}
	}
	sum.CurrentUtil = q.Utilization
	sum.ResetsAt = q.ResetsAt
	if q.ResetsAt != nil {
		sum.TimeUntilReset = time.Until(*q.ResetsAt)
	}

	// Rate / projected (simple, using last seen if available)
	if m, ok := t.lastValues[accountID]; ok {
		if last, ok := m[quotaName]; ok && last > 0 {
			// very rough: assume we have some history; for full use cycles
			sum.CurrentRate = (q.Utilization - last) // per poll, caller scales
		}
	}
	if sum.ResetsAt != nil && sum.TimeUntilReset > 0 && sum.CurrentRate > 0 {
		hours := sum.TimeUntilReset.Hours()
		if hours > 0 {
			sum.ProjectedUtil = q.Utilization + (sum.CurrentRate * hours)
		}
	}

	// Cycle stats (best effort from store)
	if cycles, err := t.store.QueryGrokCyclesForQuota(accountID, quotaName, 50); err == nil {
		sum.CompletedCycles = len(cycles)
		for _, c := range cycles {
			sum.TotalTracked += c.TotalDelta
			if c.PeakUtilization > sum.PeakCycle {
				sum.PeakCycle = c.PeakUtilization
			}
		}
		if len(cycles) > 0 {
			sum.AvgPerCycle = sum.TotalTracked / float64(len(cycles))
		}
	}
	return sum
}

// (cycle query lives in internal/store/grok_store.go as (*Store).QueryGrokCyclesForQuota)
