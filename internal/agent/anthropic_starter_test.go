package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// fakeStarterPinger records pings. When block is set, each ping waits on it.
type fakeStarterPinger struct {
	calls atomic.Int32
	err   error
	block chan struct{}
}

func (f *fakeStarterPinger) Ping(ctx context.Context) error {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func (f *fakeStarterPinger) Model() string { return "haiku" }

// starterTestAgent returns an agent with the starter wired to a fake pinger,
// a controllable clock and an enablement flag.
func starterTestAgent(t *testing.T) (*AnthropicAgent, *fakeStarterPinger, *time.Time, *atomic.Bool, *sync.WaitGroup) {
	t.Helper()
	a := NewAnthropicAgent(nil, nil, nil, time.Minute, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
	pinger := &fakeStarterPinger{}
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	var enabled atomic.Bool
	enabled.Store(true)
	var wg sync.WaitGroup
	a.SetAutoStartCheck(enabled.Load)
	a.starter.pinger = pinger
	a.starter.clock = func() time.Time { return now }
	a.starter.done = wg.Done
	return a, pinger, &now, &enabled, &wg
}

func fiveHourSnapshot(capturedAt time.Time, util float64, resetsAt *time.Time) *api.AnthropicSnapshot {
	return &api.AnthropicSnapshot{
		CapturedAt: capturedAt,
		Quotas: []api.AnthropicQuota{
			{Name: "five_hour", Utilization: util, ResetsAt: resetsAt},
			{Name: "seven_day", Utilization: 40, ResetsAt: timePtr(capturedAt.Add(72 * time.Hour))},
		},
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestFiveHourSessionInactive(t *testing.T) {
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		snap *api.AnthropicSnapshot
		want bool
	}{
		{"nil snapshot", nil, false},
		{"no reset time, nothing used", fiveHourSnapshot(at, 0, nil), true},
		{"no reset time but usage", fiveHourSnapshot(at, 12, nil), false},
		{"reset time passed", fiveHourSnapshot(at, 35, timePtr(at.Add(-time.Minute))), true},
		{"reset time exactly now", fiveHourSnapshot(at, 35, timePtr(at)), true},
		{"session running", fiveHourSnapshot(at, 5, timePtr(at.Add(4*time.Hour))), false},
		{"no five_hour quota", &api.AnthropicSnapshot{CapturedAt: at, Quotas: []api.AnthropicQuota{{Name: "seven_day"}}}, false},
	}
	for _, tc := range cases {
		if got := fiveHourSessionState(tc.snap) == fiveHourInactive; got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAnthropicAutoStart_FiresOnceForAnInactiveSession(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)

	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}

	// The next poll, read before the new session shows up, must not ping again.
	*now = now.Add(time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d after a second poll inside the cooldown, want 1", got)
	}
}

func TestAnthropicAutoStart_DoesNothingUnlessNeeded(t *testing.T) {
	t.Run("toggle off", func(t *testing.T) {
		a, pinger, now, enabled, _ := starterTestAgent(t)
		enabled.Store(false)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
		if pinger.calls.Load() != 0 {
			t.Fatal("pinged with the toggle off")
		}
	})
	t.Run("not wired", func(t *testing.T) {
		a := NewAnthropicAgent(nil, nil, nil, time.Minute, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil)
		if a.starter.pinger != nil {
			t.Fatal("a starter was created without SetAutoStartCheck")
		}
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(time.Now(), 0, nil))
	})
	t.Run("session running", func(t *testing.T) {
		a, pinger, now, _, _ := starterTestAgent(t)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 3, timePtr(now.Add(2*time.Hour))))
		if pinger.calls.Load() != 0 {
			t.Fatal("pinged while a session was running")
		}
	})
	t.Run("stale data", func(t *testing.T) {
		a, pinger, now, _, _ := starterTestAgent(t)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(now.Add(-11*time.Minute), 0, nil))
		if pinger.calls.Load() != 0 {
			t.Fatal("pinged on data older than the freshness limit")
		}
	})
	t.Run("nil snapshot", func(t *testing.T) {
		a, pinger, _, _, _ := starterTestAgent(t)
		a.maybeAutoStartSession(context.Background(), nil)
		if pinger.calls.Load() != 0 {
			t.Fatal("pinged on a nil snapshot")
		}
	})
}

func TestAnthropicAutoStart_OnePingInFlight(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pinger.block = make(chan struct{})

	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	// Past the cooldown but the first ping is still running.
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	close(pinger.block)
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1 while the first was in flight", got)
	}
}

