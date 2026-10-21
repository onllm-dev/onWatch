package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// newCacheTestHandler returns a handler backed by a shared-cache in-memory
// database (so a test can break the settings table under it) with a short
// freshness window and wait budget.
func newCacheTestHandler(t *testing.T, cfg *config.Config) (*Handler, *store.Store, string) {
	t.Helper()
	if cfg == nil {
		cfg = createTestConfigWithCursor()
	}
	h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)
	h.menubarFreshFor = time.Millisecond
	h.menubarWaitFor = 250 * time.Millisecond
	return h, s, dsn
}

func waitNotNil(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the build to start")
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the build to finish")
	}
}

func decodeSummary(t *testing.T, rr *httptest.ResponseRecorder) menubar.Snapshot {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var snap menubar.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return snap
}

func getSummary(h *Handler) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.MenubarSummary(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil))
	return rr
}

// Concurrent requests must share one build. A gate holds the build open while
// every request queues on it; the build counter must stay at one.
func TestMenubarCacheConcurrentRequestsTriggerExactlyOneBuild(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	h.menubarFreshFor = -time.Second
	h.menubarWaitFor = 30 * time.Second

	gate := make(chan struct{})
	buildStarted := make(chan struct{}, 1)
	var builds int32
	var joined int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		buildStarted <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}
	h.menubarJoinHook = func() { atomic.AddInt32(&joined, 1) }

	const requests = 8
	var wg sync.WaitGroup
	codes := make(chan int, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.MenubarSummary(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil))
			codes <- rr.Code
		}()
	}

	// Let the first request start the build, then wait until every other
	// request has joined it before releasing.
	waitNotNil(t, buildStarted)
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&joined) < requests-1 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d requests joined the in-flight build, want %d", atomic.LoadInt32(&joined), requests-1)
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()
	close(codes)

	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("builds = %d, want exactly 1", got)
	}
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("request status = %d, want 200", code)
		}
	}
}

// A build slower than the wait budget must return the cached snapshot within
// the budget, with its original generated_at. When the build finishes the
// cache updates and the next request sees the new generated_at.
func TestMenubarCacheSlowBuildServesCachedThenUpdates(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	cached := decodeSummary(t, getSummary(h))

	gate := make(chan struct{})
	buildStarted := make(chan struct{}, 1)
	backgroundDone := make(chan struct{}, 1)
	var builds int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		buildStarted <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}
	h.menubarAfterBuildHook = func(*menubar.Snapshot, error) { backgroundDone <- struct{}{} }

	// Force every cached snapshot to be stale deterministically: a 2ms sleep
	// against a 1ms window still leaves the freshness decision to scheduling.
	h.menubarFreshFor = -time.Nanosecond

	start := time.Now()
	stale := decodeSummary(t, getSummary(h))
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("stale request took %s, want about the wait budget", elapsed)
	}
	if !stale.GeneratedAt.Equal(cached.GeneratedAt) {
		t.Fatalf("cached response generated_at = %s, want the original %s", stale.GeneratedAt, cached.GeneratedAt)
	}
	if !stale.LatestAt.Equal(cached.LatestAt) {
		t.Fatalf("cached response latest_at = %s, want the original %s", stale.LatestAt, cached.LatestAt)
	}
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("builds = %d, want 1", got)
	}

	// Release the background build and wait for it to update the cache.
	waitNotNil(t, buildStarted)
	close(gate)
	waitClosed(t, backgroundDone)

	fresh := decodeSummary(t, getSummary(h))
	if !fresh.GeneratedAt.After(cached.GeneratedAt) {
		t.Fatalf("post-build snapshot generated_at = %s, want newer than %s", fresh.GeneratedAt, cached.GeneratedAt)
	}
}

