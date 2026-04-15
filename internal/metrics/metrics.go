// Package metrics provides Prometheus metrics exposition for onWatch.
package metrics

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for onWatch.
type Metrics struct {
	reg *prometheus.Registry

	quotaUtilization    *prometheus.GaugeVec
	quotaTimeUntilReset *prometheus.GaugeVec
	creditsBalance      *prometheus.GaugeVec
	authTokenStatus     *prometheus.GaugeVec
	agentLastCycleAge   *prometheus.GaugeVec
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
		quotaTimeUntilReset: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_quota_time_until_reset_seconds",
				Help: "Seconds until the quota resets",
			},
			[]string{"provider", "quota_type", "account_id"},
		),
		creditsBalance: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_credits_balance",
				Help: "Remaining credits balance",
			},
			[]string{"provider", "account_id"},
		),
		authTokenStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onwatch_auth_token_status",
				Help: "Authentication token health: 1=valid, 0=expired_or_stale",
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
	}

	reg.MustRegister(
		m.quotaUtilization,
		m.quotaTimeUntilReset,
		m.creditsBalance,
		m.authTokenStatus,
		m.agentLastCycleAge,
	)

	return m
}

// Gather returns the Prometheus registry gatherer.
func (m *Metrics) Gather() prometheus.Gatherer {
	return m.reg
}

// WriteText writes all metrics in Prometheus text format to w.
func (m *Metrics) WriteText(w io.Writer) error {
	h := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	h.ServeHTTP(&writeResponseWriter{w: w}, &http.Request{Method: "GET"})
	return nil
}

type writeResponseWriter struct {
	w io.Writer
}

func (wr *writeResponseWriter) Header() http.Header         { return make(http.Header) }
func (wr *writeResponseWriter) WriteHeader(int)             {}
func (wr *writeResponseWriter) Write(b []byte) (int, error) { return wr.w.Write(b) }

// Scrape updates all metric values by querying the store.
// Called on each Prometheus scrape.
func (m *Metrics) Scrape(s *store.Store, pollInterval time.Duration) {
	staleThreshold := pollInterval * 2

	m.quotaUtilization.Reset()
	m.quotaTimeUntilReset.Reset()
	m.creditsBalance.Reset()
	m.authTokenStatus.Reset()
	m.agentLastCycleAge.Reset()

	m.scrapeAnthropic(s, staleThreshold)
	m.scrapeCodex(s, staleThreshold)
	m.scrapeCopilot(s, staleThreshold)
	m.scrapeZai(s, staleThreshold)
	m.scrapeMiniMax(s, staleThreshold)
	m.scrapeAntigravity(s, staleThreshold)
	m.scrapeGemini(s, staleThreshold)
	m.scrapeOpenRouter(s, staleThreshold)
}

func (m *Metrics) scrapeAnthropic(s *store.Store, staleThreshold time.Duration) {
	method := "anthropic"

	snap, err := s.QueryLatestAnthropic()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.Name,
			"account_id": "",
		}
		m.quotaUtilization.With(labels).Set(v.Utilization)
		if v.ResetsAt != nil && !v.ResetsAt.IsZero() {
			m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*v.ResetsAt))
		}
	}
}

func (m *Metrics) scrapeCodex(s *store.Store, staleThreshold time.Duration) {
	method := "codex"

	accounts, err := s.QueryProviderAccounts(method)
	if err != nil {
		return
	}
	if len(accounts) == 0 {
		accounts = []store.ProviderAccount{{ID: 1, Name: "default"}}
	}

	for _, acct := range accounts {
		snap, err := s.QueryLatestCodex(acct.ID)
		if err != nil || snap == nil {
			m.setStale(method, strconv.FormatInt(acct.ID, 10), staleThreshold)
			continue
		}

		accountID := strconv.FormatInt(acct.ID, 10)
		m.recordLastCycleAge(method, accountID, snap.CapturedAt, staleThreshold)

		for _, v := range snap.Quotas {
			labels := prometheus.Labels{
				"provider":   method,
				"quota_type": v.Name,
				"account_id": accountID,
			}
			m.quotaUtilization.With(labels).Set(v.Utilization)
			if v.ResetsAt != nil && !v.ResetsAt.IsZero() {
				m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*v.ResetsAt))
			}
		}

		if snap.CreditsBalance != nil {
			m.creditsBalance.With(prometheus.Labels{
				"provider":   method,
				"account_id": accountID,
			}).Set(*snap.CreditsBalance)
		}
	}
}

func (m *Metrics) scrapeCopilot(s *store.Store, staleThreshold time.Duration) {
	method := "copilot"

	snap, err := s.QueryLatestCopilot()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		if v.Unlimited || v.Entitlement <= 0 {
			continue
		}
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.Name,
			"account_id": "",
		}
		m.quotaUtilization.With(labels).Set(100 - v.PercentRemaining)
	}
}

