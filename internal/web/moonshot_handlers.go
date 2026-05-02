package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// currentMoonshot returns Moonshot balance status
func (h *Handler) currentMoonshot(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildMoonshotCurrent())
}

// buildMoonshotCurrent builds the Moonshot current balance response map.
func (h *Handler) buildMoonshotCurrent() map[string]interface{} {
	now := time.Now().UTC()
	response := map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"balance": map[string]interface{}{
			"name":        "Balance",
			"description": "Moonshot Kimi API balance",
			"available":   0.0,
			"voucher":     0.0,
			"cash":        0.0,
			"rate":        0.0,
		},
	}

	if h.store != nil {
		latest, err := h.store.QueryLatestMoonshot()
		if err != nil {
			h.logger.Error("failed to query latest Moonshot snapshot", "error", err)
			return response
		}

		if latest != nil {
			response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
			
			status := "healthy"
			if latest.AvailableBalance == 0 {
				status = "exhausted"
			}
			
			balance := map[string]interface{}{
				"name":        "Balance",
				"description": "Moonshot Kimi API balance",
				"available":   latest.AvailableBalance,
				"voucher":     latest.VoucherBalance,
				"cash":        latest.CashBalance,
				"rate":        0.0,
				"status":      status,
			}

			// Enrich with tracker data
			if h.moonshotTracker != nil {
				if summary, err := h.moonshotTracker.UsageSummary(); err == nil && summary != nil {
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

// historyMoonshot returns Moonshot usage history
func (h *Handler) historyMoonshot(w http.ResponseWriter, r *http.Request) {
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

	snapshots, err := h.store.QueryMoonshotRange(start, end)
	if err != nil {
		h.logger.Error("failed to query Moonshot history", "error", err)
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
			"available_balance": snapshot.AvailableBalance,
			"voucher_balance":   snapshot.VoucherBalance,
			"cash_balance":      snapshot.CashBalance,
		}
		histResp = append(histResp, entry)
	}

	respondJSON(w, http.StatusOK, histResp)
}

// cyclesMoonshot returns Moonshot cycle data
func (h *Handler) cyclesMoonshot(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	quotaType := "balance"
	response := make([]map[string]interface{}, 0)

	active, err := h.store.QueryActiveMoonshotCycle(quotaType)
	if err != nil {
		h.logger.Error("failed to query active Moonshot cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	if active != nil {
		response = append(response, moonshotCycleToMap(active))
	}

	history, err := h.store.QueryMoonshotCycleHistory(quotaType, 200)
	if err != nil {
		h.logger.Error("failed to query Moonshot cycle history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	for _, cycle := range history {
		response = append(response, moonshotCycleToMap(cycle))
	}

	respondJSON(w, http.StatusOK, response)
}

func moonshotCycleToMap(cycle *store.MoonshotResetCycle) map[string]interface{} {
	result := map[string]interface{}{
		"id":           cycle.ID,
		"quotaType":    cycle.QuotaType,
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

// summaryMoonshot returns Moonshot usage summary
func (h *Handler) summaryMoonshot(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildMoonshotSummaryMap())
}

// buildMoonshotSummaryMap builds the Moonshot summary response.
func (h *Handler) buildMoonshotSummaryMap() map[string]interface{} {
	response := map[string]interface{}{
		"balance": map[string]interface{}{
			"quotaType":       "balance",
			"currentBalance":  0.0,
			"currentRate":     0.0,
			"completedCycles": 0,
			"avgPerCycle":     0.0,
			"peakCycle":       0.0,
			"totalTracked":    0.0,
			"trackingSince":   nil,
		},
	}

	if h.moonshotTracker != nil {
		if summary, err := h.moonshotTracker.UsageSummary(); err == nil && summary != nil {
			response["balance"] = map[string]interface{}{
				"quotaType":       summary.QuotaType,
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
		latest, err := h.store.QueryLatestMoonshot()
		if err != nil {
			h.logger.Error("failed to query latest Moonshot snapshot", "error", err)
			return response
		}
		if latest != nil {
			balMap := response["balance"].(map[string]interface{})
			balMap["currentBalance"] = latest.AvailableBalance
		}
	}

	return response
}

// insightsMoonshot returns Moonshot insights
func (h *Handler) insightsMoonshot(w http.ResponseWriter, r *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondJSON(w, http.StatusOK, h.buildMoonshotInsights(hidden))
}

// buildMoonshotInsights builds the Moonshot insights response.
func (h *Handler) buildMoonshotInsights(hidden map[string]bool) insightsResponse {
	resp := insightsResponse{Stats: []insightStat{}, Insights: []insightItem{}}

	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestMoonshot()
	if err != nil || latest == nil {
		resp.Insights = append(resp.Insights, insightItem{
			Type: "info", Severity: "info",
			Title: "Getting Started",
			Desc:  "Keep onWatch running to collect Moonshot usage data. Insights appear after a few snapshots.",
		})
		return resp
	}

	if !hidden["available"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Available", Value: fmt.Sprintf("¥%.2f", latest.AvailableBalance),
		})
	}
	if !hidden["voucher"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Voucher", Value: fmt.Sprintf("¥%.2f", latest.VoucherBalance),
		})
	}
	if !hidden["cash"] {
		resp.Stats = append(resp.Stats, insightStat{
			Label: "Cash", Value: fmt.Sprintf("¥%.2f", latest.CashBalance),
		})
	}

	if h.moonshotTracker != nil {
		if summary, err := h.moonshotTracker.UsageSummary(); err == nil && summary != nil {
			if !hidden["rate"] && summary.CurrentRate > 0 {
				resp.Stats = append(resp.Stats, insightStat{
					Label: "Spend Rate", Value: fmt.Sprintf("¥%.2f/hr", summary.CurrentRate),
				})
			}
		}
	}

	return resp
}

// cycleOverviewMoonshot returns Moonshot cycle overview.
func (h *Handler) cycleOverviewMoonshot(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	quotaType := "balance"
	var cycles []map[string]interface{}

	if active, err := h.store.QueryActiveMoonshotCycle(quotaType); err == nil && active != nil {
		cycles = append(cycles, moonshotCycleToMap(active))
	}
	if history, err := h.store.QueryMoonshotCycleHistory(quotaType, 50); err == nil {
		for _, c := range history {
			cycles = append(cycles, moonshotCycleToMap(c))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    quotaType,
		"provider":   "moonshot",
		"quotaNames": []string{"balance"},
		"cycles":     cycles,
	})
}

// loggingHistoryMoonshot returns Moonshot polling history.
func (h *Handler) loggingHistoryMoonshot(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"provider": "moonshot", "quotaNames": []string{}, "logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)

	snapshots, err := h.store.QueryMoonshotRange(start, end, limit)
	if err != nil {
		h.logger.Error("failed to query Moonshot logging history", "error", err)
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
				Value:    snap.AvailableBalance,
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
		"provider":   "moonshot",
		"quotaNames": quotaNames,
		"logs":       logs,
	})
}
