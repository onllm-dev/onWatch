package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

var mistralDisplayNames = map[string]string{"api_included": "Included API usage", "vibe_included": "Included Vibe Code usage"}

func (h *Handler) buildMistralCurrent() map[string]interface{} {
	showBilling := h.showMistralBilling()
	result := map[string]interface{}{"quotas": []interface{}{}, "status": "waiting", "showBilling": showBilling, "billing": map[string]interface{}{"amount": nil, "status": "unavailable"}}
	if h.store == nil {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if status, e := h.store.GetSetting("mistral_status"); e == nil && status != "" {
		result["status"] = status
	}
	snap, e := h.store.LatestMistral(ctx)
	if e != nil || snap == nil {
		return result
	}
	if result["status"] == "waiting" {
		result["status"] = snap.Status
	}
	result["capturedAt"] = snap.CapturedAt.Format(time.RFC3339)
	result["accountType"] = "subscription"
	allowancesFresh := len(snap.Quotas) == 2
	for _, q := range snap.Quotas {
		age := time.Since(q.CapturedAt)
		stale := age > 5*time.Minute || q.CapturedAt.Before(snap.CapturedAt) || result["status"] == "reconnect" || result["status"] == "stale"
		allowancesFresh = allowancesFresh && !stale
		entry := map[string]interface{}{"name": q.Name, "displayName": mistralDisplayNames[q.Name], "used": q.Used, "limit": q.Limit, "remaining": math.Max(0, q.Limit-q.Used), "utilization": q.Utilization, "format": "currency", "currency": q.Currency, "status": utilStatus(q.Utilization), "lastUpdatedAt": q.CapturedAt.Format(time.RFC3339), "ageSeconds": int64(age.Seconds()), "isStale": stale}
		if q.PercentOnly {
			entry["used"] = nil
			entry["limit"] = nil
			entry["remaining"] = nil
			entry["percentOnly"] = true
			entry["format"] = "percent"
		}
		if q.ResetsAt != nil {
			entry["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			// The menubar falls back to the raw timestamp without this, so the
			// countdown has to be formatted the same way every other provider
			// formats it.
			entry["timeUntilReset"] = formatDuration(time.Until(*q.ResetsAt))
			entry["timeUntilResetSeconds"] = int64(time.Until(*q.ResetsAt).Seconds())
		}
		result["quotas"] = append(result["quotas"].([]interface{}), entry)
	}
	if snap.Billing != nil {
		b := *snap.Billing
		if time.Since(b.CapturedAt) > 5*time.Minute || b.CapturedAt.Before(snap.CapturedAt) || result["status"] == "reconnect" || result["status"] == "stale" {
			b.Status = "stale"
		}
		result["billing"] = &b
	}
	if !showBilling && allowancesFresh && result["status"] == "partial" {
		result["status"] = "ok"
	}
	applyDisplayModeToResponse(result, h.getDisplayMode("mistral"))
	return result
}

// This is a display preference, read live so changing it needs no restart.
func (h *Handler) showMistralBilling() bool {
	if h.store == nil {
		return true
	}
	raw, err := h.store.GetSetting("provider_settings")
	if err != nil {
		return true
	}
	var settings map[string]map[string]interface{}
	if json.Unmarshal([]byte(raw), &settings) != nil {
		return true
	}
	value := settings["mistral"]["show_payg"]
	return value != "false" && value != false
}
func (h *Handler) currentMistral(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildMistralCurrent())
}
func (h *Handler) historyMistral(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, 200, []interface{}{})
		return
	}
	dur := mistralRange(r.URL.Query().Get("range"))
	rows, e := h.store.MistralHistory(r.Context(), time.Now().Add(-dur), time.Now(), 200)
	if e != nil {
		respondError(w, 500, "failed to query Mistral history")
		return
	}
	respondJSON(w, 200, rows)
}
func mistralRange(value string) time.Duration {
	switch value {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h", "1d":
		return 24 * time.Hour
	case "3d":
		return 72 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}
func (h *Handler) cyclesMistral(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("type")
	if name == "" {
		name = r.URL.Query().Get("group_by")
	}
	if name == "" {
		name = "api_included"
	}
	if h.store == nil {
		respondJSON(w, 200, []interface{}{})
		return
	}
	cycles, e := h.store.MistralCycles(r.Context(), name, 50)
	if e != nil {
		respondError(w, 500, "failed to query Mistral cycles")
		return
	}
	respondJSON(w, 200, cycles)
}
func (h *Handler) summaryMistral(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, 200, h.buildMistralSummary(r.Context()))
}
func (h *Handler) buildMistralSummary(ctx context.Context) map[string]interface{} {
	result := map[string]interface{}{}
	if h.store != nil {
		snap, e := h.store.LatestMistral(ctx)
		if e == nil && snap != nil {
			for _, q := range snap.Quotas {
				cycles, e := h.store.MistralCycles(ctx, q.Name, 50)
				if e != nil {
					continue
				}
				total, peak := 0.0, 0.0
				count := 0
				for _, c := range cycles {
					total += c.TotalDelta
					peak = math.Max(peak, c.PeakUtilization)
					if !c.IsActive {
						count++
					}
				}
				result[q.Name] = map[string]interface{}{"currentUtil": q.Utilization, "completedCycles": count, "peakCycle": peak, "totalTracked": total, "avgPerCycle": total / float64(max(1, count)), "resetsAt": q.ResetsAt}
			}
		}
	}
	return result
}
func (h *Handler) buildMistralInsights() map[string]interface{} {
	result := map[string]interface{}{"stats": []interface{}{}, "insights": []interface{}{}}
	if h.store == nil {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Now()
	rows, e := h.store.MistralHistory(ctx, now.Add(-30*time.Minute), now, 200)
	if e != nil || len(rows) < 2 {
		return result
	}
	first := map[string]api.MistralQuota{}
	last := map[string]api.MistralQuota{}
	for _, s := range rows {
		for _, q := range s.Quotas {
			if _, ok := first[q.Name]; !ok {
				first[q.Name] = q
			}
			last[q.Name] = q
		}
	}
	hidden := h.getHiddenInsightKeys()
	for _, name := range []string{"api_included", "vibe_included"} {
		a, ok := first[name]
		b := last[name]
		if !ok || hidden["forecast_"+name] || b.CapturedAt.Sub(a.CapturedAt) < 5*time.Minute || now.Sub(b.CapturedAt) > 5*time.Minute || a.Limit != b.Limit || a.ResetsAt == nil || b.ResetsAt == nil || !a.ResetsAt.Equal(*b.ResetsAt) || b.Used < a.Used {
			continue
		}
		rate := (b.Utilization - a.Utilization) / b.CapturedAt.Sub(a.CapturedAt).Hours()
		projected := b.Utilization + rate*math.Max(0, time.Until(*b.ResetsAt).Hours())
		result["stats"] = append(result["stats"].([]interface{}), map[string]interface{}{"label": mistralDisplayNames[name] + " Burn Rate", "value": fmt.Sprintf("%.1f%%/hr", rate), "sublabel": fmt.Sprintf("~%.0f%% by reset", projected), "key": "forecast_" + name})
	}
	return result
}
func (h *Handler) insightsMistral(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, 200, h.buildMistralInsights())
}
func (h *Handler) loggingHistoryMistral(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, 200, map[string]interface{}{"logs": []interface{}{}})
		return
	}
	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	rows, e := h.store.MistralHistory(r.Context(), start, end, limit)
	if e != nil {
		respondError(w, 500, "failed to query Mistral history")
		return
	}
	var at []time.Time
	var ids []int64
	var series []map[string]loggingHistoryCrossQuota
	for _, s := range rows {
		at = append(at, s.CapturedAt)
		ids = append(ids, s.ID)
		row := map[string]loggingHistoryCrossQuota{}
		for _, q := range s.Quotas {
			row[q.Name] = loggingHistoryCrossQuota{Name: q.Name, Value: q.Used, Limit: q.Limit, Percent: q.Utilization, HasValue: !q.PercentOnly, HasLimit: !q.PercentOnly}
		}
		series = append(series, row)
	}
	respondJSON(w, 200, map[string]interface{}{"provider": "mistral", "quotaNames": []string{"api_included", "vibe_included"}, "logs": loggingHistoryRowsFromSnapshots(at, ids, []string{"api_included", "vibe_included"}, series)})
}

func (h *Handler) cycleOverviewMistral(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, 200, []interface{}{})
		return
	}
	name := r.URL.Query().Get("group_by")
	if name == "" {
		name = "api_included"
	}
	rows, e := h.store.MistralCycleOverview(r.Context(), name)
	if e != nil {
		respondError(w, 500, "failed to query Mistral cycle overview")
		return
	}
	respondJSON(w, 200, rows)
}

func (h *Handler) mistralSessionProvider(provider string) string {
	if provider != "mistral" || h.store == nil {
		return provider
	}
	identity, _ := h.store.GetSetting("mistral_identity")
	return "mistral:" + identity
}
