// Package metrics provides Prometheus metrics exposition for onWatch.
package metrics

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// defaultAccountID is emitted for single-account providers so the account_id
// label is always non-empty (PromQL-friendly).
const defaultAccountID = "default"

// Metrics holds all Prometheus metrics for onWatch.
type Metrics struct {
	reg *prometheus.Registry

	// scrapeMu serializes Scrape() so concurrent callers (HTTP handler, tests,
	// signal handlers) cannot race the Reset()+repopulate sequence and observe
	// a half-empty snapshot.
	scrapeMu sync.Mutex

	quotaUtilization    *prometheus.GaugeVec
	quotaResetTimestamp *prometheus.GaugeVec
	creditsBalance      *prometheus.GaugeVec
	agentHealthy        *prometheus.GaugeVec
	agentLastCycleAge   *prometheus.GaugeVec
	buildInfo           *prometheus.GaugeVec

	// Counters live outside the Scrape() reset path so they accumulate
	// monotonically across scrapes (standard Prometheus counter semantics).
	scrapeErrorsTotal    *prometheus.CounterVec
	cyclesCompletedTotal *prometheus.CounterVec
	cyclesFailedTotal    *prometheus.CounterVec

	// API Integrations (PR #52) - counts/spend are snapshots of the current
	// DB aggregate rather than event-driven counters because ingestion is in
	// a separate process path; no `_total` suffix for honesty.
	apiIntegrationRequests *prometheus.GaugeVec
	apiIntegrationSpendUSD *prometheus.GaugeVec

	// accountInfo is a join-metric (value always 1) mapping numeric account_id
	// to human-readable account_name for Grafana etc.
	accountInfo *prometheus.GaugeVec
}

// New creates a new Metrics instance with a custom registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	// Register standard Go/process collectors for free infrastructure metrics.
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		reg: reg,
		quotaUtilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_quota_utilization_percent",
				Help: "Current quota utilization as a percentage (0-100)",
			},
			[]string{"provider", "quota_type", "account_id"},
		),
		quotaResetTimestamp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_quota_reset_timestamp_seconds",
				Help: "Unix timestamp (seconds) at which the quota next resets. Compute remaining time with: metric - time()",
			},
			[]string{"provider", "quota_type", "account_id"},
		),
		creditsBalance: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_credits_balance",
				Help: "Remaining credits balance. The `unit` label disambiguates per-provider semantics (usd, credits, prompt_credits).",
			},
			[]string{"provider", "account_id", "unit"},
		),
		agentHealthy: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_agent_healthy",
				Help: "1 if the polling agent has recent successful data (within 2x poll interval), 0 if stale. Reflects poll freshness, not OAuth token validity.",
			},
			[]string{"provider", "account_id"},
		),
		agentLastCycleAge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_agent_last_cycle_age_seconds",
				Help: "Seconds since the last successful poll cycle",
			},
			[]string{"provider", "account_id"},
		),
		buildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_build_info",
				Help: "Build metadata for the running onWatch binary. Always 1; labels carry the info.",
			},
			[]string{"version", "go_version", "commit"},
		),
		scrapeErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "onwatch_scrape_errors_total",
				Help: "Count of errors encountered while refreshing /metrics from the local store. Alert on rate(...) to detect broken metric collection itself.",
			},
			[]string{"provider", "error_type"},
		),
		cyclesCompletedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "onwatch_cycles_completed_total",
				Help: "Count of successful poll cycles per provider account. Use rate() for activity; divergence from cycles_failed_total indicates sustained failures.",
			},
			[]string{"provider", "account_id"},
		),
		cyclesFailedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "onwatch_cycles_failed_total",
				Help: "Count of failed poll cycles per provider account, labelled by reason.",
			},
			[]string{"provider", "account_id", "reason"},
		),
		apiIntegrationRequests: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_api_integration_requests",
				Help: "Number of API integration usage events currently stored in the local DB, grouped by integration.",
			},
			[]string{"integration"},
		),
		apiIntegrationSpendUSD: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_api_integration_spend_usd",
				Help: "Cumulative USD spend tracked by API integration ingestion (from the local DB).",
			},
			[]string{"integration"},
		),
		accountInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_account_info",
				Help: "Join-metric (always 1) mapping numeric account_id to human-readable account_name. Use `{..} * on(account_id) group_left(account_name) onwatch_account_info` in PromQL.",
			},
			[]string{"provider", "account_id", "account_name"},
		),
	}

	reg.MustRegister(
		m.quotaUtilization,
		m.quotaResetTimestamp,
		m.creditsBalance,
		m.agentHealthy,
		m.agentLastCycleAge,
		m.buildInfo,
		m.scrapeErrorsTotal,
		m.cyclesCompletedTotal,
		m.cyclesFailedTotal,
		m.apiIntegrationRequests,
		m.apiIntegrationSpendUSD,
		m.accountInfo,
	)

	// Populate build_info with whatever we can discover at init time.
	// main.go should call SetBuildInfo(version) once the app version is known.
	m.buildInfo.With(prometheus.Labels{
		"version":    "unknown",
		"go_version": runtime.Version(),
		"commit":     readVCSRevision(),
	}).Set(1)

	return m
}

