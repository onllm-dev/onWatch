package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

const mistralSubscriptionURL = "https://admin.mistral.ai/subscription"

type mistralFetcher interface {
	FetchSnapshot(context.Context, api.MistralSession) (*api.MistralSnapshot, error)
}
type MistralAgent struct {
	client          mistralFetcher
	store           *store.Store
	tr              *tracker.MistralTracker
	cfg             *config.Config
	logger          *slog.Logger
	sm              *SessionManager
	notifier        *notify.NotificationEngine
	pollingCheck    func() bool
	read            api.MistralCookieReader
	session         *api.MistralSession
	source          *api.MistralSource
	imported        time.Time
	next            time.Time
	paused          bool
	failures        int
	importFailures  int
	lastPrune       time.Time
	sessionIdentity string
	lastQuotas      map[string]api.MistralQuota
}

func NewMistralAgent(s *store.Store, cfg *config.Config, logger *slog.Logger) *MistralAgent {
	if logger == nil {
		logger = slog.Default()
	}
	cfgCopy := *cfg
	return &MistralAgent{client: api.NewMistralClient(), store: s, cfg: &cfgCopy, logger: logger, tr: tracker.NewMistralTracker(s), sm: NewSessionManager(s, "mistral", 15*time.Minute, logger)}
}
func (a *MistralAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
	a.tr.SetOnReset(func(key string) { n.Check(notify.QuotaStatus{Provider: "mistral", QuotaKey: key, ResetOccurred: true}) })
}
func (a *MistralAgent) SetPollingCheck(fn func() bool) { a.pollingCheck = fn }
func (a *MistralAgent) Run(ctx context.Context) error {
	interval := a.cfg.PollInterval
	if interval <= 0 {
		interval = 120 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() { a.sm.Close() }()
	if raw, e := a.store.GetSetting("mistral_source"); e == nil && raw != "" {
		var selected struct {
			Browser, Profile string
			Source           api.MistralSource
		}
		if json.Unmarshal([]byte(raw), &selected) == nil && selected.Browser == a.cfg.MistralBrowser && selected.Profile == a.cfg.MistralBrowserProfile {
			a.source = &selected.Source
		}
	}
	// Select the active history partition before polling, so a newly selected
	// source never displays the previous source's values while reconnecting.
	identity := ""
	if a.source != nil {
		identity = a.source.Identity()
	}
	if a.cfg.MistralAuthMode == "manual" || a.cfg.MistralAuthMode != "automatic" && a.cfg.MistralAuthCookie != "" {
		identity = ""
		if session, e := api.ManualMistralSession(a.cfg.MistralAuthCookie); e == nil {
			identity = session.Source.Identity()
		}
	}
	// Losing the startup WAL race must not disable the provider: a Run that
	// returns is never retried. SaveMistral re-asserts the identity in the same
	// transaction as the first successful snapshot.
	if e := a.store.SetSetting("mistral_identity", identity); e != nil {
		a.logger.Warn("Mistral identity selection deferred to first poll", "error", e)
	}
	a.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}
func (a *MistralAgent) status(status string) { _ = a.store.SetSetting("mistral_status", status) }
func (a *MistralAgent) importSession(ctx context.Context) (*api.MistralSnapshot, error) {
	a.imported = time.Now()
	if a.cfg.MistralAuthMode == "manual" || a.cfg.MistralAuthCookie != "" && a.cfg.MistralAuthMode != "automatic" {
		session, e := api.ManualMistralSession(a.cfg.MistralAuthCookie)
		if e != nil {
			return nil, e
		}
		a.session = &session
		return nil, nil
	}
	// Automatic import reads browser cookie stores, which prompts for macOS
	// Keychain or Linux keyring access. Never do that for a provider the user
	// has not enabled.
	if !a.cfg.HasProvider("mistral") {
		return nil, api.ErrMistralAuth
	}
	var sources []api.MistralSource
	if a.source != nil {
		sources = []api.MistralSource{*a.source}
	} else {
		var e error
		sources, e = api.MistralSources(a.cfg.MistralBrowser, a.cfg.MistralBrowserProfile)
		// Scan failures are diagnostics, not a verdict: report them and use
		// whatever profiles were found.
		if e != nil {
			a.logger.Warn("Mistral profile discovery incomplete", "error", e)
		}
		if len(sources) == 0 {
			if e != nil {
				return nil, e
			}
			return nil, api.ErrMistralAuth
		}
	}
	// Covers a prompted credential-store read per candidate source; each
	// ImportMistralSessions call is separately bounded.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	a.logger.Info("Mistral credential discovery", "sources", len(sources))
	for _, source := range sources {
		sessions, e := api.ImportMistralSessions(ctx, source, a.read)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			a.logger.Warn("Mistral source unreadable", "browser", source.Browser, "error", e)
			continue
		}
		a.logger.Info("Mistral source read", "browser", source.Browser, "sessions", len(sessions))
		for _, session := range sessions {
			if a.source != nil {
				a.session = &session
				return nil, nil
			}
			snap, e := a.client.FetchSnapshot(ctx, session)
			if e != nil {
				if !errors.Is(e, api.ErrMistralAuth) && !errors.Is(e, api.ErrMistralParse) {
					return nil, e
				}
				// Cookies were read but Mistral would not serve this session.
				a.logger.Warn("Mistral session not usable", "browser", source.Browser, "cookies", session.CookieNames(), "error", e)
				continue
			}
			a.source = &session.Source
			a.session = &session
			data, e := json.Marshal(struct {
				Browser, Profile string
				Source           api.MistralSource
			}{a.cfg.MistralBrowser, a.cfg.MistralBrowserProfile, session.Source})
			if e != nil {
				return nil, e
			}
			if e = a.store.SetSetting("mistral_source", string(data)); e != nil {
				return nil, e
			}
			return snap, nil
		}
	}
	return nil, api.ErrMistralAuth
}
func (a *MistralAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() || time.Now().Before(a.next) {
		return
	}
	var snap *api.MistralSnapshot
	var e error
	// Only re-read the browser when there is no usable session, or while paused
	// and waiting for the user to sign in again (a.next bounds those retries to
	// one per 10 minutes). Every read shells out to the platform credential
	// store and can prompt for the user's login password, so a session that
	// still yields cookies is reused until it expires or is rejected.
	oldHeader := ""
	if a.session != nil {
		oldHeader = a.session.Header(mistralSubscriptionURL, time.Now())
	}
	if a.session == nil || oldHeader == "" || a.paused {
		snap, e = a.importSession(ctx)
		if e != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(e, api.ErrMistralAuth) && !errors.Is(e, api.ErrMistralParse) {
				a.backoff(e)
				return
			}
			a.status("reconnect")
			// Without this the provider sits in "reconnect" with no way to tell
			// a denied keychain prompt from a signed-out browser.
			a.logger.Warn("Mistral credential import failed; sign in to Mistral in the selected browser or use manual cookies", "error", e, "browser", a.cfg.MistralBrowser, "profileSet", a.cfg.MistralBrowserProfile != "")
			// Back off progressively. A retry re-reads the credential store,
			// which can prompt for a password, so a setup that is simply not
			// signed in must not keep asking every ten minutes.
			a.importFailures = min(a.importFailures+1, 5)
			a.next = time.Now().Add(min(10*time.Minute<<a.importFailures, 6*time.Hour))
			return
		}
		if a.paused && a.session.Header(mistralSubscriptionURL, time.Now()) == oldHeader {
			a.next = time.Now().Add(10 * time.Minute)
			return
		}
		a.paused = false
		a.importFailures = 0
	}
	if a.paused {
		return
	}
	if snap == nil {
		snap, e = a.client.FetchSnapshot(ctx, *a.session)
	}
	if mistralSessionRejected(snap, e) {
		partial := snap
		_, importErr := a.importSession(ctx)
		if importErr == nil {
			snap, e = a.client.FetchSnapshot(ctx, *a.session)
		}
		if snap == nil && partial != nil {
			snap = partial
		}
		if e != nil || mistralSessionRejected(snap, nil) {
			a.paused = true
			a.status("reconnect")
			a.next = time.Now().Add(10 * time.Minute)
			if snap == nil {
				return
			}
			e = nil
		}
	}
	if e != nil {
		if ctx.Err() != nil {
			return
		}
		a.backoff(e)
		return
	}
	a.failures = 0
	if a.paused {
		a.status("reconnect")
	} else {
		a.status(snap.Status)
	}
	if snap.RetryAfter > 0 {
		a.next = time.Now().Add(min(snap.RetryAfter, time.Hour))
	}
	if e = a.store.SaveMistral(ctx, snap); e != nil {
		a.logger.Error("Mistral storage failed")
		return
	}
	if e = a.tr.Process(ctx, snap); e != nil {
		a.logger.Error("Mistral tracking failed")
		return
	}
	if a.sessionIdentity != snap.Identity {
		a.sm.Close()
		a.sm = NewSessionManager(a.store, "mistral:"+snap.Identity, 15*time.Minute, a.logger)
		a.sessionIdentity = snap.Identity
		a.lastQuotas = map[string]api.MistralQuota{}
	}
	values := []float64{}
	for _, q := range snap.Quotas {
		if old, ok := a.lastQuotas[q.Name]; ok && (q.Limit != old.Limit || q.Used < old.Used || q.PercentOnly != old.PercentOnly) {
			a.sm.Close()
			a.sm.hasPrev = false
		}
		a.lastQuotas[q.Name] = q
		if !q.PercentOnly {
			values = append(values, q.Used)
		}
		if a.notifier != nil {
			a.notifier.Check(notify.QuotaStatus{Provider: "mistral", QuotaKey: q.Name, Utilization: q.Utilization, Limit: q.Limit})
		}
	}
	if len(values) == 2 {
		a.sm.ReportPoll(values)
	}
	// Retention is configurable via MISTRAL_RETENTION; 0 disables pruning
	// entirely. The <= guard also stops a negative value slipping past config
	// validation and pruning the whole history with a future cutoff.
	if a.cfg.MistralRetention > 0 && time.Since(a.lastPrune) > 24*time.Hour {
		if a.store.PruneMistral(ctx, time.Now().Add(-a.cfg.MistralRetention)) == nil {
			a.lastPrune = time.Now()
		}
	}
}

// A failed optional endpoint does not invalidate a session that still returns
// allowances or billing. Keep polling the working data without reimporting
// cookies (and prompting for Keychain access) on every partial response.
func mistralSessionRejected(snap *api.MistralSnapshot, err error) bool {
	if errors.Is(err, api.ErrMistralAuth) {
		return true
	}
	return snap != nil && snap.AuthFailed && len(snap.Quotas) == 0 &&
		(snap.Billing == nil || snap.Billing.Amount == nil)
}

func (a *MistralAgent) backoff(e error) {

	a.failures = min(a.failures+1, 6)
	delay := time.Duration(1<<a.failures) * 30 * time.Second
	var he *api.MistralHTTPError
	if errors.As(e, &he) && he.RetryAfter > delay {
		delay = he.RetryAfter
	}
	a.next = time.Now().Add(min(delay, time.Hour))
	a.status("stale")
	a.logger.Warn("Mistral polling failed", "error", e)
}
