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

	families := api.AggregateGeminiByFamily(latest.Quotas)
	var quotas []map[string]interface{} //nolint:prealloc
	for _, fq := range families {
		quota := map[string]interface{}{
			"modelId":           fq.FamilyID,
			"displayName":       fq.DisplayName,
			"members":           fq.Members,
			"remainingFraction": fq.RemainingFraction,
			"usagePercent":      fq.UsagePercent,
			"remainingPercent":  fq.RemainingFraction * 100,
			"status":            geminiUsageStatus(fq.UsagePercent),
		}
		if fq.ResetTime != nil {
			timeUntilReset := time.Until(*fq.ResetTime)
			quota["resetTime"] = fq.ResetTime.Format(time.RFC3339)
			quota["timeUntilReset"] = formatDuration(timeUntilReset)
			quota["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if h.geminiTracker != nil {
			if summary, err := h.geminiTracker.UsageSummary(fq.FamilyID); err == nil && summary != nil {
				quota["currentRate"] = summary.CurrentRate
				quota["projectedUsage"] = summary.ProjectedUsage
			}
		}
		quotas = append(quotas, quota)
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("gemini"))
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

	// Filter out snapshots with no quota data before downsampling
	var validSnapshots []*api.GeminiSnapshot
	for _, snap := range snapshots {
		if len(snap.Quotas) > 0 {
			validSnapshots = append(validSnapshots, snap)
		}
	}

	step := downsampleStep(len(validSnapshots), maxChartPoints)
	last := len(validSnapshots) - 1
	response := make([]map[string]interface{}, 0, min(len(validSnapshots), maxChartPoints))
	for i, snap := range validSnapshots {
		if step > 1 && i != 0 && i != last && i%step != 0 {
			continue
		}
		entry := map[string]interface{}{
			"capturedAt": snap.CapturedAt.Format(time.RFC3339),
		}
		families := api.AggregateGeminiByFamily(snap.Quotas)
		for _, fq := range families {
			entry[fq.FamilyID] = fq.UsagePercent
		}
		response = append(response, entry)
	}
	respondJSON(w, http.StatusOK, response)
}

// resolveGeminiFamilyID resolves the family ID from the request param,
// falling back to the first known family if empty.
func (h *Handler) resolveGeminiFamilyID(r *http.Request) string {
	familyID := r.URL.Query().Get("model")
	if familyID != "" {
		return familyID
	}
	return h.resolveFirstGeminiFamily()
}

// resolveFirstGeminiFamily returns the first available Gemini family ID from stored data.
func (h *Handler) resolveFirstGeminiFamily() string {
	if h.store == nil {
		return ""
	}
	ids, err := h.store.QueryAllGeminiModelIDs()
	if err != nil || len(ids) == 0 {
		return ""
	}
	// Check if the stored IDs are already family IDs
	for _, id := range ids {
		switch id {
		case api.GeminiFamilyPro, api.GeminiFamilyFlash, api.GeminiFamilyFlashLite:
			return id
		}
	}
	// Legacy: convert first model ID to family
	return api.GeminiModelFamily(ids[0])
}

// cyclesGemini returns Gemini session history - built directly from snapshots
// so data is always consistent with logging history. Shows per-family usage
// at each snapshot with columns for Gemini Pro, Gemini Flash, Gemini Flash Lite.
func (h *Handler) cyclesGemini(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	limitParam := r.URL.Query().Get("limit")
	limit := 200
	if limitParam != "" {
		if v, err := strconv.Atoi(limitParam); err == nil && v > 0 && v < 200 {
			limit = v
		}
	}

	now := time.Now().UTC()
	start := now.Add(-30 * 24 * time.Hour)
	snapshots, err := h.store.QueryGeminiRange(start, now, limit)
	if err != nil || len(snapshots) == 0 {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	familyIDs := h.geminiAllFamilyIDs()
	var results []map[string]interface{}
	for i := len(snapshots) - 1; i >= 0; i-- {
		snap := snapshots[i]
		if len(snap.Quotas) == 0 {
			continue
		}
		families := api.AggregateGeminiByFamily(snap.Quotas)
		entry := map[string]interface{}{
			"id":         snap.ID,
			"capturedAt": snap.CapturedAt.Format(time.RFC3339),
		}
		for _, fq := range families {
			entry[fq.FamilyID] = fq.UsagePercent
		}
		// Fill missing families with 0
		for _, fid := range familyIDs {
			if _, ok := entry[fid]; !ok {
				entry[fid] = 0.0
			}
		}
		results = append(results, entry)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"quotaNames": familyIDs,
		"cycles":     results,
	})
}

