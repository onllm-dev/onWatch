package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// ZenmuxUsage fetches one bounded account-analytics query independently of the
// quota agent. No response is persisted, so errors cannot overwrite good quotas.
func (h *Handler) ZenmuxUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if h.config == nil {
		respondError(w, http.StatusServiceUnavailable, "Zenmux is not configured")
		return
	}
	h.config.ReadLock()
	key, disabled := h.config.ZenmuxManagementAPIKey, h.config.ZenmuxDisabled
	h.config.ReadUnlock()
	if disabled || key == "" || !h.providerPollingEnabled("zenmux", h.providerVisibilityMap()) {
		respondError(w, http.StatusServiceUnavailable, "Zenmux polling is disabled or not configured")
		return
	}
	values := r.URL.Query()
	for name, items := range values {
		if len(items) != 1 || (name != "dimension" && name != "time" && name != "bill_type" && name != "model") {
			respondError(w, http.StatusBadRequest, "invalid usage query")
			return
		}
	}
	q := api.ZenmuxUsageQuery{Dimension: values.Get("dimension"), Time: values.Get("time"), BillType: values.Get("bill_type"), Model: values.Get("model")}
	if q.Validate() != nil || (q.Model != "" && strings.Contains(q.Model, key)) {
		respondError(w, http.StatusBadRequest, "invalid usage query")
		return
	}
	// A shared account limit also applies to other clients. Serialize requests
	// within this process and leave headroom for other usage/cost callers.
	h.zenmuxUsageMu.Lock()
	if h.zenmuxUsageBusy || time.Since(h.zenmuxUsageLast) < 2*time.Second {
		h.zenmuxUsageMu.Unlock()
		w.Header().Set("Retry-After", "2")
		respondError(w, http.StatusTooManyRequests, "Please wait before requesting Zenmux usage again")
		return
	}
	h.zenmuxUsageBusy = true
	h.zenmuxUsageLast = time.Now()
	h.zenmuxUsageMu.Unlock()
	defer func() { h.zenmuxUsageMu.Lock(); h.zenmuxUsageBusy = false; h.zenmuxUsageMu.Unlock() }()
	fetch := h.zenmuxUsageFetch
	if fetch == nil {
		fetch = func(ctx context.Context, key string, q api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
			client := api.NewZenmuxClient(key)
			defer client.CloseIdleConnections()
			return client.FetchUsage(ctx, q)
		}
	}
	result, err := fetch(r.Context(), key, q)
	if err != nil {
		if errors.Is(err, api.ErrZenmuxRateLimited) {
			w.Header().Set("Retry-After", "60")
			respondError(w, http.StatusTooManyRequests, "Zenmux rate limit reached. Try again later.")
			return
		}
		if errors.Is(err, api.ErrZenmuxUnauthorized) {
			respondError(w, http.StatusBadGateway, "Zenmux rejected the personal account Management API Key.")
			return
		}
		if errors.Is(err, api.ErrZenmuxInvalidUsageQuery) {
			respondError(w, http.StatusBadRequest, "invalid usage query")
			return
		}
		respondError(w, http.StatusBadGateway, "Zenmux account usage is unavailable.")
		return
	}
	// Do not return another account's result after a settings change in flight.
	h.config.ReadLock()
	stillCurrent := !h.config.ZenmuxDisabled && h.config.ZenmuxManagementAPIKey == key
	h.config.ReadUnlock()
	if !stillCurrent || !h.providerPollingEnabled("zenmux", h.providerVisibilityMap()) {
		respondError(w, http.StatusServiceUnavailable, "Zenmux settings changed. Request usage again.")
		return
	}
	respondJSON(w, http.StatusOK, result)
}
