package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// maxCodexAuthFailures is the number of consecutive auth failures before pausing polling.
const maxCodexAuthFailures = 3

// Auto quota-starter (Beta) rate limiting. The starter is re-evaluated every
// poll (so a failed start is retried every second poll), but is hard
// capped to codexStarterMaxFires pings per account within a rolling
// codexStarterRateWindow. This guarantees we never loop or waste more than a
// handful of requests even if a start never "takes".
const (
	codexStarterMaxFires   = 5
	codexStarterRateWindow = 4 * time.Hour
)

// codexAuthPausedRetryInterval is the first delay before a paused agent
// re-attempts a poll with its stored credentials, and
// codexAuthPausedRetryMaxInterval caps the escalating schedule.
//
// Without bounded self-recovery the only exit from the paused state is a
// credential change on disk, which leaves polling dead indefinitely when the
// cause was server-side rather than a bad token (issue #127; the Anthropic
// equivalent is issue #111).
const (
	codexAuthPausedRetryInterval    = 15 * time.Minute
	codexAuthPausedRetryMaxInterval = 6 * time.Hour
)

// codexTokenRefreshThreshold is how soon before expiry we proactively refresh the token.
// Codex tokens expire weekly, so refreshing 6 hours early provides a comfortable buffer.
const codexTokenRefreshThreshold = 6 * time.Hour

// codexResetCreditsFetchInterval is how often the reset-credit detail endpoint
// is polled. The Codex CLI itself fetches details sparsely rather than on every
// usage refresh, and the count is already in the usage response, so details
// only need to refresh slowly (issue #152).
const codexResetCreditsFetchInterval = 30 * time.Minute

// codexResetCreditNotifyLead is how close to expiry a banked reset credit must
// be before onWatch sends its single warning. Fixed constant for Phase 1; the
// existing notification settings can silence the type but not change the lead.
const codexResetCreditNotifyLead = 24 * time.Hour

// CodexTokenRefreshFunc is called before each poll to get a fresh Codex token.
type CodexTokenRefreshFunc func() string

// CodexCredentialsRefreshFunc returns the full credentials for proactive OAuth refresh.
type CodexCredentialsRefreshFunc func() *api.CodexCredentials

// CodexTokenSaveFunc saves refreshed OAuth tokens to the appropriate storage.
// For named profiles this writes to the profile JSON file; for the default
// profile it writes to the global CODEX_HOME/auth.json.
//
// previousRefreshToken is the refresh token that the refresh just consumed (the
// pre-refresh one). A named profile can share that token with a system auth file
// (~/.codex/auth.json or OpenCode), so the save path uses it to write the
// rotation back to every file that still holds it. Passing the consumed token
// keeps the rule stateless: it covers every refresh, not only one that borrowed
// the credential this poll.
type CodexTokenSaveFunc func(accessToken, refreshToken, idToken string, expiresIn int, previousRefreshToken string) error

// writeCodexCredentialsNoPrevious is the nil-tokenSave fallback. It writes to
// the default auth.json path with no previous-token compare-and-swap, matching
// the legacy behaviour for callers that never installed a save hook.
func writeCodexCredentialsNoPrevious(accessToken, refreshToken, idToken string, expiresIn int, _ string) error {
	return api.WriteCodexCredentials(accessToken, refreshToken, idToken, expiresIn)
}

// codexOAuthRefreshFunc performs the OAuth token refresh. Production uses
// api.RefreshCodexToken; tests inject a fake so no real refresh is attempted.
type codexOAuthRefreshFunc func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error)

// codexRefreshCoordinator bounds recovery OAuth refreshes across agents that may
// share a credential source (for example a profile file and ~/.codex/auth.json
// holding the same token as the Codex CLI). claim admits at most one refresh per
// credential source per window.
type codexRefreshCoordinator struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newCodexRefreshCoordinator() *codexRefreshCoordinator {
	return &codexRefreshCoordinator{last: make(map[string]time.Time)}
}

