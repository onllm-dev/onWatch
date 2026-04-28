package web

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// cursorInsightsResponse is the JSON payload for Cursor deep insights.
type cursorInsightsResponse struct {
	Stats    []cursorInsightStat `json:"stats"`
	Insights []insightItem       `json:"insights"`
}

// cursorInsightStat is a stats-row shape that carries linked forecast metadata for the Cursor dashboard.
type cursorInsightStat struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Key      string `json:"key,omitempty"`
	Metric   string `json:"metric,omitempty"`
	Severity string `json:"severity,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

var cursorQuotaDisplayOrder = map[string]int{
	"total_usage": 1,
	"auto_usage":  2,
	"api_usage":   3,
	"credits":     4,
	"on_demand":   5,
}

var cursorDisplayNames = map[string]string{
	"total_usage": "Total Usage",
	"auto_usage":  "Auto + Composer",
	"api_usage":   "API Usage",
	"credits":     "Credits",
	"on_demand":   "On-Demand",
}

func cursorDisplayName(name string) string {
	if dn, ok := cursorDisplayNames[name]; ok {
		return dn
	}
	return name
}

func cursorQuotaOrder(name string) int {
	if order, ok := cursorQuotaDisplayOrder[name]; ok {
		return order
	}
	return 99
}

func utilStatus(util float64) string {
	switch {
	case util >= 95:
		return "exhausted"
	case util >= 80:
		return "critical"
	case util >= 60:
		return "warning"
	default:
		return "healthy"
	}
}

type cursorQuotaRate struct {
	Rate          float64
	HasRate       bool
	TimeToReset   time.Duration
	TimeToExhaust time.Duration
	ExhaustsFirst bool
	ProjectedPct  float64
}

func (h *Handler) computeCursorRate(quotaName string, currentUtil float64, summary *tracker.CursorSummary) cursorQuotaRate {
	var result cursorQuotaRate

	if summary != nil && summary.ResetsAt != nil {
		result.TimeToReset = time.Until(*summary.ResetsAt)
	}

	if h.store != nil {
		points, err := h.store.QueryCursorUtilizationSeries(quotaName, time.Now().Add(-30*time.Minute))
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

func buildCursorBurnRateInsight(quota api.CursorQuota, rate cursorQuotaRate) insightItem {
	item := insightItem{
		Key:   fmt.Sprintf("forecast_%s", quota.Name),
		Title: fmt.Sprintf("%s Burn Rate", cursorDisplayName(quota.Name)),
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

func (h *Handler) buildCursorCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}

	if h.store == nil {
		return response
	}

	latest, err := h.store.QueryLatestCursor()
	if err != nil || latest == nil {
		return response
	}

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	response["accountType"] = string(latest.AccountType)
	response["planName"] = latest.PlanName

	latestPerQuota, err := h.store.QueryCursorLatestPerQuota()
	if err != nil || len(latestPerQuota) == 0 {
		for _, q := range latest.Quotas {
			quotaMap := map[string]interface{}{
				"name":          q.Name,
				"displayName":   cursorDisplayName(q.Name),
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
			if h.cursorTracker != nil {
				if summary, sErr := h.cursorTracker.UsageSummary(q.Name); sErr == nil && summary != nil {
					quotaMap["currentRate"] = summary.CurrentRate
					quotaMap["projectedUtil"] = summary.ProjectedUtil
				}
			}
			response["quotas"] = append(response["quotas"].([]interface{}), quotaMap)
		}
		applyDisplayModeToResponse(response, h.getDisplayMode("cursor"))
		return response
	}

	sort.SliceStable(latestPerQuota, func(i, j int) bool {
		left := cursorQuotaOrder(latestPerQuota[i].Name)
		right := cursorQuotaOrder(latestPerQuota[j].Name)
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
			"displayName":   cursorDisplayName(q.Name),
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
		if h.cursorTracker != nil {
			if summary, sErr := h.cursorTracker.UsageSummary(q.Name); sErr == nil && summary != nil {
				qMap["currentRate"] = summary.CurrentRate
				qMap["projectedUtil"] = summary.ProjectedUtil
			}
		}
		quotas = append(quotas, qMap)
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("cursor"))
	return response
}

func (h *Handler) historyCursor(w http.ResponseWriter, r *http.Request) {
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

	snapshots, err := h.store.QueryCursorRange(start, now, 200)
	if err != nil {
		h.logger.Error("failed to query Cursor history", "error", err)
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

func (h *Handler) cyclesCursor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaName := r.URL.Query().Get("type")
	if quotaName == "" {
		quotaName = "total_usage"
	}

	active, err := h.store.QueryActiveCursorCycle(quotaName)
	if err != nil {
		h.logger.Error("failed to query active Cursor cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	history, err := h.store.QueryCursorCycleHistory(quotaName, 50)
	if err != nil {
		h.logger.Error("failed to query Cursor cycle history", "error", err)
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

func (h *Handler) cycleOverviewCursor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "total_usage"
	}

	overview, err := h.store.QueryCursorCycleOverview(groupBy, 50)
	if err != nil {
		h.logger.Error("failed to query Cursor cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	respondJSON(w, http.StatusOK, overview)
}

func (h *Handler) summaryCursor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildCursorSummaryMap())
}

func (h *Handler) insightsCursor(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildCursorInsights(hidden, rangeDur))
}

func (h *Handler) cursorQuotaNames() []string {
	if h.store == nil {
		return nil
	}
	names, err := h.store.QueryAllCursorQuotaNames()
	if err != nil {
		return nil
	}
	return names
}

func (h *Handler) buildCursorSummaryMap() map[string]interface{} {
	if h.store == nil || h.cursorTracker == nil {
		return map[string]interface{}{}
	}

	quotaNames, err := h.store.QueryAllCursorQuotaNames()
	if err != nil {
		h.logger.Error("failed to query Cursor quota names", "error", err)
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})
	for _, name := range quotaNames {
		summary, err := h.cursorTracker.UsageSummary(name)
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

func (h *Handler) buildCursorInsights(hidden map[string]bool, _ time.Duration) cursorInsightsResponse {
	resp := cursorInsightsResponse{Stats: []cursorInsightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestCursor()
	if err != nil || latest == nil || len(latest.Quotas) == 0 {
		return resp
	}

	planLabel := latest.PlanName
	if planLabel == "" {
		planLabel = string(latest.AccountType)
	}
	if planLabel != "" {
		resp.Stats = append(resp.Stats, cursorInsightStat{
			Label: "Plan",
			Value: planLabel,
		})
	}

	quotas := append([]api.CursorQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left := cursorQuotaOrder(quotas[i].Name)
		right := cursorQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	summaries := map[string]*tracker.CursorSummary{}
	if h.cursorTracker != nil {
		for _, quota := range quotas {
			summary, err := h.cursorTracker.UsageSummary(quota.Name)
			if err == nil && summary != nil {
				summaries[quota.Name] = summary
			}
		}
	}

	preferredQuotas := []string{"total_usage", "auto_usage", "api_usage"}
	selected := make([]api.CursorQuota, 0, len(preferredQuotas))
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
		rate := h.computeCursorRate(quota.Name, quota.Utilization, summaries[quota.Name])
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if hidden[insightKey] {
			continue
		}
		value := "Analyzing..."
		if rate.HasRate {
			value = fmt.Sprintf("%.1f%%/hr", rate.Rate)
		}
		insight := buildCursorBurnRateInsight(quota, rate)
		resp.Stats = append(resp.Stats, cursorInsightStat{
			Key:      insightKey,
			Label:    fmt.Sprintf("%s Burn Rate", cursorDisplayName(quota.Name)),
			Value:    value,
			Sublabel: insight.Sublabel,
			Metric:   insight.Metric,
			Severity: insight.Severity,
			Desc:     insight.Desc,
		})
	}

	return resp
}
