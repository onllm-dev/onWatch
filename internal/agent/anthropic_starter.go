package agent

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
)

// Anthropic auto quota-starter (Beta, issue #162). Opt-in and off by default.
//
// When a fresh poll shows that no five-hour session is running, the agent
// sends one tiny Claude Code turn so the next five-hour window is already
// counting down when the user comes back. Only the five-hour window is
// managed: Claude's weekly limits reset on a fixed schedule whatever the
// activity, so starting them is pointless.
//
// Duplicate and runaway starts are bounded three ways:
//   - one ping at a time per agent;
//   - after any ping, none for anthropicStarterCooldown, which covers polls
//     whose data was read before the new session showed up;
//   - at most anthropicStarterMaxFires pings in any rolling
//     anthropicStarterRateWindow, so a start that never takes (for example a
//     logged-out CLI) costs a handful of attempts, not one per poll.
const (
	anthropicStarterQuota      = "five_hour"
	anthropicStarterWeekly     = "seven_day"
	anthropicStarterMaxFires   = 4
	anthropicStarterRateWindow = 5 * time.Hour
	anthropicStarterCooldown   = 15 * time.Minute
	anthropicStarterMaxDataAge = 10 * time.Minute
	// anthropicStarterJudgeWindow is how long after a ping a snapshot can
	// still say whether it started a session. A session started by the ping
	// shows within minutes; a later snapshot could come after that session
	// already ended (Anthropic rounds its reset time, so a session can end
	// well before a full five hours), so it says nothing about the ping.
	anthropicStarterJudgeWindow = time.Hour
	anthropicStarterPingTimeout = 2 * time.Minute
)

// anthropicStarterPinger sends one starter turn. *api.AnthropicStarter
// implements it; tests use a fake.
type anthropicStarterPinger interface {
	Ping(ctx context.Context) error
	Model() string
}

// anthropicStarterState is the auto quota-starter's state, shared between the
// poll goroutine and the ping goroutine.
type anthropicStarterState struct {
	check  func() bool
	pinger anthropicStarterPinger
	clock  func() time.Time

	mu       sync.Mutex
	inFlight bool
	lastFire time.Time
	fires    []time.Time
	// lastFailure is the error text of the last failed ping, cleared by a
	// success. A failure that repeats it is logged but not sent to webhooks
	// again, so a setup that cannot work (no CLI, logged out) alerts once,
	// not every few hours.
	lastFailure string
	// startedAt is when the last ping succeeded, until a fresh poll shows the
	// session running. If polls taken a full cooldown later still show no
	// session, the ping started some other session (the claude CLI is logged
	// in to a different account than the one onWatch tracks), so the starter
	// stops and says so once. The pause (stalled, from pausedAt) lifts when
	// a session is seen running, when auto-start is seen switched off, or
	// after a full rate window, so a fixed login resumes on its own.
	startedAt time.Time
	stalled   bool
	pausedAt  time.Time
	// apiRunningUntil is the reset time of the last API snapshot that showed
	// a session running. Until then an "inactive" statusline snapshot is
	// stale (the file only changes on interactive replies), not news.
	apiRunningUntil time.Time
	// pauseAnnounced keeps a wrong-account setup from alerting on every
	// cycle: it is set by the first pause alert and cleared only when a ping
	// is seen to start the tracked session or auto-start is switched off.
	pauseAnnounced bool
	// lastSkip is the last reason a ping was skipped. Only a new reason is
	// logged at Info; a repeat on every poll goes to Debug.
	lastSkip string

	// done, when set, is called after each ping finishes. Tests only.
	done func()

	// pings tracks running ping goroutines, so Run can wait for a cancelled
	// ping to kill its CLI before the agent reports it has stopped.
	pings sync.WaitGroup
}

// anthropicStarterShutdownWait bounds how long Run waits, after its context
// ends, for an in-flight ping to kill its CLI and return. It stays under the
// agent manager's stop wait.
var anthropicStarterShutdownWait = 4 * time.Second

