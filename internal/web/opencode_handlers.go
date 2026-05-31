package web

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// opencodeInsightsResponse is the JSON payload for OpenCode deep insights.
type opencodeInsightsResponse struct {
	Stats    []opencodeInsightStat `json:"stats"`
	Insights []insightItem       `json:"insights"`
}

// opencodeInsightStat is a stats-row shape that carries linked forecast metadata for the OpenCode dashboard.
type opencodeInsightStat struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Key      string `json:"key,omitempty"`
	Metric   string `json:"metric,omitempty"`
	Severity string `json:"severity,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

var opencodeQuotaDisplayOrder = map[string]int{
	"five_hour": 1,
	"weekly":    2,
	"monthly":   3,
}

var opencodeDisplayNames = map[string]string{
	"five_hour": "5-Hour",
	"weekly":    "Weekly",
	"monthly":   "Monthly",
}

func opencodeDisplayName(name string) string {
	if dn, ok := opencodeDisplayNames[name]; ok {
		return dn
	}
	return name
}

func opencodeQuotaOrder(name string) int {
	if order, ok := opencodeQuotaDisplayOrder[name]; ok {
		return order
	}
	return 99
}



type opencodeQuotaRate struct {
	Rate          float64
	HasRate       bool
	TimeToReset   time.Duration
	TimeToExhaust time.Duration
	ExhaustsFirst bool
	ProjectedPct  float64
}

func (h *Handler) computeOpenCodeRate(quotaName string, currentUtil float64, summary *tracker.OpenCodeSummary) opencodeQuotaRate {
	var result opencodeQuotaRate

	if summary != nil && summary.ResetsAt != nil {
		result.TimeToReset = time.Until(*summary.ResetsAt)
	}

	if h.store != nil {
		points, err := h.store.QueryOpenCodeUtilizationSeries(quotaName, time.Now().Add(-30*time.Minute))
		if err == nil && len(points) >= 2 {
			first := points[0]
			last := points[len(points)-1]
			elapsed := last.CapturedAt.Sub(first.CapturedAt)
			if elapsed >= 5*time.Minute {
				delta := last.Utilization - first.Utilization
				if delta > 0 {
					result.Rate = delta / elapsed.Hours()
					result.HasRate = true
				} else {
					result.HasRate = true
				}
			}
		}
	}

	if !result.HasRate && summary != nil && summary.CurrentRate > 0 {
		result.Rate = summary.CurrentRate
		result.HasRate = true
	}

	if result.HasRate && result.Rate > 0 {
		remaining := 100 - currentUtil
		if remaining > 0 {
			result.TimeToExhaust = time.Duration(remaining / result.Rate * float64(time.Hour))
		}
		if result.TimeToReset > 0 {
			result.ProjectedPct = currentUtil + (result.Rate * result.TimeToReset.Hours())
			if result.ProjectedPct > 100 {
				result.ProjectedPct = 100
			}
			result.ExhaustsFirst = result.TimeToExhaust > 0 && result.TimeToExhaust < result.TimeToReset
		}
	}

	return result
}

func buildOpenCodeBurnRateInsight(quota api.OpenCodeQuota, rate opencodeQuotaRate) insightItem {
	item := insightItem{
		Key:   fmt.Sprintf("forecast_%s", quota.Name),
		Title: fmt.Sprintf("%s Burn Rate", opencodeDisplayName(quota.Name)),
	}

	resetStr := ""
	if rate.TimeToReset > 0 {
		resetStr = formatDuration(rate.TimeToReset)
	}
	projected := quota.Utilization
	if rate.ProjectedPct > projected {
		projected = rate.ProjectedPct
	}
	sublabel := fmt.Sprintf("~%.0f%% by reset", projected)
	if resetStr != "" {
		sublabel = fmt.Sprintf("~%.0f%% by reset in %s", projected, resetStr)
	}

	if !rate.HasRate {
		item.Type = "forecast"
		item.Severity = "info"
		item.Metric = "Analyzing..."
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. Collecting more snapshots to estimate burn rate and refine reset projection.", quota.Utilization)
		return item
	}

	if rate.Rate < 0.01 {
		item.Type = "forecast"
		item.Severity = "positive"
		item.Metric = "Idle"
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. No meaningful burn detected recently, so this quota looks stable through the rest of the cycle.", quota.Utilization)
		return item
	}

	item.Type = "forecast"
	item.Metric = fmt.Sprintf("%.1f%%/hr", rate.Rate)
	if rate.ExhaustsFirst {
		exhaustStr := formatDuration(rate.TimeToExhaust)
		item.Severity = "negative"
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset and likely to exhaust in %s before reset.", quota.Utilization, projected, exhaustStr)
		return item
	}

	if rate.ProjectedPct >= 80 {
		item.Severity = "warning"
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset.", quota.Utilization, projected)
		return item
	}

	item.Severity = "positive"
	item.Sublabel = sublabel
	item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset.", quota.Utilization, projected)
	return item
}

func (h *Handler) buildOpenCodeCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}

	if h.store == nil {
		return response
	}

	latest, err := h.store.QueryLatestOpenCode()
	if err != nil || latest == nil {
		return response
	}

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	response["accountType"] = string(latest.AccountType)
	response["planName"] = latest.PlanName

	latestPerQuota, err := h.store.QueryOpenCodeLatestPerQuota()
	if err != nil || len(latestPerQuota) == 0 {
		for _, q := range latest.Quotas {
			quotaMap := map[string]interface{}{
				"name":          q.Name,
				"displayName":   opencodeDisplayName(q.Name),
				"utilization":   q.Utilization,
				"used":          q.Used,
				"limit":         q.Limit,
				"format":        string(q.Format),
				"status":        utilStatus(q.Utilization),
				"lastUpdatedAt": latest.CapturedAt.Format(time.RFC3339),
				"ageSeconds":    int64(now.Sub(latest.CapturedAt).Seconds()),
			}
			if q.ResetsAt != nil {
				timeUntilReset := time.Until(*q.ResetsAt)
				quotaMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
				quotaMap["timeUntilReset"] = formatDuration(timeUntilReset)
				quotaMap["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
			}
			if h.opencodeTracker != nil {
				if summary, sErr := h.opencodeTracker.UsageSummary(q.Name); sErr == nil && summary != nil {
					quotaMap["currentRate"] = summary.CurrentRate
					quotaMap["projectedUtil"] = summary.ProjectedUtil
				}
			}
			response["quotas"] = append(response["quotas"].([]interface{}), quotaMap)
		}
		applyDisplayModeToResponse(response, h.getDisplayMode("opencode"))
		return response
	}

	sort.SliceStable(latestPerQuota, func(i, j int) bool {
		left := opencodeQuotaOrder(latestPerQuota[i].Name)
		right := opencodeQuotaOrder(latestPerQuota[j].Name)
		if left != right {
			return left < right
		}
		return latestPerQuota[i].Name < latestPerQuota[j].Name
	})

	var quotas []interface{}
	for _, q := range latestPerQuota {
		age := now.Sub(q.CapturedAt)
		qMap := map[string]interface{}{
			"name":          q.Name,
			"displayName":   opencodeDisplayName(q.Name),
			"utilization":   q.Utilization,
			"used":          q.Used,
			"limit":         q.Limit,
			"format":        q.Format,
			"status":        utilStatus(q.Utilization),
			"lastUpdatedAt": q.CapturedAt.Format(time.RFC3339),
			"ageSeconds":    int64(age.Seconds()),
			"isStale":       age > 30*time.Minute,
		}
		if q.ResetsAt != nil {
			timeUntilReset := time.Until(*q.ResetsAt)
			qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			qMap["timeUntilReset"] = formatDuration(timeUntilReset)
			qMap["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if h.opencodeTracker != nil {
			if summary, sErr := h.opencodeTracker.UsageSummary(q.Name); sErr == nil && summary != nil {
				qMap["currentRate"] = summary.CurrentRate
				qMap["projectedUtil"] = summary.ProjectedUtil
			}
		}
		quotas = append(quotas, qMap)
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("opencode"))
	return response
}

func (h *Handler) historyOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "7d"
	}

	now := time.Now().UTC()
	var start time.Time
	switch rangeParam {
	case "1h":
		start = now.Add(-1 * time.Hour)
	case "6h":
		start = now.Add(-6 * time.Hour)
	case "24h", "1d":
		start = now.Add(-24 * time.Hour)
	case "3d":
		start = now.Add(-3 * 24 * time.Hour)
	case "30d":
		start = now.Add(-30 * 24 * time.Hour)
	case "7d":
		start = now.Add(-7 * 24 * time.Hour)
	default:
		start = now.Add(-7 * 24 * time.Hour)
	}

	snapshots, err := h.store.QueryOpenCodeRange(start, now, 200)
	if err != nil {
		h.logger.Error("failed to query OpenCode history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	type historyEntry struct {
		CapturedAt string                   `json:"capturedAt"`
		Quotas     []map[string]interface{} `json:"quotas"`
	}

	result := make([]historyEntry, 0, len(snapshots))
	for _, snap := range snapshots {
		entry := historyEntry{
			CapturedAt: snap.CapturedAt.Format(time.RFC3339),
		}
		for _, q := range snap.Quotas {
			qMap := map[string]interface{}{
				"name":        q.Name,
				"utilization": q.Utilization,
				"used":        q.Used,
				"limit":       q.Limit,
				"format":      string(q.Format),
			}
			if q.ResetsAt != nil {
				qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			}
			entry.Quotas = append(entry.Quotas, qMap)
		}
		result = append(result, entry)
	}

	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) cyclesOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaName := r.URL.Query().Get("type")
	if quotaName == "" {
		quotaName = "total_usage"
	}

	active, err := h.store.QueryActiveOpenCodeCycle(quotaName)
	if err != nil {
		h.logger.Error("failed to query active OpenCode cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	history, err := h.store.QueryOpenCodeCycleHistory(quotaName, 50)
	if err != nil {
		h.logger.Error("failed to query OpenCode cycle history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	var cycles []map[string]interface{}
	if active != nil {
		cycleMap := map[string]interface{}{
			"id":              active.ID,
			"quotaName":       active.QuotaName,
			"cycleStart":      active.CycleStart.Format(time.RFC3339),
			"cycleEnd":        nil,
			"peakUtilization": active.PeakUtilization,
			"totalDelta":      active.TotalDelta,
			"isActive":        true,
		}
		if active.ResetsAt != nil {
			cycleMap["resetsAt"] = active.ResetsAt.Format(time.RFC3339)
			cycleMap["timeUntilReset"] = formatDuration(time.Until(*active.ResetsAt))
		}
		cycles = append(cycles, cycleMap)
	}

	for _, c := range history {
		cycleMap := map[string]interface{}{
			"id":              c.ID,
			"quotaName":       c.QuotaName,
			"cycleStart":      c.CycleStart.Format(time.RFC3339),
			"cycleEnd":        c.CycleEnd.Format(time.RFC3339),
			"peakUtilization": c.PeakUtilization,
			"totalDelta":      c.TotalDelta,
			"isActive":        false,
		}
		if c.ResetsAt != nil {
			cycleMap["resetsAt"] = c.ResetsAt.Format(time.RFC3339)
		}
		cycles = append(cycles, cycleMap)
	}

	respondJSON(w, http.StatusOK, cycles)
}

func (h *Handler) cycleOverviewOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "total_usage"
	}

	overview, err := h.store.QueryOpenCodeCycleOverview(groupBy, 50)
	if err != nil {
		h.logger.Error("failed to query OpenCode cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	respondJSON(w, http.StatusOK, overview)
}

func (h *Handler) summaryOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildOpenCodeSummaryMap())
}

func (h *Handler) insightsOpenCode(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildOpenCodeInsights(hidden, rangeDur))
}

func (h *Handler) opencodeQuotaNames() []string {
	if h.store == nil {
		return nil
	}
	names, err := h.store.QueryAllOpenCodeQuotaNames()
	if err != nil {
		return nil
	}
	return names
}

func (h *Handler) buildOpenCodeSummaryMap() map[string]interface{} {
	if h.store == nil || h.opencodeTracker == nil {
		return map[string]interface{}{}
	}

	quotaNames, err := h.store.QueryAllOpenCodeQuotaNames()
	if err != nil {
		h.logger.Error("failed to query OpenCode quota names", "error", err)
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})
	for _, name := range quotaNames {
		summary, err := h.opencodeTracker.UsageSummary(name)
		if err != nil || summary == nil {
			continue
		}
		entry := map[string]interface{}{
			"currentUtil":     summary.CurrentUtil,
			"completedCycles": summary.CompletedCycles,
			"peakCycle":       summary.PeakCycle,
			"avgPerCycle":     summary.AvgPerCycle,
			"totalTracked":    summary.TotalTracked,
		}
		if summary.ResetsAt != nil {
			entry["resetsAt"] = summary.ResetsAt.Format(time.RFC3339)
			entry["timeUntilReset"] = formatDuration(summary.TimeUntilReset)
		}
		result[name] = entry
	}
	return result
}

func (h *Handler) buildOpenCodeInsights(hidden map[string]bool, _ time.Duration) opencodeInsightsResponse {
	resp := opencodeInsightsResponse{Stats: []opencodeInsightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestOpenCode()
	if err != nil || latest == nil || len(latest.Quotas) == 0 {
		return resp
	}

	planLabel := latest.PlanName
	if planLabel == "" {
		planLabel = string(latest.AccountType)
	}
	if planLabel != "" {
		resp.Stats = append(resp.Stats, opencodeInsightStat{
			Label: "Plan",
			Value: planLabel,
		})
	}

	quotas := append([]api.OpenCodeQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left := opencodeQuotaOrder(quotas[i].Name)
		right := opencodeQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	summaries := map[string]*tracker.OpenCodeSummary{}
	if h.opencodeTracker != nil {
		for _, quota := range quotas {
			summary, err := h.opencodeTracker.UsageSummary(quota.Name)
			if err == nil && summary != nil {
				summaries[quota.Name] = summary
			}
		}
	}

	preferredQuotas := []string{"five_hour", "weekly", "monthly"}
	selected := make([]api.OpenCodeQuota, 0, len(preferredQuotas))
	for _, name := range preferredQuotas {
		for _, quota := range quotas {
			if quota.Name == name {
				selected = append(selected, quota)
				break
			}
		}
	}
	if len(selected) == 0 {
		selected = quotas
	}

	for _, quota := range selected {
		rate := h.computeOpenCodeRate(quota.Name, quota.Utilization, summaries[quota.Name])
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if hidden[insightKey] {
			continue
		}
		value := "Analyzing..."
		if rate.HasRate {
			value = fmt.Sprintf("%.1f%%/hr", rate.Rate)
		}
		insight := buildOpenCodeBurnRateInsight(quota, rate)
		resp.Stats = append(resp.Stats, opencodeInsightStat{
			Key:      insightKey,
			Label:    fmt.Sprintf("%s Burn Rate", opencodeDisplayName(quota.Name)),
			Value:    value,
			Sublabel: insight.Sublabel,
			Metric:   insight.Metric,
			Severity: insight.Severity,
			Desc:     insight.Desc,
		})
	}

	return resp
}

func (h *Handler) loggingHistoryOpenCode(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryOpenCodeRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query OpenCode snapshots", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query logging history")
		return
	}

	quotaSet := map[string]bool{}
	for _, snap := range snapshots {
		for _, q := range snap.Quotas {
			quotaSet[q.Name] = true
		}
	}

	quotaNames := make([]string, 0, len(quotaSet))
	for qn := range quotaSet {
		quotaNames = append(quotaNames, qn)
	}
	if len(quotaNames) == 0 {
		quotaNames = []string{"five_hour", "weekly", "monthly"}
	} else {
		sort.SliceStable(quotaNames, func(i, j int) bool {
			left := opencodeQuotaOrder(quotaNames[i])
			right := opencodeQuotaOrder(quotaNames[j])
			if left != right {
				return left < right
			}
			return quotaNames[i] < quotaNames[j]
		})
	}

	capturedAt := make([]time.Time, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snapshots))

	for _, snap := range snapshots {
		capturedAt = append(capturedAt, snap.CapturedAt)
		ids = append(ids, snap.ID)
		row := make(map[string]loggingHistoryCrossQuota, len(snap.Quotas))
		for _, q := range snap.Quotas {
			row[q.Name] = loggingHistoryCrossQuota{
				Name:     q.Name,
				Value:    q.Used,
				Limit:    q.Limit,
				Percent:  q.Utilization,
				HasValue: q.Used > 0 || q.Limit > 0,
				HasLimit: q.Limit > 0,
			}
		}
		series = append(series, row)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "opencode",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}