func (m *Metrics) scrapeZai(s *store.Store, staleThreshold time.Duration) {
	method := "zai"

	snap, err := s.QueryLatestZai()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	if snap.TokensUsage > 0 {
		labels := prometheus.Labels{"provider": method, "quota_type": "tokens", "account_id": ""}
		m.quotaUtilization.With(labels).Set(float64(snap.TokensPercentage))
		if snap.TokensNextResetTime != nil && !snap.TokensNextResetTime.IsZero() {
			m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*snap.TokensNextResetTime))
		}
	}
	if snap.TimeUsage > 0 {
		labels := prometheus.Labels{"provider": method, "quota_type": "time", "account_id": ""}
		m.quotaUtilization.With(labels).Set(float64(snap.TimePercentage))
	}
}

func (m *Metrics) scrapeMiniMax(s *store.Store, staleThreshold time.Duration) {
	method := "minimax"

	accounts, err := s.QueryProviderAccounts(method)
	if err != nil {
		return
	}

	for _, acct := range accounts {
		vals, err := s.QueryLatestMiniMax(acct.ID)
		if err != nil || vals == nil {
			m.setStale(method, strconv.FormatInt(acct.ID, 10), staleThreshold)
			continue
		}

		accountID := strconv.FormatInt(acct.ID, 10)
		m.recordLastCycleAge(method, accountID, vals.CapturedAt, staleThreshold)

		for _, v := range vals.Models {
			labels := prometheus.Labels{
				"provider":   method,
				"quota_type": v.ModelName,
				"account_id": accountID,
			}
			m.quotaUtilization.With(labels).Set(v.UsedPercent)
			if v.ResetAt != nil && !v.ResetAt.IsZero() {
				m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*v.ResetAt))
			}
		}
	}
}

func (m *Metrics) scrapeAntigravity(s *store.Store, staleThreshold time.Duration) {
	method := "antigravity"

	snap, err := s.QueryLatestAntigravity()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	for _, v := range snap.Models {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.ModelID,
			"account_id": "",
		}
		m.quotaUtilization.With(labels).Set(100 - v.RemainingPercent)
		if v.ResetTime != nil && !v.ResetTime.IsZero() {
			m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*v.ResetTime))
		}
	}

	if snap.PromptCredits > 0 {
		m.creditsBalance.With(prometheus.Labels{
			"provider":   method,
			"account_id": "",
		}).Set(snap.PromptCredits)
	}
}

func (m *Metrics) scrapeGemini(s *store.Store, staleThreshold time.Duration) {
	method := "gemini"

	snap, err := s.QueryLatestGemini()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	for _, v := range snap.Quotas {
		labels := prometheus.Labels{
			"provider":   method,
			"quota_type": v.ModelID,
			"account_id": "",
		}
		m.quotaUtilization.With(labels).Set(v.UsagePercent)
		if v.ResetTime != nil && !v.ResetTime.IsZero() {
			m.quotaTimeUntilReset.With(labels).Set(secondsUntil(*v.ResetTime))
		}
	}
}

func (m *Metrics) scrapeOpenRouter(s *store.Store, staleThreshold time.Duration) {
	method := "openrouter"

	snap, err := s.QueryLatestOpenRouter()
	if err != nil || snap == nil {
		m.setStale(method, "", staleThreshold)
		return
	}

	m.recordLastCycleAge(method, "", snap.CapturedAt, staleThreshold)

	labels := prometheus.Labels{"provider": method, "quota_type": "credits", "account_id": ""}
	if snap.Limit != nil && *snap.Limit > 0 {
		m.quotaUtilization.With(labels).Set(snap.Usage / *snap.Limit * 100)
	}

	if snap.LimitRemaining != nil {
		m.creditsBalance.With(prometheus.Labels{
			"provider":   method,
			"account_id": "",
		}).Set(*snap.LimitRemaining)
	}
}

func (m *Metrics) recordLastCycleAge(provider, accountID string, capturedAt time.Time, staleThreshold time.Duration) {
	ageSeconds := time.Since(capturedAt).Seconds()
	lbls := prometheus.Labels{"provider": provider, "account_id": accountID}

	if ageSeconds > staleThreshold.Seconds() {
		m.authTokenStatus.With(lbls).Set(0)
	} else {
		m.authTokenStatus.With(lbls).Set(1)
	}

	m.agentLastCycleAge.With(lbls).Set(ageSeconds)
}

func (m *Metrics) setStale(provider, accountID string, staleThreshold time.Duration) {
	lbls := prometheus.Labels{"provider": provider, "account_id": accountID}
	m.authTokenStatus.With(lbls).Set(0)
	m.agentLastCycleAge.With(lbls).Set(staleThreshold.Seconds())
}

func secondsUntil(resetAt time.Time) float64 {
	seconds := time.Until(resetAt).Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}