// waitForPings waits up to limit for running pings to return.
func (s *anthropicStarterState) waitForPings(limit time.Duration) bool {
	finished := make(chan struct{})
	go func() {
		s.pings.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return true
	case <-time.After(limit):
		return false
	}
}

// SetAutoStartCheck wires the auto quota-starter (Beta) enablement callback.
// It is read fresh on every poll, so a dashboard toggle takes effect without
// a daemon restart. A nil callback disables the starter.
func (a *AnthropicAgent) SetAutoStartCheck(fn func() bool) {
	a.starter.check = fn
	if fn != nil && a.starter.pinger == nil {
		a.starter.pinger = api.NewAnthropicStarter()
	}
}

func (s *anthropicStarterState) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// fiveHourSession is what a snapshot says about the five-hour session.
type fiveHourSession int

const (
	fiveHourUnknown  fiveHourSession = iota // no usable five_hour data
	fiveHourInactive                        // no session running
	fiveHourRunning                         // a session is running
)

// fiveHourSessionState reads the five-hour session from a snapshot. The window
// is inactive when its reset time is missing with nothing used, or has already
// passed when the data was captured, and running when its reset time is still
// ahead. A snapshot without a five_hour quota (Anthropic sent null), with
// usage but no reset time, or with a reset time that could not be read, says
// nothing either way.
func fiveHourSessionState(snapshot *api.AnthropicSnapshot) fiveHourSession {
	if snapshot == nil {
		return fiveHourUnknown
	}
	for _, q := range snapshot.Quotas {
		if q.Name != anthropicStarterQuota {
			continue
		}
		switch {
		case q.ResetsAtUnparsed:
			return fiveHourUnknown
		case q.ResetsAt == nil && q.Utilization == 0:
			return fiveHourInactive
		case q.ResetsAt == nil:
			return fiveHourUnknown
		case !q.ResetsAt.After(snapshot.CapturedAt):
			return fiveHourInactive
		default:
			return fiveHourRunning
		}
	}
	return fiveHourUnknown
}

// maybeAutoStartSession runs after a snapshot is stored. It fires one starter
// ping in the background when the feature is on, the data is fresh, and no
// five-hour session is running.
func (a *AnthropicAgent) maybeAutoStartSession(ctx context.Context, snapshot *api.AnthropicSnapshot) {
	a.autoStartFromSnapshot(ctx, snapshot, false)
}

// maybeAutoStartSessionFromStatusline is maybeAutoStartSession for a snapshot
// read from Claude Code's statusline file. That file only changes when an
// interactive Claude Code session gets a response, never for the starter's
// own `claude -p` turn, so after a ping it can keep showing the old, expired
// reset. While a ping waits to be confirmed, such a snapshot neither judges
// it (a false "did not start" pause) nor sends another one; the next API
// snapshot does.
//
// In statusline-only mode no API snapshot ever comes to confirm or refute a
// ping, so the starter does not run there at all (the settings hint says so).
//
// In auto mode a fresh statusline snapshot nearly always reads as running:
// Claude Code writes the file during a session and the snapshot carries the
// file's time, so the reset is still ahead of it. The start therefore
// normally comes from the next API cycle; this path only fires for a fresh
// file that already shows an ended session.
func (a *AnthropicAgent) maybeAutoStartSessionFromStatusline(ctx context.Context, snapshot *api.AnthropicSnapshot) {
	if a.apiPollingDisabled {
		return
	}
	a.autoStartFromSnapshot(ctx, snapshot, true)
}

