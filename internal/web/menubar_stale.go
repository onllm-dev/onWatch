package web

import (
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
)

// Staleness for the menubar is decided here, not in each provider builder, so
// the thresholds stay in one table. The card keeps its usage severity; stale
// only changes how the age label and dot are drawn.

// staleFloor is the smallest "stale after" window. It keeps a very short poll
// interval from flickering a card stale between two normal polls.
const staleFloor = 5 * time.Minute

// staleContext is everything the threshold table needs about one card.
type staleContext struct {
	// Provider is the card ID (base or account-scoped, e.g. "codex:2").
	Provider string
	// Base is the family key ("codex", "mistral", ...).
	Base string
	// Polling is the provider_visibility polling flag. False means manual
	// (dashboard-only refresh), which never ages out for network reasons.
	Polling bool
	// PollInterval is the global cfg.PollInterval.
	PollInterval time.Duration
	// Status is the builder's connection status ("ok", "stale", "reconnect").
	Status string
	// Outcome is the last published poll outcome for this card, if any.
	Outcome agent.PollOutcome
}

// staleOverride is one row of the per-provider cadence table. A zero value
// means "use the generic rule".
type staleOverride struct {
	// factor multiplies the global poll interval (the generic rule is 3).
	factor time.Duration
	// floor replaces the generic floor when non-zero.
	floor time.Duration
	// statuses widens the window when the builder reports one of them.
	statuses map[string]time.Duration
	// paused widens the window while the last poll outcome is paused (an
	// account waiting for re-authentication polls at its own backoff).
	paused time.Duration
}

// staleOverrides is the ONE cadence table. Every entry carries the reason it
// differs from "3 x pollInterval, at least 5m":
//
//   - anthropic: the statusline bridge only rewrites its file while Claude
//     Code is making calls, and the agent accepts a file up to 5m old; the
//     hybrid API poll runs every 10 intervals. 3 x 5m covers both without
//     marking a quiet-but-healthy session stale.
//   - antigravity: quota comes from the IDE language server, polled on the
//     global interval but refreshed only while the IDE/agy process runs.
//   - mistral: backs off to 6h on errors and sits in reconnect while the
//     browser session is rejected, so the error state needs the full backoff.
//   - codex: a paused account waits 15m-6h for re-authentication; the pause
//     itself is already shown by the attention ring, but its data must not
//     read as merely old for hours.
//   - opencode: key mode reuses a 60s client cache, so the honest capture time
//     can trail the poll by a minute; 3 intervals plus the floor covers it.
var staleOverrides = map[string]staleOverride{
	"anthropic":   {factor: 3, floor: 15 * time.Minute},
	"antigravity": {factor: 3, floor: 15 * time.Minute},
	"opencode":    {factor: 3, floor: 5 * time.Minute},
	"mistral": {
		factor: 3,
		statuses: map[string]time.Duration{
			// The agent's own backoff tops out at one hour per attempt; the
			// status can persist across several, so treat it as long-running.
			"stale":     6 * time.Hour,
			"reconnect": 6 * time.Hour,
		},
	},
	"codex": {factor: 3, paused: 6 * time.Hour},
}

// staleAfterFor returns the staleness window for one card. Zero means the card
// is never marked stale locally: manual (polling:false) providers are refreshed
// only from the dashboard, so their age grows by design.
func staleAfterFor(ctx staleContext) time.Duration {
	if !ctx.Polling {
		return 0
	}
	interval := ctx.PollInterval
	if interval <= 0 {
		interval = 120 * time.Second
	}
	factor := time.Duration(3)
	floor := staleFloor
	override := staleOverrides[ctx.Base]
	if override.factor > 0 {
		factor = override.factor
	}
	if override.floor > 0 {
		floor = override.floor
	}
	window := factor * interval
	if window < floor {
		window = floor
	}
	if override.statuses != nil && ctx.Status != "" {
		if widened, ok := override.statuses[ctx.Status]; ok && widened > window {
			window = widened
		}
	}
	if override.paused > 0 && ctx.Outcome == agent.PollOutcomePaused && override.paused > window {
		window = override.paused
	}
	return window
}
