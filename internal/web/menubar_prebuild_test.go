package web

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

// A poll outcome rebuilds the cached snapshot in the background while a
// menubar client is active, so the next request is answered from memory with
// the new data.
func TestMenubarPrebuildRunsAfterPollWhenActive(t *testing.T) {
	h, _, _ := newCacheTestHandler(t, nil)
	registry := agent.NewPollStatusRegistry()
	h.SetPollStatusRegistry(registry)

	h.menubarRebuildGapFor = -1
	_ = getSummary(h) // marks the menubar as in use and fills the cache

	var builds int32
	done := make(chan struct{}, 4)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		return h.buildMenubarSnapshotWithAllProviders()
	}
	h.menubarAfterBuildHook = func(*menubar.Snapshot, error) { done <- struct{}{} }

	// A burst of poll outcomes from several providers shares one build.
	registry.Clear("codex", "1")
	registry.Clear("anthropic", "")
	registry.Report("grok", "", agent.PollOutcomeNetwork)

	select {
	case <-done:
	case <-time.After(menubarPrebuildDebounce + 3*time.Second):
		t.Fatal("no prebuild after poll outcomes")
	}
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("builds = %d, want 1 for a burst of polls", got)
	}
}

// With no menubar client, poll outcomes never trigger builds: headless and
// dashboard-only installs do not pay for menubar snapshots.
func TestMenubarPrebuildSkippedWhenInactive(t *testing.T) {
	h, _, _ := newCacheTestHandler(t, nil)
	registry := agent.NewPollStatusRegistry()
	h.SetPollStatusRegistry(registry)

	var builds int32
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		return h.buildMenubarSnapshotWithAllProviders()
	}

	registry.Clear("codex", "1")
	time.Sleep(menubarPrebuildDebounce + 500*time.Millisecond)
	if got := atomic.LoadInt32(&builds); got != 0 {
		t.Fatalf("builds = %d, want 0 without a menubar client", got)
	}
}

// Background rebuilds keep a minimum gap: a stale request inside it serves the
// cache without starting a build (so a failing build is not retried on every
// request), and a prebuild inside it is deferred, not dropped.
func TestMenubarRebuildGap(t *testing.T) {
	h, _, _ := newCacheTestHandler(t, nil)
	registry := agent.NewPollStatusRegistry()
	h.SetPollStatusRegistry(registry)
	h.menubarRebuildGapFor = 3 * time.Second
	h.menubarFreshFor = -time.Nanosecond

	_ = getSummary(h) // first build; the gap starts now

	var builds int32
	done := make(chan struct{}, 4)
	h.menubarBuildHook = func(ctx context.Context) (menubarBuiltData, error) {
		atomic.AddInt32(&builds, 1)
		return h.buildMenubarSnapshotWithAllProviders()
	}
	h.menubarAfterBuildHook = func(*menubar.Snapshot, error) { done <- struct{}{} }

	for i := 0; i < 3; i++ {
		rec := getSummary(h)
		if rec.Header().Get("X-Onwatch-Refreshing") != "" {
			t.Fatal("no rebuild may run inside the gap")
		}
	}
	if got := atomic.LoadInt32(&builds); got != 0 {
		t.Fatalf("builds = %d inside the gap, want 0", got)
	}

	registry.Clear("codex", "1")
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("deferred prebuild never ran")
	}
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("builds = %d, want the one deferred prebuild", got)
	}
}
