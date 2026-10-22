package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

// Snapshot caching for the menubar endpoints.
//
// Building a snapshot runs roughly 60-90 SQL statements (about 16 provider
// cards plus tracker usage-summary enrichment) against a pool capped at two
// connections. Under load those statements queue behind the provider agents'
// writes, and several independent consumers (the companion, the popover and
// the GNOME extension) each used to start their own build. The result was a
// 30s+ /api/menubar/summary hang while every other endpoint answered in about
// a second.
//
// The daemon now keeps the last successfully built data in memory and only ever
// runs one build at a time. Concurrent requests share the in-flight build
// (singleflight). A request with fresh cached data, and one that falls back to
// stale data after the wait budget, both answer from memory: the build captured
// the settings, provider visibility and cards the response needs, so serving a
// snapshot never queries the database on the request's own goroutine. Otherwise
// the request waits a short, bounded time for the build; if that build is still
// running the request answers from the cache and the build finishes in the
// background. The cached snapshot keeps its original generated_at/latest_at,
// so the popover's ordering guard still accepts a newer build after a cached
// response, and staleness shows only through each card's age label (which
// counts up from updated_at_time).
const (
	// menubarSnapshotFreshWindow is how long built data is served without
	// touching the database. 12s is shorter than the default 60s refresh and
	// short enough that a popover re-open after the user has been away shows
	// fresh-enough numbers, while the companion, the popover and GNOME hitting
	// within the same second share one build.
	menubarSnapshotFreshWindow = 12 * time.Second

	// menubarSnapshotWaitBudget is how long a request waits for a build once
	// the cached data is stale. On an idle database the full query set takes
	// about 0.2s, so 3s tolerates heavy contention and still leaves the client
	// far inside its 15s deadline. Past it, the stale data is served
	// immediately and the build continues in the background.
	menubarSnapshotWaitBudget = 3 * time.Second

	// menubarSnapshotNoCacheBudget bounds the wait when there is no cached
	// snapshot at all (the first request after start). Nothing can be served
	// before the first build finishes, but a wedged build must not pin the
	// request forever (nor past the client's 15s deadline); the handler
	// answers 503 so the client keeps its last data.
	menubarSnapshotNoCacheBudget = 15 * time.Second
)

// errMenubarSnapshotTimeout is returned when the first build does not finish
// inside menubarSnapshotNoCacheBudget. Handlers map it to 503.
var errMenubarSnapshotTimeout = fmt.Errorf("menubar snapshot build timed out")

// menubarBuiltData is the product of one build: the filtered snapshot the
// clients see, the unfiltered card list (menubar-hidden providers included)
// that the preferences endpoint needs, the settings the build used and the
// provider visibility map it applied. One build supplies all four, so serving
// the cached snapshot (summary, tray title, preferences) never issues another
// query: settings and visibility are read once, inside the build.
type menubarBuiltData struct {
	Snapshot     *menubar.Snapshot
	AllProviders []menubar.ProviderCard
	Settings     *menubar.Settings
	Visibility   map[string]map[string]bool
}

// menubarSnapshotBuild is one in-flight build. Every request that arrives while
// it runs waits on done instead of starting another build.
type menubarSnapshotBuild struct {
	done       chan struct{}
	generation uint64
	data       menubarBuiltData
	err        error
}

// menubarFreshWindow returns the configured freshness window. Tests set a
// negative value to make every cached snapshot stale.
func (h *Handler) menubarFreshWindow() time.Duration {
	if h.menubarFreshFor != 0 {
		return h.menubarFreshFor
	}
	return menubarSnapshotFreshWindow
}

// menubarWaitBudget returns the configured build wait budget.
func (h *Handler) menubarWaitBudget() time.Duration {
	if h.menubarWaitFor > 0 {
		return h.menubarWaitFor
	}
	return menubarSnapshotWaitBudget
}

// menubarNoCacheBudget bounds the no-cache wait. Tests shorten it.
func (h *Handler) menubarNoCacheBudget() time.Duration {
	if h.menubarNoCacheFor > 0 {
		return h.menubarNoCacheFor
	}
	return menubarSnapshotNoCacheBudget
}