// summaryGemini returns Gemini usage summary.
func (h *Handler) summaryGemini(w http.ResponseWriter, r *http.Request) {
	modelID := h.resolveGeminiFamilyID(r)
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

// buildGeminiInsights builds Gemini insights matching the Anthropic insights pattern:
// per-family stat cards + per-family burn rate/forecast insight cards.
func (h *Handler) buildGeminiInsights(hidden map[string]bool, _ time.Duration) insightsResponse {
	resp := insightsResponse{Stats: []insightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	now := time.Now().UTC()
	latest, err := h.store.QueryLatestGemini()
	if err != nil || latest == nil {
		return resp
	}

	families := api.AggregateGeminiByFamily(latest.Quotas)
	if len(families) == 0 {
		return resp
	}

	// Collect per-family summaries and cycle history
	type familySummary struct {
		summary        *geminiInsightRate
		completedCount int
		avgPeak        float64
		maxPeak        float64
	}
	familyData := map[string]*familySummary{}

	for _, fq := range families {
		fid := fq.FamilyID
		fs := &familySummary{}

		// Compute burn rate from active cycle (check all member model IDs)
		rate := &geminiInsightRate{}
		memberIDs := h.geminiFamilyModelIDs(fid)
		for _, mid := range memberIDs {
			if active, err := h.store.QueryActiveGeminiCycle(mid); err == nil && active != nil {
				dur := now.Sub(active.CycleStart)
				if dur.Minutes() >= 10 && active.TotalDelta > 0 {
					r := (active.TotalDelta * 100) / dur.Hours()
					if r > rate.Rate {
						rate.Rate = r
						rate.HasRate = true
					}
				}
			}
		}
		fs.summary = rate

		// Cycle history stats (check all member model IDs)
		seen := map[int64]bool{}
		for _, mid := range memberIDs {
			cycles, err := h.store.QueryGeminiCycleHistory(mid, 50)
			if err != nil {
				continue
			}
			for _, c := range cycles {
				if seen[c.ID] {
					continue
				}
				seen[c.ID] = true
				fs.completedCount++
				peak := c.PeakUsage * 100
				fs.avgPeak += peak
				if peak > fs.maxPeak {
					fs.maxPeak = peak
				}
			}
		}
		if fs.completedCount > 0 {
			fs.avgPeak /= float64(fs.completedCount)
		}

		familyData[fid] = fs
	}

	// ═══ Stats Cards: per-family current usage (like Anthropic's per-quota avg) ═══
	for _, fq := range families {
		fid := fq.FamilyID
		fs := familyData[fid]
		if fs != nil && fs.completedCount > 0 {
			periodWord := "cycle"
			if fs.completedCount > 1 {
				periodWord = "cycles"
			}
			resp.Stats = append(resp.Stats, insightStat{
				Value:    fmt.Sprintf("%.0f%%", fs.avgPeak),
				Label:    fmt.Sprintf("Avg %s", fq.DisplayName),
				Sublabel: fmt.Sprintf("across %d %s", fs.completedCount, periodWord),
			})
		} else {
			resp.Stats = append(resp.Stats, insightStat{
				Value: fmt.Sprintf("%.0f%%", fq.UsagePercent),
				Label: fmt.Sprintf("%s (now)", fq.DisplayName),
			})
		}
	}

	// ═══ Insights: per-family burn rate & forecast (like Anthropic's forecast cards) ═══
	for _, fq := range families {
		fid := fq.FamilyID
		key := fmt.Sprintf("forecast_%s", fid)
		if hidden[key] {
			continue
		}
		fs := familyData[fid]
		if fs == nil {
			continue
		}

		remaining := fq.RemainingFraction * 100
		resetStr := ""
		if fq.ResetTime != nil {
			resetStr = formatDuration(time.Until(*fq.ResetTime))
		}

		var item insightItem
		item.Key = key
		item.Title = fq.DisplayName

		if !fs.summary.HasRate {
			item.Severity = "info"
			item.Metric = "Idle"
			if resetStr != "" {
				item.Sublabel = fmt.Sprintf("resets in %s", resetStr)
			} else {
				item.Sublabel = fmt.Sprintf("%.0f%% used", fq.UsagePercent)
			}
		} else {
			rate := fs.summary.Rate
			hoursToExhaust := remaining / rate
			exhaustDur := time.Duration(hoursToExhaust * float64(time.Hour))

			if fq.ResetTime != nil && now.Add(exhaustDur).Before(*fq.ResetTime) {
				// Exhausts before reset
				item.Severity = "negative"
				item.Metric = fmt.Sprintf("%.1f%%/hr", rate)
				item.Sublabel = fmt.Sprintf("exhausts in %s", formatDuration(exhaustDur))
			} else if rate >= 5 {
				// High burn rate but won't exhaust
				item.Severity = "warning"
				item.Metric = fmt.Sprintf("%.1f%%/hr", rate)
				if resetStr != "" {
					projected := fq.UsagePercent + (rate * time.Until(*fq.ResetTime).Hours())
					if projected > 100 {
						projected = 100
					}
					item.Sublabel = fmt.Sprintf("~%.0f%% at reset in %s", projected, resetStr)
				} else {
					item.Sublabel = fmt.Sprintf("%.0f%% remaining", remaining)
				}
			} else {
				// Comfortable
				item.Severity = "positive"
				item.Metric = fmt.Sprintf("%.1f%%/hr", rate)
				if resetStr != "" {
					item.Sublabel = fmt.Sprintf("resets in %s", resetStr)
				} else {
					item.Sublabel = fmt.Sprintf("%.0f%% remaining", remaining)
				}
			}
		}

		resp.Insights = append(resp.Insights, item)
	}

	return resp
}

// geminiInsightRate holds burn rate computation for a family.
type geminiInsightRate struct {
	Rate    float64
	HasRate bool
}

// cycleOverviewGemini is intentionally empty for Gemini.
// Gemini families share quota pools so cycle-level tracking adds no value
// beyond what logging history and session history already show.
func (h *Handler) cycleOverviewGemini(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    "",
		"provider":   "gemini",
		"quotaNames": []string{},
		"cycles":     []interface{}{},
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

	// Build quotaNames from ALL snapshots, aggregated by family
	quotaNameSet := map[string]bool{}
	for _, snap := range snapshots {
		families := api.AggregateGeminiByFamily(snap.Quotas)
		for _, fq := range families {
			quotaNameSet[fq.FamilyID] = true
		}
	}
	// Fall back to DB if no snapshots
	if len(quotaNameSet) == 0 {
		for _, fid := range h.geminiAllFamilyIDs() {
			quotaNameSet[fid] = true
		}
	}
	quotaNames := make([]string, 0, len(quotaNameSet))
	for n := range quotaNameSet {
		quotaNames = append(quotaNames, n)
	}
	sort.Strings(quotaNames)

	// Build series for loggingHistoryRowsFromSnapshots, aggregated by family
	capturedAt := make([]time.Time, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snapshots))

	for _, snap := range snapshots {
		capturedAt = append(capturedAt, snap.CapturedAt)
		ids = append(ids, snap.ID)
		families := api.AggregateGeminiByFamily(snap.Quotas)
		row := make(map[string]loggingHistoryCrossQuota, len(families))
		for _, fq := range families {
			row[fq.FamilyID] = loggingHistoryCrossQuota{
				Name:    fq.FamilyID,
				Percent: fq.UsagePercent,
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

// geminiAllFamilyIDs returns the deduplicated family IDs from stored Gemini data.
func (h *Handler) geminiAllFamilyIDs() []string {
	if h.store == nil {
		return nil
	}
	modelIDs, err := h.store.QueryAllGeminiModelIDs()
	if err != nil || len(modelIDs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var families []string
	for _, mid := range modelIDs {
		fid := api.GeminiModelFamily(mid)
		if !seen[fid] {
			seen[fid] = true
			families = append(families, fid)
		}
	}
	sort.Strings(families)
	return families
}

// geminiFamilyModelIDs returns all DB model_id values that belong to a given family.
func (h *Handler) geminiFamilyModelIDs(familyID string) []string {
	if h.store == nil {
		return []string{familyID}
	}
	allIDs, err := h.store.QueryAllGeminiModelIDs()
	if err != nil {
		return []string{familyID}
	}
	seen := map[string]bool{familyID: true}
	result := []string{familyID}
	for _, mid := range allIDs {
		if !seen[mid] && (api.GeminiModelFamily(mid) == familyID) {
			seen[mid] = true
			result = append(result, mid)
		}
	}
	return result
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
