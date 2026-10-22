package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// openCodeQuotaStatus maps the upstream window status (key mode) to the
// dashboard status vocabulary. A window whose status is not "ok" is critical
// whatever its percent; an empty or unknown status falls back to the percent
// bands, so an unrecognised value never silently reads as healthy.
func openCodeQuotaStatus(utilization float64, status string) string {
	if s := strings.TrimSpace(status); s != "" && !strings.EqualFold(s, "ok") {
		return "critical"
	}
	return utilStatus(utilization)
}

// SetOpenCodeTracker sets the OpenCode tracker for usage summary enrichment.
func (h *Handler) SetOpenCodeTracker(t *tracker.OpenCodeTracker) {
	h.opencodeTracker = t
}

func (h *Handler) currentOpenCode(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.buildOpenCodeCurrent())
}

// opencodeInsightsResponse is the JSON payload for OpenCode deep insights.
type opencodeInsightsResponse struct {
	Stats    []opencodeInsightStat `json:"stats"`
	Insights []insightItem         `json:"insights"`
	// partial marks an answer built after a swallowed read error; it is
	// served but never cached.
	partial bool
}

func (r opencodeInsightsResponse) isPartial() bool { return r.partial }

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
			// A handful of polls over the full window is the only trustworthy
			// basis for an hourly rate; extrapolating from a short burst
			// produces the misleading double-digit rates the dashboard used to
			// show for the monthly window.
			if elapsed >= 20*time.Minute {
				result.Rate = math.Max(0, last.Utilization-first.Utilization) / elapsed.Hours()
				result.HasRate = true
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

// buildOpenCodeBurnRateInsight returns the card for a measured rate, or an
// empty Metric until a window long enough to measure exists.
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
		return item
	}

	if rate.Rate < 0.01 {
		// A measured flat window is a real result, not a missing one.
		item.Type = "forecast"
		item.Severity = "positive"
		item.Metric = "Flat"
		item.Sublabel = sublabel
		item.Desc = fmt.Sprintf("Currently at %.0f%%. No measurable increase in the last 30 minutes, so this window is on pace to finish near %.0f%%.", quota.Utilization, projected)
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
	response, _, _ := h.buildOpenCodeCurrentOK()
	return response
}

// buildOpenCodeCurrentOK is buildOpenCodeCurrent with an explicit storage
// outcome. readOK is false only when the read that supplied the payload failed.
// The per-quota read is a refinement, so its failure still leaves the latest
// snapshot payload intact.
func (h *Handler) buildOpenCodeCurrentOK() (response map[string]interface{}, readOK bool, hasData bool) {
	now := time.Now().UTC()
	response = map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}

	if h.store == nil {
		return response, true, false
	}

	latest, err := h.store.QueryLatestOpenCode()
	if err != nil {
		return response, false, false
	}
	if latest == nil {
		return response, true, false
	}
	hasData = true

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
				"percentOnly":   q.Format == api.OpenCodeQuotaFormatPercent,
				"status":        openCodeQuotaStatus(q.Utilization, q.Status),
				"statusLabel":   q.Status,
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
		return response, true, hasData
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
			"percentOnly":   q.Format == string(api.OpenCodeQuotaFormatPercent),
			"status":        openCodeQuotaStatus(q.Utilization, q.Status),
			"statusLabel":   q.Status,
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
	return response, true, hasData
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

	snapshots, err := h.store.QueryOpenCodeChartRange(start, now)
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
		quotaName = "five_hour"
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

// cycleOverviewProviderLimit caps the per-provider cycle overview at the
// project's 200-cycle query budget. Each cycle costs an extra quota-values
// query, so the generic 500-row page limit does not apply here.
func cycleOverviewProviderLimit(r *http.Request) int {
	const maxCycles = 200
	limit := parseCycleOverviewLimit(r)
	if limit > maxCycles {
		return maxCycles
	}
	return limit
}

func (h *Handler) cycleOverviewOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}, "quotaNames": []string{}})
		return
	}

	groupBy := r.URL.Query().Get("groupBy")
	if groupBy == "" {
		groupBy = r.URL.Query().Get("group_by")
	}
	if groupBy == "" {
		groupBy = "five_hour"
	}

	overview, err := h.store.QueryOpenCodeCycleOverview(groupBy, cycleOverviewProviderLimit(r))
	if err != nil {
		h.logger.Error("failed to query OpenCode cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	quotaNames, err := h.store.QueryAllOpenCodeQuotaNames()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to query quota names")
		return
	}
	sort.SliceStable(quotaNames, func(i, j int) bool { return opencodeQuotaOrder(quotaNames[i]) < opencodeQuotaOrder(quotaNames[j]) })
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider": "opencode", "groupBy": groupBy, "quotaNames": quotaNames,
		"cycles": cycleOverviewRowsToJSON(overview),
	})
}

