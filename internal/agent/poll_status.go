package agent

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// Poll outcome classes. They are the only thing published about a failed poll:
// never the raw error, which can carry URLs, credentials or provider internals.
type PollOutcome string

const (
	PollOutcomeNetwork     PollOutcome = "network"
	PollOutcomeAuth        PollOutcome = "auth"
	PollOutcomeRateLimit   PollOutcome = "rate_limit"
	PollOutcomeUpstream    PollOutcome = "upstream"
	PollOutcomeParse       PollOutcome = "parse"
	PollOutcomePaused      PollOutcome = "paused"
	PollOutcomeSourceStale PollOutcome = "source_stale"
)

// Reason returns a short user-facing phrase for the outcome class. It is
// deliberately fixed text: nothing from the provider is interpolated.
func (o PollOutcome) Reason() string {
	switch o {
	case PollOutcomeNetwork:
		return "network error"
	case PollOutcomeAuth:
		return "authentication error"
	case PollOutcomeRateLimit:
		return "rate limited"
	case PollOutcomeUpstream:
		return "upstream error"
	case PollOutcomeParse:
		return "invalid response"
	case PollOutcomePaused:
		return "polling paused"
	case PollOutcomeSourceStale:
		return "source data is old"
	default:
		return ""
	}
}

// maxPollStatusEntries bounds the registry. One entry per card (provider or
// account) is the intended size; the cap is headroom for transient account
// churn and keeps a misbehaving publisher from growing it without bound.
const maxPollStatusEntries = 64

type pollStatusEntry struct {
	outcome PollOutcome
	at      int64 // unix nano, retained for tests and diagnostics only
}

// PollStatusRegistry publishes the last poll outcome class per provider (and
// per account where accounts exist). Agents report on failure and clear on
// success. The menubar reads it to explain why a card is stale. It is a tiny
// in-memory map: no error text is stored, and it is bounded to one entry per
// provider/account key.
type PollStatusRegistry struct {
	mu      sync.RWMutex
	entries map[string]pollStatusEntry
	now     func() int64
	// onPoll runs after every published outcome, success or failure. The
	// menubar uses it to rebuild its cached snapshot ahead of requests.
	onPoll atomic.Pointer[func(provider string)]
}

// SetOnPoll installs a callback run after every Report and Clear with the
// provider that polled. It must not block: the menubar only arms a debounce
// timer and the response cache bumps a counter.
func (r *PollStatusRegistry) SetOnPoll(fn func(provider string)) {
	if r == nil {
		return
	}
	if fn == nil {
		r.onPoll.Store(nil)
		return
	}
	r.onPoll.Store(&fn)
}

func (r *PollStatusRegistry) notifyPoll(provider string) {
	if fn := r.onPoll.Load(); fn != nil {
		(*fn)(provider)
	}
}

// NewPollStatusRegistry returns an empty registry.
func NewPollStatusRegistry() *PollStatusRegistry {
	return &PollStatusRegistry{entries: make(map[string]pollStatusEntry, 8)}
}

// Report records the last poll outcome for a provider or account. account is
// empty for providers without accounts. Publishing a new key over the cap
// evicts nothing that is currently reported: the oldest entry is dropped.
func (r *PollStatusRegistry) Report(provider, account string, outcome PollOutcome) {
	if r == nil || provider == "" || outcome == "" {
		return
	}
	key := pollStatusKey(provider, account)
	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[string]pollStatusEntry, 8)
	}
	if _, exists := r.entries[key]; !exists && len(r.entries) >= maxPollStatusEntries {
		r.evictOldestLocked()
	}
	r.entries[key] = pollStatusEntry{outcome: outcome, at: r.nowNano()}
	r.mu.Unlock()
	r.notifyPoll(provider)
}

// Clear removes the entry, called after a successful poll. Providers that only
// ever succeed never create an entry at all.
func (r *PollStatusRegistry) Clear(provider, account string) {
	if r == nil || provider == "" {
		return
	}
	r.mu.Lock()
	delete(r.entries, pollStatusKey(provider, account))
	r.mu.Unlock()
	r.notifyPoll(provider)
}

// Get returns the last outcome class for a provider or account.
func (r *PollStatusRegistry) Get(provider, account string) (PollOutcome, bool) {
	if r == nil || provider == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[pollStatusKey(provider, account)]
	return entry.outcome, ok
}