func (h *Handler) menubarNowTime() time.Time {
	if h.menubarNow != nil {
		return h.menubarNow()
	}
	return time.Now()
}

// invalidateMenubarSnapshot bumps the settings generation and marks the cached
// data stale, so the next request rebuilds instead of serving configuration
// from before the write. The data itself is kept and still served as the
// fallback when the new build does not finish inside the wait budget: showing
// the previous cards with their real ages beats blanking the menubar. Only the
// next successful build replaces it. The in-flight build pointer is cleared so
// waiters can start the next build without waiting for the superseded
// goroutine; that goroutine skips its queries or discards its result because
// its generation no longer matches.
func (h *Handler) invalidateMenubarSnapshot() {
	// Every settings or provider-configuration write lands here, and the same
	// writes reshape dashboard aggregates, so they drop those cached responses.
	h.responses.invalidateAll()
	h.menubarSnapshotMu.Lock()
	h.menubarSnapshotGen++
	h.menubarSnapshotAt = time.Time{}
	h.menubarSnapshotBuild = nil
	h.menubarSnapshotMu.Unlock()
}

// markMenubarSnapshotStale forces the next request to rebuild while keeping
// the cached data as the fallback. Used by the manual refresh endpoint: a
// refresh that stalls past the wait budget must still answer with the last
// data.
func (h *Handler) markMenubarSnapshotStale() {
	h.menubarSnapshotMu.Lock()
	h.menubarSnapshotAt = time.Time{}
	h.menubarSnapshotMu.Unlock()
}

// menubarCachedData returns the cached build payload (immutable once stored),
// its build time, the current generation and any in-flight build.
func (h *Handler) menubarCachedData() (menubarBuiltData, time.Time, uint64, *menubarSnapshotBuild) {
	h.menubarSnapshotMu.Lock()
	defer h.menubarSnapshotMu.Unlock()
	return h.menubarSnapshotData, h.menubarSnapshotAt, h.menubarSnapshotGen, h.menubarSnapshotBuild
}

// menubarDataForRequest applies the cache policy and returns the built data
// (snapshot, unfiltered providers and settings). It serves fresh cached data
// without building, waits up to the budget for stale data to be rebuilt, and
// otherwise serves the cached data while the rebuild continues in the
// background.
func (h *Handler) menubarDataForRequest(ctx context.Context) (menubarBuiltData, error) {
	h.menubarLastRequest.Store(time.Now().UnixNano())
	deadline := time.Now().Add(h.menubarWaitBudget())
	noCacheDeadline := time.Now().Add(h.menubarNoCacheBudget())

	for {
		cached, cachedAt, generation, build := h.menubarCachedData()

		if cached.Snapshot != nil && h.menubarNowTime().Sub(cachedAt) < h.menubarFreshWindow() {
			return cached, nil
		}

		// Stale-while-revalidate: data that has only aged (not been invalidated
		// by a settings write or a manual refresh, which zero cachedAt) is
		// answered immediately and rebuilt in the background. Opening the
		// popover never waits on SQL; the response carries the refreshing hint
		// so the client can pick up the rebuilt snapshot a moment later. Builds
		// normally run ahead of requests anyway (see scheduleMenubarPrebuild).
		if cached.Snapshot != nil && !cachedAt.IsZero() && !h.menubarAlwaysWait {
			if build == nil || build.generation != generation {
				h.menubarSnapshotMu.Lock()
				if h.menubarSnapshotBuild == nil || h.menubarSnapshotBuild.generation != h.menubarSnapshotGen {
					h.startMenubarSnapshotBuildLocked(h.menubarSnapshotGen)
				}
				h.menubarSnapshotMu.Unlock()
			}
			return cached, nil
		}

		joined := build != nil && build.generation == generation
		if !joined {
			h.menubarSnapshotMu.Lock()
			if h.menubarSnapshotBuild != nil && h.menubarSnapshotBuild.generation == h.menubarSnapshotGen {
				build = h.menubarSnapshotBuild
				joined = true
				h.menubarSnapshotMu.Unlock()
			} else {
				build = h.startMenubarSnapshotBuildLocked(h.menubarSnapshotGen)
				h.menubarSnapshotMu.Unlock()
			}
		}
		if joined && h.menubarJoinHook != nil {
			h.menubarJoinHook()
		}

		if cached.Snapshot == nil {
			// First request after start: there is nothing to serve yet, so wait
			// for this build as the endpoints did before, but never without a
			// bound. A wedged build must not pin the request past the client
			// deadline.
			remaining := time.Until(noCacheDeadline)
			if remaining <= 0 {
				return menubarBuiltData{}, errMenubarSnapshotTimeout
			}
			timer := time.NewTimer(remaining)
			select {
			case <-build.done:
				timer.Stop()
				if h.menubarGenerationChanged(build.generation) {
					continue
				}
				if build.err != nil {
					return menubarBuiltData{}, build.err
				}
				if build.data.Snapshot == nil {
					return menubarBuiltData{}, fmt.Errorf("menubar snapshot build returned no data")
				}
				return build.data, nil
			case <-timer.C:
				return menubarBuiltData{}, errMenubarSnapshotTimeout
			case <-ctx.Done():
				timer.Stop()
				return menubarBuiltData{}, errMenubarSnapshotTimeout
			}
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return cached, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-build.done:
			timer.Stop()
			if h.menubarGenerationChanged(build.generation) {
				continue
			}
			if build.err == nil && build.data.Snapshot != nil {
				return build.data, nil
			}
			// The build failed (for example the settings read is broken):
			// serve the last good data instead of a 500.
			return cached, nil
		case <-timer.C:
			// Still building. Serve what we have; the build finishes in the
			// background and updates the cache when it does.
			return cached, nil
		case <-ctx.Done():
			timer.Stop()
			return cached, nil
		}
	}
}