// SetBuildInfo records the running binary's version in onwatch_build_info.
// Safe to call once at startup; the previous {version="unknown", ...} series
// is cleared so only one build_info series exists at a time.
func (m *Metrics) SetBuildInfo(version string) {
	if version == "" {
		return
	}
	m.buildInfo.Reset()
	m.buildInfo.With(prometheus.Labels{
		"version":    version,
		"go_version": runtime.Version(),
		"commit":     readVCSRevision(),
	}).Set(1)
}

// readVCSRevision returns the VCS commit hash embedded by the Go toolchain, or
// "unknown" if the binary was not built from a VCS checkout.
func readVCSRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "unknown"
}

// Gather returns the Prometheus registry gatherer.
func (m *Metrics) Gather() prometheus.Gatherer {
	return m.reg
}

// Handler returns an http.Handler that refreshes metric values from the store
// and serves them in Prometheus text format. Using this replaces the previous
// WriteText/writeResponseWriter shim with the standard promhttp pipeline.
//
// Compression is disabled: onWatch metrics bodies are small (<50KB), so the
// transport overhead of gzip is not worth it, and leaving it enabled breaks
// callers that consume the body directly without an http.Client that decodes
// gzip transparently.
func (m *Metrics) Handler(s *store.Store, pollInterval time.Duration) http.Handler {
	promHandler := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		DisableCompression: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Scrape(s, pollInterval)
		promHandler.ServeHTTP(w, r)
	})
}

// Scrape updates all metric values by querying the store.
// Called on each Prometheus scrape.
func (m *Metrics) Scrape(s *store.Store, pollInterval time.Duration) {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()

	staleThreshold := pollInterval * 2

	m.quotaUtilization.Reset()
	m.quotaResetTimestamp.Reset()
	m.creditsBalance.Reset()
	m.agentHealthy.Reset()
	m.agentLastCycleAge.Reset()
	m.apiIntegrationRequests.Reset()
	m.apiIntegrationSpendUSD.Reset()
	m.accountInfo.Reset()

	m.scrapeAnthropic(s, staleThreshold)
	m.scrapeCodex(s, staleThreshold)
	m.scrapeCopilot(s, staleThreshold)
	m.scrapeZai(s, staleThreshold)
	m.scrapeMiniMax(s, staleThreshold)
	m.scrapeAntigravity(s, staleThreshold)
	m.scrapeGemini(s, staleThreshold)
	m.scrapeOpenRouter(s, staleThreshold)
	m.scrapeMoonshot(s, staleThreshold)
	m.scrapeDeepSeek(s, staleThreshold)
	m.scrapeAPIIntegrations(s, staleThreshold)
}

func (m *Metrics) scrapeAPIIntegrations(s *store.Store, staleThreshold time.Duration) {
	method := "api_integrations"

	summary, err := s.QueryAPIIntegrationUsageSummary()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}

	// Aggregate per integration (summary rows can split by account/model).
	type agg struct {
		requests       int
		spendUSD       float64
		lastCapturedAt time.Time
	}
	perIntegration := map[string]*agg{}
	for _, row := range summary {
		if row.IntegrationName == "" {
			continue
		}
		a := perIntegration[row.IntegrationName]
		if a == nil {
			a = &agg{}
			perIntegration[row.IntegrationName] = a
		}
		a.requests += row.RequestCount
		a.spendUSD += row.TotalCostUSD
		if row.LastCapturedAt.After(a.lastCapturedAt) {
			a.lastCapturedAt = row.LastCapturedAt
		}
	}

	if len(perIntegration) == 0 {
		return
	}

	var newest time.Time
	for integration, a := range perIntegration {
		m.apiIntegrationRequests.WithLabelValues(integration).Set(float64(a.requests))
		m.apiIntegrationSpendUSD.WithLabelValues(integration).Set(a.spendUSD)
		if a.lastCapturedAt.After(newest) {
			newest = a.lastCapturedAt
		}
	}

	if !newest.IsZero() {
		m.recordLastCycleAge(method, defaultAccountID, newest, staleThreshold)
	}
}