func (a *AnthropicAgent) autoStartFromSnapshot(ctx context.Context, snapshot *api.AnthropicSnapshot, fromStatusline bool) {
	s := &a.starter
	if s.check == nil || s.pinger == nil || snapshot == nil {
		return
	}
	now := s.now()
	// Read the toggle on every poll, whatever the data shows: turning
	// auto-start off lifts a pause, so a user who fixed the CLI login can
	// switch it off and on again to resume.
	if !s.check() {
		s.clearPause()
		return
	}
	if now.Sub(snapshot.CapturedAt) > anthropicStarterMaxDataAge {
		return
	}
	switch fiveHourSessionState(snapshot) {
	case fiveHourRunning:
		// Only the API's data confirms a ping or lifts a pause. The
		// statusline file can still show the session that ran before the
		// ping, and it reflects the account the claude CLI uses, which is
		// the one in question after a pause.
		if !fromStatusline {
			s.sessionSeenRunning()
			s.noteAPISessionRunning(snapshot)
		}
		return
	case fiveHourUnknown:
		return
	}
	if !fromStatusline {
		// The API now shows no session, so an earlier running reading no
		// longer holds back the statusline (Anthropic can end a window
		// before the reset time it first reported).
		s.forgetAPISessionRunning()
	}
	if fromStatusline && (s.awaitingConfirmation(snapshot.CapturedAt) || s.apiSessionRunningAt(now)) {
		return
	}
	if s.pingDidNotStartSession(snapshot.CapturedAt) {
		msg := "The last starter ping went through, but the tracked five-hour session did not start. The claude CLI may be logged in to a different account than the one onWatch tracks. Auto-start pauses for five hours, or until a session is seen running or auto-start is switched off and on again."
		a.logger.Warn("Anthropic auto quota-starter paused", "quota", anthropicStarterQuota, "reason", msg)
		if s.announcePause() {
			a.sendAnthropicStarterEventPaused(msg)
		}
		return
	}
	claimed, reason := false, "paused until a five-hour session is seen running"
	switch {
	case s.isStalled():
	case weeklyLimitReached(snapshot):
		// Claude Code refuses every turn until the weekly limit resets, so
		// a ping would only fail.
		reason = "weekly limit reached"
	default:
		claimed, reason = s.claim(now)
	}
	if !claimed {
		if reason != "" {
			if s.noteSkip(reason) {
				a.logger.Info("Anthropic auto quota-starter skipped", "reason", reason)
			} else {
				a.logger.Debug("Anthropic auto quota-starter skipped", "reason", reason)
			}
		}
		return
	}
	s.pings.Add(1)
	go a.sendAnthropicStarterPing(ctx)
}

// claim reserves the next ping, or says why it cannot run. An empty reason
// means a ping is already in flight, which needs no log line.
func (s *anthropicStarterState) claim(now time.Time) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight {
		return false, ""
	}
	// A ping that went through waits for a poll to show its session, or to
	// show it did not start one (pingDidNotStartSession, which counts from
	// when the ping finished). Firing again first would skip that check.
	if !s.startedAt.IsZero() {
		return false, "waiting for the last ping's session to show up"
	}
	if !s.lastFire.IsZero() && now.Sub(s.lastFire) < anthropicStarterCooldown {
		return false, "cooldown after the last ping"
	}
	kept := s.fires[:0]
	for _, t := range s.fires {
		if now.Sub(t) < anthropicStarterRateWindow {
			kept = append(kept, t)
		}
	}
	s.fires = kept
	if len(s.fires) >= anthropicStarterMaxFires {
		return false, "rate cap reached"
	}
	s.fires = append(s.fires, now)
	s.lastFire = now
	s.inFlight = true
	s.lastSkip = ""
	return true, ""
}

// pingInFlight reports whether a starter ping is running now.
func (s *anthropicStarterState) pingInFlight() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight
}

func (s *anthropicStarterState) release() {
	s.mu.Lock()
	s.inFlight = false
	done := s.done
	s.mu.Unlock()
	if done != nil {
		done()
	}
}