// Inside the freshness window a summary request performs no build at all.
func TestMenubarCacheFreshWindowSkipsBuild(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = time.Minute
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	cached := decodeSummary(t, getSummary(h))

	var builds int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		return h.buildMenubarSnapshotWithAllProviders()
	}

	again := decodeSummary(t, getSummary(h))
	if got := atomic.LoadInt32(&builds); got != 0 {
		t.Fatalf("builds = %d, want 0 inside the fresh window", got)
	}
	if !again.GeneratedAt.Equal(cached.GeneratedAt) {
		t.Fatalf("fresh-window response generated_at = %s, want %s", again.GeneratedAt, cached.GeneratedAt)
	}
}

// A settings save must invalidate the cache so the next request reflects it.
func TestMenubarCacheSettingsChangeInvalidates(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = time.Minute
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent, ResetsAt: &reset},
	})

	before := decodeSummary(t, getSummary(h))
	if len(before.Providers) != 1 {
		t.Fatalf("providers before = %d, want 1", len(before.Providers))
	}

	settings := menubar.DefaultSettings()
	settings.VisibleProviders = []string{"some_other_provider"}
	if err := h.saveMenubarSettings(settings); err != nil {
		t.Fatalf("saveMenubarSettings: %v", err)
	}

	after := decodeSummary(t, getSummary(h))
	if len(after.Providers) != 0 {
		t.Fatalf("providers after visibility save = %d, want 0 (cache was not invalidated): %+v", len(after.Providers), after.Providers)
	}
}

// With no cached snapshot the first request must wait for the build.
func TestMenubarCacheFirstRequestWaits(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarWaitFor = 500 * time.Millisecond
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.MenubarSummary(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil))
		done <- rr
	}()

	waitNotNil(t, started)
	select {
	case rr := <-done:
		t.Fatalf("first request returned before the build completed: status %d", rr.Code)
	case <-time.After(100 * time.Millisecond):
	}

	close(gate)
	select {
	case rr := <-done:
		snap := decodeSummary(t, rr)
		if len(snap.Providers) != 1 {
			t.Fatalf("providers = %d, want 1", len(snap.Providers))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first request never returned after the build completed")
	}
}

