package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// fakeClock drives the auth-pause schedule so recovery is testable without
// sleeping for the real 15 minute backoff.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newPausableCodexAgent builds an agent whose token never changes, so the only
// way out of the auth-paused state is the bounded self-recovery path.
func newPausableCodexAgent(t *testing.T, handler http.HandlerFunc) (*CodexAgent, *store.Store, *fakeClock) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.DiscardHandler)
	client := api.NewCodexClient("tok", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)
	ag.SetTokenRefresh(func() string { return "tok" })

	clock := &fakeClock{now: time.Now()}
	ag.now = clock.Now
	return ag, st, clock
}

func TestCodexAgent_PausedPollingRecoversAfterBackoff(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var healthy atomic.Bool
	ag, st, clock := newPausableCodexAgent(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !healthy.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	})

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures; i++ {
		ag.poll(ctx)
	}
	if !ag.authPaused {
		t.Fatalf("expected polling to pause after %d auth failures", maxCodexAuthFailures)
	}

	// Before the backoff elapses the agent stays quiet.
	pausedCalls := calls.Load()
	ag.poll(ctx)
	if got := calls.Load(); got != pausedCalls {
		t.Fatalf("calls = %d, want %d (no requests before the retry deadline)", got, pausedCalls)
	}

	// Once the backoff elapses and the endpoint is healthy again, polling
	// recovers on its own - no re-authentication, no daemon restart.
	healthy.Store(true)
	clock.Advance(codexAuthPausedRetryInterval + time.Minute)
	ag.poll(ctx)

	if ag.authPaused {
		t.Fatal("expected the auth pause to lift after a successful recovery poll")
	}
	if ag.authFailCount != 0 {
		t.Fatalf("authFailCount = %d, want 0", ag.authFailCount)
	}
	latest, err := st.QueryLatestCodex(store.DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest == nil {
		t.Fatal("expected a snapshot after recovery")
	}
}

func TestCodexAgent_PausedRecoveryBackoffEscalates(t *testing.T) {
	t.Parallel()
	ag, _, clock := newPausableCodexAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures; i++ {
		ag.poll(ctx)
	}
	if !ag.authPaused {
		t.Fatal("expected polling to pause")
	}

	// Each failed recovery attempt pushes the next one further out, so a dead
	// credential is retried on a decaying schedule rather than every interval.
	prev := time.Duration(0)
	for attempt := 1; attempt <= 3; attempt++ {
		clock.Advance(codexAuthPausedRetryMaxInterval)
		before := ag.authRetryAt
		ag.poll(ctx)
		if ag.authRetryCount != attempt {
			t.Fatalf("authRetryCount = %d, want %d", ag.authRetryCount, attempt)
		}
		gap := ag.authRetryAt.Sub(clock.Now())
		if gap <= prev && gap < codexAuthPausedRetryMaxInterval {
			t.Fatalf("attempt %d gap = %v, want longer than the previous %v", attempt, gap, prev)
		}
		if !ag.authRetryAt.After(before) {
			t.Fatalf("attempt %d did not reschedule the next retry", attempt)
		}
		if !ag.authPaused {
			t.Fatalf("attempt %d: expected to stay paused while the endpoint keeps failing", attempt)
		}
		prev = gap
	}
}

func TestCodexAuthRetryBackoff_Escalation(t *testing.T) {
	t.Parallel()
	if got := codexAuthRetryBackoff(0); got != codexAuthPausedRetryInterval {
		t.Fatalf("backoff(0) = %v, want %v", got, codexAuthPausedRetryInterval)
	}
	if got := codexAuthRetryBackoff(1); got != codexAuthPausedRetryInterval {
		t.Fatalf("backoff(1) = %v, want %v", got, codexAuthPausedRetryInterval)
	}
	if got := codexAuthRetryBackoff(2); got != 2*codexAuthPausedRetryInterval {
		t.Fatalf("backoff(2) = %v, want %v", got, 2*codexAuthPausedRetryInterval)
	}
	if got := codexAuthRetryBackoff(50); got != codexAuthPausedRetryMaxInterval {
		t.Fatalf("backoff(50) = %v, want the %v cap", got, codexAuthPausedRetryMaxInterval)
	}
}

// A bot challenge is a transport-level block, not a credential problem: it must
// never consume the auth failure budget that pauses polling.
func TestCodexAgent_AccessBlockedDoesNotPausePolling(t *testing.T) {
	t.Parallel()
	ag, _, _ := newPausableCodexAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<html><body>Just a moment...</body></html>")
	})

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures+2; i++ {
		ag.poll(ctx)
	}

	if ag.authPaused {
		t.Fatal("a challenge response must not pause polling")
	}
	if ag.authFailCount != 0 {
		t.Fatalf("authFailCount = %d, want 0", ag.authFailCount)
	}
}
