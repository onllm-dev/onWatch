package web

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func zenmuxQuotas(s *api.ZenmuxSnapshot) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, 3)
	for _, item := range []struct {
		name, label string
		window      *api.ZenmuxWindow
	}{{"five_hour", "5-Hour Flows", s.Quota5Hour}, {"weekly", "7-Day Flows", s.Quota7Day}} {
		w := item.window
		if w == nil {
			continue
		}
		q := map[string]interface{}{"name": item.name, "displayName": item.label, "used": w.UsedFlows, "limit": w.MaxFlows, "remaining": w.RemainingFlows, "resetsAt": w.ResetsAt, "usedValueUsd": w.UsedValueUSD, "maxValueUsd": w.MaxValueUSD}
		if w.UsagePercentage != nil {
			q["utilization"] = *w.UsagePercentage * 100
		}
		if item.name == "five_hour" {
			q["remainingResets"] = w.RemainingResets
		}
		result = append(result, q)
	}
	if s.QuotaMonthly != nil {
		result = append(result, map[string]interface{}{"name": "monthly", "displayName": "Monthly Flow Limit", "limit": s.QuotaMonthly.MaxFlows, "maxValueUsd": s.QuotaMonthly.MaxValueUSD})
	}
	return result
}

// zenmuxEstimate uses only a continuous, fresh sequence in the same rolling window.
func zenmuxEstimate(snaps []*api.ZenmuxSnapshot, name string, now time.Time, poll time.Duration) (float64, float64, bool) {
	if len(snaps) < 3 || len(snaps) > 200 || poll <= 0 {
		return 0, 0, false
	}
	window := func(s *api.ZenmuxSnapshot) *api.ZenmuxWindow {
		if name == "five_hour" {
			return s.Quota5Hour
		}
		return s.Quota7Day
	}
	latest := window(snaps[0])
	if latest == nil || latest.ResetsAt == nil || !latest.ResetsAt.After(now) || latest.MaxFlows == nil || *latest.MaxFlows <= 0 || latest.UsagePercentage == nil {
		return 0, 0, false
	}
	if snaps[0].CapturedAt.After(now) || now.Sub(snaps[0].CapturedAt) > 2*poll {
		return 0, 0, false
	}
	previous := snaps[0]
	for _, snap := range snaps {
		w := window(snap)
		if w == nil || w.ResetsAt == nil || !w.ResetsAt.Equal(*latest.ResetsAt) || w.MaxFlows == nil || *w.MaxFlows != *latest.MaxFlows || w.UsagePercentage == nil || math.IsNaN(*w.UsagePercentage) || math.IsInf(*w.UsagePercentage, 0) || *w.UsagePercentage < 0 || *w.UsagePercentage > 1 {
			return 0, 0, false
		}
		if snap != snaps[0] {
			gap := previous.CapturedAt.Sub(snap.CapturedAt)
			if gap <= 0 || gap > 2*poll || *w.UsagePercentage > *window(previous).UsagePercentage {
				return 0, 0, false
			}
		}
		previous = snap
	}
	elapsed := snaps[0].CapturedAt.Sub(previous.CapturedAt)
	if elapsed < 20*time.Minute {
		return 0, 0, false
	}
	rate := 100 * (*latest.UsagePercentage - *window(previous).UsagePercentage) / elapsed.Hours()
	exhaustHours := 0.0
	if rate > 0 && *latest.UsagePercentage < 1 {
		exhaustHours = (100 - 100**latest.UsagePercentage) / rate
		if exhaustHours >= latest.ResetsAt.Sub(now).Hours() {
			exhaustHours = 0
		}
	}
	return rate, exhaustHours, true
}

func (h *Handler) buildZenmuxCurrent(ctx context.Context) map[string]interface{} {
	response, _, _ := h.buildZenmuxCurrentOK(ctx)
	return response
}

func (h *Handler) buildZenmuxCurrentOK(ctx context.Context) (response map[string]interface{}, readOK bool, hasData bool) {
	response = map[string]interface{}{"quotas": []interface{}{}}
	if h.store == nil {
		return response, true, false
	}
	snap, err := h.store.QueryLatestZenmux()
	if err != nil {
		return response, false, false
	}
	if snap == nil {
		return response, true, false
	}
	response["capturedAt"] = snap.CapturedAt
	response["plan"] = snap.Plan
	response["currency"] = snap.Currency
	response["baseUsdPerFlow"] = snap.BaseUSDPerFlow
	response["effectiveUsdPerFlow"] = snap.EffectiveUSDPerFlow
	response["accountStatus"] = snap.AccountStatus
	quotas := zenmuxQuotas(snap)
	now := time.Now().UTC()
	poll := time.Duration(h.getPollIntervalSec()) * time.Second
	if history, err := h.store.QueryRecentZenmux(ctx, now.Add(-30*time.Minute)); err == nil && len(history) > 0 && history[0].ID == snap.ID {
		for _, q := range quotas {
			name := q["name"].(string)
			if name == "monthly" {
				continue
			}
			if rate, hours, ok := zenmuxEstimate(history, name, now, poll); ok {
				q["ratePerHour"] = rate
				if hours > 0 {
					q["exhaustHours"] = hours
				}
			}
		}
	}
	response["quotas"] = quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("zenmux"))
	return response, true, true
}

func zenmuxHistoryRows(snaps []*api.ZenmuxSnapshot) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(snaps))
	for _, snap := range snaps {
		// Monthly entries retain only their published limits. Charts render
		// utilization for rolling windows and keep missing values as gaps.
		rows = append(rows, map[string]interface{}{"capturedAt": snap.CapturedAt, "quotas": zenmuxQuotas(snap)})
	}
	return rows
}

func (h *Handler) historyZenmux(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}
	start, end := parseProviderHistoryRange(r.URL.Query().Get("range"))
	snaps, err := h.store.QueryZenmuxChartRange(start, end)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}
	respondJSON(w, http.StatusOK, zenmuxHistoryRows(snaps))
}

// Ensure the history helper's bounds are UTC when used by aggregate views.
func (h *Handler) zenmuxHistoryRange(start, end time.Time) ([]map[string]interface{}, error) {
	snaps, err := h.store.QueryZenmuxChartRange(start, end)
	if err != nil {
		return nil, err
	}
	return zenmuxHistoryRows(snaps), nil
}