// Len reports the current entry count (tests and diagnostics).
func (r *PollStatusRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *PollStatusRegistry) evictOldestLocked() {
	oldestKey := ""
	oldestAt := int64(1<<63 - 1)
	for key, entry := range r.entries {
		if entry.at < oldestAt {
			oldestAt = entry.at
			oldestKey = key
		}
	}
	if oldestKey != "" {
		delete(r.entries, oldestKey)
	}
}

func (r *PollStatusRegistry) nowNano() int64 {
	if r.now != nil {
		return r.now()
	}
	return time.Now().UnixNano()
}

type pollStatusSnapshotEntry struct {
	provider string
	account  string
	outcome  PollOutcome
}

// entriesSnapshotKeys returns every key, used by tests to prove re-reporting
// does not grow the registry.
func (r *PollStatusRegistry) entriesSnapshotKeys() []string {
	entries := r.entriesSnapshot()
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, pollStatusKey(entry.provider, entry.account))
	}
	return keys
}

func (r *PollStatusRegistry) entriesSnapshot() []pollStatusSnapshotEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]pollStatusSnapshotEntry, 0, len(r.entries))
	for key, entry := range r.entries {
		provider, account := splitPollStatusKey(key)
		out = append(out, pollStatusSnapshotEntry{provider: provider, account: account, outcome: entry.outcome})
	}
	return out
}

func pollStatusKey(provider, account string) string {
	if account == "" {
		return provider
	}
	return provider + ":" + account
}

func splitPollStatusKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// ClassifyPollError maps a provider error to an outcome class. It only ever
// inspects typed sentinels; the message text is never read, so a provider error
// containing sensitive data cannot influence (or reach) what is published.
func ClassifyPollError(err error) PollOutcome {
	if err == nil {
		return ""
	}
	for _, class := range pollErrorClasses {
		for _, sentinel := range class.errors {
			if errors.Is(err, sentinel) {
				return class.outcome
			}
		}
	}
	return PollOutcomeUpstream
}

type pollErrorClass struct {
	outcome PollOutcome
	errors  []error
}

// pollErrorClasses lists the typed errors each provider client returns. Keeping
// it as one table means a new provider only adds its sentinels here.
var pollErrorClasses = []pollErrorClass{
	{PollOutcomeNetwork, []error{
		api.ErrNetworkError, api.ErrAnthropicNetworkError, api.ErrCodexNetworkError,
		api.ErrCopilotNetworkError, api.ErrCursorNetworkError, api.ErrDeepSeekNetworkError,
		api.ErrGeminiNetworkError, api.ErrGrokNetworkError, api.ErrKimiNetworkError,
		api.ErrMiniMaxNetworkError, api.ErrMoonshotNetworkError, api.ErrMuseNetworkError,
		api.ErrOllamaNetworkError, api.ErrOpenCodeNetworkError, api.ErrOpenRouterNetworkError,
		api.ErrZaiNetworkError, api.ErrCommandCodeNetworkError,
	}},
	{PollOutcomeAuth, []error{
		api.ErrUnauthorized, api.ErrAnthropicUnauthorized, api.ErrAnthropicForbidden,
		api.ErrCodexUnauthorized, api.ErrCodexForbidden, api.ErrCopilotUnauthorized,
		api.ErrCopilotForbidden, api.ErrCursorUnauthorized, api.ErrCursorForbidden,
		api.ErrCursorSessionExpired, api.ErrDeepSeekUnauthorized, api.ErrGeminiUnauthorized,
		api.ErrGeminiForbidden, api.ErrGrokUnauthorized, api.ErrGrokForbidden,
		api.ErrKimiUnauthorized, api.ErrMiniMaxUnauthorized, api.ErrMoonshotUnauthorized,
		api.ErrMuseUnauthorized, api.ErrOllamaUnauthorized, api.ErrOpenCodeUnauthorized,
		api.ErrOpenCodeForbidden, api.ErrOpenRouterUnauthorized, api.ErrZaiUnauthorized,
		api.ErrMistralAuth, api.ErrCommandCodeUnauthorized, api.ErrCodexRefreshTokenInvalid,
		api.ErrCodexRefreshTokenReused, api.ErrOAuthInvalidGrant,
	}},
	{PollOutcomeRateLimit, []error{
		api.ErrAnthropicRateLimited, api.ErrOAuthRateLimited, api.ErrOpenCodeRateLimited,
		api.ErrDeepSeekRateLimited, api.ErrKimiRateLimited, api.ErrMoonshotRateLimited,
		api.ErrMuseRateLimited, api.ErrOllamaRateLimited, api.ErrOpenRouterRateLimited,
		api.ErrCommandCodeRateLimited,
	}},
	{PollOutcomeParse, []error{
		api.ErrInvalidResponse, api.ErrAnthropicInvalidResponse, api.ErrCodexInvalidResponse,
		api.ErrCopilotInvalidResponse, api.ErrCursorInvalidResponse, api.ErrDeepSeekInvalidResponse,
		api.ErrGeminiInvalidResponse, api.ErrGrokInvalidResponse, api.ErrGrokParseFailed,
		api.ErrKimiInvalidResponse, api.ErrMiniMaxInvalidResponse, api.ErrMoonshotInvalidResponse,
		api.ErrMuseInvalidResponse, api.ErrOllamaInvalidResponse, api.ErrOpenCodeInvalidResponse,
		api.ErrOpenCodeParseFailed, api.ErrOpenRouterInvalidResponse, api.ErrZaiInvalidResponse,
		api.ErrMistralParse, api.ErrCommandCodeInvalidResponse,
	}},
	{PollOutcomeUpstream, []error{
		api.ErrServerError, api.ErrAnthropicServerError, api.ErrCodexServerError,
		api.ErrCodexAccessBlocked, api.ErrCopilotServerError, api.ErrCursorServerError,
		api.ErrDeepSeekServerError, api.ErrGeminiServerError, api.ErrGrokServerError,
		api.ErrKimiServerError, api.ErrMiniMaxServerError, api.ErrMiniMaxAccessBlocked,
		api.ErrMoonshotServerError, api.ErrMuseServerError, api.ErrOllamaServerError,
		api.ErrOpenCodeServerError, api.ErrOpenRouterServerError, api.ErrZaiServerError,
		api.ErrZaiAPIError, api.ErrCommandCodeServerError,
	}},
}