// menubarSnapshotForRequest is the summary/tray-title accessor.
func (h *Handler) menubarSnapshotForRequest(ctx context.Context) (*menubar.Snapshot, error) {
	data, err := h.menubarDataForRequest(ctx)
	if err != nil {
		return nil, err
	}
	return h.menubarStaleData(data).Snapshot, nil
}

// menubarGenerationChanged reports whether the snapshot generation moved past
// the given build's generation (a settings write invalidated its result).
func (h *Handler) menubarGenerationChanged(generation uint64) bool {
	h.menubarSnapshotMu.Lock()
	defer h.menubarSnapshotMu.Unlock()
	return h.menubarSnapshotGen != generation
}

// startMenubarSnapshotBuildLocked starts a build. The caller must hold
// menubarSnapshotMu. The builder runs in its own goroutine on a background
// context, so a cancelled HTTP request cannot kill a build other requests are
// waiting on. menubarBuildMu serialises builds, keeping at most one set of
// snapshot queries in flight at a time.
//
// A build that is already superseded when it reaches menubarBuildMu skips its
// queries entirely: its generation no longer matches the settings it would
// read. Every waiter on it notices the generation change and joins or starts
// the current build, so a burst of invalidations produces at most one extra
// build rather than one per invalidation.
func (h *Handler) startMenubarSnapshotBuildLocked(generation uint64) *menubarSnapshotBuild {
	build := &menubarSnapshotBuild{done: make(chan struct{}), generation: generation}
	h.menubarSnapshotBuild = build
	if h.menubarBuildRegisteredHook != nil {
		h.menubarBuildRegisteredHook(generation)
	}

	go func() {
		var (
			built menubarBuiltData
			err   error
		)
		h.menubarBuildMu.Lock()
		currentGeneration := h.menubarCurrentGeneration()
		ran := currentGeneration == build.generation
		if h.menubarBuildAcquiredHook != nil {
			h.menubarBuildAcquiredHook(build.generation, ran)
		}
		if ran {
			if h.menubarBuildHook != nil {
				built, err = h.menubarBuildHook(context.Background())
			} else {
				built, err = h.buildMenubarSnapshotWithAllProviders()
			}
		}
		h.menubarBuildMu.Unlock()

		h.menubarSnapshotMu.Lock()
		if h.menubarSnapshotBuild == build {
			h.menubarSnapshotBuild = nil
		}
		// Only a build whose generation still matches may be stored as fresh.
		// A superseded build may have run its queries against the old settings
		// (a just-disabled provider, an old theme); storing it with a fresh
		// timestamp would serve that state for the whole freshness window.
		// Its result is dropped rather than kept as a fallback, because the
		// previously cached data is already the better fallback.
		if err == nil && built.Snapshot != nil && h.menubarSnapshotGen == build.generation {
			h.menubarSnapshotData = built
			h.menubarSnapshotAt = h.menubarNowTime()
		}
		h.menubarSnapshotMu.Unlock()

		build.data = built
		build.err = err
		if h.menubarAfterBuildHook != nil {
			h.menubarAfterBuildHook(built.Snapshot, err)
		}
		close(build.done)
	}()

	return build
}

