package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// SetKimiTracker sets the Kimi tracker for usage summary enrichment.
func (h *Handler) SetKimiTracker(t *tracker.KimiTracker) {
	h.kimiTracker = t
}

func (h *Handler) currentKimi(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildKimiCurrent())
}

func (h *Handler) latestKimiSnapshot() *api.KimiSnapshot {
	if h.store == nil {
		return nil
	}
	latest, err := h.store.QueryLatestKimi(store.DefaultKimiAccountID)
	if err == nil && latest != nil && len(latest.Quotas) > 0 {
		return latest
	}
	now := time.Now().UTC()
	if snaps, rerr := h.store.QueryKimiRange(store.DefaultKimiAccountID, now.Add(-7*24*time.Hour), now); rerr == nil {
		for i := len(snaps) - 1; i >= 0; i-- {
			if len(snaps[i].Quotas) > 0 {
				return snaps[i]
			}
		}
	}
	return latest
}

// buildKimiCurrent builds the /api/current?provider=kimi payload.
func (h *Handler) buildKimiCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"provider":   "kimi",
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}
	if h.store == nil {
		return response
	}

	latest := h.latestKimiSnapshot()
	if latest == nil {
		if creds := api.DetectKimiCredentials(h.logger); creds != nil {
			response["login_method"] = "oauth"
			response["configured"] = true
		}
		return response
	}

	quotas := make([]map[string]interface{}, 0, len(latest.Quotas))
	for _, q := range latest.Quotas {
		display := api.KimiDisplayName(q.Name)
		qm := map[string]interface{}{
			"name":        q.Name,
			"displayName": display,
			"label":       display,
			"utilization": q.Utilization,
			"status":      q.Status,
		}
		if q.ResetsAt != nil {
			// CamelCase for menubar normalizeQuotas / other providers; snake_case
			// kept for dashboard renderKimiQuotaCards (reads resets_at || resetsAt).
			timeUntilReset := time.Until(*q.ResetsAt)
			resetStr := q.ResetsAt.Format(time.RFC3339)
			qm["resetsAt"] = resetStr
			qm["resets_at"] = resetStr
			qm["timeUntilReset"] = formatDuration(timeUntilReset)
			qm["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if q.Limit > 0 {
			qm["limit"] = q.Limit
			qm["used"] = q.Used
			qm["remaining"] = q.Remaining
		}
		quotas = append(quotas, qm)
	}
	response["quotas"] = quotas
	response["user_id"] = latest.UserID
	response["region"] = latest.Region
	if latest.Membership != "" {
		response["membership"] = api.KimiMembershipDisplayName(latest.Membership)
		response["membership_level"] = latest.Membership
	}
	response["login_method"] = "oauth"

	if h.kimiTracker != nil && len(latest.Quotas) > 0 {
		primary := latest.Quotas[0]
		sum := h.kimiTracker.GetKimiSummary(store.DefaultKimiAccountID, primary.Name, latest)
		if sum != nil {
			sm := map[string]interface{}{
				"current_util":     sum.CurrentUtil,
				"current_rate":     sum.CurrentRate,
				"projected_util":   sum.ProjectedUtil,
				"completed_cycles": sum.CompletedCycles,
			}
			if sum.ResetsAt != nil {
				timeUntilReset := time.Until(*sum.ResetsAt)
				resetStr := sum.ResetsAt.Format(time.RFC3339)
				sm["resetsAt"] = resetStr
				sm["resets_at"] = resetStr
				sm["timeUntilReset"] = formatDuration(timeUntilReset)
				sm["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
			}
			response["summary"] = sm
		}
	}
	return response
}

func (h *Handler) historyKimi(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}
	duration, err := parseTimeRange(r.URL.Query().Get("range"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	snaps, err := h.store.QueryKimiRange(store.DefaultKimiAccountID, now.Add(-duration), now)
	if err != nil {
		h.logger.Error("failed to query kimi range for history", "error", err)
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}
	withQuotas := make([]*api.KimiSnapshot, 0, len(snaps))
	for _, s := range snaps {
		if len(s.Quotas) > 0 {
			withQuotas = append(withQuotas, s)
		}
	}
	step := downsampleStep(len(withQuotas), maxChartPoints)
	last := len(withQuotas) - 1
	out := make([]map[string]interface{}, 0, min(len(withQuotas), maxChartPoints))
	for i, s := range withQuotas {
		if step > 1 && i != 0 && i != last && i%step != 0 {
			continue
		}
		entry := map[string]interface{}{"capturedAt": s.CapturedAt.Format(time.RFC3339)}
		for _, q := range s.Quotas {
			entry[q.Name] = q.Utilization
		}
		out = append(out, entry)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) loggingHistoryKimi(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"provider": "kimi", "quotaNames": []string{}, "logs": []interface{}{}})
		return
	}
	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snaps, err := h.store.QueryKimiRange(store.DefaultKimiAccountID, start, end, limit)
	if err != nil {
		h.logger.Error("failed to query kimi logging history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	// Discover quota names from data, prefer weekly first.
	nameSet := map[string]struct{}{}
	order := []string{}
	for _, s := range snaps {
		for _, q := range s.Quotas {
			if _, ok := nameSet[q.Name]; !ok {
				nameSet[q.Name] = struct{}{}
				order = append(order, q.Name)
			}
		}
	}
	// stable preferred order
	preferred := []string{api.KimiQuotaSevenDay, api.KimiQuota5h}
	quotaNames := make([]string, 0, len(order))
	for _, p := range preferred {
		if _, ok := nameSet[p]; ok {
			quotaNames = append(quotaNames, p)
		}
	}
	for _, n := range order {
		found := false
		for _, q := range quotaNames {
			if q == n {
				found = true
				break
			}
		}
		if !found {
			quotaNames = append(quotaNames, n)
		}
	}

	capturedAt := make([]time.Time, 0, len(snaps))
	ids := make([]int64, 0, len(snaps))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snaps))
	for _, s := range snaps {
		if len(s.Quotas) == 0 {
			continue
		}
		row := map[string]loggingHistoryCrossQuota{}
		for _, q := range s.Quotas {
			row[q.Name] = loggingHistoryCrossQuota{
				Name:     q.Name,
				Value:    q.Utilization,
				Limit:    100,
				Percent:  q.Utilization,
				HasValue: true,
				HasLimit: true,
			}
		}
		capturedAt = append(capturedAt, s.CapturedAt)
		ids = append(ids, s.ID)
		series = append(series, row)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "kimi",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}

func kimiCycleToMap(c *store.KimiResetCycle, liveUtil float64) map[string]interface{} {
	peak := c.PeakUtilization
	delta := c.TotalDelta
	if c.CycleEnd == nil && liveUtil > peak {
		peak = liveUtil
	}
	m := map[string]interface{}{
		"id":           c.ID,
		"quotaType":    c.QuotaName,
		"cycleStart":   c.CycleStart.Format(time.RFC3339),
		"cycleEnd":     nil,
		"peakRequests": peak,
		"totalDelta":   delta,
		"crossQuotas": []map[string]interface{}{{
			"name":    c.QuotaName,
			"value":   peak,
			"limit":   100.0,
			"percent": peak,
			"delta":   delta,
		}},
	}
	if c.CycleEnd != nil {
		m["cycleEnd"] = c.CycleEnd.Format(time.RFC3339)
	}
	return m
}

func (h *Handler) cycleOverviewKimi(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}, "provider": "kimi"})
		return
	}
	quotaType := r.URL.Query().Get("quota")
	if quotaType == "" {
		quotaType = api.KimiQuotaSevenDay
	}
	liveUtil := 0.0
	quotaNames := []string{api.KimiQuotaSevenDay}
	if latest := h.latestKimiSnapshot(); latest != nil {
		names := make([]string, 0, len(latest.Quotas))
		for _, q := range latest.Quotas {
			names = append(names, q.Name)
			if q.Name == quotaType {
				liveUtil = q.Utilization
			}
		}
		if len(names) > 0 {
			quotaNames = names
		}
	}
	cycles := make([]map[string]interface{}, 0)
	if history, err := h.store.QueryKimiCyclesForQuota(store.DefaultKimiAccountID, quotaType, 50); err == nil {
		for _, c := range history {
			cycles = append(cycles, kimiCycleToMap(c, liveUtil))
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    quotaType,
		"provider":   "kimi",
		"quotaNames": quotaNames,
		"cycles":     cycles,
	})
}

