package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// currentDeepSeek returns DeepSeek balance status
func (h *Handler) currentDeepSeek(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildDeepSeekCurrent())
}

// buildDeepSeekCurrent builds the DeepSeek current balance response map.
func (h *Handler) buildDeepSeekCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"balance": map[string]interface{}{
			"name":        "Balance",
			"description": "DeepSeek API balance",
			"available":   true,
			"currency":    "",
			"total":       0.0,
			"granted":     0.0,
			"toppedUp":    0.0,
			"rate":        0.0,
		},
	}

	if h.store != nil {
		latest, err := h.store.QueryLatestDeepSeek()
		if err != nil {
			h.logger.Error("failed to query latest DeepSeek snapshot", "error", err)
			return response
		}

		if latest != nil {
			response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
			
			status := "healthy"
			if latest.TotalBalance == 0 {
				status = "exhausted"
			}
			
			balance := map[string]interface{}{
				"name":        "Balance",
				"description": "DeepSeek API balance",
				"available":   latest.IsAvailable,
				"currency":    latest.Currency,
				"total":       latest.TotalBalance,
				"granted":     latest.GrantedBalance,
				"toppedUp":    latest.ToppedUpBalance,
				"rate":        0.0,
				"status":      status,
			}

			// Enrich with tracker data
			if h.deepseekTracker != nil && latest.Currency != "" {
				if summary, err := h.deepseekTracker.UsageSummary(latest.Currency); err == nil && summary != nil {
					balance["rate"] = summary.CurrentRate
					balance["completedCycles"] = summary.CompletedCycles
					balance["avgPerCycle"] = summary.AvgPerCycle
					balance["peakCycle"] = summary.PeakCycle
					balance["totalTracked"] = summary.TotalTracked
					if !summary.TrackingSince.IsZero() {
						balance["trackingSince"] = summary.TrackingSince.Format(time.RFC3339)
					}
				}
			}

			response["balance"] = balance
		}
	}

	return response
}

// historyDeepSeek returns DeepSeek usage history
func (h *Handler) historyDeepSeek(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	rangeStr := r.URL.Query().Get("range")
	duration, err := parseTimeRange(rangeStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	start := now.Add(-duration)
	end := now

	snapshots, err := h.store.QueryDeepSeekRange(start, end)
	if err != nil {
		h.logger.Error("failed to query DeepSeek history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	step := downsampleStep(len(snapshots), maxChartPoints)
	last := len(snapshots) - 1
	histResp := make([]map[string]interface{}, 0, min(len(snapshots), maxChartPoints))
	for i, snapshot := range snapshots {
		if step > 1 && i != 0 && i != last && i%step != 0 {
			continue
		}
		entry := map[string]interface{}{
			"capturedAt":        snapshot.CapturedAt.Format(time.RFC3339),
			"available":         snapshot.IsAvailable,
			"currency":          snapshot.Currency,
			"total_balance":     snapshot.TotalBalance,
			"granted_balance":   snapshot.GrantedBalance,
			"topped_up_balance": snapshot.ToppedUpBalance,
		}
		histResp = append(histResp, entry)
	}

	respondJSON(w, http.StatusOK, histResp)
}

// cyclesDeepSeek returns DeepSeek cycle data
func (h *Handler) cyclesDeepSeek(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaType := "balance"
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "CNY" // Default
	}
	response := make([]map[string]interface{}, 0)

	active, err := h.store.QueryActiveDeepSeekCycle(quotaType, currency)
	if err != nil {
		h.logger.Error("failed to query active DeepSeek cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	if active != nil {
		response = append(response, deepseekCycleToMap(active))
	}

	history, err := h.store.QueryDeepSeekCycleHistory(quotaType, currency, 200)
	if err != nil {
		h.logger.Error("failed to query DeepSeek cycle history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	for _, cycle := range history {
		response = append(response, deepseekCycleToMap(cycle))
	}

	respondJSON(w, http.StatusOK, response)
}

func deepseekCycleToMap(cycle *store.DeepSeekResetCycle) map[string]interface{} {
	result := map[string]interface{}{
		"id":           cycle.ID,
		"quotaType":    cycle.QuotaType,
		"currency":     cycle.Currency,
		"cycleStart":   cycle.CycleStart.Format(time.RFC3339),
		"cycleEnd":     nil,
		"peakRequests": cycle.PeakUsage,
		"totalDelta":   cycle.TotalDelta,
	}

	if cycle.CycleEnd != nil {
		result["cycleEnd"] = cycle.CycleEnd.Format(time.RFC3339)
	}

	return result
}

// summaryDeepSeek returns DeepSeek usage summary
func (h *Handler) summaryDeepSeek(w http.ResponseWriter, r *http.Request) {
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "CNY"
	}
	respondJSON(w, http.StatusOK, h.buildDeepSeekSummaryMap(currency))
}

// buildDeepSeekSummaryMap builds the DeepSeek summary response.
func (h *Handler) buildDeepSeekSummaryMap(currency string) map[string]interface{} {
	response := map[string]interface{}{
		"balance": map[string]interface{}{
			"quotaType":       "balance",
			"currency":        currency,
			"currentBalance":  0.0,
			"currentRate":     0.0,
			"completedCycles": 0,
			"avgPerCycle":     0.0,
			"peakCycle":       0.0,
			"totalTracked":    0.0,
			"trackingSince":   nil,
		},
	}

	if h.deepseekTracker != nil {
		if summary, err := h.deepseekTracker.UsageSummary(currency); err == nil && summary != nil {
			response["balance"] = map[string]interface{}{
				"quotaType":       summary.QuotaType,
				"currency":        summary.Currency,
				"currentBalance":  summary.CurrentBalance,
				"currentRate":     summary.CurrentRate,
				"completedCycles": summary.CompletedCycles,
				"avgPerCycle":     summary.AvgPerCycle,
				"peakCycle":       summary.PeakCycle,
				"totalTracked":    summary.TotalTracked,
				"trackingSince":   nil,
			}
			if !summary.TrackingSince.IsZero() {
				response["balance"].(map[string]interface{})["trackingSince"] = summary.TrackingSince.Format(time.RFC3339)
			}
		}
		return response
	}

	if h.store != nil {
		latest, err := h.store.QueryLatestDeepSeek()
		if err != nil {
			h.logger.Error("failed to query latest DeepSeek snapshot", "error", err)
			return response
		}
		if latest != nil && latest.Currency == currency {
			balMap := response["balance"].(map[string]interface{})
			balMap["currentBalance"] = latest.TotalBalance
		}
	}

	return response
}

// insightsDeepSeek returns DeepSeek insights
func (h *Handler) insightsDeepSeek(w http.ResponseWriter, r *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "CNY"
	}
	respondJSON(w, http.StatusOK, h.buildDeepSeekInsights(currency, hidden))
}

// buildDeepSeekInsights builds the DeepSeek insights response.
func (h *Handler) buildDeepSeekInsights(currency string, hidden map[string]bool) insightsResponse {
	resp := insightsResponse{Stats: []insightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestDeepSeek()
	if err != nil || latest == nil {
		resp.Insights = append(resp.Insights, insightItem{
			Type: "info", Severity: "info",
			Title: "Getting Started",
			Desc:  "Keep onWatch running to collect DeepSeek usage data. Insights appear after a few snapshots.",
		})
		return resp
	}
	
	if latest.Currency != currency {
		// Only reporting for currently tracked currency
		return resp
	}

	currencySymbol := ""
	if currency == "CNY" {
		currencySymbol = "¥"
	} else if currency == "USD" {
		currencySymbol = "$"
	}

	if !hidden["total"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Total Balance", Value: fmt.Sprintf("%s%.2f", currencySymbol, latest.TotalBalance),
		})
	}
	if !hidden["granted"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Granted", Value: fmt.Sprintf("%s%.2f", currencySymbol, latest.GrantedBalance),
		})
	}
	if !hidden["topped_up"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Topped Up", Value: fmt.Sprintf("%s%.2f", currencySymbol, latest.ToppedUpBalance),
		})
	}

	if h.deepseekTracker != nil {
		if summary, err := h.deepseekTracker.UsageSummary(currency); err == nil && summary != nil {
			if !hidden["rate"] && summary.CurrentRate > 0 {
				resp.Stats = append(resp.Stats, insightStat{
					Label: "Spend Rate", Value: fmt.Sprintf("%s%.4f/hr", currencySymbol, summary.CurrentRate),
				})
			}
		}
	}
	
	if !latest.IsAvailable {
		resp.Insights = append(resp.Insights, insightItem{
			Type: "warning", Severity: "high",
			Title: "Service Unavailable",
			Desc:  "DeepSeek API is currently reporting that the service is not available.",
		})
	}

	return resp
}

