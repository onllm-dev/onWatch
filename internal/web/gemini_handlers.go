package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// currentGemini returns current Gemini model quotas.
func (h *Handler) currentGemini(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildGeminiCurrent())
}

// buildGeminiCurrent builds current Gemini response.
func (h *Handler) buildGeminiCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}
	if h.store == nil {
		return response
	}

	latest, err := h.store.QueryLatestGemini()
	if err != nil {
		h.logger.Error("failed to query latest Gemini snapshot", "error", err)
		return response
	}
	if latest == nil {
		return response
	}

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	if latest.Tier != "" {
		response["tier"] = latest.Tier
	}
	if latest.ProjectID != "" {
		response["projectId"] = latest.ProjectID
	}

	var quotas []map[string]interface{} //nolint:prealloc
	for _, q := range latest.Quotas {
		quota := map[string]interface{}{
			"modelId":           q.ModelID,
			"displayName":       api.GeminiDisplayName(q.ModelID),
			"remainingFraction": q.RemainingFraction,
			"usagePercent":      q.UsagePercent,
			"remainingPercent":  q.RemainingFraction * 100,
			"status":            geminiUsageStatus(q.UsagePercent),
		}
		if q.ResetTime != nil {
			timeUntilReset := time.Until(*q.ResetTime)
			quota["resetTime"] = q.ResetTime.Format(time.RFC3339)
			quota["timeUntilReset"] = formatDuration(timeUntilReset)
			quota["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if h.geminiTracker != nil {
			if summary, err := h.geminiTracker.UsageSummary(q.ModelID); err == nil && summary != nil {
				quota["currentRate"] = summary.CurrentRate
				quota["projectedUsage"] = summary.ProjectedUsage
			}
		}
		quotas = append(quotas, quota)
	}
	response["quotas"] = quotas
	return response
}

// historyGemini returns Gemini usage history as a flat array.
// Each entry has capturedAt and model-keyed usage values.
func (h *Handler) historyGemini(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	rangeStr := r.URL.Query().Get("range")
	rangeDur, err := parseTimeRange(rangeStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	start := now.Add(-rangeDur)

	snapshots, err := h.store.QueryGeminiRange(start, now)
	if err != nil {
		h.logger.Error("failed to query Gemini history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	step := downsampleStep(len(snapshots), maxChartPoints)
	last := len(snapshots) - 1
	response := make([]map[string]interface{}, 0, min(len(snapshots), maxChartPoints))
	for i, snap := range snapshots {
		if step > 1 && i != 0 && i != last && i%step != 0 {
			continue
		}
		entry := map[string]interface{}{
			"capturedAt": snap.CapturedAt.Format(time.RFC3339),
		}
		for _, q := range snap.Quotas {
			entry[q.ModelID] = q.UsagePercent
		}
		response = append(response, entry)
	}
	respondJSON(w, http.StatusOK, response)
}

// resolveGeminiModelID resolves the model ID from the request param,
// falling back to the first known model if empty.
func (h *Handler) resolveGeminiModelID(r *http.Request) string {
	modelID := r.URL.Query().Get("model")
	if modelID != "" {
		return modelID
	}
	if h.store != nil {
		if ids, err := h.store.QueryAllGeminiModelIDs(); err == nil && len(ids) > 0 {
			return ids[0]
		}
	}
	return ""
}

// cyclesGemini returns Gemini reset cycles.
func (h *Handler) cyclesGemini(w http.ResponseWriter, r *http.Request) {
	modelID := h.resolveGeminiModelID(r)
	if modelID == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	limitParam := r.URL.Query().Get("limit")
	limit := 50
	if limitParam != "" {
		if v, err := strconv.Atoi(limitParam); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 200 {
		limit = 200
	}

	cycles, err := h.store.QueryGeminiCycleHistory(modelID, limit)
	if err != nil {
		h.logger.Error("failed to query Gemini cycles", "error", err)
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	var results []map[string]interface{} //nolint:prealloc
	for _, c := range cycles {
		entry := map[string]interface{}{
			"id":         c.ID,
			"modelId":    c.ModelID,
			"cycleStart": c.CycleStart.Format(time.RFC3339),
			"peakUsage":  c.PeakUsage,
			"totalDelta": c.TotalDelta,
		}
		if c.CycleEnd != nil {
			entry["cycleEnd"] = c.CycleEnd.Format(time.RFC3339)
		}
		if c.ResetTime != nil {
			entry["resetTime"] = c.ResetTime.Format(time.RFC3339)
		}
		results = append(results, entry)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": results})
}

// summaryGemini returns Gemini usage summary.
func (h *Handler) summaryGemini(w http.ResponseWriter, r *http.Request) {
	modelID := h.resolveGeminiModelID(r)
	if modelID == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"error": "no model available"})
		return
	}

	if h.geminiTracker == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"error": "tracker not available"})
		return
	}

	summary, err := h.geminiTracker.UsageSummary(modelID)
	if err != nil {
		h.logger.Error("failed to get Gemini summary", "error", err, "model", modelID)
		respondError(w, http.StatusInternalServerError, "failed to compute summary")
		return
	}

	result := map[string]interface{}{
		"modelId":           summary.ModelID,
		"remainingFraction": summary.RemainingFraction,
		"usagePercent":      summary.UsagePercent,
		"currentRate":       summary.CurrentRate,
		"projectedUsage":    summary.ProjectedUsage,
		"completedCycles":   summary.CompletedCycles,
		"avgPerCycle":       summary.AvgPerCycle,
		"peakCycle":         summary.PeakCycle,
		"totalTracked":      summary.TotalTracked,
	}
	if summary.ResetTime != nil {
		result["resetTime"] = summary.ResetTime.Format(time.RFC3339)
		result["timeUntilReset"] = formatDuration(summary.TimeUntilReset)
		result["timeUntilResetSeconds"] = int64(summary.TimeUntilReset.Seconds())
	}
	if !summary.TrackingSince.IsZero() {
		result["trackingSince"] = summary.TrackingSince.Format(time.RFC3339)
	}

	respondJSON(w, http.StatusOK, result)
}

