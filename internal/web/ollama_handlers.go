package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// SetOllamaTracker sets the Ollama tracker for usage summary enrichment.
func (h *Handler) SetOllamaTracker(t *tracker.OllamaTracker) {
	h.ollamaTracker = t
}

func (h *Handler) currentOllama(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.buildOllamaCurrent())
}

// ollamaInsightsResponse is the JSON payload for Ollama deep insights.
type ollamaInsightsResponse struct {
	Stats    []ollamaInsightStat `json:"stats"`
	Insights []insightItem       `json:"insights"`
}

// ollamaInsightStat is a stats-row shape that carries linked forecast metadata for the Ollama dashboard.
type ollamaInsightStat struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Key      string `json:"key,omitempty"`
	Metric   string `json:"metric,omitempty"`
	Severity string `json:"severity,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

var ollamaQuotaDisplayOrder = map[string]int{
	"monthly": 1,
}

var ollamaDisplayNames = map[string]string{
	"monthly": "Monthly Included Usage",
}

func ollamaDisplayName(name string) string {
	if dn, ok := ollamaDisplayNames[name]; ok {
		return dn
	}
	return name
}

func ollamaQuotaOrder(name string) int {
	if order, ok := ollamaQuotaDisplayOrder[name]; ok {
		return order
	}
	return 99
}

// ollamaTitleCase upper-cases the first rune of each space-separated word.
func ollamaTitleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

type ollamaQuotaRate struct {
	Rate          float64
	HasRate       bool
	TimeToReset   time.Duration
	TimeToExhaust time.Duration
	ExhaustsFirst bool
	ProjectedPct  float64
}

func (h *Handler) computeOllamaRate(quotaName string, currentUtil float64, summary *tracker.OllamaSummary) ollamaQuotaRate {
	var result ollamaQuotaRate

	if summary != nil && summary.ResetsAt != nil {
		result.TimeToReset = time.Until(*summary.ResetsAt)
	}

	if h.store != nil {
		points, err := h.store.QueryOllamaUtilizationSeries(quotaName, time.Now().Add(-30*time.Minute))
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

func buildOllamaBurnRateInsight(quota api.OllamaQuota, rate ollamaQuotaRate) insightItem {
	item := insightItem{
		Key:   fmt.Sprintf("forecast_%s", quota.Name),
		Title: fmt.Sprintf("%s Burn Rate", ollamaDisplayName(quota.Name)),
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

func (h *Handler) buildOllamaCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}

	if h.store == nil {
		return response
	}

	latest, err := h.store.QueryLatestOllama()
	if err != nil || latest == nil {
		return response
	}

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	response["plan"] = latest.Plan
	response["accountName"] = latest.AccountName
	response["monthlyUsedUsd"] = latest.MonthlyUsedUSD
	response["monthlyLimitUsd"] = latest.MonthlyLimitUSD
	response["extraCostUsd"] = latest.ExtraCostUSD

	models := make([]interface{}, 0, len(latest.Models))
	for _, m := range latest.Models {
		models = append(models, map[string]interface{}{
			"name":         m.Name,
			"requestCount": m.RequestCount,
			"cost":         m.Cost,
		})
	}
	response["models"] = models

	quotaFromLatest := func(q api.OllamaQuota, capturedAt time.Time) map[string]interface{} {
		age := now.Sub(capturedAt)
		qMap := map[string]interface{}{
			"name":          q.Name,
			"displayName":   ollamaDisplayName(q.Name),
			"utilization":   q.Utilization,
			"used":          q.Used,
			"limit":         q.Limit,
			"format":        string(q.Format),
			"status":        utilStatus(q.Utilization),
			"lastUpdatedAt": capturedAt.Format(time.RFC3339),
			"ageSeconds":    int64(age.Seconds()),
			"isStale":       age > 30*time.Minute,
		}
		if q.Limit == 0 {
			qMap["limitUnknown"] = true
		}
		if q.ResetsAt != nil {
			timeUntilReset := time.Until(*q.ResetsAt)
			qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			qMap["timeUntilReset"] = formatDuration(timeUntilReset)
			qMap["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if h.ollamaTracker != nil {
			if summary, sErr := h.ollamaTracker.UsageSummary(q.Name); sErr == nil && summary != nil {
				qMap["currentRate"] = summary.CurrentRate
				qMap["projectedUtil"] = summary.ProjectedUtil
			}
		}
		return qMap
	}

	latestPerQuota, err := h.store.QueryOllamaLatestPerQuota()
	if err != nil || len(latestPerQuota) == 0 {
		for _, q := range latest.Quotas {
			response["quotas"] = append(response["quotas"].([]interface{}), quotaFromLatest(q, latest.CapturedAt))
		}
		applyDisplayModeToResponse(response, h.getDisplayMode("ollama"))
		return response
	}

	sort.SliceStable(latestPerQuota, func(i, j int) bool {
		left := ollamaQuotaOrder(latestPerQuota[i].Name)
		right := ollamaQuotaOrder(latestPerQuota[j].Name)
		if left != right {
			return left < right
		}
		return latestPerQuota[i].Name < latestPerQuota[j].Name
	})

	var quotas []interface{}
	for _, q := range latestPerQuota {
		quotas = append(quotas, quotaFromLatest(api.OllamaQuota{
			Name:        q.Name,
			Used:        q.Used,
			Limit:       q.Limit,
			Utilization: q.Utilization,
			Format:      api.OllamaQuotaFormat(q.Format),
			ResetsAt:    q.ResetsAt,
		}, q.CapturedAt))
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("ollama"))
	return response
}

func (h *Handler) historyOllama(w http.ResponseWriter, r *http.Request) {
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

	snapshots, err := h.store.QueryOllamaRange(start, now, 200)
	if err != nil {
		h.logger.Error("failed to query Ollama history", "error", err)
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

func (h *Handler) cyclesOllama(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaName := r.URL.Query().Get("type")
	if quotaName == "" {
		quotaName = "monthly"
	}

	active, err := h.store.QueryActiveOllamaCycle(quotaName)
	if err != nil {
		h.logger.Error("failed to query active Ollama cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	history, err := h.store.QueryOllamaCycleHistory(quotaName, 50)
	if err != nil {
		h.logger.Error("failed to query Ollama cycle history", "error", err)
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

func (h *Handler) cycleOverviewOllama(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "monthly"
	}

	overview, err := h.store.QueryOllamaCycleOverview(groupBy, 50)
	if err != nil {
		h.logger.Error("failed to query Ollama cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	respondJSON(w, http.StatusOK, overview)
}

func (h *Handler) summaryOllama(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildOllamaSummaryMap())
}

func (h *Handler) insightsOllama(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildOllamaInsights(hidden, rangeDur))
}

func (h *Handler) ollamaQuotaNames() []string {
	if h.store == nil {
		return nil
	}
	names, err := h.store.QueryAllOllamaQuotaNames()
	if err != nil {
		return nil
	}
	return names
}

func (h *Handler) buildOllamaSummaryMap() map[string]interface{} {
	if h.store == nil || h.ollamaTracker == nil {
		return map[string]interface{}{}
	}

	quotaNames, err := h.store.QueryAllOllamaQuotaNames()
	if err != nil {
		h.logger.Error("failed to query Ollama quota names", "error", err)
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})
	for _, name := range quotaNames {
		summary, err := h.ollamaTracker.UsageSummary(name)
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

func (h *Handler) buildOllamaInsights(hidden map[string]bool, _ time.Duration) ollamaInsightsResponse {
	resp := ollamaInsightsResponse{Stats: []ollamaInsightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestOllama()
	if err != nil || latest == nil || len(latest.Quotas) == 0 {
		return resp
	}

	if planLabel := ollamaTitleCase(latest.Plan); planLabel != "" {
		resp.Stats = append(resp.Stats, ollamaInsightStat{
			Label: "Plan",
			Value: planLabel,
		})
	}

	if latest.AccountName != "" {
		resp.Stats = append(resp.Stats, ollamaInsightStat{
			Label: "Account",
			Value: latest.AccountName,
		})
	}

	if latest.ExtraCostUSD > 0 {
		resp.Stats = append(resp.Stats, ollamaInsightStat{
			Label: "Extra Usage",
			Value: fmt.Sprintf("$%.2f", latest.ExtraCostUSD),
		})
	}

	// Top models by request count.
	models := append([]api.OllamaModelUsage(nil), latest.Models...)
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].RequestCount != models[j].RequestCount {
			return models[i].RequestCount > models[j].RequestCount
		}
		return models[i].Name < models[j].Name
	})
	topModels := 3
	for i, m := range models {
		if i >= topModels || m.RequestCount == 0 {
			break
		}
		value := fmt.Sprintf("%d req", m.RequestCount)
		sub := ""
		if m.Cost > 0 {
			sub = fmt.Sprintf("$%.2f", m.Cost)
		}
		resp.Stats = append(resp.Stats, ollamaInsightStat{
			Label:    m.Name,
			Value:    value,
			Sublabel: sub,
		})
	}

	quotas := append([]api.OllamaQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left := ollamaQuotaOrder(quotas[i].Name)
		right := ollamaQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	summaries := map[string]*tracker.OllamaSummary{}
	if h.ollamaTracker != nil {
		for _, quota := range quotas {
			summary, err := h.ollamaTracker.UsageSummary(quota.Name)
			if err == nil && summary != nil {
				summaries[quota.Name] = summary
			}
		}
	}

	preferredQuotas := []string{"monthly"}
	selected := make([]api.OllamaQuota, 0, len(preferredQuotas))
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
		// A quota with an unknown cap has no meaningful utilization to forecast.
		if quota.Limit == 0 {
			continue
		}
		rate := h.computeOllamaRate(quota.Name, quota.Utilization, summaries[quota.Name])
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if hidden[insightKey] {
			continue
		}
		value := "Analyzing..."
		if rate.HasRate {
			value = fmt.Sprintf("%.1f%%/hr", rate.Rate)
		}
		insight := buildOllamaBurnRateInsight(quota, rate)
		resp.Stats = append(resp.Stats, ollamaInsightStat{
			Key:      insightKey,
			Label:    fmt.Sprintf("%s Burn Rate", ollamaDisplayName(quota.Name)),
			Value:    value,
			Sublabel: insight.Sublabel,
			Metric:   insight.Metric,
			Severity: insight.Severity,
			Desc:     insight.Desc,
		})
	}

	return resp
}

func (h *Handler) loggingHistoryOllama(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryOllamaRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query Ollama snapshots", "error", err)
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
		quotaNames = []string{"monthly"}
	} else {
		sort.SliceStable(quotaNames, func(i, j int) bool {
			left := ollamaQuotaOrder(quotaNames[i])
			right := ollamaQuotaOrder(quotaNames[j])
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
		"provider":   "ollama",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}