// claim reserves one refresh for the fingerprint. It reports false when another
// agent already refreshed that fingerprint within the window.
func (c *codexRefreshCoordinator) claim(fingerprint string, now time.Time, window time.Duration) bool {
	if fingerprint == "" {
		return true // unknown identity: never block a lone agent
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.last[fingerprint]; ok && now.Sub(last) < window {
		return false
	}
	c.last[fingerprint] = now
	return true
}

// CodexCredentialFingerprintFunc returns a stable fingerprint of the credential
// source the agent polls with (a profile file, or the global Codex/OpenCode
// auth file). It changes only when the access or refresh token changes, so a
// re-authentication is distinguishable from onWatch's own bookkeeping rewrites.
// Empty means the source is unreadable.
type CodexCredentialFingerprintFunc func() string

// CodexPauseState describes one account whose Codex polling is paused pending
// re-authentication (or a bounded self-recovery retry).
type CodexPauseState struct {
	Paused bool      `json:"paused"`
	Since  time.Time `json:"since,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// codexTokenFingerprint hashes an access/refresh token pair. It never exposes
// the token values, only a digest, and is empty when both tokens are absent.
func codexTokenFingerprint(accessToken, refreshToken string) string {
	access := strings.TrimSpace(accessToken)
	refresh := strings.TrimSpace(refreshToken)
	if access == "" && refresh == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(access + "\x00" + refresh))
	return hex.EncodeToString(sum[:])
}

// codexCredentialFingerprint reads a Codex or OpenCode auth file and returns a
// token digest. Unreadable or malformed files yield an empty fingerprint.
func codexCredentialFingerprint(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var raw struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			Access       string `json:"access"`
			Refresh      string `json:"refresh"`
		} `json:"tokens"`
		OpenAI struct {
			Access  string `json:"access"`
			Refresh string `json:"refresh"`
		} `json:"openai"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	access := raw.Tokens.AccessToken
	if access == "" {
		access = raw.Tokens.Access
	}
	if access == "" {
		access = raw.OpenAI.Access
	}
	refresh := raw.Tokens.RefreshToken
	if refresh == "" {
		refresh = raw.Tokens.Refresh
	}
	if refresh == "" {
		refresh = raw.OpenAI.Refresh
	}
	return codexTokenFingerprint(access, refresh)
}

// isCodexAuthError returns true if the error is an authentication/authorization error.
func isCodexAuthError(err error) bool {
	return errors.Is(err, api.ErrCodexUnauthorized) || errors.Is(err, api.ErrCodexForbidden)
}

// CodexAgent manages the background polling loop for Codex quota tracking.
type CodexAgent struct {
	client                *api.CodexClient
	store                 *store.Store
	tracker               *tracker.CodexTracker
	interval              time.Duration
	logger                *slog.Logger
	sm                    *SessionManager
	notifier              *notify.NotificationEngine
	pollingCheck          func() bool
	tokenRefresh          CodexTokenRefreshFunc
	credsRefresh          CodexCredentialsRefreshFunc
	tokenSave             CodexTokenSaveFunc
	credentialFingerprint CodexCredentialFingerprintFunc
	oauthRefresh          codexOAuthRefreshFunc
	lastToken             string

	// Multi-account support
	accountID   int64  // Database account ID from provider_accounts
	profileName string // Profile name for logging

	// Auth failure rate limiting
	authFailCount            int
	authPaused               bool
	lastFailedToken          string
	proactiveRefreshFailures int // consecutive proactive refresh failures (non-reused-token)

	// authPausedAt is when the current pause began, and authPauseLogged tracks
	// whether the pause notice has been emitted for it, so the message is
	// logged on the state change instead of on every tick.
	authPausedAt    time.Time
	authPauseLogged bool

	// lastFailedFingerprint is the credential fingerprint at the moment polling
	// was paused. A different fingerprint while paused means the user
	// re-authenticated, which lifts the pause.
	lastFailedFingerprint string

	// Bounded self-recovery while authPaused: authRetryAt is when the next
	// recovery poll is allowed and authRetryCount drives the escalating backoff.
	authRetryAt    time.Time
	authRetryCount int

	// pauseSnapshot is the published, goroutine-safe view of the pause state.
	// The fields above are owned by the poll goroutine; the web layer reads
	// AuthPaused/PauseState from handler goroutines, so those read this atomic
	// snapshot instead of the raw fields (issue #147 review). nil means not
	// paused.
	pauseSnapshot atomic.Pointer[CodexPauseState]

	// refreshCoordinator rate-limits recovery refreshes across agents sharing a
	// credential source. refreshTerminalFingerprint is the fingerprint whose
	// refresh token is known dead (reused/invalid_grant/401): while it still
	// matches the credential source, no further refresh is attempted. A
	// re-authentication changes the fingerprint and clears this implicitly.
	//
	// Both are in-memory, process-lifetime state: a restart forgets them, which
	// is acceptable. A refreshed token is persisted before use, so on restart the
	// agent re-reads the latest token from disk; the worst case after a restart is
	// one redundant refresh of a token that is legitimately expiring.
	refreshCoordinator         *codexRefreshCoordinator
	refreshTerminalFingerprint string

	// now overrides the clock used by the auth-pause schedule. Tests only;
	// nil means time.Now. Snapshot and quota-window timestamps deliberately
	// keep using the real clock.
	now func() time.Time

	// Auto quota-starter (Beta).
	// codexAccountID is the Codex account_id (string) used for the
	// ChatGPT-Account-ID header on starter pings. autoStartCheck reports, fresh
	// per poll, whether auto-start is enabled for a given window ("five_hour" /
	// "seven_day"). starterFires (guarded by starterMu) holds recent ping times
	// for the rolling rate cap. starterGen (also guarded) goes up when a ping is
	// claimed and again when it finishes, so it is odd while one is in flight;
	// both are read/written from a goroutine concurrent with poll(). starterSeen
	// holds each window's reset time from the previous poll and is owned by
	// poll().
	codexAccountID string
	autoStartCheck func(quotaName string) bool
	starterMu      sync.Mutex
	starterFires   []time.Time
	starterGen     uint64
	starterSeen    map[string]codexResetObservation

	// Banked reset credits (issue #152). resetCreditsCheckedAt is the last
	// detail fetch, resetCreditsCount the usage count seen at that time (with
	// resetCreditsCountKnown distinguishing absent from 0), and
	// resetCreditsFetchedOnce forces a baseline fetch on the first poll.
	// All three are owned by the poll goroutine.
	resetCreditsCheckedAt   time.Time
	resetCreditsCount       int64
	resetCreditsCountKnown  bool
	resetCreditsFetchedOnce bool
	profileLabel            string // human account label for reset-credit notifications
}

// codexResetObservation is a window's reset time as seen by one poll, with
// when that poll's usage request was sent and starterGen at that point.
type codexResetObservation struct {
	resetsAt time.Time
	at       time.Time
	gen      uint64
}

// codexAutoStartWindowSeconds returns the nominal length of an auto-startable
// Codex quota window, or 0 for windows the starter does not manage.
func codexAutoStartWindowSeconds(quotaName string) int64 {
	switch quotaName {
	case "five_hour":
		return 5 * 60 * 60
	case "seven_day":
		return 7 * 24 * 60 * 60
	default:
		return 0
	}
}

// codexUnstartedTolerance is how close the time-until-reset must be to the full
// window length to treat the window as "unstarted". An unstarted Codex window
// reports reset_at = now + window (it rolls forward every poll, so the countdown
// stays pinned at ~the full length). Once a turn is sent the window's reset_at
// becomes fixed and the countdown starts decreasing, dropping below this band.
const codexUnstartedTolerance = 2 * time.Minute

// codexRollingSlack is how far a rolling window's reset time may drift from the
// time elapsed between two usage requests: reset times are whole seconds, plus
// a margin for request latency varying between the polls. codexRollingMinGap
// is the shortest gap between polls that tells a fixed reset apart from it.
const (
	codexRollingSlack  = 2 * time.Second
	codexRollingMinGap = 5 * time.Second
)

// isUnstartedCodexWindow reports whether a quota window looks unstarted: its
// time-until-reset is within codexUnstartedTolerance of the full window length.
func isUnstartedCodexWindow(quotaName string, resetsAt *time.Time, now time.Time) bool {
	windowSec := codexAutoStartWindowSeconds(quotaName)
	if windowSec == 0 || resetsAt == nil {
		return false
	}
	remaining := resetsAt.Sub(now)
	if remaining <= 0 {
		return false
	}
	full := time.Duration(windowSec) * time.Second
	return remaining >= full-codexUnstartedTolerance
}

// SetCodexAccountID sets the Codex account_id used for the ChatGPT-Account-ID
// header on usage requests and auto quota-starter pings.
func (a *CodexAgent) SetCodexAccountID(id string) {
	a.codexAccountID = id
	a.client.SetAccountID(id)
}

// SetProfileLabel sets a human-readable account label used in notifications
// (the profile name, never a token).
func (a *CodexAgent) SetProfileLabel(label string) {
	a.profileLabel = strings.TrimSpace(label)
}

// resetCreditsDue reports whether this poll should fetch reset-credit details:
// once for a baseline, then on the slow cadence, or immediately when the usage
// count changed since the last fetch.
func (a *CodexAgent) resetCreditsDue(now time.Time, snapshot *api.CodexSnapshot) bool {
	if !a.resetCreditsFetchedOnce {
		return true
	}
	count := snapshot.ResetCreditsAvailableCount
	if count.Valid && (!a.resetCreditsCountKnown || count.Value != a.resetCreditsCount) {
		return true
	}
	return now.Sub(a.resetCreditsCheckedAt) >= codexResetCreditsFetchInterval
}

// maybeFetchResetCredits refreshes banked reset credits when due. It is strictly
// informational: every failure is logged at debug/info and leaves usage
// polling, pause state and the #147 failure counters untouched. It never
// refreshes tokens and never redeems anything.
func (a *CodexAgent) maybeFetchResetCredits(ctx context.Context, snapshot *api.CodexSnapshot, now time.Time) {
	if a.store == nil {
		return
	}
	if !a.resetCreditsDue(now, snapshot) {
		return
	}

	// Mark the attempt before the request so a hanging or failing endpoint is
	// retried only after the slow cadence, never on every usage poll.
	a.resetCreditsFetchedOnce = true
	a.resetCreditsCheckedAt = now
	if count := snapshot.ResetCreditsAvailableCount; count.Valid {
		a.resetCreditsCount = count.Value
		a.resetCreditsCountKnown = true
	}

	resp, err := a.client.FetchResetCredits(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		a.recordResetCreditsUnavailable(now)
		a.logger.Debug("Codex reset credits unavailable",
			"error", err, "account_id", a.accountID)
		return
	}

	records := make([]store.CodexResetCreditRecord, 0, len(resp.Credits))
	parsed := make([]api.CodexResetCredit, 0, len(resp.Credits))
	for _, c := range resp.Credits {
		records = append(records, store.CodexResetCreditRecord{
			CreditID:          c.ID,
			Status:            c.Status.String(),
			ResetType:         c.ResetType,
			Title:             c.Title,
			Description:       c.Description,
			GrantedAt:         c.GrantedAt,
			ExpiresAt:         c.ExpiresAt,
			UnsupportedByPlan: c.UnsupportedByPlan,
			ExpiryUnknown:     c.ExpiryUnknown,
		})
		parsed = append(parsed, c)
	}
	if err := a.store.SyncCodexResetCredits(a.accountID, records, now); err != nil {
		a.logger.Warn("Failed to store Codex reset credits", "error", err, "account_id", a.accountID)
		// Storage failed, so nothing below can be trusted; the next due fetch retries.
		a.recordResetCreditsUnavailable(now)
		return
	}

	// nil when the detail payload omitted the count: unknown, never 0.
	if err := a.store.SetCodexResetCreditCheckStatus(a.accountID, "ok", resp.AvailableCount, resp.ApplicableAvailableCount, now); err != nil {
		a.logger.Warn("Failed to record Codex reset-credit check", "error", err, "account_id", a.accountID)
	}

	a.notifyExpiringResetCredits(ctx, parsed, now)
}

// recordResetCreditsUnavailable stores the last-check outcome as unavailable.
func (a *CodexAgent) recordResetCreditsUnavailable(now time.Time) {
	if err := a.store.SetCodexResetCreditCheckStatus(a.accountID, "unavailable", nil, nil, now); err != nil {
		a.logger.Debug("Failed to record Codex reset-credit check status", "error", err, "account_id", a.accountID)
	}
}

// notifyExpiringResetCredits sends at most one warning per credit inside the
// lead window. The notified_at marker is claimed atomically before sending;
// a send that reached no channel releases the claim so a later fetch retries.
// Never-expiring credits are skipped by construction (the store query requires
// a non-null expiry).
func (a *CodexAgent) notifyExpiringResetCredits(ctx context.Context, credits []api.CodexResetCredit, now time.Time) {
	if a.notifier == nil {
		return
	}
	pending, err := a.store.QueryCodexResetCreditsNeedingExpiryNotice(a.accountID, now, codexResetCreditNotifyLead)
	if err != nil {
		a.logger.Warn("Failed to query Codex credits needing expiry notice", "error", err, "account_id", a.accountID)
		return
	}
	if len(pending) == 0 {
		return
	}
	byID := make(map[string]api.CodexResetCredit, len(credits))
	for _, c := range credits {
		byID[c.ID] = c
	}
	accountName := a.resetCreditsAccountName()
	for _, row := range pending {
		if ctx.Err() != nil {
			return
		}
		if row.ExpiresAt == nil {
			continue
		}
		claimedAt := time.Now().UTC()
		claimed, err := a.store.ClaimCodexResetCreditNotice(a.accountID, row.CreditID, claimedAt)
		if err != nil {
			a.logger.Warn("Failed to claim Codex reset-credit notice", "error", err, "account_id", a.accountID)
			continue
		}
		if !claimed {
			continue // another poller or a previous run already sent it
		}

		title := row.Title
		if credit, ok := byID[row.CreditID]; ok && credit.Title != "" {
			title = credit.Title
		}
		sent := a.notifier.SendResetCreditExpiryNotification(notify.ResetCreditExpiryAlert{
			AccountID:   fmt.Sprintf("%d", a.accountID),
			AccountName: accountName,
			CreditID:    row.CreditID,
			Title:       title,
			ExpiresAt:   *row.ExpiresAt,
		})
		if !sent {
			if err := a.store.ReleaseCodexResetCreditNotice(a.accountID, row.CreditID, claimedAt); err != nil {
				a.logger.Debug("Failed to release Codex reset-credit notice claim", "error", err)
			}
			continue
		}
		a.logger.Info("Sent Codex reset-credit expiry notification",
			"credit_id", row.CreditID,
			"expires_at", row.ExpiresAt.UTC(),
			"account_id", a.accountID)
	}
}

// resetCreditsAccountName returns the human label used in notifications. It
// prefers the configured profile label and never falls back to credentials.
func (a *CodexAgent) resetCreditsAccountName() string {
	if a.profileLabel != "" {
		return a.profileLabel
	}
	return fmt.Sprintf("Codex account %d", a.accountID)
}

// SetAutoStartCheck wires a callback that reports, fresh per poll, whether the
// auto quota-starter (Beta) is enabled for a given window. Read fresh so a
// dashboard toggle takes effect without a daemon restart.
func (a *CodexAgent) SetAutoStartCheck(fn func(quotaName string) bool) {
	a.autoStartCheck = fn
}

// codexStarterPoll describes the usage fetch behind one poll's quotas: when the
// request was sent and starterGen read just before it.
type codexStarterPoll struct {
	fetchedAt time.Time
	gen       uint64
}

// maybeAutoStartWindows inspects the freshly polled quotas and, if any enabled
// window is observed unstarted, fires one rate-capped starter ping. One turn
// counts against every window, so a single ping starts them all. Pings run in
// a goroutine so they never block the poll.
func (a *CodexAgent) maybeAutoStartWindows(ctx context.Context, quotas []api.CodexQuota, now time.Time, sp codexStarterPoll) {
	if a.autoStartCheck == nil {
		return
	}
	// A ping in flight or finishing during the fetch may have started a window
	// between the server's reading and now, so this poll is not comparable.
	comparable := sp.gen%2 == 0 && a.starterGeneration() == sp.gen
	var windows []string
	for _, q := range quotas {
		if codexAutoStartWindowSeconds(q.Name) == 0 {
			continue
		}
		// Record every managed window before any other check, so the next
		// poll has a previous reset time to compare against.
		rolling := a.resetRolling(q.Name, q.ResetsAt, sp, comparable)
		if !rolling || !isUnstartedCodexWindow(q.Name, q.ResetsAt, now) {
			continue
		}
		if !a.autoStartCheck(q.Name) {
			continue
		}
		windows = append(windows, q.Name)
	}
	if len(windows) == 0 {
		return
	}
	claimed, capped := a.claimStarter()
	if capped {
		a.logger.Info("Codex auto quota-starter skipped (rate cap reached)",
			"quotas", windows, "account_id", a.accountID,
			"max_fires", codexStarterMaxFires, "window", codexStarterRateWindow)
	}
	if !claimed {
		return
	}
	go a.sendStarterPing(ctx, a.codexAccountID, windows, a.releaseStarter)
}

// resetRolling records the window's reset time and reports whether it moved
// forward with the clock since the previous poll. An unstarted window's reset
// time rolls forward with the clock every poll, while a started window's stays
// fixed. The countdown of a just-started window stays inside
// codexUnstartedTolerance for a while, so without this the next poll fired a
// duplicate ping (issue #129).
//
// Only a pair of comparable polls with no ping claimed or released between
// them counts, so a ping's own turn is never mistaken for a rolling window. A
// window seen for the first time never counts either, so a restarted agent
// waits one poll instead of pinging a window that may already be started.
func (a *CodexAgent) resetRolling(quotaName string, resetsAt *time.Time, sp codexStarterPoll, comparable bool) bool {
	prev, seen := a.starterSeen[quotaName]
	if resetsAt == nil {
		delete(a.starterSeen, quotaName)
		return false
	}
	if a.starterSeen == nil {
		a.starterSeen = make(map[string]codexResetObservation)
	}
	a.starterSeen[quotaName] = codexResetObservation{resetsAt: *resetsAt, at: sp.fetchedAt, gen: sp.gen}
	if !seen || !comparable || prev.gen != sp.gen {
		return false
	}
	elapsed := sp.fetchedAt.Sub(prev.at)
	if elapsed < codexRollingMinGap {
		return false
	}
	// The move must match the elapsed time: a window started between the polls
	// (for example by the user's own Codex turn) moved by less.
	drift := resetsAt.Sub(prev.resetsAt) - elapsed
	return drift >= -codexRollingSlack && drift <= codexRollingSlack
}

// claimStarter reserves the account for one ping. It refuses while a ping is
// in flight - the window keeps rolling until the ping's turn commits - and,
// reporting capped, when the rate cap is reached.
func (a *CodexAgent) claimStarter() (claimed, capped bool) {
	a.starterMu.Lock()
	defer a.starterMu.Unlock()
	if a.starterGen%2 == 1 {
		return false, false
	}
	if !a.allowStarterPing() {
		return false, true
	}
	a.starterGen++
	return true, false
}

// releaseStarter ends the claim once the ping request has finished.
func (a *CodexAgent) releaseStarter() {
	a.starterMu.Lock()
	defer a.starterMu.Unlock()
	a.starterGen++
}

// starterGeneration returns starterGen.
func (a *CodexAgent) starterGeneration() uint64 {
	a.starterMu.Lock()
	defer a.starterMu.Unlock()
	return a.starterGen
}

// allowStarterPing enforces the rolling rate cap using a bounded ring of the
// last codexStarterMaxFires fire timestamps for the account:
//   - fewer than codexStarterMaxFires recorded -> always fire (append now);
//   - otherwise fire only if the oldest of the five is more than
//     codexStarterRateWindow ago (then drop it and append now).
//
// This guarantees at most codexStarterMaxFires pings per rolling
// codexStarterRateWindow. The slot is reserved even if the subsequent send
// fails, so repeated unstarted observations retry at the polling cadence but
// can never exceed the cap. starterMu must be held.
func (a *CodexAgent) allowStarterPing() bool {
	now := time.Now()

	if len(a.starterFires) < codexStarterMaxFires {
		a.starterFires = append(a.starterFires, now)
		return true
	}
	// Five recorded: only fire if the oldest is older than the rate window.
	if now.Sub(a.starterFires[0]) <= codexStarterRateWindow {
		return false
	}
	next := make([]time.Time, 0, codexStarterMaxFires)
	next = append(next, a.starterFires[1:]...)
	next = append(next, now)
	a.starterFires = next
	return true
}

// currentTime returns the clock used by the auth-pause schedule.
func (a *CodexAgent) currentTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// pauseAuth stops polling after repeated auth failures and schedules the first
// bounded self-recovery attempt. An already scheduled retry is preserved so a
// failed recovery keeps escalating instead of restarting at the base interval.
// It reports whether this call newly entered the paused state, so callers can
// log the transition once instead of on every tick.
func (a *CodexAgent) pauseAuth(failedToken string) bool {
	newlyPaused := !a.authPaused
	if newlyPaused {
		a.authPausedAt = a.currentTime()
		a.authPauseLogged = false
		a.lastFailedFingerprint = a.currentFingerprint()
	}
	a.authPaused = true
	a.lastFailedToken = failedToken
	if a.authRetryAt.IsZero() {
		a.authRetryAt = a.currentTime().Add(codexAuthPausedRetryInterval)
	}
	a.publishPauseState()
	return newlyPaused
}

// publishPauseState republishes the atomic pause snapshot from the raw fields.
// The raw fields are owned by the poll goroutine; the snapshot is what the web
// layer reads, so time.Time values never tear across goroutines.
func (a *CodexAgent) publishPauseState() {
	if !a.authPaused {
		a.pauseSnapshot.Store(nil)
		return
	}
	a.pauseSnapshot.Store(&CodexPauseState{
		Paused: true,
		Since:  a.authPausedAt,
		Reason: "Codex polling is paused after repeated authentication failures. Re-authenticate via 'codex auth' to resume.",
	})
}

// currentFingerprint returns the credential source digest, or empty when no
// fingerprint hook is wired or the source is unreadable.
func (a *CodexAgent) currentFingerprint() string {
	if a.credentialFingerprint == nil {
		return ""
	}
	return a.credentialFingerprint()
}

// resolveFingerprint returns the credential-source fingerprint, falling back to
// the token digest when no hook is wired, so the dead-token check and
// cross-agent dedup key on the same value in both cases.
func (a *CodexAgent) resolveFingerprint(creds *api.CodexCredentials) string {
	if fingerprint := a.currentFingerprint(); fingerprint != "" {
		return fingerprint
	}
	if creds == nil {
		return ""
	}
	return codexTokenFingerprint(creds.AccessToken, creds.RefreshToken)
}

// resumeAuth clears all auth pause state after credentials are known good.
//
// It deliberately does NOT reset proactiveRefreshFailures. That counter is a
// safety fuse: it bounds how many times the proactive OAuth refresh may fail
// before the agent stops hammering the token endpoint. Clearing it on every
// successful poll would let a flapping endpoint re-arm the fuse endlessly.
// It is reset only when a proactive refresh actually succeeds.
func (a *CodexAgent) resumeAuth() {
	a.authPaused = false
	a.authFailCount = 0
	a.lastFailedToken = ""
	a.authRetryAt = time.Time{}
	a.authRetryCount = 0
	a.authPausedAt = time.Time{}
	a.authPauseLogged = false
	a.lastFailedFingerprint = ""
	a.publishPauseState()
}

// logPausedOnce emits the pause notice on the transition into the paused
// state, so a permanently paused agent does not log the same message every
// tick (issue #147). Only the first caller after pauseAuth wins.
func (a *CodexAgent) logPausedOnce(message string) {
	if a.authPauseLogged {
		return
	}
	a.authPauseLogged = true
	a.logger.Error(message,
		"failure_count", max(a.authFailCount, a.proactiveRefreshFailures),
		"action", "Re-authenticate via 'codex auth' to resume polling",
		"account_id", a.accountID)
}

// AuthPaused reports whether polling is currently paused pending
// re-authentication. Safe to call from the web layer: it reads the atomic
// snapshot published by the poll goroutine, never the raw fields.
func (a *CodexAgent) AuthPaused() bool {
	return a.pauseSnapshot.Load() != nil
}

// PauseState reports the pause state for API surfacing. Since is the time the
// current pause began (zero when not paused). Safe to call from the web layer.
func (a *CodexAgent) PauseState() CodexPauseState {
	if snapshot := a.pauseSnapshot.Load(); snapshot != nil {
		return *snapshot
	}
	return CodexPauseState{}
}

// SetCredentialFingerprint wires a callback that returns a digest of the
// credential source. While paused, a change in the fingerprint means the user
// re-authenticated, so the pause is lifted and polling resumes immediately
// (issue #147).
func (a *CodexAgent) SetCredentialFingerprint(fn CodexCredentialFingerprintFunc) {
	a.credentialFingerprint = fn
}

// credentialsChanged reports whether the credential source differs from the
// one that failed. Returns false when no fingerprint hook is wired or the
// source is unreadable, so an unknown state never silently lifts a pause.
func (a *CodexAgent) credentialsChanged() bool {
	if a.credentialFingerprint == nil {
		return false
	}
	current := a.credentialFingerprint()
	if current == "" {
		return false
	}
	return current != a.lastFailedFingerprint
}

// codexAuthRetryBackoff returns the delay before the nth paused-state recovery
// attempt, doubling from codexAuthPausedRetryInterval up to the max.
func codexAuthRetryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return codexAuthPausedRetryInterval
	}
	shift := attempt - 1
	if shift > 10 {
		shift = 10 // prevent overflow
	}
	backoff := codexAuthPausedRetryInterval * (1 << shift)
	if backoff > codexAuthPausedRetryMaxInterval {
		return codexAuthPausedRetryMaxInterval
	}
	return backoff
}

