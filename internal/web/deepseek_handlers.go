package web

import (
	"fmt"
	"net/http"
	"strings"
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
				status = "critical"
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
	currency := h.deepseekCurrency(r.URL.Query().Get("currency"))
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
		"cycleId":      cycle.ID, // the dashboard cycle tables read cycleId
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

// deepseekCurrency resolves the currency to report: the requested one, else
// the currency of the latest snapshot. DeepSeek accounts hold either CNY or
// USD, and a view queried in the wrong currency comes back empty (#137).
func (h *Handler) deepseekCurrency(requested string) string {
	if requested = strings.ToUpper(strings.TrimSpace(requested)); requested != "" {
		return requested
	}
	if h.store != nil {
		if latest, err := h.store.QueryLatestDeepSeek(); err == nil && latest != nil && latest.Currency != "" {
			return latest.Currency
		}
	}
	return "CNY"
}

// summaryDeepSeek returns DeepSeek usage summary
func (h *Handler) summaryDeepSeek(w http.ResponseWriter, r *http.Request) {
	currency := h.deepseekCurrency(r.URL.Query().Get("currency"))
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
	currency := h.deepseekCurrency(r.URL.Query().Get("currency"))
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
	respondJSON(w, http.StatusOK, h.deepseekCycleOverview(h.deepseekCurrency(r.URL.Query().Get("currency"))))
}

// deepseekCycleOverview lists the active and recent balance cycles. Balance
// cycles carry only a spend delta, so there are no per-quota columns.
func (h *Handler) deepseekCycleOverview(currency string) map[string]interface{} {
	quotaType := "balance"
	cycles := []map[string]interface{}{}
	if h.store != nil {
		if active, err := h.store.QueryActiveDeepSeekCycle(quotaType, currency); err == nil && active != nil {
			cycles = append(cycles, deepseekCycleToMap(active))
		}
		if history, err := h.store.QueryDeepSeekCycleHistory(quotaType, currency, 50); err == nil {
			for _, c := range history {
				cycles = append(cycles, deepseekCycleToMap(c))
			}
		}
	}
	return map[string]interface{}{
		"groupBy":    quotaType,
		"provider":   "deepseek",
		"currency":   currency,
		"quotaNames": []string{},
		"cycles":     cycles,
	}
}

// loggingHistoryDeepSeek returns DeepSeek polling history.
func (h *Handler) loggingHistoryDeepSeek(w http.ResponseWriter, r *http.Request) {
	quotaNames := []string{"total_balance", "granted_balance", "topped_up_balance"}
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"provider": "deepseek", "quotaNames": quotaNames, "logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryDeepSeekRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query DeepSeek logging history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}

	// Rows are labelled with one currency, so skip snapshots taken in another
	// (an account that switched between CNY and USD).
	currency := h.deepseekCurrency("")
	capturedAt := make([]time.Time, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snapshots))
	for _, snap := range snapshots {
		if snap.Currency != "" && snap.Currency != currency {
			continue
		}
		capturedAt = append(capturedAt, snap.CapturedAt)
		ids = append(ids, snap.ID)
		series = append(series, balanceCrossQuotas(quotaNames, snap.TotalBalance, snap.GrantedBalance, snap.ToppedUpBalance))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "deepseek",
		"currency":   currency,
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}

// balanceCrossQuotas maps balance amounts to logging-history cells. Balances
// have no limit, so only the value is set.
func balanceCrossQuotas(names []string, values ...float64) map[string]loggingHistoryCrossQuota {
	row := make(map[string]loggingHistoryCrossQuota, len(names))
	for i, name := range names {
		row[name] = loggingHistoryCrossQuota{Name: name, Value: values[i], HasValue: true}
	}
	return row
}