// menubarCurrentGeneration reads the settings generation under the snapshot
// mutex.
func (h *Handler) menubarCurrentGeneration() uint64 {
	h.menubarSnapshotMu.Lock()
	defer h.menubarSnapshotMu.Unlock()
	return h.menubarSnapshotGen
}

// recordMenubarLastSettings keeps the last settings read that succeeded, so a
// later settings read failure can still build a snapshot (gap A) instead of
// answering 500 while a usable configuration is in memory.
func (h *Handler) recordMenubarLastSettings(settings *menubar.Settings) {
	if settings == nil {
		return
	}
	normalized := settings.Normalize()
	h.menubarLastSettingsMu.Lock()
	h.menubarLastSettings = normalized
	h.menubarLastSettingsMu.Unlock()
}

func (h *Handler) menubarLastSettingsSnapshot() *menubar.Settings {
	h.menubarLastSettingsMu.Lock()
	defer h.menubarLastSettingsMu.Unlock()
	return h.menubarLastSettings
}

// menubarServeSettings picks the settings a cached response formats with: the
// ones the build captured, then the last settings read successfully, then
// defaults. No database access, so a stale snapshot can always be served.
func (h *Handler) menubarServeSettings(data menubarBuiltData) *menubar.Settings {
	if data.Settings != nil {
		return data.Settings.Normalize()
	}
	if last := h.menubarLastSettingsSnapshot(); last != nil {
		return last
	}
	return menubar.DefaultSettings()
}

const (
	// menubarPrebuildDebounce coalesces the poll completions of several
	// providers into one build. Agents publish success just before inserting
	// the snapshot, so the delay also lets that write land first.
	menubarPrebuildDebounce = 2 * time.Second

	// menubarPrebuildActiveWindow limits background builds to daemons whose
	// menubar is actually in use: a menubar client (tray, popover, GNOME,
	// VS Code) has asked for a snapshot within this window. Headless and
	// dashboard-only installs never pay for menubar builds.
	menubarPrebuildActiveWindow = 10 * time.Minute
)

// scheduleMenubarPrebuild rebuilds the snapshot shortly after new provider
// data arrives, so menubar requests are answered from memory with current
// numbers instead of computing on demand. Calls within the debounce window
// share one build.
func (h *Handler) scheduleMenubarPrebuild() {
	last := h.menubarLastRequest.Load()
	if last == 0 || time.Since(time.Unix(0, last)) > menubarPrebuildActiveWindow {
		return
	}
	if !h.menubarPrebuildPending.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(menubarPrebuildDebounce, func() {
		h.menubarPrebuildPending.Store(false)
		h.menubarSnapshotMu.Lock()
		if h.menubarSnapshotBuild == nil || h.menubarSnapshotBuild.generation != h.menubarSnapshotGen {
			h.startMenubarSnapshotBuildLocked(h.menubarSnapshotGen)
		}
		h.menubarSnapshotMu.Unlock()
	})
}

// menubarRefreshing reports whether a build is in flight, so a response
// served from the cache can tell the client a newer snapshot is coming.
func (h *Handler) menubarRefreshing() bool {
	h.menubarSnapshotMu.Lock()
	defer h.menubarSnapshotMu.Unlock()
	return h.menubarSnapshotBuild != nil
}

// setMenubarRefreshingHeader marks a cached response whose rebuild is still
// running. Clients that understand it re-fetch once shortly after; others
// ignore it.
func (h *Handler) setMenubarRefreshingHeader(w http.ResponseWriter) {
	if h.menubarRefreshing() {
		w.Header().Set("X-Onwatch-Refreshing", "1")
	}
}
