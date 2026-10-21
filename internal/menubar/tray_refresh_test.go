package menubar

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A failed fetch must never blank the tray. The last good snapshot is reused
// and flagged stale; only a refresh that has never succeeded returns nil.
func TestNextTraySnapshotKeepsCacheOnFailure(t *testing.T) {
	t.Parallel()
	cached := &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}

	got, stale := nextTraySnapshot(cached, nil, errors.New("timeout"))
	if got != cached {
		t.Fatalf("expected cached snapshot on failure, got %#v", got)
	}
	if !stale {
		t.Fatal("expected stale=true when the fetch failed")
	}
}

// A nil fetch result (no error) is still a failure: keep the cache.
func TestNextTraySnapshotNilFetchKeepsCache(t *testing.T) {
	t.Parallel()
	cached := &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}

	got, stale := nextTraySnapshot(cached, nil, nil)
	if got != cached || !stale {
		t.Fatalf("expected cached snapshot marked stale, got %#v stale=%v", got, stale)
	}
}

func TestNextTraySnapshotUsesFreshData(t *testing.T) {
	t.Parallel()
	cached := &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}
	fresh := &Snapshot{Aggregate: Aggregate{ProviderCount: 2}}

	got, stale := nextTraySnapshot(cached, fresh, nil)
	if got != fresh {
		t.Fatalf("expected fresh snapshot, got %#v", got)
	}
	if stale {
		t.Fatal("expected stale=false for a successful fetch")
	}
}

// The very first refresh, before any snapshot has ever succeeded, is the only
// case allowed to render "--"/offline.
func TestNextTraySnapshotNilWithoutHistory(t *testing.T) {
	t.Parallel()
	got, stale := nextTraySnapshot(nil, nil, errors.New("timeout"))
	if got != nil {
		t.Fatalf("expected nil snapshot with no history, got %#v", got)
	}
	if stale {
		t.Fatal("expected stale=false when there is nothing cached")
	}
}

// The worker serializes refresh runs: fetch and apply never overlap, and many
// concurrent request publishers (the systray click goroutine, the SIGUSR1 /
// marker watcher, the ticker) cannot re-enter either. Publishers are funneled
// in during the first fetch, so the second run cannot start until the first
// apply has finished by construction of the worker loop.
func TestRefreshWorkerNeverOverlaps(t *testing.T) {
	t.Parallel()
	var active, overlap int32
	guard := func() {
		if atomic.AddInt32(&active, 1) > 1 {
			atomic.StoreInt32(&overlap, 1)
		}
	}
	releaseGuard := func() { atomic.AddInt32(&active, -1) }

	var runs int32
	applied := make(chan struct{}, 4)
	var publishOnce sync.Once
	var publishers sync.WaitGroup
	var worker *refreshWorker
	worker = newRefreshWorker(
		func() (*Snapshot, error) {
			guard()
			defer releaseGuard()
			publishOnce.Do(func() {
				for i := 0; i < 32; i++ {
					publishers.Add(1)
					go func() {
						defer publishers.Done()
						worker.Request()
					}()
				}
				publishers.Wait()
			})
			return &Snapshot{Aggregate: Aggregate{ProviderCount: int(atomic.AddInt32(&runs, 1))}}, nil
		},
		func(*Snapshot, bool, time.Duration, error) {
			guard()
			defer releaseGuard()
			applied <- struct{}{}
		})

	stop, wait := startTestWorker(t, worker)
	worker.Request()
	<-applied // first run
	<-applied // coalesced follow-up
	stop()
	wait()

	if atomic.LoadInt32(&runs) != 2 {
		t.Fatalf("runs = %d, want 2 (32 requests must coalesce into one follow-up)", atomic.LoadInt32(&runs))
	}
	if atomic.LoadInt32(&overlap) == 1 {
		t.Fatal("fetch and apply overlapped: the worker must run them serially")
	}
}