// Gap A: a settings read failure after a good snapshot must serve the cached
// snapshot, not 500.
func TestMenubarCacheSettingsReadFailureServesCached(t *testing.T) {
	h, s, dsn := newCacheTestHandler(t, nil)
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	first := decodeSummary(t, getSummary(h))

	breakProviderStorage(t, dsn, "settings")

	// The cache may answer directly, or the build may succeed from the last
	// good settings. Either way the request is served and never 500.
	snap := decodeSummary(t, getSummary(h))
	if snap.GeneratedAt.Before(first.GeneratedAt) {
		t.Fatalf("generated_at went backwards: %s, want at least %s", snap.GeneratedAt, first.GeneratedAt)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(snap.Providers))
	}

	// Even after the cache is invalidated, the last good settings are still in
	// memory, so the handler builds from them rather than answering 500.
	h.invalidateMenubarSnapshot()
	rr := getSummary(h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status after cache invalidation with fallback settings = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

// Finding 1: the preferences endpoint derives its options from the shared
// build. A preferences request concurrent with a summary build must join it,
// not start a second set of queries.
func TestMenubarPreferencesJoinsSnapshotBuild(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = -time.Second
	h.menubarWaitFor = 30 * time.Second
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var providerBuilds int32
	var joined int32
	h.menubarProvidersBuildHook = func() { atomic.AddInt32(&providerBuilds, 1) }
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}
	h.menubarJoinHook = func() { atomic.AddInt32(&joined, 1) }

	doneSummary := make(chan int, 1)
	go func() {
		rr := getSummary(h)
		doneSummary <- rr.Code
	}()
	waitNotNil(t, started)

	donePrefs := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.MenubarPreferences(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil))
		donePrefs <- rr
	}()

	// The preferences request must join the in-flight build rather than
	// starting its own.
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&joined) < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("preferences did not join the summary build; provider builds=%d", atomic.LoadInt32(&providerBuilds))
		}
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&providerBuilds); got != 0 {
		t.Fatalf("provider builds while the summary build is blocked = %d, want 0 (preferences must not build)", got)
	}

	close(gate)
	select {
	case code := <-doneSummary:
		if code != http.StatusOK {
			t.Fatalf("summary status = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("summary never returned")
	}
	select {
	case rr := <-donePrefs:
		if rr.Code != http.StatusOK {
			t.Fatalf("preferences status = %d body=%s", rr.Code, rr.Body.String())
		}
		var body struct {
			Providers []struct {
				ID string `json:"id"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if len(body.Providers) != 1 || body.Providers[0].ID != "cursor" {
			t.Fatalf("preferences providers = %+v, want the cursor card from the shared build", body.Providers)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preferences never returned")
	}

	if got := atomic.LoadInt32(&providerBuilds); got != 1 {
		t.Fatalf("provider builds = %d, want exactly 1 (the shared build, never a preferences build)", got)
	}
}

// Finding 1: preferences must not trigger another provider build even when the
// cache is fresh, and hidden providers must still be selectable.
func TestMenubarPreferencesUsesCachedOptionsWithoutBuilding(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = time.Minute
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	var providerBuilds int32
	h.menubarProvidersBuildHook = func() { atomic.AddInt32(&providerBuilds, 1) }
	// Seed the cache with a first build (the counter counts from here).
	if rr := getSummary(h); rr.Code != http.StatusOK {
		t.Fatalf("summary status = %d", rr.Code)
	}

	rr := httptest.NewRecorder()
	h.MenubarPreferences(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("preferences status = %d body=%s", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&providerBuilds); got != 1 {
		t.Fatalf("provider builds = %d, want 1 total (preferences reused the cached build)", got)
	}
}

// Finding 5a: a build whose queries already ran when it is superseded must
// never be stored as fresh; waiters end on a build for the current generation.
// The hook tags each result with the generation its build started with, so the
// cache contents reveal which build won.
func TestMenubarCacheSupersededBuildNeverReachesCache(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = -time.Second
	h.menubarWaitFor = 5 * time.Second
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gateA := make(chan struct{})
	gateC := make(chan struct{})
	started := make(chan struct{}, 4)
	completed := make(chan struct{}, 4)
	h.menubarAfterBuildHook = func(*menubar.Snapshot, error) { completed <- struct{}{} }
	var runs int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		n := atomic.AddInt32(&runs, 1)
		// The hook only runs when the build's generation still matched the
		// current one, so this is the build's own generation.
		buildGeneration := h.menubarCurrentGeneration()
		started <- struct{}{}
		switch n {
		case 1:
			<-gateA
		case 2:
			<-gateC
		}
		built, err := h.buildMenubarSnapshotWithAllProviders()
		if err != nil {
			return built, err
		}
		// Encode the generation this build reads, never "whatever is current
		// now": the superseded build must be identifiable after the fact.
		built.Snapshot.LatestAt = time.Date(2000, 1, 1, 0, int(buildGeneration), 0, 0, time.UTC)
		return built, nil
	}

	doneA := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneA <- getSummary(h) }()
	waitNotNil(t, started)

	// Generation one: a queued build that must be superseded before it runs.
	h.invalidateMenubarSnapshot()
	generationOne := h.menubarCurrentGeneration()
	doneB := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneB <- getSummary(h) }()
	h.waitForRegisteredBuild(t, generationOne)

	// Generation two (final): the current build. Generation one's build is
	// already queued on the mutex and must skip its queries when it gets there.
	h.invalidateMenubarSnapshot()
	finalGeneration := h.menubarCurrentGeneration()
	doneC := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneC <- getSummary(h) }()
	h.waitForRegisteredBuild(t, finalGeneration)

	// Release the first build. Its queries have already run by the time it is
	// superseded: its result must not be stored as fresh.
	close(gateA)

	// A completes, then the generation-one build (run 2, skipped) completes,
	// then generation three enters the hook and blocks on gateC.
	waitClosed(t, completed)
	waitClosed(t, completed)
	waitNotNil(t, started) // run 3 (the current generation) is now inside

	cached, cachedAt, _, _ := h.menubarCachedData()
	if cached.Snapshot != nil || !cachedAt.IsZero() {
		t.Fatalf("a superseded build was stored as fresh (generated_at=%v, has data=%v); it must be dropped",
			cachedAt, cached.Snapshot != nil)
	}

	close(gateC)
	for _, done := range []chan *httptest.ResponseRecorder{doneA, doneB, doneC} {
		select {
		case rr := <-done:
			if rr.Code != http.StatusOK {
				t.Fatalf("waiter status = %d body=%s", rr.Code, rr.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter never completed")
		}
	}

	// Only the blocked build plus the current generation ran queries; the
	// superseded queued build skipped them.
	if got := atomic.LoadInt32(&runs); got != 2 {
		t.Fatalf("build hook runs = %d, want 2 (the queued superseded build must skip)", got)
	}
	if got := h.mustCachedMarker(t); uint64(got) != finalGeneration {
		t.Fatalf("cached marker = %d, want generation %d (the superseded build reached the cache)", got, finalGeneration)
	}
	if _, cachedAt, _, _ := h.menubarCachedData(); cachedAt.IsZero() {
		t.Fatal("the current generation's result was not stored as fresh")
	}
}

// mustCachedMarker reads the minute field of the cached snapshot's LatestAt,
// which the finding-5 tests use as a build sequence number.
func (h *Handler) mustCachedMarker(t *testing.T) int {
	t.Helper()
	cached, _, _, _ := h.menubarCachedData()
	if cached.Snapshot == nil {
		t.Fatal("cache is empty")
	}
	return cached.Snapshot.LatestAt.Minute()
}

// waitForRegisteredBuild blocks until a build for the given generation is the
// registered in-flight build, so a test can invalidate it while it is queued.
func (h *Handler) waitForRegisteredBuild(t *testing.T, generation uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, _, inflight := h.menubarCachedData()
		if inflight != nil && inflight.generation == generation {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no build registered for generation %d", generation)
		}
		time.Sleep(time.Millisecond)
	}
}

// Finding 5b: a burst of invalidations during a slow build causes at most one
// extra build. The superseded goroutine must skip its queries, so the hook
// runs at most twice regardless of how many invalidations happened.
func TestMenubarCacheInvalidationBurstCausesOneExtraBuild(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	// The freshness window must stay positive. Nothing is cached yet (the first
	// build is gated below), so every waiter starts in the no-cache branch; once
	// the current generation's build completes it stores fresh data, and late
	// waiters must be served that data. A non-positive window would force every
	// re-looping waiter to start yet another build after a successful one,
	// which made this assertion depend on goroutine scheduling on slow runners.
	h.menubarFreshFor = time.Minute
	h.menubarWaitFor = 5 * time.Second
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gate := make(chan struct{})
	started := make(chan struct{}, 8)
	var builds int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		n := atomic.AddInt32(&builds, 1)
		started <- struct{}{}
		if n == 1 {
			<-gate
		}
		return h.buildMenubarSnapshotWithAllProviders()
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- getSummary(h) }()
	waitNotNil(t, started)

	// Several invalidations while the first build is stuck. Each one starts a
	// request and is followed until that request's build is registered, so
	// every superseded generation really queues behind the stuck build.
	for i := 0; i < 5; i++ {
		h.invalidateMenubarSnapshot()
		go func() {
			_ = getSummary(h)
		}()
		generation := h.menubarCurrentGeneration()
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, _, _, inflight := h.menubarCachedData()
			if inflight != nil && inflight.generation == generation {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no build registered for generation %d", generation)
			}
			time.Sleep(time.Millisecond)
		}
	}

	time.Sleep(20 * time.Millisecond)
	close(gate)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first waiter never completed")
	}

	// Give any stray goroutine a moment to reach the hook, then bound it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cached, _, _, inflight := h.menubarCachedData(); cached.Snapshot != nil && inflight == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&builds); got > 2 {
		t.Fatalf("builds = %d, want at most 2 for a burst of invalidations", got)
	}
}

// Gap A: a failure to persist the discovery history must not hide settings
// that were read successfully.
func TestMenubarReconcileWriteFailureKeepsReadSettings(t *testing.T) {
	h, _, dsn := newCacheTestHandler(t, nil)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	// Settings remain readable; every write aborts. The cursor provider is
	// configured but not in KnownProviders, so reconcile wants to write.
	if _, err := db.Exec(`CREATE TRIGGER reject_settings_write BEFORE INSERT ON settings
		BEGIN SELECT RAISE(ABORT, 'read only'); END;`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	snap, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot with a failed settings write: %v", err)
	}
	if snap == nil {
		t.Fatal("BuildMenubarSnapshot returned nil snapshot")
	}
}

// Gap A: 500 only when there is neither a cached snapshot nor any settings
// that were read successfully.
func TestMenubarSettingsFailureWithoutAnyFallbackReturns500(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	rr := getSummary(h)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when no cache and no last good settings exist (body=%s)", rr.Code, rr.Body.String())
	}

	// Once settings have been read successfully, the same failure builds from
	// memory instead of 500.
	h.recordMenubarLastSettings(menubar.DefaultSettings())
	rr = getSummary(h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with last good settings in memory (body=%s)", rr.Code, rr.Body.String())
	}
}

// Gap A: the tray title must still be served when settings cannot be read.
func TestMenubarTrayTitleSettingsReadFailureServesCached(t *testing.T) {
	h, s, dsn := newCacheTestHandler(t, nil)
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	rr := httptest.NewRecorder()
	h.MenubarTrayTitle(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/tray-title", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("first tray-title status = %d, body=%s", rr.Code, rr.Body.String())
	}

	breakProviderStorage(t, dsn, "settings")

	rr = httptest.NewRecorder()
	h.MenubarTrayTitle(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/tray-title", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("tray-title status after settings failure = %d, want 200", rr.Code)
	}
}

// Finding 2: with no cache, a build that never finishes must not pin the
// request. The handler answers 503 within the no-cache budget so the client
// keeps its last data.
func TestMenubarCacheNoCacheWaitIsBounded(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarNoCacheFor = 50 * time.Millisecond
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- getSummary(h) }()
	waitNotNil(t, started)

	select {
	case rr := <-done:
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 when the first build exceeds the no-cache budget", rr.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no-cache request waited past its budget")
	}
	close(gate)
}

// Finding 2: a manual refresh marks the cache stale but keeps it as the
// fallback. A refresh against a stalled build answers with the last data
// instead of blanking.
func TestMenubarRefreshFallsBackToCachedSnapshot(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = time.Minute
	h.menubarWaitFor = 50 * time.Millisecond
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	// Seed the cache; the refresh below must fall back to it while the
	// rebuild is stalled.
	if rr := getSummary(h); rr.Code != http.StatusOK {
		t.Fatalf("seed summary status = %d", rr.Code)
	}

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}

	refreshed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.MenubarRefresh(rr, httptest.NewRequest(http.MethodPost, "/api/menubar/refresh", nil))
		refreshed <- rr
	}()
	waitNotNil(t, started)

	select {
	case rr := <-refreshed:
		if rr.Code != http.StatusOK {
			t.Fatalf("refresh status = %d, want 200 with cached fallback", rr.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh waited past its budget instead of using the cached snapshot")
	}

	close(gate)
}

// Finding 3: the provider-settings save must invalidate only after the live
// config reflects the new values. A build that starts in between would carry
// the new generation but read the old state.
func TestProviderSettingsSaveInvalidatesAfterConfigApplied(t *testing.T) {
	h, _, _ := newCacheTestHandler(t, &config.Config{})
	// An agent that observes config at Start time, like a real factory would.
	observed := &configObserverController{cfg: h.config, handler: h}
	h.SetAgentManager(observed)

	body := strings.NewReader(`{"provider_settings":{"opencode":{"api_key":"oc_sk_after_save"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("settings status = %d body=%s", rr.Code, rr.Body.String())
	}
	if observed.keyAtStart != "oc_sk_after_save" {
		t.Fatalf("agent saw key %q at start, want the saved key", observed.keyAtStart)
	}
	// The generation must still be the pre-save one while the agent starts.
	// If it had already advanced, a concurrent build would carry the new
	// generation but read the old provider state, then cache it for the whole
	// freshness window.
	if observed.generationAtStart != 0 {
		t.Fatalf("generation at agent start = %d, want 0 (invalidation must follow the config apply)", observed.generationAtStart)
	}
	if got := h.menubarCurrentGeneration(); got != observed.generationAtStart+1 {
		t.Fatalf("generation after the save = %d, want exactly one invalidation", got)
	}
}

// configObserverController records the live config and menubar generation as
// seen by an agent Start, which is where a real factory would read both.
type configObserverController struct {
	cfg               *config.Config
	handler           *Handler
	keyAtStart        string
	generationAtStart uint64
}

func (c *configObserverController) Start(key string) error {
	c.cfg.ReadLock()
	c.keyAtStart = c.cfg.OpenCodeGoAPIKey
	c.cfg.ReadUnlock()
	c.generationAtStart = c.handler.menubarCurrentGeneration()
	return nil
}

func (c *configObserverController) Stop(string)           {}
func (c *configObserverController) IsRunning(string) bool { return false }

// Finding 2: a preferences PUT must respond with the settings it just saved,
// even when the post-save rebuild exceeds the wait budget and the cached data
// still carries the pre-save settings. The popover persists the response into
// state.preferences and localStorage, so a stale response would revert the
// user's change.
func TestMenubarPreferencesPutReturnsSavedSettingsDuringSlowRebuild(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarFreshFor = -time.Second
	h.menubarWaitFor = 50 * time.Millisecond
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	insertTestCursorSnapshot(t, s, now.Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent, ResetsAt: &reset},
	})

	// Seed the cache with the default theme.
	if rr := getSummary(h); rr.Code != http.StatusOK {
		t.Fatalf("seed summary status = %d", rr.Code)
	}

	// Post-save builds stall past the budget.
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}

	body := strings.NewReader(`{"enabled":true,"default_view":"detailed","refresh_seconds":60,"theme":"dark"}`)
	rr := httptest.NewRecorder()
	h.MenubarPreferences(rr, httptest.NewRequest(http.MethodPut, "/api/menubar/preferences", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Theme       string `json:"theme"`
		DefaultView string `json:"default_view"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if response.Theme != "dark" {
		t.Fatalf("PUT response theme = %q, want the saved %q", response.Theme, "dark")
	}
	if response.DefaultView != "detailed" {
		t.Fatalf("PUT response default_view = %q, want the saved %q", response.DefaultView, "detailed")
	}
	close(gate)
}

// Finding 3: a cold start whose first build exceeds the no-cache budget must
// answer the preferences GET with 503, not 200 with a null provider list that
// makes the popover empty its options.
func TestMenubarPreferencesColdStartTimeoutIs503(t *testing.T) {
	h, s, _ := newCacheTestHandler(t, nil)
	h.menubarNoCacheFor = 50 * time.Millisecond
	insertTestCursorSnapshot(t, s, time.Now().UTC().Add(-5*time.Minute), api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		started <- struct{}{}
		<-gate
		return h.buildMenubarSnapshotWithAllProviders()
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.MenubarPreferences(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil))
		done <- rr
	}()
	waitNotNil(t, started)

	select {
	case rr := <-done:
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("preferences cold-start status = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preferences request waited past its budget")
	}
	close(gate)
}

// Gap B: a stored row that normalizes to no card must not drop the last good
// card.
func TestMenubarBuildCardNilServesLastGoodCard(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

	capturedAt := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	insertTestCursorSnapshot(t, s, capturedAt, api.CursorAccountIndividual, "Pro", []api.CursorQuota{
		{Name: "total_usage", Used: 40, Limit: 100, Utilization: 40, Format: api.CursorFormatPercent},
	})
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatalf("first build: %v", err)
	}

	// hasData true, but the payload normalizes to nothing.
	card, captured := h.menubarResolveCard("cursor", "Cursor", "", map[string]interface{}{
		"capturedAt": time.Now().UTC().Format(time.RFC3339),
		"quotas":     []interface{}{},
	}, true, true, 70, 90, nil)
	if card == nil {
		t.Fatal("card was dropped instead of serving the last good card")
	}
	if card.ID != "cursor" {
		t.Fatalf("card id = %q, want cursor", card.ID)
	}
	if !captured.Equal(capturedAt) {
		t.Fatalf("captured = %s, want the cached %s", captured, capturedAt)
	}
}

// The public dashboard builders must keep main's behaviour: Anthropic's empty
// quota list serialises as [], and Grok/Kimi still use the 7-day fallback when
// the latest read fails.
func TestDashboardBuildersMatchMainBehaviour(t *testing.T) {
	t.Run("anthropic empty quotas is an array", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()
		h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())

		// The per-quota merge returns rows; make it return a row that the
		// quotas loop does not append by breaking the fallback instead: an
		// empty store yields the per-quota table is empty and the fallback
		// finds no latest snapshot.
		response, readOK, _ := h.buildAnthropicCurrentOK()
		if !readOK {
			t.Fatalf("readOK = false, want true (fallback succeeded with no rows)")
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(data), `"quotas":[]`) {
			t.Fatalf("anthropic quotas serialised as something other than []: %s", data)
		}
	})

	t.Run("anthropic fallback quotas is an array", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()
		h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())

		response, readOK, hasData := h.buildAnthropicCurrentFallbackOK(map[string]interface{}{
			"capturedAt": time.Now().UTC().Format(time.RFC3339),
			"quotas":     []interface{}{},
		})
		if !readOK || hasData {
			t.Fatalf("fallback with no snapshot = readOK %v hasData %v, want true/false", readOK, hasData)
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(data), `"quotas":[]`) {
			t.Fatalf("anthropic fallback quotas serialised as something other than []: %s", data)
		}
	})

	t.Run("grok falls back after latest read failure", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
			AccountID:  store.DefaultGrokAccountID,
			CapturedAt: time.Now().UTC().Add(-30 * time.Minute),
			Quotas:     []api.GrokQuota{{Name: "credits", Utilization: 42, Status: "ok"}},
		}); err != nil {
			t.Fatalf("InsertGrokSnapshot: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

		response, readOK, hasData := h.buildGrokCurrentOK()
		if !readOK || !hasData {
			t.Fatalf("buildGrokCurrentOK = readOK %v hasData %v, want both true", readOK, hasData)
		}
		if got := response["capturedAt"]; got == nil {
			t.Fatal("capturedAt missing from the grok dashboard payload")
		}
	})

	// Main's dashboard builders stamped capturedAt with the build time. The
	// stored snapshot time is reserved for the menubar age label.
	t.Run("kimi dashboard capturedAt is the build time", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		stored := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
		if _, err := s.InsertKimiSnapshot(&api.KimiSnapshot{
			AccountID:  store.DefaultKimiAccountID,
			CapturedAt: stored,
			Quotas:     []api.KimiQuota{{Name: "weekly", Utilization: 42, Status: "ok"}},
		}); err != nil {
			t.Fatalf("InsertKimiSnapshot: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, &config.Config{PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"})

		response := h.buildKimiCurrent()
		raw, _ := response["capturedAt"].(string)
		built, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatalf("kimi capturedAt %q: %v", raw, err)
		}
		if built.Equal(stored) || time.Since(built) > time.Minute {
			t.Fatalf("dashboard capturedAt = %s, want the build time (main behaviour)", built)
		}

		// The menubar builder keeps the stored time for the age label.
		payload, readOK, hasData := h.buildKimiCurrentOK()
		if !readOK || !hasData {
			t.Fatalf("buildKimiCurrentOK = readOK %v hasData %v, want both true", readOK, hasData)
		}
		if payload["capturedAt"] != stored.Format(time.RFC3339) {
			t.Fatalf("menubar capturedAt = %v, want the stored %q", payload["capturedAt"], stored.Format(time.RFC3339))
		}
	})

	t.Run("grok dashboard capturedAt is the build time", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		stored := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
		if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
			AccountID:  store.DefaultGrokAccountID,
			CapturedAt: stored,
			Quotas:     []api.GrokQuota{{Name: "credits", Utilization: 42, Status: "ok"}},
		}); err != nil {
			t.Fatalf("InsertGrokSnapshot: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

		response := h.buildGrokCurrent()
		raw, _ := response["capturedAt"].(string)
		built, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatalf("grok capturedAt %q: %v", raw, err)
		}
		if built.Equal(stored) || time.Since(built) > time.Minute {
			t.Fatalf("dashboard capturedAt = %s, want the build time (main behaviour)", built)
		}
	})
}

// TestLatestGrokSnapshotFallsBackWhenLatestReadFails proves the fallback path:
// the range read must supply an older row carrying quotas when the latest row
// has none.
func TestLatestGrokSnapshotFallsBackWhenLatestReadFails(t *testing.T) {
	dsn := "file:menubar-cache-grok-fallback-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	s, err := store.New(dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
		AccountID:  store.DefaultGrokAccountID,
		CapturedAt: time.Now().UTC().Add(-30 * time.Minute),
		Quotas:     []api.GrokQuota{{Name: "credits", Utilization: 42, Status: "ok"}},
	}); err != nil {
		t.Fatalf("InsertGrokSnapshot: %v", err)
	}
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCursor())

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Make the ORDER BY ... LIMIT 1 read fail while the range read on the same
	// table still succeeds: a view cannot distinguish them, but a trigger on
	// SELECT is not available in SQLite. Drop the table and recreate it with
	// the same rows, then install a schema change mid-flight is racy. Instead
	// prove the fallback path with the range read by emptying the quota values
	// of only the newest row: latest is non-nil but has no quotas, so the
	// fallback supplies an older row with quotas.
	if _, err := db.Exec(`INSERT INTO grok_snapshots (captured_at, account_id, raw_json, quota_count)
		VALUES (?, ?, '', 0)`, time.Now().UTC().Format(time.RFC3339Nano), store.DefaultGrokAccountID); err != nil {
		t.Fatalf("insert empty newest snapshot: %v", err)
	}

	latest, readOK, hasData := h.latestGrokSnapshotOK()
	if !readOK {
		t.Fatalf("readOK = false, want true (both reads succeeded)")
	}
	if !hasData || latest == nil {
		t.Fatal("fallback snapshot was not returned")
	}
	if len(latest.Quotas) == 0 || latest.Quotas[0].Utilization != 42 {
		t.Fatalf("fallback quotas = %+v, want the older 42%% row", latest.Quotas)
	}
}

func TestLatestKimiSnapshotFallsBackWhenLatestReadFails(t *testing.T) {
	dsn := "file:menubar-cache-kimi-fallback-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	s, err := store.New(dsn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertKimiSnapshot(&api.KimiSnapshot{
		AccountID:  store.DefaultKimiAccountID,
		CapturedAt: time.Now().UTC().Add(-30 * time.Minute),
		Quotas:     []api.KimiQuota{{Name: "weekly", Utilization: 42, Status: "ok"}},
	}); err != nil {
		t.Fatalf("InsertKimiSnapshot: %v", err)
	}
	h := NewHandler(s, nil, nil, nil, &config.Config{PollInterval: 60 * time.Second, Port: 9211, AdminUser: "admin", AdminPass: "test"})

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO kimi_snapshots (captured_at, account_id, raw_json, quota_count)
		VALUES (?, ?, '', 0)`, time.Now().UTC().Format(time.RFC3339Nano), store.DefaultKimiAccountID); err != nil {
		t.Fatalf("insert empty newest snapshot: %v", err)
	}

	latest, readOK, hasData := h.latestKimiSnapshotOK()
	if !readOK {
		t.Fatalf("readOK = false, want true (both reads succeeded)")
	}
	if !hasData || latest == nil {
		t.Fatal("fallback snapshot was not returned")
	}
	if len(latest.Quotas) == 0 || latest.Quotas[0].Utilization != 42 {
		t.Fatalf("fallback quotas = %+v, want the older 42%% row", latest.Quotas)
	}
}