// Failed starts retry after the cooldown, at most anthropicStarterMaxFires
// times in a rolling anthropicStarterRateWindow.
func TestAnthropicAutoStart_RateCap(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pinger.err = errors.New("anthropic starter: claude exited with exit status 1")

	// claimed reports whether the last call reserved a ping (it then runs in
	// the background and calls done when finished).
	claimed := func() bool {
		a.starter.mu.Lock()
		defer a.starter.mu.Unlock()
		return a.starter.lastFire.Equal(*now)
	}
	attempt := func() {
		wg.Add(1)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
		if !claimed() {
			wg.Done() // nothing fired
			return
		}
		wg.Wait()
	}
	start := *now
	for i := 0; i < anthropicStarterMaxFires+2; i++ {
		attempt()
		*now = now.Add(anthropicStarterCooldown)
	}
	if got := pinger.calls.Load(); got != anthropicStarterMaxFires {
		t.Fatalf("pings = %d, want the cap of %d", got, anthropicStarterMaxFires)
	}

	// Once the oldest ping leaves the rolling window, one more is allowed.
	*now = start.Add(anthropicStarterRateWindow + time.Second)
	attempt()
	if got := pinger.calls.Load(); got != anthropicStarterMaxFires+1 {
		t.Fatalf("pings = %d after the window rolled, want %d", got, anthropicStarterMaxFires+1)
	}
}

func TestAnthropicAutoStart_ToggleIsReadEveryPoll(t *testing.T) {
	a, pinger, now, enabled, wg := starterTestAgent(t)
	enabled.Store(false)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	enabled.Store(true)
	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1 after the toggle was turned on", got)
	}
}

// End to end through poll(): a usage response with no running five-hour
// session triggers exactly one starter ping.
func TestAnthropicAgent_PollTriggersAutoStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":12,"resets_at":"2030-01-01T00:00:00Z"}}`))
	}))
	defer server.Close()
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer str.Close()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	a := NewAnthropicAgent(client, str, tracker.NewAnthropicTracker(str, logger), time.Minute, logger, nil)

	pinger := &fakeStarterPinger{}
	var wg sync.WaitGroup
	a.SetAutoStartCheck(func() bool { return true })
	a.starter.pinger = pinger
	a.starter.done = wg.Done

	wg.Add(1)
	a.poll(context.Background())
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}

	// A running session (as the next poll would show) is left alone.
	running := &api.AnthropicSnapshot{CapturedAt: time.Now(), Quotas: []api.AnthropicQuota{
		{Name: "five_hour", Utilization: 1, ResetsAt: timePtr(time.Now().Add(5 * time.Hour))},
	}}
	if fiveHourSessionState(running) == fiveHourInactive {
		t.Fatal("a running session was treated as inactive")
	}
}

func TestAnthropicAgent_PollWithActiveSessionDoesNotStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anthropicResponse(20, 10)))
	}))
	defer server.Close()
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer str.Close()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	a := NewAnthropicAgent(client, str, tracker.NewAnthropicTracker(str, logger), time.Minute, logger, nil)
	pinger := &fakeStarterPinger{}
	a.SetAutoStartCheck(func() bool { return true })
	a.starter.pinger = pinger

	a.poll(context.Background())
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d, want 0 while a session is running", got)
	}
}

// pingOnce fires one starter ping at now for an inactive session and waits
// for it to finish.
func pingOnce(t *testing.T, a *AnthropicAgent, wg *sync.WaitGroup, now time.Time) {
	t.Helper()
	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(now, 0, nil))
	wg.Wait()
}

// Each ping outcome reaches webhooks as provider "anthropic", quota
// "five_hour". A failure that repeats the previous one is not sent again, so a
// setup that cannot work (no CLI, logged out) alerts once rather than every
// few hours; a success, or a different failure, is always sent.
func TestAnthropicAutoStart_WebhookOutcomesAndRepeatFailures(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)

	step := func(err error) {
		pinger.err = err
		pingOnce(t, a, wg, *now)
		*now = now.Add(anthropicStarterCooldown + time.Minute)
	}

	step(api.ErrClaudeCLINotFound)
	step(api.ErrClaudeCLINotFound) // same failure: logged, not re-sent
	step(api.ErrAnthropicStarterAuth)
	step(nil)

	events := waitDelivered(t, delivered, 3)
	want := []string{"starter_failure", "starter_failure", "starter_success"}
	if len(events) != len(want) {
		t.Fatalf("delivered = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("delivered = %v, want %v", events, want)
		}
	}

	// After a success, the same failure as before counts as new again. The
	// success started the session, which a later poll shows running.
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 3, timePtr(now.Add(4*time.Hour))))
	*now = now.Add(anthropicStarterRateWindow)
	step(api.ErrClaudeCLINotFound)
	if events := waitDelivered(t, delivered, 4); len(events) != 4 || events[3] != "starter_failure" {
		t.Fatalf("delivered = %v, want a fourth starter_failure after the success", events)
	}
}