// A request published at any point during a run causes exactly one more run.
// The three phases are injected through hooks and the fetch/apply closures, so
// no sleep decides the ordering.
func TestRefreshWorkerRequestDuringRunRunsOnceMore(t *testing.T) {
	t.Parallel()
	phases := []struct {
		name string
		set  func(w *refreshWorker, publish func())
	}{
		{
			name: "before fetch",
			set: func(w *refreshWorker, publish func()) {
				var once sync.Once
				w.testHookDuringFetch = func() { once.Do(publish) }
			},
		},
		{
			name: "during fetch",
			set: func(w *refreshWorker, publish func()) {
				var once sync.Once
				base := w.fetch
				w.fetch = func() (*Snapshot, error) {
					once.Do(publish)
					return base()
				}
			},
		},
		{
			name: "before apply",
			set: func(w *refreshWorker, publish func()) {
				var once sync.Once
				w.testHookDuringApply = func() { once.Do(publish) }
			},
		},
	}
	for _, phase := range phases {
		phase := phase
		t.Run(phase.name, func(t *testing.T) {
			t.Parallel()
			var fetches int32
			applied := make(chan struct{}, 4)
			worker := newRefreshWorker(
				func() (*Snapshot, error) {
					atomic.AddInt32(&fetches, 1)
					return &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}, nil
				},
				func(*Snapshot, bool, time.Duration, error) { applied <- struct{}{} })

			phase.set(worker, worker.Request)
			stop, wait := startTestWorker(t, worker)
			worker.Request()

			<-applied // first run
			<-applied // follow-up run for the request published mid-run

			if got := atomic.LoadInt32(&fetches); got != 2 {
				t.Fatalf("fetches = %d, want 2", got)
			}

			// No third run can happen: the inbox is empty. Stop and confirm.
			stop()
			wait()
			if got := atomic.LoadInt32(&fetches); got != 2 {
				t.Fatalf("fetches after stop = %d, want 2", got)
			}
		})
	}
}

// Several requests that arrive during one run collapse into exactly one
// follow-up run, because the buffer-1 inbox holds a single token.
func TestRefreshWorkerRequestsCoalesce(t *testing.T) {
	t.Parallel()
	var fetches int32
	applied := make(chan struct{}, 4)
	worker := newRefreshWorker(
		func() (*Snapshot, error) {
			atomic.AddInt32(&fetches, 1)
			return &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}, nil
		},
		func(*Snapshot, bool, time.Duration, error) { applied <- struct{}{} })

	var once sync.Once
	worker.testHookDuringFetch = func() {
		once.Do(func() {
			for i := 0; i < 32; i++ {
				worker.Request()
			}
		})
	}
	stop, wait := startTestWorker(t, worker)
	worker.Request()

	<-applied // first run
	<-applied // the single coalesced follow-up

	stop()
	wait()
	if got := atomic.LoadInt32(&fetches); got != 2 {
		t.Fatalf("fetches = %d, want 2 (requests must coalesce into one extra run)", got)
	}
}

// Requests sent while the worker is idle each cause their own run, in the
// order they were published. The channel is the only synchronization.
func TestRefreshWorkerIdleRequestsEachRun(t *testing.T) {
	t.Parallel()
	var fetches int32
	applied := make(chan *Snapshot, 4)
	worker := newRefreshWorker(
		func() (*Snapshot, error) {
			n := atomic.AddInt32(&fetches, 1)
			return &Snapshot{Aggregate: Aggregate{ProviderCount: int(n)}}, nil
		},
		func(snapshot *Snapshot, _ bool, _ time.Duration, _ error) { applied <- snapshot })

	stop, wait := startTestWorker(t, worker)
	for i := int32(1); i <= 3; i++ {
		worker.Request()
		snapshot := <-applied
		if got := snapshot.Aggregate.ProviderCount; got != int(i) {
			t.Fatalf("run %d applied provider count %d, want %d", i, got, i)
		}
	}
	stop()
	wait()
	if got := atomic.LoadInt32(&fetches); got != 3 {
		t.Fatalf("fetches = %d, want 3", got)
	}
}

// Request must never block, even while a run is in flight, so the systray
// goroutine cannot stall behind a slow daemon.
func TestRefreshWorkerRequestNeverBlocks(t *testing.T) {
	t.Parallel()
	inFetch := make(chan struct{})
	release := make(chan struct{})
	var signalOnce sync.Once
	worker := newRefreshWorker(
		func() (*Snapshot, error) {
			signalOnce.Do(func() { close(inFetch) })
			<-release
			return &Snapshot{Aggregate: Aggregate{ProviderCount: 1}}, nil
		},
		func(*Snapshot, bool, time.Duration, error) {})

	stop, wait := startTestWorker(t, worker)
	worker.Request()
	<-inFetch

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 4; i++ {
			worker.Request()
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Request blocked while a run was in flight")
	}

	close(release)
	stop()
	wait()
}

// A failure before any success yields a nil snapshot so the caller shows "--",
// and the error is surfaced for logging.
func TestRefreshWorkerFirstFailureReturnsNil(t *testing.T) {
	t.Parallel()
	type outcome struct {
		snapshot *Snapshot
		stale    bool
		age      time.Duration
		err      error
	}
	got := make(chan outcome, 1)
	worker := newRefreshWorker(
		func() (*Snapshot, error) { return nil, errors.New("boom") },
		func(s *Snapshot, st bool, a time.Duration, e error) {
			got <- outcome{s, st, a, e}
		})

	stop, wait := startTestWorker(t, worker)
	worker.Request()
	out := <-got
	stop()
	wait()

	if out.snapshot != nil || out.stale {
		t.Fatalf("expected nil, not stale; got %#v stale=%v", out.snapshot, out.stale)
	}
	if out.age != 0 {
		t.Fatalf("expected zero age, got %s", out.age)
	}
	if out.err == nil {
		t.Fatal("expected the fetch error to be surfaced for logging")
	}
}