func (h *Handler) summaryOpenCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildOpenCodeSummaryMap())
}

func (h *Handler) insightsOpenCode(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondInsights(w, h.buildOpenCodeInsights(hidden, rangeDur))
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
	markPartial(&resp.partial, err)
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
	// The three upstream windows are distinct meters; an unrecognised quota has
	// no display name or documented reset semantics, so it is not charted here.
	now := time.Now()
	for _, quota := range selected {
		rate := h.computeOpenCodeRate(quota.Name, quota.Utilization, summaries[quota.Name])
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if !hidden[insightKey] {
			insight := buildOpenCodeBurnRateInsight(quota, rate)
			if insight.Metric != "" {
				resp.Stats = append(resp.Stats, opencodeInsightStat{
					Key:      insightKey,
					Label:    fmt.Sprintf("%s Burn Rate", opencodeDisplayName(quota.Name)),
					Value:    insight.Metric,
					Sublabel: insight.Sublabel,
					Metric:   insight.Metric,
					Severity: insight.Severity,
					Desc:     insight.Desc,
				})
				if insight.Severity == "negative" || insight.Severity == "warning" {
					resp.Insights = appendWindowInsight(resp.Insights, insight)
				}
			}
		}
		if quota.ResetsAt == nil || hidden["reset_"+quota.Name] {
			continue
		}
		resetIn := quota.ResetsAt.Sub(now)
		if resetIn <= 0 {
			continue
		}
		display := opencodeDisplayName(quota.Name)
		remaining := 100 - quota.Utilization
		desc := fmt.Sprintf("%s resets in %s with %.0f%% of the window still available.", display, formatDuration(resetIn), remaining)
		severity := "info"
		switch {
		case rate.HasRate && rate.ExhaustsFirst:
			severity = "negative"
			desc = fmt.Sprintf("%s resets in %s. At the current burn the remaining %.0f%% would be used in %s, before the window resets.", display, formatDuration(resetIn), remaining, formatDuration(rate.TimeToExhaust))
		case rate.HasRate && rate.ProjectedPct >= 80:
			severity = "warning"
			desc = fmt.Sprintf("%s resets in %s and current burn projects %.0f%% by then, close to the limit.", display, formatDuration(resetIn), rate.ProjectedPct)
		case rate.HasRate:
			severity = "positive"
			desc = fmt.Sprintf("%s resets in %s. Current burn projects %.0f%% by then, leaving about %.0f%%.", display, formatDuration(resetIn), rate.ProjectedPct, 100-rate.ProjectedPct)
		}
		resp.Insights = appendWindowInsight(resp.Insights, insightItem{
			Key:      "reset_" + quota.Name,
			Type:     "window",
			Severity: severity,
			Title:    display + " Reset",
			Metric:   formatDuration(resetIn),
			Sublabel: fmt.Sprintf("%.0f%% used", quota.Utilization),
			Desc:     desc,
		})
	}

	resp.Stats, resp.Insights = boundWindowInsights(resp.Stats, resp.Insights)
	return resp
}

// maxProviderInsights bounds the dashboard insight payload per CLAUDE.md.
const maxProviderInsights = 50

// boundWindowInsights caps the payload without dropping stats the page needs.
func boundWindowInsights[T any](stats []T, insights []insightItem) ([]T, []insightItem) {
	room := maxProviderInsights - len(stats)
	if room < 0 {
		room = 0
	}
	if len(insights) > room {
		insights = insights[:room]
	}
	return stats, insights
}

// appendWindowInsight appends unless the key is already present, keeping the
// window-provider payloads free of duplicate cards.
func appendWindowInsight(items []insightItem, item insightItem) []insightItem {
	for _, existing := range items {
		if existing.Key == item.Key {
			return items
		}
	}
	return append(items, item)
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
				Format: string(q.Format), ResetsAt: q.ResetsAt,
				Name:     q.Name,
				Value:    q.Used,
				Limit:    q.Limit,
				Percent:  q.Utilization,
				HasValue: true,
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