// tryPausedAuthRecovery reports whether a paused agent may attempt one poll
// this cycle, rescheduling the next attempt on an escalating backoff. Before
// re-polling it may make at most one bounded OAuth refresh attempt for the
// window, so a transient token-endpoint outage does not strand a profile whose
// refresh token is still good (issue #147 review).
//
// Codex refresh tokens are one-time use, so the refresh is deliberately
// rate-limited: only when the access token is actually expiring, at most once
// per backoff window, coordinated across agents that share a credential source,
// and never repeated once the token is known dead (reused / invalid_grant /
// a 401 rejection).
func (a *CodexAgent) tryPausedAuthRecovery(ctx context.Context) bool {
	now := a.currentTime()
	if a.authRetryAt.IsZero() || now.Before(a.authRetryAt) {
		return false
	}

	window := codexAuthRetryBackoff(a.authRetryCount + 1)
	a.authRetryCount++
	a.authRetryAt = now.Add(codexAuthRetryBackoff(a.authRetryCount))
	a.logger.Info("Attempting recovery from paused Codex polling",
		"attempt", a.authRetryCount,
		"next_retry_at", a.authRetryAt,
		"account_id", a.accountID)

	a.tryRecoveryRefresh(ctx, now, window)
	return true
}

// tryRecoveryRefresh makes at most one OAuth refresh attempt for the recovery
// window, and only when the access token is actually expiring. A success
// installs the fresh token but deliberately does NOT lift the pause: only a
// successful usage poll (or an external credential-file change) resumes
// polling, so a usage endpoint that keeps rejecting the credential does not
// restart the backoff - the recovery would otherwise rotate the token every few
// minutes forever and log the Codex CLI out (issue #147 review, item 1).
func (a *CodexAgent) tryRecoveryRefresh(ctx context.Context, now time.Time, window time.Duration) {
	if a.credsRefresh == nil {
		return
	}
	creds := a.credsRefresh()
	if creds == nil || creds.RefreshToken == "" {
		return
	}
	// Only refresh a token that is actually expiring. Walking the pause path on
	// a non-token failure (a persistent 403, say) must never rotate tokens.
	if !creds.IsExpiringSoon(codexTokenRefreshThreshold) {
		return
	}
	if a.store != nil && !a.store.AutoRefreshTokensEnabled() {
		return
	}
	// The credential-source fingerprint identifies the refresh token for both
	// the dead-token check and cross-agent dedup.
	fingerprint := a.resolveFingerprint(creds)
	// A dead refresh token stays dead until the credential source changes.
	if a.refreshTerminalFingerprint != "" && a.refreshTerminalFingerprint == fingerprint {
		return
	}
	if a.refreshCoordinator != nil && !a.refreshCoordinator.claim(fingerprint, now, window) {
		a.logger.Info("Skipping Codex recovery refresh - another agent refreshed this credential recently",
			"account_id", a.accountID)
		return
	}

	newTokens, err := a.refreshCodexToken(ctx, creds.RefreshToken)
	if err != nil {
		if isCodexRefreshTerminalError(err) {
			a.refreshTerminalFingerprint = fingerprint
			a.logger.Error("Codex recovery refresh token is dead - stopping recovery refreshes until re-auth",
				"error", err, "account_id", a.accountID)
			return
		}
		a.logger.Warn("Codex recovery refresh failed - will retry next window",
			"error", err, "account_id", a.accountID)
		return
	}

	// Persist immediately: refresh tokens are one-time use.
	saveFn := a.tokenSave
	if saveFn == nil {
		saveFn = writeCodexCredentialsNoPrevious
	}
	if err := saveFn(newTokens.AccessToken, newTokens.RefreshToken, newTokens.IDToken, newTokens.ExpiresIn, creds.RefreshToken); err != nil {
		a.logger.Error("Failed to save recovery-refreshed Codex credentials", "error", err, "account_id", a.accountID)
		return
	}
	a.client.SetToken(newTokens.AccessToken)
	a.lastToken = newTokens.AccessToken
	a.proactiveRefreshFailures = 0
	// Our own write is not an external re-authentication: record the rotated
	// credential as the one that was failing, so neither credentialsChanged() nor
	// the tokenRefresh check lifts the pause (and resets the backoff) on the next
	// tick. Only a successful poll or a genuinely external credential change
	// resumes polling.
	a.lastFailedToken = newTokens.AccessToken
	a.lastFailedFingerprint = a.currentFingerprint()
	if a.lastFailedFingerprint == "" {
		a.lastFailedFingerprint = codexTokenFingerprint(newTokens.AccessToken, newTokens.RefreshToken)
	}
	a.logger.Info("Codex recovery refresh succeeded - awaiting a successful poll to resume",
		"account_id", a.accountID)
}

