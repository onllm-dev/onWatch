package tracker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// KimiTracker manages reset cycle detection for Kimi Code quotas.
type KimiTracker struct {
	store      *store.Store
	logger     *slog.Logger
	lastValues map[int64]map[string]float64
	lastResets map[int64]map[string]time.Time
	hasLast    map[int64]bool
	onReset    func(quotaName string)
}

// DefaultKimiAccountID is the default account for single-user Kimi Code.
const DefaultKimiAccountID int64 = 1

// KimiSummary holds computed stats for a Kimi quota.
type KimiSummary struct {
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

// NewKimiTracker creates a KimiTracker.
func NewKimiTracker(store *store.Store, logger *slog.Logger) *KimiTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &KimiTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[int64]map[string]float64),
		lastResets: make(map[int64]map[string]time.Time),
		hasLast:    make(map[int64]bool),
	}
}

// SetOnReset registers a callback when a quota reset is detected.
func (t *KimiTracker) SetOnReset(fn func(string)) {
	t.onReset = fn
}

// Process runs reset detection for all quotas in the snapshot.
func (t *KimiTracker) Process(snapshot *api.KimiSnapshot) error {
	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	for _, q := range snapshot.Quotas {
		if err := t.processQuota(accountID, q, snapshot.CapturedAt); err != nil {
			return fmt.Errorf("kimi tracker: %s: %w", q.Name, err)
		}
	}
	t.hasLast[accountID] = true
	return nil
}

func (t *KimiTracker) processQuota(accountID int64, q api.KimiQuota, capturedAt time.Time) error {
	quotaName := q.Name
	current := q.Utilization

	cycle, err := t.store.QueryActiveKimiResetCycle(accountID, quotaName)
	if err != nil {
		return fmt.Errorf("query active cycle: %w", err)
	}

	if cycle == nil {
		newC := &store.KimiResetCycle{
			AccountID:       accountID,
			QuotaName:       quotaName,
			CycleStart:      capturedAt,
			PeakUtilization: current,
			ResetsAt:        q.ResetsAt,
		}
		if _, err := t.store.InsertKimiResetCycle(newC); err != nil {
			return fmt.Errorf("create cycle: %w", err)
		}
		t.initLast(accountID, quotaName, current, q.ResetsAt)
		t.logger.Info("Created new Kimi cycle", "quota", quotaName, "initial", current)
		return nil
	}

	resetDetected := false
	if t.hasLastFor(accountID, quotaName) {
		last := t.lastValues[accountID][quotaName]
		// large drop in utilization ⇒ new billing/reset window
		if last > 0 && current < last*0.5 {
			resetDetected = true
		}
	}
	// also detect when resetTime jumps forward relative to last known
	if !resetDetected && q.ResetsAt != nil {
		if m := t.lastResets[accountID]; m != nil {
			if prev, ok := m[quotaName]; ok && q.ResetsAt.After(prev.Add(time.Minute)) {
				// only if utilization also dropped or is near zero
				if current < 20 || (t.hasLastFor(accountID, quotaName) && current < t.lastValues[accountID][quotaName]) {
					resetDetected = true
				}
			}
		}
	}

	if resetDetected {
		if err := t.store.UpdateKimiResetCycleEnd(cycle.ID, capturedAt, cycle.PeakUtilization, cycle.TotalDelta); err != nil {
			return fmt.Errorf("close cycle: %w", err)
		}
		newC := &store.KimiResetCycle{
			AccountID:       accountID,
			QuotaName:       quotaName,
			CycleStart:      capturedAt,
			PeakUtilization: current,
			ResetsAt:        q.ResetsAt,
		}
		if _, err := t.store.InsertKimiResetCycle(newC); err != nil {
			return fmt.Errorf("create after reset: %w", err)
		}
		t.initLast(accountID, quotaName, current, q.ResetsAt)
		t.logger.Info("Detected Kimi quota reset", "quota", quotaName, "prevPeak", cycle.PeakUtilization)
		if t.onReset != nil {
			t.onReset(quotaName)
		}
		return nil
	}

	if t.hasLastFor(accountID, quotaName) {
		last := t.lastValues[accountID][quotaName]
		delta := current - last
		if delta > 0 {
			cycle.TotalDelta += delta
		}
		if current > cycle.PeakUtilization {
			cycle.PeakUtilization = current
		}
	} else if current > cycle.PeakUtilization {
		cycle.PeakUtilization = current
	}

	t.setLast(accountID, quotaName, current, q.ResetsAt)
	return nil
}

func (t *KimiTracker) initLast(account int64, quota string, val float64, resets *time.Time) {
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

func (t *KimiTracker) setLast(account int64, quota string, val float64, resets *time.Time) {
	t.initLast(account, quota, val, resets)
}

func (t *KimiTracker) hasLastFor(account int64, quota string) bool {
	if !t.hasLast[account] {
		return false
	}
	m, ok := t.lastValues[account]
	return ok && m != nil
}

// GetKimiSummary computes live stats for a quota.
func (t *KimiTracker) GetKimiSummary(accountID int64, quotaName string, latest *api.KimiSnapshot) *KimiSummary {
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	sum := &KimiSummary{QuotaName: quotaName, TrackingSince: time.Now()}
	if latest == nil || len(latest.Quotas) == 0 {
		return sum
	}
	var q api.KimiQuota
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
	if m, ok := t.lastValues[accountID]; ok {
		if last, ok := m[quotaName]; ok {
			sum.CurrentRate = q.Utilization - last
		}
	}
	if cycles, err := t.store.QueryKimiCyclesForQuota(accountID, quotaName, 50); err == nil {
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
