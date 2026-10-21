package menubar

import (
	"strings"
	"time"
)

// This file holds the refresh-fallback logic with no build tag so it is
// compiled and tested by the default `go test ./...` build (the companion
// itself is behind the `menubar` tag).

// nextTraySnapshot decides what a refresh should display. A successful fetch
// replaces the cache. A failed fetch (an error, or a nil snapshot) reuses the
// last good snapshot and reports stale=true, so the caller keeps the numbers
// and only marks the tooltip/status. Only the very first refresh, with nothing
// cached yet, returns nil.
func nextTraySnapshot(cached, fetched *Snapshot, err error) (*Snapshot, bool) {
	if err == nil && fetched != nil {
		return fetched, false
	}
	if cached != nil {
		return cached, true
	}
	return nil, false
}

// trayPlan is the outcome of one refresh as the companion should apply it. It
// is a pure value so the controller's decisions are testable without systray.
type trayPlan struct {
	// Snapshot is the snapshot to display, nil only before any success.
	Snapshot *Snapshot
	// Settings are the preferences the visual was built from. Nil when the
	// visual came from the cached last-good state rather than this run.
	Settings *Settings
	// Visual is what to hand the platform layer.
	Visual trayVisual
	// Tooltip is the hover text.
	Tooltip string
	// MenuLines are the tray menu rows.
	MenuLines []MenuLine
	// Stale is true when the fetch failed and cached data is being shown.
	Stale bool
	// Offline is true when nothing has ever been fetched.
	Offline bool
	// FreshSettings is true when this run fetched preferences successfully and
	// they should replace the cached ones.
	FreshSettings bool
}

// planTrayRefresh turns a snapshot refresh plus the cached preferences and
// last-good visual into the plan the companion applies. It is the fix for the
// outage regression: a failed refresh keeps the last-good title, badge text
// and display mode, and changes only the status/icon tint and the tooltip. Only
// the very first refresh, with nothing ever fetched, blanks the tray.
//
// freshSettings is this run's preferences (nil when the preferences fetch
// failed). cachedSettings and lastVisual are the last successfully applied
// values. age is the stale age to show, tooltipLimit caps the tooltip (0 = no
// cap; the platform passes its own trayTooltipLimit).
func planTrayRefresh(snapshot *Snapshot, stale bool, age time.Duration, freshSettings *Settings, cachedSettings *Settings, lastVisual trayVisual, tooltipLimit int) trayPlan {
	return planTrayRefreshAt(snapshot, stale, age, freshSettings, cachedSettings, lastVisual, tooltipLimit, time.Now())
}

// planTrayRefreshAt is planTrayRefresh with an explicit clock, so staleness
// re-evaluation is deterministic in tests.
func planTrayRefreshAt(snapshot *Snapshot, stale bool, age time.Duration, freshSettings *Settings, cachedSettings *Settings, lastVisual trayVisual, tooltipLimit int, now time.Time) trayPlan {
	if snapshot == nil {
		// Only reached before any snapshot has ever succeeded.
		return trayPlan{
			Tooltip: TrayTooltipText(nil, tooltipLimit),
			Offline: true,
			Visual:  trayVisual{Title: "--", Text: "-", Status: trayStatusOffline},
		}
	}

	settings := freshSettings
	if settings == nil {
		// Preferences failed. Fall back to the last good preferences so the
		// title, badge and display mode survive a daemon outage. DefaultSettings
		// is only correct before preferences have ever been fetched.
		settings = cachedSettings
		if settings == nil {
			settings = DefaultSettings()
		}
	}
	segments := TraySegments(snapshot, settings)
	visual := trayVisual{
		Title:    TrayTitle(snapshot, settings),
		Text:     TrayIconText(segments, true),
		Status:   TrayBadgeStatus(snapshot, segments),
		Online:   true,
		IconOnly: settings.StatusDisplay.Mode == StatusDisplayIconOnly,
		// Staleness is decided per card from honest capture times, so the
		// title dims while the daemon is up but one provider stopped polling.
		Dim: TrayTitleDimmed(snapshot, settings, now),
	}
	tooltip := TrayTooltipText(snapshot, tooltipLimit)
	staleLines := StaleProviderTooltipLines(snapshot, now)
	if stale {
		// Numbers stay; only the status/icon tint and the tooltip change. If a
		// last-good visual exists and this run could not build one (no cached
		// settings and the preferences fetch failed), keep it rather than
		// blanking the tray.
		visual.Status = trayStatusOffline
		if lastVisual.Online && visual.Title == "" && visual.Text == "" {
			visual.Title = lastVisual.Title
			visual.Text = lastVisual.Text
			visual.IconOnly = lastVisual.IconOnly
		}
		tooltip = TrayTooltipStaleText(snapshot, tooltipLimit, age)
	} else if len(staleLines) > 0 {
		// The refresh succeeded; some providers inside it are old. Keep the
		// normal provider line and append one line per stale provider.
		tooltip = truncateTooltip(tooltip+"\n"+strings.Join(staleLines, "\n"), tooltipLimit)
	}
	return trayPlan{
		Snapshot:      snapshot,
		Settings:      settings,
		Visual:        visual,
		Tooltip:       tooltip,
		MenuLines:     BuildMenuLines(snapshot),
		Stale:         stale,
		FreshSettings: freshSettings != nil,
	}
}