// insightsGemini returns Gemini usage insights matching insightsResponse contract.
func (h *Handler) insightsGemini(w http.ResponseWriter, _ *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildGeminiInsights(hidden, rangeDur))
}

// buildGeminiInsights builds Gemini insights with burn rates, ETA, and per-model analysis.
func (h *Handler) buildGeminiInsights(hidden map[string]bool, rangeDur time.Duration) insightsResponse {
	resp := insightsResponse{Stats: []insightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	now := time.Now().UTC()
	rangeStart := now.Add(-rangeDur)
	latest, err := h.store.QueryLatestGemini()
	if err != nil || latest == nil {
		return resp
	}

	modelIDs, _ := h.store.QueryAllGeminiModelIDs()
	if len(modelIDs) == 0 {
		return resp
	}

	// Compute per-model burn rates from cycles
	type burnRateStats struct {
		avgCompleted float64
		current      float64
		hasCompleted bool
		hasCurrent   bool
	}
	burnRatesByModel := map[string]burnRateStats{}

	for _, modelID := range modelIDs {
		stats := burnRateStats{}
		cycles, err := h.store.QueryGeminiCycleHistory(modelID, 200)
		if err == nil {
			var sum float64
			var count int
			for _, cycle := range cycles {
				if cycle == nil || cycle.CycleEnd == nil || cycle.CycleStart.Before(rangeStart) {
					continue
				}
				dur := cycle.CycleEnd.Sub(cycle.CycleStart)
				if dur <= 0 || cycle.TotalDelta <= 0 {
					continue
				}
				rate := (cycle.TotalDelta * 100) / dur.Hours()
				if rate > 0 {
					sum += rate
					count++
				}
			}
			if count > 0 {
				stats.avgCompleted = sum / float64(count)
				stats.hasCompleted = true
			}
		}

		if active, err := h.store.QueryActiveGeminiCycle(modelID); err == nil && active != nil {
			dur := now.Sub(active.CycleStart)
			if dur > 0 && active.TotalDelta > 0 {
				rate := (active.TotalDelta * 100) / dur.Hours()
				if rate > 0 {
					stats.current = rate
					stats.hasCurrent = true
				}
			}
		}
		burnRatesByModel[modelID] = stats
	}

	// Aggregate burn rate across all models
	totalCurrentBurn := 0.0
	totalAvgBurn := 0.0
	currentCount := 0
	avgCount := 0
	for _, stats := range burnRatesByModel {
		if stats.hasCurrent {
			totalCurrentBurn += stats.current
			currentCount++
		}
		if stats.hasCompleted {
			totalAvgBurn += stats.avgCompleted
			avgCount++
		}
	}
	if currentCount > 0 {
		totalCurrentBurn /= float64(currentCount)
	}
	if avgCount > 0 {
		totalAvgBurn /= float64(avgCount)
	}

	effectiveBurn := totalCurrentBurn
	burnLabel := "Current Burn"
	if effectiveBurn <= 0 && totalAvgBurn > 0 {
		effectiveBurn = totalAvgBurn
		burnLabel = "Avg Burn Rate"
	}
	if !hidden["avg_burn_rate"] && effectiveBurn > 0 {
		resp.Stats = append(resp.Stats, insightStat{
			Label: burnLabel,
			Value: fmt.Sprintf("%.1f%%/hr", effectiveBurn),
		})
	}

	// Find lowest remaining model and soonest reset
	var lowestRemaining float64 = 100
	var lowestModel string
	var soonestReset *time.Time
	for _, q := range latest.Quotas {
		remaining := q.RemainingFraction * 100
		if remaining < lowestRemaining {
			lowestRemaining = remaining
			lowestModel = api.GeminiDisplayName(q.ModelID)
		}
		if q.ResetTime != nil {
			if soonestReset == nil || q.ResetTime.Before(*soonestReset) {
				soonestReset = q.ResetTime
			}
		}
	}

	if !hidden["lowest_remaining"] && len(latest.Quotas) > 0 {
		resp.Stats = append(resp.Stats, insightStat{
			Value:    fmt.Sprintf("%.0f%%", lowestRemaining),
			Label:    "Lowest Remaining",
			Sublabel: lowestModel,
		})
	}

	if !hidden["next_reset"] && soonestReset != nil {
		resp.Stats = append(resp.Stats, insightStat{
			Value: formatDuration(time.Until(*soonestReset)),
			Label: "Next Reset",
		})
	}

	// Per-model insight cards with burn rate, ETA, and exhaustion warnings
	var globalEta *time.Time

	for _, q := range latest.Quotas {
		modelID := q.ModelID
		displayName := api.GeminiDisplayName(modelID)
		remaining := q.RemainingFraction * 100
		key := "burn_model_" + modelID
		if hidden[key] {
			continue
		}

		stats := burnRatesByModel[modelID]
		modelRate := stats.current
		if modelRate <= 0 && stats.hasCompleted {
			modelRate = stats.avgCompleted
		}

		severity := "info"
		metric := "No burn"
		sublabel := fmt.Sprintf("%.0f%% left", remaining)

		if modelRate > 0 {
			metric = fmt.Sprintf("%.1f%%/hr", modelRate)
			hoursToZero := remaining / modelRate
			if hoursToZero > 0 {
				eta := now.Add(time.Duration(hoursToZero * float64(time.Hour)))
				if q.ResetTime != nil && eta.Before(*q.ResetTime) {
					severity = "critical"
					sublabel = fmt.Sprintf("Exhausts %s", eta.Format("Jan 2 15:04"))
					if globalEta == nil || eta.Before(*globalEta) {
						t := eta
						globalEta = &t
					}
				} else {
					if modelRate >= 5 {
						severity = "warning"
					}
					sublabel = fmt.Sprintf("~%s left", formatDuration(time.Duration(hoursToZero*float64(time.Hour))))
				}
			}
		}

		resp.Insights = append(resp.Insights, insightItem{
			Key:      key,
			Title:    displayName,
			Metric:   metric,
			Sublabel: sublabel,
			Severity: severity,
		})
	}

	// Exhaustion warning stat card
	if globalEta != nil && !hidden["exhaustion_warning"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Exhausts By",
			Value: globalEta.Format("Jan 2 15:04"),
		})
	}

	// Coverage insight
	snapshots, err := h.store.QueryGeminiRange(rangeStart, now)
	if err == nil && len(snapshots) >= 2 {
		if !hidden["coverage"] {
			first := snapshots[0]
			last := snapshots[len(snapshots)-1]
			dur := last.CapturedAt.Sub(first.CapturedAt)
			resp.Insights = append(resp.Insights, insightItem{
				Key:      "coverage",
				Title:    "Coverage",
				Metric:   formatDuration(dur),
				Sublabel: fmt.Sprintf("%d polls", len(snapshots)),
				Severity: "info",
			})
		}
	}

	return resp
}