func TestAnthropicStarterState_NoteFailure(t *testing.T) {
	var s anthropicStarterState
	if !s.noteFailure("a") {
		t.Fatal("first failure should be new")
	}
	if s.noteFailure("a") {
		t.Fatal("a repeated failure should not be new")
	}
	if !s.noteFailure("b") {
		t.Fatal("a different failure should be new")
	}
	s.noteSuccess(time.Now())
	if !s.noteFailure("b") {
		t.Fatal("a failure after a success should be new")
	}
}

// A CLI that hangs is stopped by the ping timeout, the in-flight slot is
// released, and the next ping can run once the cooldown passes.
func TestAnthropicAutoStart_HangingPingIsBoundedAndReleased(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pinger.block = make(chan struct{}) // never closed: Ping waits for ctx

	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	a.maybeAutoStartSession(ctx, fiveHourSnapshot(*now, 0, nil))
	cancel() // stands in for the 2-minute ping timeout
	wg.Wait()

	a.starter.mu.Lock()
	inFlight := a.starter.inFlight
	a.starter.mu.Unlock()
	if inFlight {
		t.Fatal("in-flight slot still held after the ping ended")
	}

	pinger.block = nil
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	pingOnce(t, a, wg, *now)
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want 2", got)
	}
}

// Many polls at once (statusline and API polls racing) still fire one ping.
func TestAnthropicAutoStart_ConcurrentPollsFireOnce(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	release := make(chan struct{})
	pinger.block = release

	wg.Add(1)
	var start sync.WaitGroup
	for i := 0; i < 20; i++ {
		start.Add(1)
		go func() {
			defer start.Done()
			a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
		}()
	}
	start.Wait()
	close(release)
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}
}

// Clock skew: a reset time a little in the future of the capture time is a
// running session, and one at or before it is not.
func TestFiveHourSessionInactive_Boundaries(t *testing.T) {
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	if fiveHourSessionState(fiveHourSnapshot(at, 5, timePtr(at.Add(time.Second)))) == fiveHourInactive {
		t.Fatal("a session ending in 1s is still running")
	}
	if fiveHourSessionState(fiveHourSnapshot(at, 5, timePtr(at))) != fiveHourInactive {
		t.Fatal("a session whose reset equals the capture time has ended")
	}
	if fiveHourSessionState(fiveHourSnapshot(at, 0.5, nil)) == fiveHourInactive {
		t.Fatal("some use with no reset time is not an idle session")
	}
	if fiveHourSessionState(&api.AnthropicSnapshot{CapturedAt: at}) == fiveHourInactive {
		t.Fatal("a snapshot without a five_hour quota says nothing")
	}
}

// A ping that "succeeds" but never starts the tracked session (the claude CLI
// is logged in to another account) is caught on the first fresh poll a full
// cooldown later: no more pings, one failure alert, and the starter resumes
// once a session is seen running.
func TestAnthropicAutoStart_SuccessWithoutSessionPausesAndAlertsOnce(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)

	pingOnce(t, a, wg, *now) // succeeds
	if got := waitDelivered(t, delivered, 1); len(got) != 1 || got[0] != "starter_success" {
		t.Fatalf("delivered = %v, want one starter_success", got)
	}

	// Polls a cooldown later still show no session: paused, one alert.
	for i := 0; i < 3; i++ {
		*now = now.Add(anthropicStarterCooldown + time.Minute)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want no more pings while paused", got)
	}
	events := waitDelivered(t, delivered, 2)
	if len(events) != 2 || events[1] != "starter_failure" {
		t.Fatalf("delivered = %v, want one starter_failure after the success", events)
	}

	// The user starts a session themselves; afterwards the starter works again.
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 2, timePtr(now.Add(time.Hour))))
	*now = now.Add(5 * time.Hour)
	pingOnce(t, a, wg, *now)
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want the starter to resume after a running session", got)
	}
}

