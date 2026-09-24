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

// SetCommandCodeTracker sets the Command Code tracker for usage enrichment.
func (h *Handler) SetCommandCodeTracker(t *tracker.CommandCodeTracker) {
	h.commandCodeTracker = t
}

func (h *Handler) currentCommandCode(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.buildCommandCodeCurrent())
}

// commandCodeInsightsResponse is the JSON payload for Command Code insights.
type commandCodeInsightsResponse struct {
	Stats    []commandCodeInsightStat `json:"stats"`
	Insights []insightItem            `json:"insights"`
}

// commandCodeInsightStat carries linked forecast metadata for the dashboard.
type commandCodeInsightStat struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Key      string `json:"key,omitempty"`
	Metric   string `json:"metric,omitempty"`
	Severity string `json:"severity,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

var commandCodeQuotaDisplayOrder = map[string]int{
	api.CommandCodeQuotaFiveHour: 1,
	api.CommandCodeQuotaWeekly:   2,
	api.CommandCodeQuotaMonthly:  3,
}

var commandCodeDisplayNames = map[string]string{
	api.CommandCodeQuotaFiveHour: "5-Hour Credits",
	api.CommandCodeQuotaWeekly:   "Weekly Credits",
	api.CommandCodeQuotaMonthly:  "Monthly Credits",
}

func commandCodeDisplayName(name string) string {
	if dn, ok := commandCodeDisplayNames[name]; ok {
		return dn
	}
	return name
}

func commandCodeQuotaOrder(name string) int {
	if order, ok := commandCodeQuotaDisplayOrder[name]; ok {
		return order
	}
	return 99
}

type commandCodeQuotaRate struct {
	Rate          float64
	HasRate       bool
	TimeToReset   time.Duration
	TimeToExhaust time.Duration
	ExhaustsFirst bool
	ProjectedPct  float64
}

func (h *Handler) computeCommandCodeRate(quotaName string, currentUtil float64, summary *tracker.CommandCodeSummary) commandCodeQuotaRate {
	var result commandCodeQuotaRate

	if summary != nil && summary.ResetsAt != nil {
		result.TimeToReset = time.Until(*summary.ResetsAt)
	}

	if h.store != nil {
		points, err := h.store.QueryCommandCodeUtilizationSeries(quotaName, time.Now().Add(-30*time.Minute))
		if err == nil && len(points) >= 2 {
			first := points[0]
			last := points[len(points)-1]
			elapsed := last.CapturedAt.Sub(first.CapturedAt)
			if elapsed >= 5*time.Minute {
				if delta := last.Utilization - first.Utilization; delta > 0 {
					result.Rate = delta / elapsed.Hours()
				}
				result.HasRate = true
			}
		}
	}

	if !result.HasRate && summary != nil && summary.CurrentRate > 0 {
		result.Rate = summary.CurrentRate
		result.HasRate = true
	}

	if result.HasRate && result.Rate > 0 {
		if remaining := 100 - currentUtil; remaining > 0 {
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

func buildCommandCodeBurnRateInsight(quota api.CommandCodeQuota, rate commandCodeQuotaRate) insightItem {
	item := insightItem{
		Key:   fmt.Sprintf("forecast_%s", quota.Name),
		Title: fmt.Sprintf("%s Burn Rate", commandCodeDisplayName(quota.Name)),
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
		item.Severity = "negative"
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset and likely to exhaust in %s before reset.", quota.Utilization, projected, formatDuration(rate.TimeToExhaust))
		return item
	}

	if rate.ProjectedPct >= 80 {
		item.Severity = "warning"
	} else {
		item.Severity = "positive"
	}
	item.Sublabel = sublabel
	item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset.", quota.Utilization, projected)
	return item
}

func (h *Handler) buildCommandCodeCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}

	if h.store == nil {
		return response
	}

	latest, err := h.store.QueryLatestCommandCode()
	if err != nil || latest == nil {
		return response
	}

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	if latest.AccountName != "" {
		response["accountName"] = latest.AccountName
	}
	if plan := api.CommandCodeDisplayPlan(latest.Plan); plan != "" {
		response["plan"] = plan
	}
	if latest.Status != "" {
		response["subscriptionStatus"] = latest.Status
	}
	response["remainingCredits"] = latest.RemainingCredits
	response["monthlyCredits"] = latest.MonthlyCredits
	response["purchasedCredits"] = latest.PurchasedCredits
	if latest.FreeCredits > 0 {
		response["freeCredits"] = latest.FreeCredits
	}
	response["periodCostUsd"] = latest.PeriodCostUSD
	response["periodRequests"] = latest.PeriodReqs
	response["periodTokens"] = latest.PeriodTokens
	if latest.PeriodEnd != nil {
		response["periodEnd"] = latest.PeriodEnd.Format(time.RFC3339)
	}

	// One summary per quota per request: UsageSummary runs an active-cycle
	// query plus a bounded history scan plus a latest-snapshot join, and the
	// closure below is called for every quota on every dashboard refresh.
	summaries := map[string]*tracker.CommandCodeSummary{}
	summaryFor := func(name string) *tracker.CommandCodeSummary {
		if h.commandCodeTracker == nil {
			return nil
		}
		if cached, ok := summaries[name]; ok {
			return cached
		}
		summary, sErr := h.commandCodeTracker.UsageSummary(name)
		if sErr != nil {
			summary = nil
		}
		summaries[name] = summary
		return summary
	}

	quotaFromLatest := func(q api.CommandCodeQuota, capturedAt time.Time) map[string]interface{} {
		age := now.Sub(capturedAt)
		// An unknown-cap monthly quota carries no percentage to project or
		// alert on. Force the Ollama unknown-cap shape - utilization 0 with a
		// healthy status - so the menubar shows the dollar balance instead of
		// a bar, and the dashboard label reads "remaining".
		unknownCapMonthly := q.Name == api.CommandCodeQuotaMonthly && q.Limit <= 0
		utilization := q.Utilization
		if unknownCapMonthly {
			utilization = 0
		}
		qMap := map[string]interface{}{
			"name":          q.Name,
			"displayName":   commandCodeDisplayName(q.Name),
			"utilization":   utilization,
			"used":          q.Used,
			"limit":         q.Limit,
			"format":        string(q.Format),
			"status":        utilStatus(utilization),
			"lastUpdatedAt": capturedAt.Format(time.RFC3339),
			"ageSeconds":    int64(age.Seconds()),
			"isStale":       age > 30*time.Minute,
		}
		if q.Remaining > 0 {
			qMap["remaining"] = q.Remaining
		}
		if unknownCapMonthly {
			qMap["limitUnknown"] = true
		}
		if q.ResetsAt != nil {
			timeUntilReset := time.Until(*q.ResetsAt)
			qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			qMap["timeUntilReset"] = formatDuration(timeUntilReset)
			qMap["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if summary := summaryFor(q.Name); summary != nil {
			qMap["currentRate"] = summary.CurrentRate
			qMap["projectedUtil"] = summary.ProjectedUtil
			if !summary.TrackingSince.IsZero() {
				qMap["trackingSince"] = summary.TrackingSince.Format(time.RFC3339)
			}
		}
		return qMap
	}

	latestPerQuota, err := h.store.QueryCommandCodeLatestPerQuota()
	if err != nil || len(latestPerQuota) == 0 {
		for _, q := range latest.Quotas {
			response["quotas"] = append(response["quotas"].([]interface{}), quotaFromLatest(q, latest.CapturedAt))
		}
		applyDisplayModeToResponse(response, h.getDisplayMode("commandcode"))
		return response
	}

	sort.SliceStable(latestPerQuota, func(i, j int) bool {
		left := commandCodeQuotaOrder(latestPerQuota[i].Name)
		right := commandCodeQuotaOrder(latestPerQuota[j].Name)
		if left != right {
			return left < right
		}
		return latestPerQuota[i].Name < latestPerQuota[j].Name
	})

	quotas := make([]interface{}, 0, len(latestPerQuota))
	for _, q := range latestPerQuota {
		quotas = append(quotas, quotaFromLatest(api.CommandCodeQuota{
			Name:        q.Name,
			Used:        q.Used,
			Limit:       q.Limit,
			Utilization: q.Utilization,
			Format:      api.CommandCodeQuotaFormat(q.Format),
			ResetsAt:    q.ResetsAt,
			Remaining:   q.Remaining,
		}, q.CapturedAt))
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("commandcode"))
	return response
}

func (h *Handler) historyCommandCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	start, end := parseProviderHistoryRange(r.URL.Query().Get("range"))
	snapshots, err := h.store.QueryCommandCodeRange(start, end, 200)
	if err != nil {
		h.logger.Error("failed to query Command Code history", "error", err)
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

func (h *Handler) cyclesCommandCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaName := r.URL.Query().Get("type")
	if quotaName == "" {
		quotaName = api.CommandCodeQuotaFiveHour
	}

	active, err := h.store.QueryActiveCommandCodeCycle(quotaName)
	if err != nil {
		h.logger.Error("failed to query active Command Code cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	history, err := h.store.QueryCommandCodeCycleHistory(quotaName, 50)
	if err != nil {
		h.logger.Error("failed to query Command Code cycle history", "error", err)
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
			"peakUtilization": c.PeakUtilization,
			"totalDelta":      c.TotalDelta,
			"isActive":        false,
		}
		if c.CycleEnd != nil {
			cycleMap["cycleEnd"] = c.CycleEnd.Format(time.RFC3339)
		}
		if c.ResetsAt != nil {
			cycleMap["resetsAt"] = c.ResetsAt.Format(time.RFC3339)
		}
		cycles = append(cycles, cycleMap)
	}

	respondJSON(w, http.StatusOK, cycles)
}

func (h *Handler) cycleOverviewCommandCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	// The dashboard sends `groupBy`; the other quota-list providers' handlers
	// were written against `group_by`. Accept both so the Quota filter in the
	// Billing Cycle Overview actually selects the window it names.
	groupBy := r.URL.Query().Get("groupBy")
	if groupBy == "" {
		groupBy = r.URL.Query().Get("group_by")
	}
	if groupBy == "" {
		groupBy = api.CommandCodeQuotaFiveHour
	}

	overview, err := h.store.QueryCommandCodeCycleOverview(groupBy, parseCycleOverviewLimit(r))
	if err != nil {
		h.logger.Error("failed to query Command Code cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	quotaNames := []string{
		api.CommandCodeQuotaFiveHour,
		api.CommandCodeQuotaWeekly,
		api.CommandCodeQuotaMonthly,
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    groupBy,
		"provider":   "commandcode",
		"quotaNames": quotaNames,
		"cycles":     cycleOverviewRowsToJSON(overview),
	})
}

func (h *Handler) summaryCommandCode(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildCommandCodeSummaryMap())
}

func (h *Handler) insightsCommandCode(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildCommandCodeInsights(hidden, rangeDur))
}

func (h *Handler) buildCommandCodeSummaryMap() map[string]interface{} {
	if h.store == nil || h.commandCodeTracker == nil {
		return map[string]interface{}{}
	}

	quotaNames, err := h.store.QueryAllCommandCodeQuotaNames()
	if err != nil {
		h.logger.Error("failed to query Command Code quota names", "error", err)
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})
	for _, name := range quotaNames {
		summary, err := h.commandCodeTracker.UsageSummary(name)
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

func (h *Handler) buildCommandCodeInsights(hidden map[string]bool, _ time.Duration) commandCodeInsightsResponse {
	resp := commandCodeInsightsResponse{Stats: []commandCodeInsightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestCommandCode()
	if err != nil || latest == nil || len(latest.Quotas) == 0 {
		return resp
	}

	if plan := api.CommandCodeDisplayPlan(latest.Plan); plan != "" {
		value := plan
		if latest.Status != "" {
			value = fmt.Sprintf("%s (%s)", plan, latest.Status)
		}
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Label: "Plan",
			Value: value,
		})
	}

	if latest.AccountName != "" {
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Label: "Account",
			Value: latest.AccountName,
		})
	}

	if latest.RemainingCredits > 0 || latest.PeriodCostUSD > 0 {
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Label:    "Credits",
			Value:    fmt.Sprintf("$%.2f", latest.RemainingCredits),
			Sublabel: fmt.Sprintf("$%.2f used this period", latest.PeriodCostUSD),
		})
	}

	if latest.PeriodReqs > 0 {
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Label:    "Requests",
			Value:    formatCount(latest.PeriodReqs),
			Sublabel: formatTokenCount(latest.PeriodTokens) + " tokens",
		})
	}

	if latest.PeriodEnd != nil {
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Label:    "Renews",
			Value:    latest.PeriodEnd.UTC().Format("Jan 2"),
			Sublabel: formatDuration(time.Until(*latest.PeriodEnd)),
		})
	}

	quotas := append([]api.CommandCodeQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left := commandCodeQuotaOrder(quotas[i].Name)
		right := commandCodeQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	summaries := map[string]*tracker.CommandCodeSummary{}
	if h.commandCodeTracker != nil {
		for _, quota := range quotas {
			summary, err := h.commandCodeTracker.UsageSummary(quota.Name)
			if err == nil && summary != nil {
				summaries[quota.Name] = summary
			}
		}
	}

	for _, quota := range quotas {
		// A quota with no cap has no meaningful percentage to forecast.
		if quota.Limit <= 0 {
			continue
		}
		rate := h.computeCommandCodeRate(quota.Name, quota.Utilization, summaries[quota.Name])
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if hidden[insightKey] {
			continue
		}
		value := "Analyzing..."
		if rate.HasRate {
			value = fmt.Sprintf("%.1f%%/hr", rate.Rate)
		}
		insight := buildCommandCodeBurnRateInsight(quota, rate)
		resp.Stats = append(resp.Stats, commandCodeInsightStat{
			Key:      insightKey,
			Label:    fmt.Sprintf("%s Burn Rate", commandCodeDisplayName(quota.Name)),
			Value:    value,
			Sublabel: insight.Sublabel,
			Metric:   insight.Metric,
			Severity: insight.Severity,
			Desc:     insight.Desc,
		})
	}

	return resp
}

func (h *Handler) loggingHistoryCommandCode(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryCommandCodeRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query Command Code snapshots", "error", err)
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
		quotaNames = []string{api.CommandCodeQuotaFiveHour, api.CommandCodeQuotaWeekly, api.CommandCodeQuotaMonthly}
	} else {
		sort.SliceStable(quotaNames, func(i, j int) bool {
			left := commandCodeQuotaOrder(quotaNames[i])
			right := commandCodeQuotaOrder(quotaNames[j])
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
		"provider":   "commandcode",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}

// parseProviderHistoryRange maps a range query value to a start/end window.
func parseProviderHistoryRange(rangeParam string) (time.Time, time.Time) {
	if rangeParam == "" {
		rangeParam = "7d"
	}
	now := time.Now().UTC()
	switch rangeParam {
	case "1h":
		return now.Add(-1 * time.Hour), now
	case "6h":
		return now.Add(-6 * time.Hour), now
	case "24h", "1d":
		return now.Add(-24 * time.Hour), now
	case "3d":
		return now.Add(-3 * 24 * time.Hour), now
	case "30d":
		return now.Add(-30 * 24 * time.Hour), now
	default:
		return now.Add(-7 * 24 * time.Hour), now
	}
}

// formatCount renders a request count with thousands separators.
func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

// formatTokenCount renders a token total as 1.1B / 12.3M / 4.5k.
func formatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