func (h *Handler) insightsKimi(w http.ResponseWriter, _ *http.Request, _ time.Duration) {
	respondJSON(w, http.StatusOK, h.buildKimiInsights(h.getHiddenInsightKeys()))
}

func (h *Handler) buildKimiInsights(hidden map[string]bool) insightsResponse {
	resp := insightsResponse{Stats: []insightStat{}, Insights: []insightItem{}}
	if h.store == nil {
		return resp
	}
	latest := h.latestKimiSnapshot()
	if latest == nil || len(latest.Quotas) == 0 {
		resp.Insights = append(resp.Insights, insightItem{
			Type: "info", Severity: "info",
			Title: "Getting Started",
			Desc:  "Keep onWatch running to collect Kimi Code usage data. Insights appear after a few snapshots.",
		})
		return resp
	}

	for _, q := range latest.Quotas {
		display := api.KimiDisplayName(q.Name)
		statKey := "kimi-" + q.Name
		if !hidden["utilization"] && !hidden[statKey] {
			resp.Stats = append(resp.Stats, insightStat{
				Label: display + " Used", Value: fmt.Sprintf("%.1f%%", q.Utilization), Sublabel: "current cycle",
			})
		}
		if q.Utilization >= 90 && !hidden["high_usage"] {
			resp.Insights = append(resp.Insights, insightItem{
				Type: "warning", Severity: "high",
				Title: display + " Nearly Exhausted",
				Desc:  fmt.Sprintf("%s utilization is at %.1f%%.", display, q.Utilization),
			})
		} else if q.Utilization >= 75 && !hidden["moderate_usage"] {
			resp.Insights = append(resp.Insights, insightItem{
				Type: "info", Severity: "medium",
				Title: display + " Running High",
				Desc:  fmt.Sprintf("%s utilization is at %.1f%%.", display, q.Utilization),
			})
		}
		if q.ResetsAt != nil && !hidden["resets_at"] {
			resp.Insights = append(resp.Insights, insightItem{
				Type: "info", Severity: "info",
				Title: display + " Reset",
				Desc:  fmt.Sprintf("%s resets at %s.", display, q.ResetsAt.Format("Jan 2, 15:04 MST")),
			})
		}
	}
	if latest.Membership != "" && !hidden["membership"] {
		planName := api.KimiMembershipDisplayName(latest.Membership)
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Membership", Value: planName, Sublabel: "Kimi Code plan",
		})
	}
	return resp
}