// A poll inside the cooldown after a successful ping, read before the new
// session shows up, is not mistaken for a ping that did not take.
func TestAnthropicAutoStart_EarlyPollAfterSuccessIsNotAMismatch(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pingOnce(t, a, wg, *now)

	*now = now.Add(2 * time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if a.starter.isStalled() {
		t.Fatal("paused on a poll taken before the session could show up")
	}
	*now = now.Add(3 * time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 1, timePtr(now.Add(5*time.Hour))))
	*now = now.Add(5 * time.Hour)
	pingOnce(t, a, wg, *now)
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want 2", got)
	}
}

// The first poll after a long gap (a laptop asleep, the daemon stopped) says
// nothing about the last ping: its session may have run and ended meanwhile.
// It is not taken as a ping that did not start a session.
func TestAnthropicAutoStart_PollAfterLongGapIsNotAMismatch(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)
	pingOnce(t, a, wg, *now)
	waitDelivered(t, delivered, 1)

	for _, gap := range []time.Duration{anthropicStarterRateWindow, 9 * time.Hour} {
		*now = now.Add(gap)
		pingOnce(t, a, wg, *now)
		if a.starter.isStalled() {
			t.Fatalf("paused after a %v gap", gap)
		}
	}
	if got := pinger.calls.Load(); got != 3 {
		t.Fatalf("pings = %d, want 3", got)
	}
	for _, ev := range waitDelivered(t, delivered, 3) {
		if ev != "starter_success" {
			t.Fatalf("delivered %q, want only starter_success", ev)
		}
	}
}

// Just inside the judge window the missing session still counts; after it,
// the ping is forgotten.
func TestAnthropicStarterState_MismatchWindow(t *testing.T) {
	base := time.Unix(1750000000, 0)
	cases := []struct {
		gap  time.Duration
		want bool
	}{
		{anthropicStarterCooldown - time.Second, false},
		{anthropicStarterCooldown, true},
		{anthropicStarterJudgeWindow - time.Second, true},
		{anthropicStarterJudgeWindow, false},
		{4*time.Hour + 47*time.Minute, false}, // the session may have run and ended
		{anthropicStarterRateWindow, false},
	}
	for _, tc := range cases {
		s := &anthropicStarterState{}
		s.noteSuccess(base)
		if got := s.pingDidNotStartSession(base.Add(tc.gap)); got != tc.want {
			t.Errorf("gap %v: got %v, want %v", tc.gap, got, tc.want)
		}
		if s.pingDidNotStartSession(base.Add(tc.gap)) {
			t.Errorf("gap %v: reported twice", tc.gap)
		}
	}
}

func TestFiveHourSessionState(t *testing.T) {
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		snap *api.AnthropicSnapshot
		want fiveHourSession
	}{
		{"nil snapshot", nil, fiveHourUnknown},
		{"five_hour null (dropped by the parser)", &api.AnthropicSnapshot{CapturedAt: at, Quotas: []api.AnthropicQuota{{Name: "seven_day", Utilization: 3}}}, fiveHourUnknown},
		{"usage but no reset time", fiveHourSnapshot(at, 12, nil), fiveHourUnknown},
		{"nothing used, no reset time", fiveHourSnapshot(at, 0, nil), fiveHourInactive},
		{"reset passed", fiveHourSnapshot(at, 40, timePtr(at.Add(-time.Second))), fiveHourInactive},
		{"running", fiveHourSnapshot(at, 1, timePtr(at.Add(time.Hour))), fiveHourRunning},
	}
	for _, tc := range cases {
		if got := fiveHourSessionState(tc.snap); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Data that says nothing about the session neither pings nor lifts a pause,
// and does not count as the poll that confirms or refutes the last ping.
func TestAnthropicAutoStart_UnknownSessionDataChangesNothing(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	noFiveHour := func(at time.Time) *api.AnthropicSnapshot {
		return &api.AnthropicSnapshot{CapturedAt: at, Quotas: []api.AnthropicQuota{{Name: "seven_day", Utilization: 3}}}
	}

	a.maybeAutoStartSession(context.Background(), noFiveHour(*now))
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 7, nil))
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d, want none on data that says nothing", got)
	}

	pingOnce(t, a, wg, *now)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("expected a pause after a ping that started nothing")
	}
	*now = now.Add(time.Minute)
	a.maybeAutoStartSession(context.Background(), noFiveHour(*now))
	if !a.starter.isStalled() {
		t.Fatal("a snapshot without five_hour data lifted the pause")
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}
}

