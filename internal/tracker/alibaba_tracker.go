package tracker

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// alibabaResetDriftTolerance bounds ResetsAt jitter before a change counts as
// a new quota window. 90 minutes matches the other percent-window providers.
const alibabaResetDriftTolerance = 90 * time.Minute

// alibabaDropThreshold is how many percentage points utilization must fall
// between two polls to count as a reset when no reset time confirms the open
// window. While both the stored and the reported reset time are known and
// agree, a small dip is treated as noise or rolling decay instead; only a
// sharp drop (alibabaSharpDrop) still counts as a reset.
const (
	alibabaDropThreshold = 2.0
	alibabaSharpDrop     = 10.0
)

// alibabaSummaryCycleLimit caps the cycles read for a usage summary so the
// dashboard request path cannot materialise every cycle ever recorded.
const alibabaSummaryCycleLimit = 200

// AlibabaTracker manages reset cycle detection for Alibaba Token Plan windows.
// One tracker serves every account; state and cycles are scoped by account id.
type AlibabaTracker struct {
	store  *store.Store
	logger *slog.Logger

	mu         sync.Mutex
	lastValues map[alibabaTrackerKey]float64
	onReset    func(quotaName string)
}

type alibabaTrackerKey struct {
	accountID int64
	quotaName string
}

// SetOnReset registers a callback invoked when a quota reset is detected.
func (t *AlibabaTracker) SetOnReset(fn func(string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onReset = fn
}

// AlibabaSummary contains computed usage statistics for one window.
type AlibabaSummary struct {
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

// NewAlibabaTracker creates a new AlibabaTracker.
func NewAlibabaTracker(store *store.Store, logger *slog.Logger) *AlibabaTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &AlibabaTracker{
		store:      store,
		logger:     logger,
		lastValues: make(map[alibabaTrackerKey]float64),
	}
}

// Process compares the snapshot quotas with previous state and maintains
// cycles for one account.
func (t *AlibabaTracker) Process(snapshot *api.AlibabaSnapshot, accountID int64) error {
	if snapshot == nil {
		return fmt.Errorf("alibaba tracker: nil snapshot")
	}
	for _, quota := range snapshot.Quotas {
		if err := t.processQuota(quota, snapshot.CapturedAt, accountID); err != nil {
			return fmt.Errorf("alibaba tracker: %s: %w", quota.Name, err)
		}
	}
	return nil
}

func (t *AlibabaTracker) processQuota(quota api.AlibabaQuota, capturedAt time.Time, accountID int64) error {
	quotaName := quota.Name
	currentUtil := quota.Utilization
	key := alibabaTrackerKey{accountID: accountID, quotaName: quotaName}

	// A reset that is not after the sample was already passed when the sample
	// was taken. Storing it would give the new cycle a reset in the past, and
	// the next poll would mistake that stale value for a fresh window.
	quota.ResetsAt = futureResetsAt(quota.ResetsAt, capturedAt)

	cycle, err := t.store.QueryActiveAlibabaCycle(quotaName, accountID)
	if err != nil {
		return fmt.Errorf("failed to query active cycle: %w", err)
	}

	t.mu.Lock()
	lastUtil, hasLast := t.lastValues[key]
	onReset := t.onReset
	t.mu.Unlock()
	setLast := func(v float64) {
		t.mu.Lock()
		t.lastValues[key] = v
		t.mu.Unlock()
	}

	if cycle == nil {
		if _, err := t.store.CreateAlibabaCycle(quotaName, capturedAt, quota.ResetsAt, accountID); err != nil {
			return fmt.Errorf("failed to create cycle: %w", err)
		}
		if err := t.store.UpdateAlibabaCycle(quotaName, currentUtil, 0, accountID); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}
		setLast(currentUtil)
		t.logger.Info("Created new Alibaba cycle",
			"account", accountID,
			"quota", quotaName,
			"resetsAt", quota.ResetsAt,
			"initialUtil", currentUtil,
		)
		return nil
	}
	if cycle.ResetsAt != nil && !cycle.ResetsAt.After(cycle.CycleStart) {
		cycle.ResetsAt = nil
	}

	resetDetected := false
	resetReason := ""
	storedResetPassed := cycle.ResetsAt != nil && capturedAt.After(cycle.ResetsAt.Add(2*time.Minute))
	currentResetIsFuture := quota.ResetsAt != nil && quota.ResetsAt.After(capturedAt)
	if storedResetPassed && !currentResetIsFuture {
		resetDetected = true
		resetReason = "time-based (stored ResetsAt passed)"
	}

	if !resetDetected && quota.ResetsAt != nil && cycle.ResetsAt != nil {
		diff := quota.ResetsAt.Sub(*cycle.ResetsAt)
		if diff < 0 {
			diff = -diff
		}
		if diff > alibabaResetDriftTolerance {
			resetDetected = true
			resetReason = "api-based (ResetsAt changed)"
		}
	}

	if !resetDetected && hasLast {
		resetTimesAgree := quota.ResetsAt != nil && cycle.ResetsAt != nil
		drop := lastUtil - currentUtil
		if drop >= alibabaSharpDrop || (!resetTimesAgree && drop > alibabaDropThreshold) {
			resetDetected = true
			resetReason = "usage-based (utilization dropped)"
		}
	}

	if !resetDetected && quota.ResetsAt != nil && cycle.ResetsAt == nil {
		// Without a reset time the window had no known boundary; attach the
		// one the API now reports to the open cycle.
		if err := t.store.UpdateAlibabaCycleResetsAt(quotaName, quota.ResetsAt, accountID); err != nil {
			return fmt.Errorf("failed to attach reset to cycle: %w", err)
		}
		cycle.ResetsAt = quota.ResetsAt
	}

	if resetDetected {
		cycleEndTime := capturedAt
		if cycle.ResetsAt != nil && capturedAt.After(*cycle.ResetsAt) {
			cycleEndTime = *cycle.ResetsAt
		}
		if cycleEndTime.Before(cycle.CycleStart) {
			cycleEndTime = cycle.CycleStart
		}

		// The sample that revealed the reset belongs to the new cycle.
		if err := t.store.CloseAlibabaCycle(quotaName, cycleEndTime, cycle.PeakUtilization, cycle.TotalDelta, accountID); err != nil {
			return fmt.Errorf("failed to close cycle: %w", err)
		}
		newStart, initialUtil := capturedAt, currentUtil
		if storedResetPassed && !currentResetIsFuture {
			// Do not assign a possibly pre-reset sample to the new window.
			newStart, initialUtil = cycleEndTime, 0
		}
		if _, err := t.store.CreateAlibabaCycle(quotaName, newStart, nextWindowReset(quota.ResetsAt, capturedAt), accountID); err != nil {
			return fmt.Errorf("failed to create new cycle: %w", err)
		}
		if err := t.store.UpdateAlibabaCycle(quotaName, initialUtil, 0, accountID); err != nil {
			return fmt.Errorf("failed to set initial peak: %w", err)
		}

		setLast(currentUtil)
		t.logger.Info("Detected Alibaba quota reset",
			"account", accountID,
			"quota", quotaName,
			"reason", resetReason,
			"oldResetsAt", cycle.ResetsAt,
			"newResetsAt", quota.ResetsAt,
			"cycleEndTime", cycleEndTime,
			"totalDelta", cycle.TotalDelta,
		)
		if onReset != nil {
			onReset(quotaName)
		}
		return nil
	}

	changed := false
	if hasLast {
		if delta := currentUtil - lastUtil; delta > 0 {
			cycle.TotalDelta += delta
			changed = true
		}
	}
	if currentUtil > cycle.PeakUtilization {
		cycle.PeakUtilization = currentUtil
		changed = true
	}
	if changed {
		if err := t.store.UpdateAlibabaCycle(quotaName, cycle.PeakUtilization, cycle.TotalDelta, accountID); err != nil {
			return fmt.Errorf("failed to update cycle: %w", err)
		}
	}

	setLast(currentUtil)
	return nil
}