// isCodexRefreshTerminalError reports whether a refresh failure means the
// refresh token is dead and must not be replayed.
func isCodexRefreshTerminalError(err error) bool {
	return errors.Is(err, api.ErrCodexRefreshTokenReused) || errors.Is(err, api.ErrCodexRefreshTokenInvalid)
}

// NewCodexAgent creates a new CodexAgent with the given dependencies.
// Uses DefaultCodexAccountID (1) for backward compatibility.
func NewCodexAgent(client *api.CodexClient, st *store.Store, tracker *tracker.CodexTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *CodexAgent {
	return NewCodexAgentWithAccount(client, st, tracker, interval, logger, sm, store.DefaultCodexAccountID)
}

// NewCodexAgentWithAccount creates a new CodexAgent for a specific account.
func NewCodexAgentWithAccount(client *api.CodexClient, st *store.Store, tracker *tracker.CodexTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager, accountID int64) *CodexAgent {
	if logger == nil {
		logger = slog.Default()
	}
	if accountID == 0 {
		accountID = store.DefaultCodexAccountID
	}
	return &CodexAgent{
		client:    client,
		store:     st,
		tracker:   tracker,
		interval:  interval,
		logger:    logger,
		sm:        sm,
		accountID: accountID,
	}
}

// SetPollingCheck sets a function called before each poll.
func (a *CodexAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets notification engine for sending alerts.
func (a *CodexAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// SetTokenRefresh sets a function called before each poll to refresh Codex token from credentials.
func (a *CodexAgent) SetTokenRefresh(fn CodexTokenRefreshFunc) {
	a.tokenRefresh = fn
}

// SetCredentialsRefresh sets a function that returns full credentials for
// proactive OAuth token refresh before expiry.
func (a *CodexAgent) SetCredentialsRefresh(fn CodexCredentialsRefreshFunc) {
	a.credsRefresh = fn
}

// SetTokenSave sets a function that saves refreshed OAuth tokens to disk.
// This allows named profiles to write to their profile file instead of
// the global CODEX_HOME/auth.json, preventing auth contamination between profiles.
func (a *CodexAgent) SetTokenSave(fn CodexTokenSaveFunc) {
	a.tokenSave = fn
}

// refreshCodexToken performs the proactive OAuth refresh, using the injected
// seam when present so tests never reach the real endpoint.
func (a *CodexAgent) refreshCodexToken(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
	if a.oauthRefresh != nil {
		return a.oauthRefresh(ctx, refreshToken)
	}
	return api.RefreshCodexToken(ctx, refreshToken)
}

// sendStarterPing sends a minimal Codex generation request to "start" the given
// limit windows after a reset (auto quota-starter, Beta). Failures are logged,
// never fatal, and not retried here - the next polls retry a window that is
// still observed unstarted, within the rate cap. A stale/paused token simply
// yields a logged auth error. codexAccountID is the Codex account_id used for
// the ChatGPT-Account-ID header; windows name the observed unstarted windows
// for logging and the per-window notifications. finished, when set, is called
// as soon as the request completes, before any notification is delivered.
//
// This may run in a goroutine concurrent with poll(), so it must not read
// poll-owned agent state (e.g. authPaused) without synchronization. It relies
// only on the client, which is internally synchronized.
func (a *CodexAgent) sendStarterPing(ctx context.Context, codexAccountID string, windows []string, finished func()) {
	a.logger.Info("Codex auto quota-starter firing",
		"quotas", windows, "account_id", a.accountID, "model", api.CodexStarterModel())

	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := a.client.SendStarterPing(pingCtx, codexAccountID)
	cancel()
	if finished != nil {
		finished()
	}
	if err != nil {
		if ctx.Err() != nil {
			// The daemon is shutting down: not a start failure worth notifying.
			a.logger.Info("Codex auto quota-starter ping cancelled",
				"quotas", windows, "account_id", a.accountID)
			return
		}
		a.logger.Warn("Codex auto quota-starter ping failed",
			"quotas", windows, "account_id", a.accountID, "model", api.CodexStarterModel(), "error", err)
		for _, w := range windows {
			a.sendStarterEvent(w, false, err.Error())
		}
		return
	}
	a.logger.Info("Codex auto quota-starter ping sent",
		"quotas", windows, "account_id", a.accountID, "model", api.CodexStarterModel())
	for _, w := range windows {
		a.sendStarterEvent(w, true, "")
	}
}

// sendStarterEvent reports an auto quota-starter outcome to the notifier.
// The detail is the transport error text, which never contains credentials.
func (a *CodexAgent) sendStarterEvent(quotaName string, success bool, detail string) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendStarterEvent(notify.StarterEvent{
		Provider:  "codex",
		QuotaKey:  quotaName,
		AccountID: fmt.Sprintf("%d", a.accountID),
		Success:   success,
		Detail:    detail,
	})
}

// sendAuthErrorNotification sends an auth error notification via the notifier.
func (a *CodexAgent) sendAuthErrorNotification(title, message string, isRecoverable bool) {
	if a.notifier == nil {
		return
	}
	a.notifier.SendAuthErrorNotification(notify.AuthErrorAlert{
		Provider:    "codex",
		Title:       title,
		Message:     message,
		AccountID:   fmt.Sprintf("%d", a.accountID),
		IsRecovable: isRecoverable,
	})
}

// Run starts the agent polling loop.
func (a *CodexAgent) Run(ctx context.Context) error {
	a.logger.Info("Codex agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Codex agent stopped")
	}()

	a.poll(ctx)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *CodexAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	// While paused, a change in the credential source means the user
	// re-authenticated: lift the pause and poll again immediately (issue #147).
	// This runs before any OAuth refresh so a paused agent never hits the token
	// endpoint with a dead refresh token.
	if a.authPaused && a.credentialsChanged() {
		a.resumeAuth()
		a.logger.Info("Codex auth failure pause lifted - new credentials detected", "account_id", a.accountID)
	}

	// Proactive OAuth refresh: check if token expires soon and refresh via OAuth API.
	// Skipped entirely while paused: the refresh token that just failed must not
	// be replayed every tick against the OAuth endpoint (issue #147).
	if a.credsRefresh != nil && !a.authPaused {
		if creds := a.credsRefresh(); creds != nil {
			// Check if token is expiring soon or already expired
			if creds.IsExpiringSoon(codexTokenRefreshThreshold) && creds.RefreshToken != "" {
				if a.store != nil && !a.store.AutoRefreshTokensEnabled() {
					a.logger.Debug("Skipping Codex proactive OAuth refresh - auto_refresh_tokens disabled")
				} else {
					a.logger.Info("Codex token expiring soon, attempting proactive OAuth refresh",
						"expires_in", creds.ExpiresIn.Round(time.Second))

					newTokens, err := a.refreshCodexToken(ctx, creds.RefreshToken)
					if err != nil {
						if isCodexRefreshTerminalError(err) {
							// Unrecoverable - the refresh token is dead (reused,
							// invalid_grant, or a 401 rejection). Pause at once
							// rather than counting toward the transient budget.
							a.refreshTerminalFingerprint = a.resolveFingerprint(creds)
							a.logger.Error("Codex refresh token is dead - re-authenticate via 'codex auth'",
								"error", err)
							if a.pauseAuth(creds.AccessToken) {
								a.logPausedOnce("Codex proactive refresh PAUSED - refresh token rejected")
							}
							// Send auth error notification
							a.sendAuthErrorNotification(
								"Token Refresh Failed",
								"Codex refresh token is no longer valid. Please re-authenticate via 'codex auth' to resume quota tracking.",
								false, // not recoverable
							)
						} else {
							a.proactiveRefreshFailures++
							a.logger.Error("Proactive Codex OAuth refresh failed",
								"error", err,
								"consecutive_failures", a.proactiveRefreshFailures)
							if a.proactiveRefreshFailures >= maxCodexAuthFailures {
								if a.pauseAuth(creds.AccessToken) {
									a.logPausedOnce("Codex proactive refresh PAUSED - too many consecutive failures")
								}
								a.sendAuthErrorNotification(
									"Token Refresh Failed",
									fmt.Sprintf("Codex proactive OAuth refresh failed %d times. Please re-authenticate via 'codex auth' to resume.", a.proactiveRefreshFailures),
									false,
								)
							}
						}
					} else {
						// Proactive refresh succeeded - reset failure counter
						a.proactiveRefreshFailures = 0

						// CRITICAL: Save new tokens to disk IMMEDIATELY (refresh tokens are one-time use!)
						saveFn := a.tokenSave
						if saveFn == nil {
							saveFn = writeCodexCredentialsNoPrevious // fallback for backward compat
						}
						if err := saveFn(newTokens.AccessToken, newTokens.RefreshToken, newTokens.IDToken, newTokens.ExpiresIn, creds.RefreshToken); err != nil {
							a.logger.Error("Failed to save refreshed Codex credentials", "error", err)
						} else {
							a.client.SetToken(newTokens.AccessToken)
							a.lastToken = newTokens.AccessToken
							a.logger.Info("Proactively refreshed Codex OAuth token",
								"expires_in_hours", newTokens.ExpiresIn/3600)

							// Reset auth failures since we have fresh credentials
							if a.authPaused {
								a.resumeAuth()
								a.logger.Info("Codex auth failure pause lifted - token refreshed via OAuth")
							}
						}
					}
				} // end auto_refresh_tokens enabled
			}
		}
	}

	// Refresh token before each poll (picks up rotated credentials from disk)
	if a.tokenRefresh != nil {
		newToken := a.tokenRefresh()
		if newToken != "" && newToken != a.lastToken {
			a.client.SetToken(newToken)
			a.lastToken = newToken
			a.logger.Info("Codex token refreshed from credentials")
		}

		// A token that differs from the one that failed is also a
		// re-authentication signal, independent of the fingerprint hook.
		if a.authPaused && newToken != "" && newToken != a.lastFailedToken {
			a.resumeAuth()
			a.logger.Info("Codex auth failure pause lifted - new credentials detected", "account_id", a.accountID)
		}
	}

	// If auth is paused, skip polling until credentials change or a bounded
	// recovery attempt comes due.
	if a.authPaused && !a.tryPausedAuthRecovery(ctx) {
		return
	}

	sp := codexStarterPoll{fetchedAt: time.Now().UTC(), gen: a.starterGeneration()}
	resp, err := a.client.FetchUsage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		// On auth error, force token re-read and retry once.
		if isCodexAuthError(err) && a.tokenRefresh != nil {
			a.logger.Warn("Codex auth error, forcing credential re-read", "error", err)
			a.lastToken = "" // force re-read even if unchanged on disk
			if retryToken := a.tokenRefresh(); retryToken != "" {
				a.client.SetToken(retryToken)
				a.lastToken = retryToken
				a.logger.Info("Retrying Codex poll with refreshed token")
				sp.fetchedAt = time.Now().UTC()
				resp, err = a.client.FetchUsage(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					if isCodexAuthError(err) {
						a.authFailCount++
						a.logger.Error("Codex auth retry failed",
							"error", err,
							"failure_count", a.authFailCount,
							"max_failures", maxCodexAuthFailures)

						if a.authFailCount >= maxCodexAuthFailures {
							if a.pauseAuth(retryToken) {
								a.logPausedOnce("Codex polling PAUSED due to repeated auth failures")
							}
							// Send auth error notification
							a.sendAuthErrorNotification(
								"Authentication Failed",
								"Codex polling has been paused due to repeated authentication failures. Please re-authenticate via 'codex auth' to resume. onWatch also retries on its own, starting 15 minutes from now, in case the failure was server-side.",
								false, // re-auth is the expected fix; the bounded retry is only a safety net
							)
						}
					} else {
						a.logger.Error("Codex retry failed with non-auth error", "error", err)
					}
					return
				}
				// Retry succeeded, reset auth failure count.
				a.authFailCount = 0
				if a.authPaused {
					a.resumeAuth()
					a.logger.Info("Codex auth failure pause lifted - poll succeeded", "account_id", a.accountID)
				}
			} else {
				a.logger.Error("No Codex token available after re-read")
				return
			}
		} else if errors.Is(err, api.ErrCodexAccessBlocked) {
			// A challenge response is an edge block, not a credential problem:
			// log it and leave the auth failure budget untouched.
			a.logger.Warn("Codex usage request blocked by a challenge response",
				"error", err, "account_id", a.accountID)
			return
		} else {
			a.logger.Error("Failed to fetch Codex usage", "error", err)
			return
		}
	} else {
		// Success, reset auth failure count.
		a.authFailCount = 0
		if a.authPaused {
			a.resumeAuth()
			a.logger.Info("Codex auth failure pause lifted - poll succeeded", "account_id", a.accountID)
		}
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)
	snapshot.AccountID = a.accountID // Set the database account ID

	if _, err := a.store.InsertCodexSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Codex snapshot", "error", err, "account_id", a.accountID)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Codex tracker processing failed", "error", err, "account_id", a.accountID)
		}
	}

	// Auto quota-starter (Beta): start any enabled window that is observed
	// unstarted (reset countdown pinned at ~full window length).
	a.maybeAutoStartWindows(ctx, snapshot.Quotas, now, sp)

	// Banked reset credits (issue #152): slow cadence, read-only. Failures are
	// contained inside maybeFetchResetCredits and never touch usage state.
	// The clock hook keeps the cadence deterministic in tests.
	a.maybeFetchResetCredits(ctx, snapshot, a.currentTime().UTC())

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "codex",
				QuotaKey:    q.Name,
				AccountID:   fmt.Sprintf("%d", a.accountID),
				Utilization: q.Utilization,
				Limit:       100,
				ResetAt:     derefTime(q.ResetsAt),
			})
		}
	}

	if a.sm != nil {
		values := make([]float64, 0, len(snapshot.Quotas))
		for _, q := range snapshot.Quotas {
			values = append(values, q.Utilization)
		}
		a.sm.ReportPoll(values)
	}

	for _, q := range snapshot.Quotas {
		a.logger.Info("Codex poll complete", "quota", q.Name, "utilization", q.Utilization, "plan", resp.PlanType, "account_id", a.accountID)
	}
}