// A failure after a success reuses the cached snapshot and marks it stale.
func TestRefreshWorkerFailureKeepsLastGood(t *testing.T) {
	t.Parallel()
	var fetches int32
	applied := make(chan struct{}, 2)
	var gotSnapshot *Snapshot
	var gotStale bool
	worker := newRefreshWorker(
		func() (*Snapshot, error) {
			if atomic.AddInt32(&fetches, 1) == 1 {
				return &Snapshot{LatestAt: time.Unix(1_700_000_000, 0), Aggregate: Aggregate{ProviderCount: 1}}, nil
			}
			return nil, errors.New("boom")
		},
		func(s *Snapshot, stale bool, _ time.Duration, _ error) {
			gotSnapshot, gotStale = s, stale
			applied <- struct{}{}
		})

	stop, wait := startTestWorker(t, worker)
	worker.Request()
	<-applied
	worker.Request()
	<-applied
	stop()
	wait()

	if gotSnapshot == nil {
		t.Fatal("cache lost the successful snapshot")
	}
	if !gotStale {
		t.Fatal("expected the cached snapshot to be marked stale")
	}
}

// startTestWorker runs the worker loop and returns idempotent stop and wait
// helpers. Cleanup stops it if a test exits early.
func startTestWorker(t *testing.T, worker *refreshWorker) (stop func(), wait func()) {
	t.Helper()
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.RunWorker(stopCh)
	}()
	var once sync.Once
	stop = func() { once.Do(func() { close(stopCh) }) }
	wait = func() { <-done }
	t.Cleanup(func() {
		stop()
		wait()
	})
	return stop, wait
}

// The stale age counts from the newest provider data (latest_at), not the
// fetch time, so "updated 2h ago" does not snap back to "just now" on the
// first failure.
func TestStaleSnapshotAgeUsesLatestAt(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	snapshot := &Snapshot{GeneratedAt: now, LatestAt: now.Add(-2 * time.Hour)}
	if got := staleSnapshotAge(snapshot, now, now); got != 2*time.Hour {
		t.Fatalf("staleSnapshotAge = %s, want 2h (from LatestAt)", got)
	}
	// No latest_at: fall back to when the snapshot was fetched.
	noLatest := &Snapshot{GeneratedAt: now}
	if got := staleSnapshotAge(noLatest, now.Add(-5*time.Minute), now); got != 5*time.Minute {
		t.Fatalf("staleSnapshotAge = %s, want 5m (from fetch time)", got)
	}
	if got := staleSnapshotAge(&Snapshot{}, time.Time{}, now); got != 0 {
		t.Fatalf("staleSnapshotAge with no source = %s, want 0", got)
	}
}

func TestFormatAgeAgoGrowsSmoothly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		age  time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{60 * time.Second, "1m ago"},
		{90 * time.Second, "1m ago"},
		{2 * time.Minute, "2m ago"},
		{59 * time.Minute, "59m ago"},
		{60 * time.Minute, "1h ago"},
		{90 * time.Minute, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{24 * time.Hour, "1d ago"},
		{49 * time.Hour, "2d ago"},
	}
	for _, tc := range cases {
		if got := formatAgeAgo(tc.age); got != tc.want {
			t.Errorf("formatAgeAgo(%s) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

func TestTrayTooltipStaleTextCountsUp(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	got := TrayTooltipStaleText(snapshot, 0, 3*time.Minute)
	for _, want := range []string{"Claude 82%", "Codex 18%", "last updated 3m ago"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in stale tooltip %q", want, got)
		}
	}
	// A stale tooltip must not repeat the frozen server-side "updated ..." line.
	if strings.Contains(got, "updated 12s ago") {
		t.Fatalf("stale tooltip must use the client-computed age, got %q", got)
	}
}

func TestTrayTooltipStaleTextRespectsLimit(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	for i := 0; i < 20; i++ {
		snapshot.Providers = append(snapshot.Providers, ProviderCard{ID: "p", Label: "Provider With A Long Name", HighestPercent: 50})
	}
	got := TrayTooltipStaleText(snapshot, 127, time.Minute)
	if len([]rune(got)) > 127 {
		t.Fatalf("stale tooltip exceeds limit: %d runes", len([]rune(got)))
	}
}

func TestTrayTooltipStaleTextNilSnapshot(t *testing.T) {
	t.Parallel()
	got := TrayTooltipStaleText(nil, 0, time.Minute)
	if !strings.Contains(got, "daemon") {
		t.Fatalf("expected offline hint for nil snapshot, got %q", got)
	}
}