// cycleOverviewGemini returns Gemini cycle overview matching cross-quota contract.
func (h *Handler) cycleOverviewGemini(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"groupBy":    "",
			"provider":   "gemini",
			"quotaNames": []string{},
			"cycles":     []interface{}{},
		})
		return
	}

	groupBy := r.URL.Query().Get("groupBy")
	limit := parseCycleOverviewLimit(r)

	quotaNames, err := h.store.QueryAllGeminiModelIDs()
	if err != nil {
		quotaNames = []string{}
	}

	if groupBy == "" && len(quotaNames) > 0 {
		groupBy = quotaNames[0]
	}

	var rows []map[string]interface{}
	if groupBy != "" {
		cycleRows, queryErr := h.store.QueryGeminiCycleOverview(groupBy, limit)
		if queryErr != nil {
			h.logger.Error("failed to query Gemini cycle overview", "error", queryErr)
			respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
			return
		}
		rows = cycleOverviewRowsToJSON(cycleRows)
	} else {
		rows = []map[string]interface{}{}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    groupBy,
		"provider":   "gemini",
		"quotaNames": quotaNames,
		"cycles":     rows,
	})
}

// loggingHistoryGemini returns Gemini raw snapshot history for logging view.
func (h *Handler) loggingHistoryGemini(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"provider":   "gemini",
			"quotaNames": []string{},
			"logs":       []interface{}{},
		})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryGeminiRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query Gemini logging history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query logging history")
		return
	}

	// Build quotaNames from ALL snapshots (models may appear/disappear across polls)
	quotaNameSet := map[string]bool{}
	for _, snap := range snapshots {
		for _, q := range snap.Quotas {
			quotaNameSet[q.ModelID] = true
		}
	}
	// Fall back to DB if no snapshots
	if len(quotaNameSet) == 0 {
		if dbNames, err := h.store.QueryAllGeminiModelIDs(); err == nil {
			for _, n := range dbNames {
				quotaNameSet[n] = true
			}
		}
	}
	quotaNames := make([]string, 0, len(quotaNameSet))
	for n := range quotaNameSet {
		quotaNames = append(quotaNames, n)
	}
	sort.Strings(quotaNames)

	// Build series for loggingHistoryRowsFromSnapshots
	capturedAt := make([]time.Time, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snapshots))

	for _, snap := range snapshots {
		capturedAt = append(capturedAt, snap.CapturedAt)
		ids = append(ids, snap.ID)
		row := make(map[string]loggingHistoryCrossQuota, len(snap.Quotas))
		for _, q := range snap.Quotas {
			row[q.ModelID] = loggingHistoryCrossQuota{
				Name:    q.ModelID,
				Percent: q.UsagePercent,
			}
		}
		series = append(series, row)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "gemini",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}

func geminiUsageStatus(usagePercent float64) string {
	switch {
	case usagePercent >= 95:
		return "critical"
	case usagePercent >= 80:
		return "danger"
	case usagePercent >= 50:
		return "warning"
	default:
		return "healthy"
	}
}