func (m *Metrics) scrapeAnthropic(s *store.Store, staleThreshold time.Duration) {
	method := "anthropic"

	snap, err := s.QueryLatestAnthropic()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.Name,
			"account_id": defaultAccountID,
		}
		m.quotaUtilization.With(labels).Set(v.Utilization)
		if v.ResetsAt != nil && !v.ResetsAt.IsZero() {
			m.quotaResetTimestamp.With(labels).Set(float64(v.ResetsAt.Unix()))
		}
	}
}

func (m *Metrics) scrapeCodex(s *store.Store, staleThreshold time.Duration) {
	method := "codex"

	accounts, err := s.QueryProviderAccounts(method)
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if len(accounts) == 0 {
		accounts = []store.ProviderAccount{{ID: 1, Name: "default"}}
	}

	for _, acct := range accounts {
		accountID := strconv.FormatInt(acct.ID, 10)
		if acct.Name != "" {
			m.accountInfo.WithLabelValues(method, accountID, acct.Name).Set(1)
		}

		snap, err := s.QueryLatestCodex(acct.ID)
		if err != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			continue
		}
		if snap == nil {
			continue
		}

		m.recordLastCycleAge(method, accountID, snap.CapturedAt, staleThreshold)

		for _, v := range snap.Quotas {
			labels := prometheus.Labels{
				"provider":   method,
				"quota_type": v.Name,
				"account_id": accountID,
			}
			m.quotaUtilization.With(labels).Set(v.Utilization)
			if v.ResetsAt != nil && !v.ResetsAt.IsZero() {
				m.quotaResetTimestamp.With(labels).Set(float64(v.ResetsAt.Unix()))
			}
		}

		if snap.CreditsBalance != nil {
			m.creditsBalance.With(prometheus.Labels{
				"provider":   method,
				"account_id": accountID,
				"unit":       "credits",
			}).Set(*snap.CreditsBalance)
		}
	}
}

func (m *Metrics) scrapeCopilot(s *store.Store, staleThreshold time.Duration) {
	method := "copilot"

	snap, err := s.QueryLatestCopilot()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		if v.Unlimited || v.Entitlement <= 0 {
			continue
		}
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.Name,
			"account_id": defaultAccountID,
		}
		m.quotaUtilization.With(labels).Set(100 - v.PercentRemaining)
	}
}

func (m *Metrics) scrapeZai(s *store.Store, staleThreshold time.Duration) {
	method := "zai"

	snap, err := s.QueryLatestZai()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	if snap.TokensUsage > 0 {
		labels := prometheus.Labels{"provider": method, "quota_type": "tokens", "account_id": defaultAccountID}
		m.quotaUtilization.With(labels).Set(float64(snap.TokensPercentage))
		if snap.TokensNextResetTime != nil && !snap.TokensNextResetTime.IsZero() {
			m.quotaResetTimestamp.With(labels).Set(float64(snap.TokensNextResetTime.Unix()))
		}
	}
	if snap.TimeUsage > 0 {
		labels := prometheus.Labels{"provider": method, "quota_type": "time", "account_id": defaultAccountID}
		m.quotaUtilization.With(labels).Set(float64(snap.TimePercentage))
	}
}

func (m *Metrics) scrapeMiniMax(s *store.Store, staleThreshold time.Duration) {
	method := "minimax"

	accounts, err := s.QueryProviderAccounts(method)
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}

	for _, acct := range accounts {
		accountID := strconv.FormatInt(acct.ID, 10)
		if acct.Name != "" {
			m.accountInfo.WithLabelValues(method, accountID, acct.Name).Set(1)
		}

		vals, err := s.QueryLatestMiniMax(acct.ID)
		if err != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			continue
		}
		if vals == nil {
			continue
		}

		m.recordLastCycleAge(method, accountID, vals.CapturedAt, staleThreshold)

		for _, v := range vals.Models {
			labels := prometheus.Labels{
				"provider":   method,
				"quota_type": v.ModelName,
				"account_id": accountID,
			}
			m.quotaUtilization.With(labels).Set(v.UsedPercent)
			if v.ResetAt != nil && !v.ResetAt.IsZero() {
				m.quotaResetTimestamp.With(labels).Set(float64(v.ResetAt.Unix()))
			}
		}
	}
}