// A skip reason is reported once until it changes or a ping goes out, so a
// starter held back by its rate cap does not repeat the same line every poll.
func TestAnthropicStarterState_NoteSkip(t *testing.T) {
	s := &anthropicStarterState{}
	if !s.noteSkip("cooldown after the last ping") {
		t.Fatal("first skip not reported")
	}
	if s.noteSkip("cooldown after the last ping") {
		t.Fatal("repeated skip reported again")
	}
	if !s.noteSkip("rate cap reached") {
		t.Fatal("a new reason was not reported")
	}
	if ok, _ := s.claim(time.Unix(1750000000, 0)); !ok {
		t.Fatal("claim refused")
	}
	if !s.noteSkip("rate cap reached") {
		t.Fatal("a reason after a ping was not reported")
	}
}

// In hybrid mode a statusline snapshot can still show the old, expired reset
// after a ping that did start the session (the statusline only changes when
// interactive Claude Code runs). It neither pauses the starter nor pings
// again; the API snapshot decides.
func TestAnthropicAutoStart_StaleStatuslineAfterPingIsNotJudged(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)
	pingOnce(t, a, wg, *now)
	waitDelivered(t, delivered, 1)

	for _, gap := range []time.Duration{20 * time.Minute, 40 * time.Minute} {
		at := now.Add(gap)
		*now = at
		a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(at.Add(-time.Minute), 30, timePtr(at.Add(-2*time.Hour))))
	}
	if a.starter.isStalled() {
		t.Fatal("a stale statusline snapshot paused the starter")
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want no second ping on statusline data", got)
	}
	// The API snapshot shows the session running: confirmed, no alert.
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 3, timePtr(now.Add(4*time.Hour))))
	if a.starter.awaitingConfirmation(*now) {
		t.Fatal("the running session did not confirm the ping")
	}
	if events := waitDelivered(t, delivered, 1); len(events) != 1 {
		t.Fatalf("delivered = %v, want only the success", events)
	}
}

// With statusline data only, a ping is forgotten after a full rate window, so
// the starter is not held back forever.
func TestAnthropicAutoStart_StatuslineOnlyResumesAfterRateWindow(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pingOnce(t, a, wg, *now)

	*now = now.Add(anthropicStarterRateWindow + time.Minute)
	wg.Add(1)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 0, nil))
	wg.Wait()
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want a new ping after the rate window", got)
	}
}

// Turning auto-start off lifts a pause, so switching it off and on resumes
// pings, and a pause that comes back alerts again. While paused, the skip is
// recorded once rather than on every poll.
func TestAnthropicAutoStart_ToggleOffLiftsPause(t *testing.T) {
	a, pinger, now, enabled, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)

	pingOnce(t, a, wg, *now)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused")
	}
	waitDelivered(t, delivered, 2)

	for i := 0; i < 3; i++ {
		*now = now.Add(time.Minute)
		a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	}
	if a.starter.lastSkip != "paused until a five-hour session is seen running" {
		t.Fatalf("lastSkip = %q", a.starter.lastSkip)
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d while paused, want 1", got)
	}

	// Off for one poll, then on again: the next poll pings.
	enabled.Store(false)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if a.starter.isStalled() {
		t.Fatal("turning auto-start off did not lift the pause")
	}
	enabled.Store(true)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	pingOnce(t, a, wg, *now)
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want the starter to resume", got)
	}

	// Still the wrong account: it pauses again and says so again.
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	events := waitDelivered(t, delivered, 4)
	if len(events) != 4 || events[3] != "starter_failure" {
		t.Fatalf("delivered = %v, want a second starter_failure", events)
	}
}

// A known failure compares by kind, so changing CLI details (a request id)
// do not send a webhook on every attempt; an unknown one compares by text.
func TestAnthropicStarterFailureKey(t *testing.T) {
	a := fmt.Errorf("%w: request req_1", api.ErrAnthropicStarterAuth)
	b := fmt.Errorf("%w: request req_2", api.ErrAnthropicStarterAuth)
	if anthropicStarterFailureKey(a) != anthropicStarterFailureKey(b) {
		t.Fatal("two auth failures with different details compare as different")
	}
	if anthropicStarterFailureKey(a) == anthropicStarterFailureKey(api.ErrClaudeCLINotFound) {
		t.Fatal("different kinds compare as the same")
	}
	if anthropicStarterFailureKey(fmt.Errorf("ping: %w", context.DeadlineExceeded)) != context.DeadlineExceeded.Error() {
		t.Fatal("a timeout is not keyed by kind")
	}
	x, y := errors.New("exit 1: boom"), errors.New("exit 1: bang")
	if anthropicStarterFailureKey(x) == anthropicStarterFailureKey(y) {
		t.Fatal("unknown failures with different text compare as the same")
	}
}