// sendAnthropicStarterPing runs one starter turn and reports the outcome. It
// runs concurrently with poll(), so it touches only the starter state, the
// logger and the notifier, which are all safe for that.
func (a *AnthropicAgent) sendAnthropicStarterPing(ctx context.Context) {
	s := &a.starter
	defer s.pings.Done()
	defer s.release()
	model := s.pinger.Model()
	a.logger.Info("Anthropic auto quota-starter firing", "quota", anthropicStarterQuota, "model", model)

	pingCtx, cancel := context.WithTimeout(ctx, anthropicStarterPingTimeout)
	err := s.pinger.Ping(pingCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			a.logger.Info("Anthropic auto quota-starter ping cancelled", "quota", anthropicStarterQuota)
			return
		}
		a.logger.Warn("Anthropic auto quota-starter ping failed",
			"quota", anthropicStarterQuota, "model", model, "error", err)
		if s.noteFailure(anthropicStarterFailureKey(err)) {
			a.sendAnthropicStarterEvent(false, api.RedactHomePath(err.Error()))
		}
		return
	}
	s.noteSuccess(s.now())
	a.logger.Info("Anthropic auto quota-starter ping sent", "quota", anthropicStarterQuota, "model", model)
	a.sendAnthropicStarterEvent(true, "")
}

// anthropicStarterFailureKey is what a failure is compared by to decide
// whether it repeats the last one. A known cause compares by kind, because its
// text carries details from the CLI (a request id, a reset time) that change
// from run to run; anything else compares by its full text.
func anthropicStarterFailureKey(err error) string {
	for _, known := range []error{
		api.ErrClaudeCLINotFound,
		api.ErrAnthropicStarterAuth,
		api.ErrAnthropicStarterNotSubscription,
		context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	// Other failures compare with numbers and request ids masked, so an
	// "API Error: 529 Overloaded (request req_abc)" that repeats with a new
	// id is still the same failure.
	return anthropicStarterVolatile.ReplaceAllString(err.Error(), "#")
}

var anthropicStarterVolatile = regexp.MustCompile(`(?i)\breq_[a-z0-9]+|[0-9]{4,}`)

// sessionSeenRunning records that a fresh poll shows a running session, which
// confirms the last ping (if any) and lifts a pause.
func (s *anthropicStarterState) sessionSeenRunning() {
	s.mu.Lock()
	if !s.startedAt.IsZero() {
		s.pauseAnnounced = false // a ping just worked
	}
	s.startedAt = time.Time{}
	s.stalled = false
	s.mu.Unlock()
}

// pingDidNotStartSession reports, once, that data captured a full cooldown
// after a successful ping still shows no session, and pauses the starter.
// Data captured a judge window or more after the ping says nothing about it:
// a session it started could have run and ended in between (a laptop asleep,
// the daemon stopped, polls failing), so the ping is forgotten instead.
func (s *anthropicStarterState) pingDidNotStartSession(capturedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() || s.inFlight {
		return false
	}
	gap := capturedAt.Sub(s.startedAt)
	if gap < anthropicStarterCooldown {
		return false
	}
	if gap >= anthropicStarterJudgeWindow {
		s.startedAt = time.Time{}
		return false
	}
	s.startedAt = time.Time{}
	s.stalled = true
	s.pausedAt = s.now()
	return true
}

// awaitingConfirmation reports whether a successful ping has not yet been
// confirmed or refuted by a later snapshot. A ping a full rate window older
// than capturedAt is forgotten, so a setup with no API snapshots (statusline
// only) is not held back forever; by then any session it started has ended.
func (s *anthropicStarterState) awaitingConfirmation(capturedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() {
		return false
	}
	if capturedAt.Sub(s.startedAt) >= anthropicStarterRateWindow {
		s.startedAt = time.Time{}
		return false
	}
	return true
}

// clearPause lifts a pause and forgets an unconfirmed ping and the last
// failure, so a setup that is still wrong after the user turns auto-start
// back on alerts again.
func (s *anthropicStarterState) clearPause() {
	s.mu.Lock()
	s.startedAt = time.Time{}
	s.stalled = false
	s.lastFailure = ""
	s.pauseAnnounced = false
	s.mu.Unlock()
}

// noteAPISessionRunning records until when an API snapshot showed the
// five-hour session running.
func (s *anthropicStarterState) noteAPISessionRunning(snapshot *api.AnthropicSnapshot) {
	for _, q := range snapshot.Quotas {
		if q.Name == anthropicStarterQuota && q.ResetsAt != nil {
			s.mu.Lock()
			s.apiRunningUntil = *q.ResetsAt
			s.mu.Unlock()
			return
		}
	}
}

// forgetAPISessionRunning drops the last API running reading.
func (s *anthropicStarterState) forgetAPISessionRunning() {
	s.mu.Lock()
	s.apiRunningUntil = time.Time{}
	s.mu.Unlock()
}

// weeklyLimitReached reports whether a snapshot shows the weekly all-model
// limit used up, with its reset not yet passed when the data was captured.
func weeklyLimitReached(snapshot *api.AnthropicSnapshot) bool {
	for _, q := range snapshot.Quotas {
		if q.Name == anthropicStarterWeekly && q.Utilization >= 100 {
			return q.ResetsAt == nil || q.ResetsAt.After(snapshot.CapturedAt)
		}
	}
	return false
}

// apiSessionRunningAt reports whether the last API snapshot showed a session
// that is still running at now.
func (s *anthropicStarterState) apiSessionRunningAt(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.apiRunningUntil)
}

