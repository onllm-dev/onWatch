package metrics

import (
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func (m *Metrics) scrapeZenmux(s *store.Store, staleThreshold time.Duration) {
	snap, err := s.QueryLatestZenmux()
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues("zenmux", "query_failed").Inc()
		return
	}
	if snap == nil {
		return
	}
	m.recordLastCycleAge("zenmux", defaultAccountID, snap.CapturedAt, staleThreshold)
	for _, item := range []struct {
		name   string
		window *api.ZenmuxWindow
	}{{"five_hour", snap.Quota5Hour}, {"weekly", snap.Quota7Day}} {
		if item.window == nil {
			continue
		}
		labels := prometheus.Labels{"provider": "zenmux", "quota_type": item.name, "account_id": defaultAccountID}
		if item.window.UsagePercentage != nil {
			m.quotaUtilization.With(labels).Set(*item.window.UsagePercentage * 100)
		}
		if item.window.ResetsAt != nil && item.window.ResetsAt.After(time.Now()) {
			m.quotaResetTimestamp.With(labels).Set(float64(item.window.ResetsAt.Unix()))
		}
	}
}