// The cooldown counts from when a ping was claimed, the "did not start"
// check from when it finished. A poll in between must not fire a second ping
// that would skip the check: the starter waits, then pauses.
func TestAnthropicAutoStart_NoSecondPingBeforeTheFirstIsJudged(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	claimedAt := *now
	pingOnce(t, a, wg, *now)
	a.starter.mu.Lock()
	a.starter.startedAt = claimedAt.Add(20 * time.Second) // the CLI took 20s
	a.starter.mu.Unlock()

	*now = claimedAt.Add(anthropicStarterCooldown)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want no second ping before the first is judged", got)
	}
	if a.starter.isStalled() {
		t.Fatal("paused before a full cooldown after the ping finished")
	}

	*now = claimedAt.Add(20*time.Second + anthropicStarterCooldown)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused once the session failed to show up")
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}
}

// A reset time Anthropic sent but onWatch could not read says nothing about
// the session, even with nothing used.
func TestFiveHourSessionState_UnreadableResetIsUnknown(t *testing.T) {
	util, bad := 0.0, "2026-11-25 14:00"
	resp := api.AnthropicQuotaResponse{"five_hour": {Utilization: &util, ResetsAt: &bad}}
	snap := resp.ToSnapshot(time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC))
	if got := fiveHourSessionState(snap); got != fiveHourUnknown {
		t.Fatalf("state = %v, want unknown", got)
	}
	empty := ""
	resp = api.AnthropicQuotaResponse{"five_hour": {Utilization: &util, ResetsAt: &empty}}
	if got := fiveHourSessionState(resp.ToSnapshot(time.Now())); got != fiveHourInactive {
		t.Fatalf("state = %v for an empty reset, want inactive", got)
	}
}

// A statusline snapshot that still shows the session from before the ping,
// or a running session after a pause, neither confirms the ping nor lifts the
// pause. Only API data does.
func TestAnthropicAutoStart_StatuslineRunningDoesNotConfirmOrLiftPause(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	pingOnce(t, a, wg, *now)

	// Written before the ping, reset still ahead of its capture time.
	old := now.Add(-6 * time.Minute)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(old, 90, timePtr(now.Add(-time.Minute))))
	if !a.starter.awaitingConfirmation(*now) {
		t.Fatal("a statusline snapshot from before the ping confirmed it")
	}

	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused")
	}
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 5, timePtr(now.Add(4*time.Hour))))
	if !a.starter.isStalled() {
		t.Fatal("statusline data lifted the pause")
	}
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 5, timePtr(now.Add(4*time.Hour))))
	if a.starter.isStalled() {
		t.Fatal("API data showing a running session did not lift the pause")
	}
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}
}

// A pause lifts on its own after a full rate window. A setup that is still
// wrong then pauses again without a second pause alert, so a wrong-account
// setup does not alert every cycle; a ping that is seen to work re-arms it.
func TestAnthropicAutoStart_PauseLiftsAfterRateWindow(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	engine, delivered := starterWebhookEngine(t)
	a.SetNotifier(engine)
	pingOnce(t, a, wg, *now)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused")
	}
	waitDelivered(t, delivered, 2)

	*now = now.Add(anthropicStarterRateWindow - time.Minute)
	if !a.starter.isStalled() {
		t.Fatal("pause lifted before the rate window")
	}
	*now = now.Add(time.Minute)
	pingOnce(t, a, wg, *now)
	if got := pinger.calls.Load(); got != 2 {
		t.Fatalf("pings = %d, want the starter to resume after the rate window", got)
	}
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused again")
	}
	events := waitDelivered(t, delivered, 4)
	if len(events) != 3 || events[2] != "starter_success" {
		t.Fatalf("delivered = %v, want no second pause alert", events)
	}

	// The user fixes the login: the next ping is seen to start the session,
	// which re-arms the alert for a later pause.
	*now = now.Add(anthropicStarterRateWindow)
	pingOnce(t, a, wg, *now)
	*now = now.Add(time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 1, timePtr(now.Add(4*time.Hour))))
	*now = now.Add(5 * time.Hour)
	pingOnce(t, a, wg, *now)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	events = waitDelivered(t, delivered, 6)
	if len(events) != 6 || events[5] != "starter_failure" {
		t.Fatalf("delivered = %v, want the pause alert re-armed after a working ping", events)
	}
}

// In statusline-only mode the starter never pings, as the settings hint says;
// in hybrid mode the statusline can still start one.
func TestAnthropicAutoStart_StatuslineOnlyModeNeverPings(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	a.SetAPIPollingDisabled(true)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d in statusline-only mode, want 0", got)
	}
	a.SetAPIPollingDisabled(false)
	wg.Add(1)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 0, nil))
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d in hybrid mode, want 1", got)
	}
}