// applyFunc receives the result of one refresh run: the snapshot to display
// (nil only before any success), whether it is stale, the age to show for a
// stale snapshot, and the fetch error (nil on success) for the caller to log.
type applyFunc func(snapshot *Snapshot, stale bool, age time.Duration, err error)

// refreshWorker owns the cached snapshot and runs every refresh serially on a
// single goroutine. Fetch and apply are never concurrent, so an older failure
// can never overwrite a newer success and no lock is needed around them.
//
// Requests are a non-blocking send on a buffer-1 channel. A request that
// arrives while a run is in flight leaves one token queued, so the worker runs
// exactly once more; any further requests during that run collapse into the
// same token. There is no pending flag that a descheduled caller could set
// after the worker has already decided to stop, so a request can never be
// lost.
type refreshWorker struct {
	// requests is the worker's inbox. Buffer 1 is the coalescing point: a
	// non-blocking send succeeds for the first request and is dropped for the
	// rest, which are already covered by the queued run.
	requests chan struct{}

	fetch SnapshotProvider
	apply applyFunc

	snapshot  *Snapshot
	fetchedAt time.Time

	// Test hooks. Each runs on the worker goroutine at a fixed point in a run
	// (both nil in production), so tests can publish requests at those points
	// without timing sleeps.
	testHookDuringFetch func()
	testHookDuringApply func()
}

func newRefreshWorker(fetch SnapshotProvider, apply applyFunc) *refreshWorker {
	return &refreshWorker{
		requests: make(chan struct{}, 1),
		fetch:    fetch,
		apply:    apply,
	}
}

// Request asks the worker for a refresh. It never blocks, so a click on the
// systray goroutine cannot stall behind a slow daemon. If a run is in flight
// (or already queued), the request is covered by the follow-up run and the
// send is a no-op.
func (w *refreshWorker) Request() {
	select {
	case w.requests <- struct{}{}:
	default:
	}
}

// RunWorker is the worker loop. It starts once and runs for the process
// lifetime, or until stop is closed. All fetch and apply calls happen on this
// goroutine, which is what guarantees ordering and the absence of overlap.
func (w *refreshWorker) RunWorker(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-w.requests:
			w.runOnce()
		}
	}
}

// runOnce is a single fetch+merge+apply pass. The clock is read when the run
// starts, not when the request was published, so the recorded fetch time
// reflects the data actually fetched.
func (w *refreshWorker) runOnce() {
	now := time.Now()
	if hook := w.testHookDuringFetch; hook != nil {
		hook()
	}

	fetched, err := w.fetch()
	snapshot, stale := nextTraySnapshot(w.snapshot, fetched, err)
	if snapshot == nil {
		w.apply(nil, false, 0, err)
		return
	}
	if !stale {
		w.snapshot = snapshot
		w.fetchedAt = now
	}
	if hook := w.testHookDuringApply; hook != nil {
		hook()
	}
	w.apply(snapshot, stale, staleSnapshotAge(snapshot, w.fetchedAt, now), err)
}

// staleSnapshotAge reports the age to show for a stale snapshot. It counts from
// the snapshot's newest provider data (latest_at) so a failure does not make
// "updated 2h ago" snap back to "just now". It falls back to when the snapshot
// was fetched, and returns 0 when neither is known.
func staleSnapshotAge(snapshot *Snapshot, fetchedAt, now time.Time) time.Duration {
	var from time.Time
	if snapshot != nil {
		from = snapshot.LatestAt
	}
	if from.IsZero() {
		from = fetchedAt
	}
	if from.IsZero() {
		return 0
	}
	return now.Sub(from)
}