// cycleOverviewDeepSeek returns DeepSeek cycle overview.
func (h *Handler) cycleOverviewDeepSeek(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	quotaType := "balance"
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "CNY"
	}
	var cycles []map[string]interface{}

	if active, err := h.store.QueryActiveDeepSeekCycle(quotaType, currency); err == nil && active != nil {
		cycles = append(cycles, deepseekCycleToMap(active))
	}
	if history, err := h.store.QueryDeepSeekCycleHistory(quotaType, currency, 50); err == nil {
		for _, c := range history {
			cycles = append(cycles, deepseekCycleToMap(c))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    quotaType,
		"provider":   "deepseek",
		"quotaNames": []string{"balance"},
		"cycles":     cycles,
	})
}

// loggingHistoryDeepSeek returns DeepSeek polling history.
func (h *Handler) loggingHistoryDeepSeek(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"provider": "deepseek", "quotaNames": []string{}, "logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)

	snapshots, err := h.store.QueryDeepSeekRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query DeepSeek logging history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	quotaNames := []string{"balance"}
	type quotaVal struct {
		Name     string
		Value    float64
		HasValue bool
	}

	capturedAt := make([]string, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]quotaVal, 0, len(snapshots))

	for _, snap := range snapshots {
		capturedAt = append(capturedAt, snap.CapturedAt.Format(time.RFC3339))
		ids = append(ids, snap.ID)

		row := map[string]quotaVal{
			"balance": {
				Name:     "balance",
				Value:    snap.TotalBalance,
				HasValue: true,
			},
		}
		series = append(series, row)
	}

	logs := make([]map[string]interface{}, 0, len(snapshots))
	for i := range snapshots {
		entry := map[string]interface{}{
			"capturedAt": capturedAt[i],
			"id":         ids[i],
			"quotas":     map[string]interface{}{},
		}
		quotas := map[string]interface{}{}
		for _, qn := range quotaNames {
			if qv, ok := series[i][qn]; ok {
				quotas[qn] = map[string]interface{}{
					"name":     qv.Name,
					"value":    qv.Value,
					"hasValue": qv.HasValue,
				}
			}
		}
		entry["quotas"] = quotas
		logs = append(logs, entry)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "deepseek",
		"quotaNames": quotaNames,
		"logs":       logs,
	})
}