// While the starter's own claude turn runs, the agent treats Claude Code as
// running, so it does not refresh the shared OAuth token at the same time,
// even with Claude Code detection switched off.
func TestAnthropicAutoStart_InFlightPingCountsAsClaudeCodeRunning(t *testing.T) {
	a, _, now, _, wg := starterTestAgent(t)
	a.isClaudeCodeRunning = func() bool { return false }
	if a.claudeCodeRunning() {
		t.Fatal("claude reported running with no ping in flight")
	}
	block := make(chan struct{})
	pinger := &fakeStarterPinger{block: block}
	a.starter.pinger = pinger
	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	deadline := time.Now().Add(5 * time.Second)
	for pinger.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !a.claudeCodeRunning() {
		t.Fatal("a ping in flight did not count as Claude Code running")
	}
	close(block)
	wg.Wait()
	if a.claudeCodeRunning() {
		t.Fatal("still reported running after the ping ended")
	}
}

func TestAnthropicStarterFailureKey_MasksVolatileParts(t *testing.T) {
	a := errors.New("anthropic starter: turn did not succeed: API Error: 529 Overloaded (request req_011CabC)")
	b := errors.New("anthropic starter: turn did not succeed: API Error: 529 Overloaded (request req_022XyZ)")
	if anthropicStarterFailureKey(a) != anthropicStarterFailureKey(b) {
		t.Fatalf("keys differ: %q vs %q", anthropicStarterFailureKey(a), anthropicStarterFailureKey(b))
	}
	c := errors.New("anthropic starter: turn did not succeed: usage limit reached")
	if anthropicStarterFailureKey(a) == anthropicStarterFailureKey(c) {
		t.Fatal("different failures compare as the same")
	}
	// Short status and exit codes are part of what failed.
	d := errors.New("anthropic starter: claude exited with exit status 127: API Error: 500")
	e := errors.New("anthropic starter: claude exited with exit status 1: API Error: 529")
	if anthropicStarterFailureKey(d) == anthropicStarterFailureKey(e) {
		t.Fatal("different exit and status codes compare as the same")
	}
	// A long number, like a timestamp, still varies from run to run.
	f := errors.New("anthropic starter: turn did not succeed: retry after 1764115200")
	g := errors.New("anthropic starter: turn did not succeed: retry after 1764118800")
	if anthropicStarterFailureKey(f) != anthropicStarterFailureKey(g) {
		t.Fatal("a changing timestamp made the same failure look new")
	}
}

// The toggle is read on every poll, so switching auto-start off lifts a pause
// even when that poll's data says nothing (stale or no five_hour quota).
func TestAnthropicAutoStart_ToggleOffSeenOnAnyPoll(t *testing.T) {
	a, _, now, enabled, wg := starterTestAgent(t)
	pingOnce(t, a, wg, *now)
	*now = now.Add(anthropicStarterCooldown + time.Minute)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if !a.starter.isStalled() {
		t.Fatal("want the starter paused")
	}
	enabled.Store(false)
	a.maybeAutoStartSession(context.Background(), &api.AnthropicSnapshot{CapturedAt: now.Add(-time.Hour)})
	if a.starter.isStalled() {
		t.Fatal("a stale poll with auto-start off did not lift the pause")
	}
}

// In hybrid mode, a cycle that also polls the API leaves the auto-start
// decision to the API snapshot: here the statusline still shows an expired
// window, but the API shows a session started elsewhere, so no ping is sent.
// On a statusline-only cycle the statusline decides.
func TestAnthropicAgent_HybridCycleLetsTheAPIDecide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reset := time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339)
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":3,"resets_at":"` + reset + `"}}`))
	}))
	defer server.Close()
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer str.Close()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	a := NewAnthropicAgent(client, str, nil, time.Minute, logger, nil)

	path := filepath.Join(t.TempDir(), "anthropic-statusline.json")
	expired := time.Now().Add(-time.Hour).Unix()
	payload := fmt.Sprintf(`{"rate_limits":{"five_hour":{"used_percentage":40,"resets_at":%d},"seven_day":{"used_percentage":10,"resets_at":%d}}}`,
		expired, time.Now().Add(72*time.Hour).Unix())
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	a.statuslinePath = path
	a.statuslineStaleness = time.Hour
	a.SetAPIPollCycleInterval(10) // hybrid: an API poll every 10 cycles

	pinger := &fakeStarterPinger{}
	a.SetAutoStartCheck(func() bool { return true })
	a.starter.pinger = pinger

	// No API reading yet, so this cycle polls the API too.
	a.poll(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d on a hybrid API cycle with a running session, want 0", got)
	}

	// The next cycle is statusline-only (the API was just polled), with the
	// statusline rewritten but still showing the expired window. The API saw
	// a session running until a later reset, so that is not news: no ping.
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	a.poll(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d while the API's session is still running, want 0", got)
	}
}