// announcePause reports whether a pause should be sent as an alert, once
// until the pause is cleared by a working ping or the toggle.
func (s *anthropicStarterState) announcePause() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pauseAnnounced {
		return false
	}
	s.pauseAnnounced = true
	return true
}

// isStalled reports whether the starter is paused. A pause a full rate window
// old lifts here, and the last failure is forgotten so a ping that still
// fails alerts again. A repeat pause stays quiet (announcePause) until a ping
// is seen to start the session or the toggle is switched off.
func (s *anthropicStarterState) isStalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stalled && s.now().Sub(s.pausedAt) >= anthropicStarterRateWindow {
		s.stalled = false
		s.lastFailure = ""
	}
	return s.stalled
}

// noteSkip records why a ping was skipped and reports whether the reason is
// new. A claimed ping resets it.
func (s *anthropicStarterState) noteSkip(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reason == s.lastSkip {
		return false
	}
	s.lastSkip = reason
	return true
}

// noteFailure records a failed ping and reports whether it is new, that is,
// different from the previous failure.
func (s *anthropicStarterState) noteFailure(msg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg == s.lastFailure {
		return false
	}
	s.lastFailure = msg
	return true
}

func (s *anthropicStarterState) noteSuccess(at time.Time) {
	s.mu.Lock()
	s.lastFailure = ""
	s.startedAt = at
	s.mu.Unlock()
}

func (a *AnthropicAgent) sendAnthropicStarterEvent(success bool, detail string) {
	if a.notifier == nil {
		return
	}
	ev := notify.StarterEvent{
		Provider: "anthropic",
		QuotaKey: anthropicStarterQuota,
		Success:  success,
		Detail:   detail,
	}
	if success {
		// The CLI turn went through. The next poll shows whether the session
		// actually started, so the message does not claim it already has.
		ev.Summary = "The auto quota-starter sent a Claude Code turn to start a new five-hour session."
		ev.Outcome = "starter turn sent"
	}
	a.notifier.SendStarterEvent(ev)
}

// sendAnthropicStarterEventPaused reports that the starter paused itself.
func (a *AnthropicAgent) sendAnthropicStarterEventPaused(detail string) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendStarterEvent(notify.StarterEvent{
		Provider: "anthropic",
		QuotaKey: anthropicStarterQuota,
		Paused:   true,
		Detail:   detail,
	})
}