// UsageSummary computes statistics for one Alibaba window of an account.
func (t *AlibabaTracker) UsageSummary(quotaName string, accountID int64) (*AlibabaSummary, error) {
	activeCycle, err := t.store.QueryActiveAlibabaCycle(quotaName, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query active cycle: %w", err)
	}

	history, err := t.store.QueryAlibabaCycleHistory(quotaName, accountID, alibabaSummaryCycleLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to query cycle history: %w", err)
	}

	summary := &AlibabaSummary{
		QuotaName:       quotaName,
		CompletedCycles: len(history),
	}

	if len(history) > 0 {
		var totalDelta float64
		summary.TrackingSince = history[len(history)-1].CycleStart
		for _, cycle := range history {
			totalDelta += cycle.TotalDelta
			if cycle.PeakUtilization > summary.PeakCycle {
				summary.PeakCycle = cycle.PeakUtilization
			}
		}
		summary.AvgPerCycle = totalDelta / float64(len(history))
		summary.TotalTracked = totalDelta
	}

	if activeCycle == nil {
		return summary, nil
	}

	if summary.TrackingSince.IsZero() {
		summary.TrackingSince = activeCycle.CycleStart
	}
	summary.TotalTracked += activeCycle.TotalDelta
	if activeCycle.PeakUtilization > summary.PeakCycle {
		summary.PeakCycle = activeCycle.PeakUtilization
	}
	if activeCycle.ResetsAt != nil {
		summary.ResetsAt = activeCycle.ResetsAt
		summary.TimeUntilReset = time.Until(*activeCycle.ResetsAt)
	}

	latest, err := t.store.QueryLatestAlibaba(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest: %w", err)
	}
	if latest == nil {
		return summary, nil
	}

	for _, q := range latest.Quotas {
		if q.Name == quotaName {
			summary.CurrentUtil = q.Utilization
			if summary.ResetsAt == nil && q.ResetsAt != nil {
				summary.ResetsAt = q.ResetsAt
				summary.TimeUntilReset = time.Until(*q.ResetsAt)
			}
			break
		}
	}

	// A burn-rate projection needs a window long enough for the delta to be
	// meaningful; a few minutes of data would project wildly.
	elapsed := time.Since(activeCycle.CycleStart)
	if elapsed.Minutes() >= 30 && activeCycle.TotalDelta > 0 {
		summary.CurrentRate = activeCycle.TotalDelta / elapsed.Hours()
		if summary.ResetsAt != nil {
			if hoursLeft := time.Until(*summary.ResetsAt).Hours(); hoursLeft > 0 {
				projected := summary.CurrentUtil + (summary.CurrentRate * hoursLeft)
				if projected > 100 {
					projected = 100
				}
				summary.ProjectedUtil = projected
			}
		}
	}

	return summary, nil
}