// defaultPollStatus is the process-wide registry the daemon wires up at
// startup. Agents publish to it; the web handler reads the same instance. It
// may be nil in tests and library use, which makes every publish a no-op.
var defaultPollStatus atomic.Pointer[PollStatusRegistry]

// SetPollStatusRegistry installs the process-wide registry.
func SetPollStatusRegistry(r *PollStatusRegistry) {
	defaultPollStatus.Store(r)
}

// GetPollStatusRegistry returns the installed registry, or nil.
func GetPollStatusRegistry() *PollStatusRegistry {
	return defaultPollStatus.Load()
}

// Get returns the last outcome class for a provider or account (empty account
// for single-account providers). A nil registry has no entries.
func (r *PollStatusRegistry) GetOutcome(provider, account string) (PollOutcome, bool) {
	return r.Get(provider, account)
}

// pollFailed publishes a classified failure to the default registry.
func pollFailed(provider, account string, err error) {
	reportPollFailure(GetPollStatusRegistry(), provider, account, err)
}

// pollPaused publishes a deliberate pause to the default registry.
func pollPaused(provider, account string) {
	reportPollPaused(GetPollStatusRegistry(), provider, account)
}

// pollSucceeded clears the default registry entry for a provider/account.
func pollSucceeded(provider, account string) {
	reportPollSuccess(GetPollStatusRegistry(), provider, account)
}

// reportPollFailure publishes a classified failure. account is empty for
// single-account providers.
func reportPollFailure(registry *PollStatusRegistry, provider, account string, err error) {
	if registry == nil || err == nil {
		return
	}
	registry.Report(provider, account, ClassifyPollError(err))
}

// reportPollPaused publishes a paused state, which is a deliberate stop rather
// than an error. The raw reason is not kept.
func reportPollPaused(registry *PollStatusRegistry, provider, account string) {
	if registry == nil {
		return
	}
	registry.Report(provider, account, PollOutcomePaused)
}

// reportPollSuccess clears the entry for a provider/account.
func reportPollSuccess(registry *PollStatusRegistry, provider, account string) {
	if registry == nil {
		return
	}
	registry.Clear(provider, account)
}

// Provider display keys used by the registry match the menubar card base IDs.
func pollStatusAccountKey(accountID int64) string {
	if accountID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", accountID)
}