func (m *Metrics) scrapeAntigravity(s *store.Store, staleThreshold time.Duration) {
	method := "antigravity"

	snap, err := s.QueryLatestAntigravity()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	for _, v := range snap.Models {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.ModelID,
			"account_id": defaultAccountID,
		}
		m.quotaUtilization.With(labels).Set(100 - v.RemainingPercent)
		if v.ResetTime != nil && !v.ResetTime.IsZero() {
			m.quotaResetTimestamp.With(labels).Set(float64(v.ResetTime.Unix()))
		}
	}

	if snap.PromptCredits > 0 {
		m.creditsBalance.With(prometheus.Labels{
			"provider":   method,
			"account_id": defaultAccountID,
			"unit":       "prompt_credits",
		}).Set(snap.PromptCredits)
	}
}

func (m *Metrics) scrapeGemini(s *store.Store, staleThreshold time.Duration) {
	method := "gemini"

	snap, err := s.QueryLatestGemini()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.ModelID,
			"account_id": defaultAccountID,
		}
		m.quotaUtilization.With(labels).Set(v.UsagePercent)
		if v.ResetTime != nil && !v.ResetTime.IsZero() {
			m.quotaResetTimestamp.With(labels).Set(float64(v.ResetTime.Unix()))
		}
	}
}

func (m *Metrics) scrapeOpenRouter(s *store.Store, staleThreshold time.Duration) {
	method := "openrouter"

	snap, err := s.QueryLatestOpenRouter()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	labels := prometheus.Labels{"provider": method, "quota_type": "credits", "account_id": defaultAccountID}
	if snap.Limit != nil && *snap.Limit > 0 {
		m.quotaUtilization.With(labels).Set(snap.Usage / *snap.Limit * 100)
	}

	if snap.LimitRemaining != nil {
		m.creditsBalance.With(prometheus.Labels{
			"provider":   method,
			"account_id": defaultAccountID,
			"unit":       "usd",
		}).Set(*snap.LimitRemaining)
	}
}

func (m *Metrics) scrapeMoonshot(s *store.Store, staleThreshold time.Duration) {
	method := "moonshot"

	snap, err := s.QueryLatestMoonshot()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       "cny_available",
	}).Set(snap.AvailableBalance)
	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       "cny_voucher",
	}).Set(snap.VoucherBalance)
	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       "cny_cash",
	}).Set(snap.CashBalance)
}

func (m *Metrics) scrapeDeepSeek(s *store.Store, staleThreshold time.Duration) {
	method := "deepseek"

	snap, err := s.QueryLatestDeepSeek()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}

	m.recordLastCycleAge(method, defaultAccountID, snap.CapturedAt, staleThreshold)

	unitPrefix := "cny"
	if snap.Currency == "USD" {
		unitPrefix = "usd"
	} else if snap.Currency != "CNY" && snap.Currency != "" {
		unitPrefix = snap.Currency
	}

	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       unitPrefix + "_total",
	}).Set(snap.TotalBalance)
	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       unitPrefix + "_granted",
	}).Set(snap.GrantedBalance)
	m.creditsBalance.With(prometheus.Labels{
		"provider":   method,
		"account_id": defaultAccountID,
		"unit":       unitPrefix + "_topped_up",
	}).Set(snap.ToppedUpBalance)
}

// RecordCycleCompleted increments the successful-poll counter.
// Safe to call on a nil receiver (no-op), so agents can be instantiated
// without wiring metrics.
func (m *Metrics) RecordCycleCompleted(provider, accountID string) {
	if m == nil {
		return
	}
	if accountID == "" {
		accountID = defaultAccountID
	}
	m.cyclesCompletedTotal.WithLabelValues(provider, accountID).Inc()
}

// RecordCycleFailed increments the failed-poll counter with a reason label.
// Safe to call on a nil receiver.
func (m *Metrics) RecordCycleFailed(provider, accountID, reason string) {
	if m == nil {
		return
	}
	if accountID == "" {
		accountID = defaultAccountID
	}
	if reason == "" {
		reason = "unknown"
	}
	m.cyclesFailedTotal.WithLabelValues(provider, accountID, reason).Inc()
}

// RecordScrapeError is called internally when Scrape() cannot reach the local
// store. Exposed for tests.
func (m *Metrics) RecordScrapeError(provider, errorType string) {
	if m == nil {
		return
	}
	m.scrapeErrorsTotal.WithLabelValues(provider, errorType).Inc()
}

func (m *Metrics) recordLastCycleAge(provider, accountID string, capturedAt time.Time, staleThreshold time.Duration) {
	ageSeconds := time.Since(capturedAt).Seconds()
	lbls := prometheus.Labels{"provider": provider, "account_id": accountID}

	if ageSeconds > staleThreshold.Seconds() {
		m.agentHealthy.With(lbls).Set(0)
	} else {
		m.agentHealthy.With(lbls).Set(1)
	}

	m.agentLastCycleAge.With(lbls).Set(ageSeconds)
}