// After the reset the API reported, a statusline snapshot showing no session
// can start one again.
func TestAnthropicAutoStart_StatuslineWaitsForTheAPIReset(t *testing.T) {
	a, pinger, now, _, wg := starterTestAgent(t)
	reset := now.Add(2 * time.Hour)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 5, timePtr(reset)))

	*now = now.Add(time.Hour)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 0, nil))
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d before the API's reset, want 0", got)
	}

	*now = reset.Add(time.Minute)
	wg.Add(1)
	a.maybeAutoStartSessionFromStatusline(context.Background(), fiveHourSnapshot(*now, 0, nil))
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d after the API's reset, want 1", got)
	}
}

// An API snapshot showing the session ended early clears the running reading,
// so the statusline is no longer held back until the old reset time.
func TestAnthropicAutoStart_APIInactiveForgetsRunningReading(t *testing.T) {
	a, _, now, _, wg := starterTestAgent(t)
	reset := now.Add(2 * time.Hour)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 5, timePtr(reset)))
	if !a.starter.apiSessionRunningAt(*now) {
		t.Fatal("a running API snapshot was not recorded")
	}

	*now = now.Add(time.Hour)
	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), fiveHourSnapshot(*now, 0, timePtr(now.Add(-time.Minute))))
	wg.Wait()
	if a.starter.apiSessionRunningAt(*now) {
		t.Fatal("the API showed the session ended, but the old reset still holds back the statusline")
	}
}

// With the weekly limit used up, every turn is refused, so no ping is sent
// until the weekly window resets.
func TestAnthropicAutoStart_WeeklyLimitReached(t *testing.T) {
	weekly := func(at time.Time, util float64, reset *time.Time) *api.AnthropicSnapshot {
		snap := fiveHourSnapshot(at, 0, nil)
		snap.Quotas[1] = api.AnthropicQuota{Name: "seven_day", Utilization: util, ResetsAt: reset}
		return snap
	}
	a, pinger, now, _, wg := starterTestAgent(t)

	a.maybeAutoStartSession(context.Background(), weekly(*now, 100, timePtr(now.Add(24*time.Hour))))
	a.maybeAutoStartSession(context.Background(), weekly(*now, 100, nil))
	if got := pinger.calls.Load(); got != 0 {
		t.Fatalf("pings = %d with the weekly limit used up, want 0", got)
	}

	// Just under the limit, or once its reset has passed, a ping is sent.
	wg.Add(1)
	a.maybeAutoStartSession(context.Background(), weekly(*now, 99.5, timePtr(now.Add(24*time.Hour))))
	wg.Wait()
	if got := pinger.calls.Load(); got != 1 {
		t.Fatalf("pings = %d just under the weekly limit, want 1", got)
	}
	b, pingerB, nowB, _, wgB := starterTestAgent(t)
	wgB.Add(1)
	b.maybeAutoStartSession(context.Background(), weekly(*nowB, 100, timePtr(nowB.Add(-time.Minute))))
	wgB.Wait()
	if got := pingerB.calls.Load(); got != 1 {
		t.Fatalf("pings = %d after the weekly reset passed, want 1", got)
	}
}

// A ping still running when the agent stops is waited for, so its cancelled
// CLI is killed before the agent reports it has stopped.
func TestAnthropicAutoStart_ShutdownWaitsForPing(t *testing.T) {
	a, pinger, now, _, _ := starterTestAgent(t)
	a.starter.done = nil
	pinger.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.maybeAutoStartSession(ctx, fiveHourSnapshot(*now, 0, nil))
	deadline := time.Now().Add(2 * time.Second)
	for !a.starter.pingInFlight() {
		if time.Now().After(deadline) {
			t.Fatal("ping never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if a.starter.waitForPings(50 * time.Millisecond) {
		t.Fatal("waitForPings returned while a ping was still running")
	}
	cancel()
	if !a.starter.waitForPings(2 * time.Second) {
		t.Fatal("a cancelled ping was not waited for")
	}
	if a.starter.pingInFlight() {
		t.Fatal("ping still marked in flight after it returned")
	}
}
