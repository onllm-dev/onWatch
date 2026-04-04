package web

import (
	"net/http"
	"sort"
	"time"
)

type apiIntegrationCurrentModelBreakdown struct {
	Model            string   `json:"model"`
	RequestCount     int      `json:"requestCount"`
	PromptTokens     int      `json:"promptTokens"`
	CompletionTokens int      `json:"completionTokens"`
	TotalTokens      int      `json:"totalTokens"`
	TotalCostUSD     *float64 `json:"totalCostUsd,omitempty"`
	LastCapturedAt   string   `json:"lastCapturedAt"`
}

type apiIntegrationCurrentAccountBreakdown struct {
	Account          string                                `json:"account"`
	RequestCount     int                                   `json:"requestCount"`
	PromptTokens     int                                   `json:"promptTokens"`
	CompletionTokens int                                   `json:"completionTokens"`
	TotalTokens      int                                   `json:"totalTokens"`
	TotalCostUSD     *float64                              `json:"totalCostUsd,omitempty"`
	LastCapturedAt   string                                `json:"lastCapturedAt"`
	Models           []apiIntegrationCurrentModelBreakdown `json:"models"`
}

type apiIntegrationCurrentProviderBreakdown struct {
	Provider         string                                  `json:"provider"`
	RequestCount     int                                     `json:"requestCount"`
	PromptTokens     int                                     `json:"promptTokens"`
	CompletionTokens int                                     `json:"completionTokens"`
	TotalTokens      int                                     `json:"totalTokens"`
	TotalCostUSD     *float64                                `json:"totalCostUsd,omitempty"`
	LastCapturedAt   string                                  `json:"lastCapturedAt"`
	Accounts         []apiIntegrationCurrentAccountBreakdown `json:"accounts"`
}

// APIIntegrationsCurrent returns grouped current API integration usage totals.
func (h *Handler) APIIntegrationsCurrent(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildAPIIntegrationsCurrent())
}

func (h *Handler) buildAPIIntegrationsCurrent() map[string]interface{} {
	response := map[string]interface{}{}
	if h.store == nil {
		return response
	}

	rows, err := h.store.QueryAPIIntegrationUsageSummary()
	if err != nil {
		h.logger.Error("failed to query API integrations current", "error", err)
		return response
	}

	type modelNode struct {
		row apiIntegrationCurrentModelBreakdown
	}
	type accountNode struct {
		row    apiIntegrationCurrentAccountBreakdown
		models map[string]*modelNode
	}
	type providerNode struct {
		row      apiIntegrationCurrentProviderBreakdown
		accounts map[string]*accountNode
	}
	type integrationNode struct {
		RequestCount     int
		PromptTokens     int
		CompletionTokens int
		TotalTokens      int
		TotalCostUSD     float64
		HasCost          bool
		LastCapturedAt   time.Time
		Providers        map[string]*providerNode
	}

	integrationsMap := make(map[string]*integrationNode)
	for _, entry := range rows {
		integrationState, ok := integrationsMap[entry.IntegrationName]
		if !ok {
			integrationState = &integrationNode{Providers: make(map[string]*providerNode)}
			integrationsMap[entry.IntegrationName] = integrationState
		}
		providerState, ok := integrationState.Providers[entry.Provider]
		if !ok {
			providerState = &providerNode{
				row:      apiIntegrationCurrentProviderBreakdown{Provider: entry.Provider},
				accounts: make(map[string]*accountNode),
			}
			integrationState.Providers[entry.Provider] = providerState
		}
		accountState, ok := providerState.accounts[entry.AccountName]
		if !ok {
			accountState = &accountNode{
				row:    apiIntegrationCurrentAccountBreakdown{Account: entry.AccountName},
				models: make(map[string]*modelNode),
			}
			providerState.accounts[entry.AccountName] = accountState
		}

		model := apiIntegrationCurrentModelBreakdown{
			Model:            entry.Model,
			RequestCount:     entry.RequestCount,
			PromptTokens:     entry.PromptTokens,
			CompletionTokens: entry.CompletionTokens,
			TotalTokens:      entry.TotalTokens,
			LastCapturedAt:   entry.LastCapturedAt.UTC().Format(time.RFC3339),
		}
		if entry.TotalCostUSD > 0 {
			cost := entry.TotalCostUSD
			model.TotalCostUSD = &cost
		}
		accountState.models[entry.Model] = &modelNode{row: model}

		acc := &accountState.row
		acc.RequestCount += entry.RequestCount
		acc.PromptTokens += entry.PromptTokens
		acc.CompletionTokens += entry.CompletionTokens
		acc.TotalTokens += entry.TotalTokens
		acc.LastCapturedAt = laterTimeString(acc.LastCapturedAt, entry.LastCapturedAt)
		if entry.TotalCostUSD > 0 {
			var current float64
			if acc.TotalCostUSD != nil {
				current = *acc.TotalCostUSD
			}
			current += entry.TotalCostUSD
			acc.TotalCostUSD = &current
		}

		prov := &providerState.row
		prov.RequestCount += entry.RequestCount
		prov.PromptTokens += entry.PromptTokens
		prov.CompletionTokens += entry.CompletionTokens
		prov.TotalTokens += entry.TotalTokens
		prov.LastCapturedAt = laterTimeString(prov.LastCapturedAt, entry.LastCapturedAt)
		if entry.TotalCostUSD > 0 {
			var current float64
			if prov.TotalCostUSD != nil {
				current = *prov.TotalCostUSD
			}
			current += entry.TotalCostUSD
			prov.TotalCostUSD = &current
		}

		integrationState.RequestCount += entry.RequestCount
		integrationState.PromptTokens += entry.PromptTokens
		integrationState.CompletionTokens += entry.CompletionTokens
		integrationState.TotalTokens += entry.TotalTokens
		integrationState.TotalCostUSD += entry.TotalCostUSD
		integrationState.HasCost = integrationState.HasCost || entry.TotalCostUSD > 0
		if entry.LastCapturedAt.After(integrationState.LastCapturedAt) {
			integrationState.LastCapturedAt = entry.LastCapturedAt
		}
	}

	for integrationName, integrationState := range integrationsMap {
		providers := make([]apiIntegrationCurrentProviderBreakdown, 0, len(integrationState.Providers))
		for _, providerState := range integrationState.Providers {
			accounts := make([]apiIntegrationCurrentAccountBreakdown, 0, len(providerState.accounts))
			for _, accountState := range providerState.accounts {
				models := make([]apiIntegrationCurrentModelBreakdown, 0, len(accountState.models))
				for _, modelState := range accountState.models {
					models = append(models, modelState.row)
				}
				sortAPIIntegrationModels(models)
				accountState.row.Models = models
				accounts = append(accounts, accountState.row)
			}
			sortAPIIntegrationAccounts(accounts)
			providerState.row.Accounts = accounts
			providers = append(providers, providerState.row)
		}
		sortAPIIntegrationProviders(providers)

		item := map[string]interface{}{
			"integration":      integrationName,
			"requestCount":     integrationState.RequestCount,
			"promptTokens":     integrationState.PromptTokens,
			"completionTokens": integrationState.CompletionTokens,
			"totalTokens":      integrationState.TotalTokens,
			"lastCapturedAt":   integrationState.LastCapturedAt.UTC().Format(time.RFC3339),
			"providers":        providers,
		}
		if integrationState.HasCost {
			item["totalCostUsd"] = integrationState.TotalCostUSD
		}
		response[integrationName] = item
	}

	return response
}

// APIIntegrationsHistory returns chart-ready aggregated history grouped by integration.
func (h *Handler) APIIntegrationsHistory(w http.ResponseWriter, r *http.Request) {
	rangeStr := r.URL.Query().Get("range")
	duration, err := parseTimeRange(rangeStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, h.buildAPIIntegrationsHistory(duration))
}

func (h *Handler) buildAPIIntegrationsHistory(duration time.Duration) map[string]interface{} {
	response := map[string]interface{}{}
	if h.store == nil {
		return response
	}

	now := time.Now().UTC()
	start := now.Add(-duration)
	bucketSize := apiIntegrationHistoryBucketSize(duration)
	rows, err := h.store.QueryAPIIntegrationUsageBuckets(start, now, bucketSize)
	if err != nil {
		h.logger.Error("failed to query API integrations history", "error", err)
		return response
	}

	byIntegration := make(map[string][]map[string]interface{})
	for _, row := range rows {
		entry := map[string]interface{}{
			"capturedAt":       row.BucketStart.UTC().Format(time.RFC3339),
			"requestCount":     row.RequestCount,
			"promptTokens":     row.PromptTokens,
			"completionTokens": row.CompletionTokens,
			"totalTokens":      row.TotalTokens,
		}
		if row.TotalCostUSD > 0 {
			entry["totalCostUsd"] = row.TotalCostUSD
		}
		byIntegration[row.IntegrationName] = append(byIntegration[row.IntegrationName], entry)
	}

	for integrationName, entries := range byIntegration {
		step := downsampleStep(len(entries), maxChartPoints)
		if step <= 1 {
			response[integrationName] = entries
			continue
		}
		downsampled := make([]map[string]interface{}, 0, min(len(entries), maxChartPoints))
		last := len(entries) - 1
		for index, entry := range entries {
			if index != 0 && index != last && index%step != 0 {
				continue
			}
			downsampled = append(downsampled, entry)
		}
		response[integrationName] = downsampled
	}

	return response
}

// APIIntegrationsHealth returns ingest subsystem status for API integrations telemetry.
func (h *Handler) APIIntegrationsHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildAPIIntegrationsHealth())
}

func (h *Handler) buildAPIIntegrationsHealth() map[string]interface{} {
	response := map[string]interface{}{
		"enabled": false,
		"dir":     "",
		"running": false,
		"files":   []map[string]interface{}{},
		"alerts":  []map[string]interface{}{},
	}
	if h.config != nil {
		response["enabled"] = h.config.APIIntegrationsEnabled
		response["dir"] = h.config.APIIntegrationsDir
	}
	if enabled, _ := response["enabled"].(bool); !enabled {
		return response
	}
	if h.agentManager != nil {
		response["running"] = h.agentManager.IsRunning("api_integrations")
	}
	if h.store == nil {
		return response
	}

	files, err := h.store.QueryAPIIntegrationIngestHealth()
	if err == nil {
		payload := make([]map[string]interface{}, 0, len(files))
		for _, file := range files {
			item := map[string]interface{}{
				"sourcePath":  file.SourcePath,
				"offsetBytes": file.OffsetBytes,
				"fileSize":    file.FileSize,
				"partialLine": file.PartialLine,
				"updatedAt":   file.UpdatedAt.UTC().Format(time.RFC3339),
			}
			if file.FileModTime != nil {
				item["fileModTime"] = file.FileModTime.UTC().Format(time.RFC3339)
			}
			if file.LastCapturedAt != nil {
				item["lastCapturedAt"] = file.LastCapturedAt.UTC().Format(time.RFC3339)
			}
			payload = append(payload, item)
		}
		response["files"] = payload
	}

	alerts, err := h.store.GetActiveSystemAlertsByProvider("api_integrations", 20)
	if err == nil {
		payload := make([]map[string]interface{}, 0, len(alerts))
		for _, alert := range alerts {
			item := map[string]interface{}{
				"id":        alert.ID,
				"type":      alert.AlertType,
				"title":     alert.Title,
				"message":   alert.Message,
				"severity":  alert.Severity,
				"createdAt": alert.CreatedAt.UTC().Format(time.RFC3339),
			}
			if alert.Metadata != "" {
				item["metadata"] = alert.Metadata
			}
			payload = append(payload, item)
		}
		response["alerts"] = payload
	}

	return response
}

func apiIntegrationHistoryBucketSize(duration time.Duration) time.Duration {
	switch {
	case duration <= time.Hour:
		return time.Minute
	case duration <= 6*time.Hour:
		return 5 * time.Minute
	case duration <= 24*time.Hour:
		return 15 * time.Minute
	case duration <= 7*24*time.Hour:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

func laterTimeString(current string, candidate time.Time) string {
	if current == "" {
		return candidate.UTC().Format(time.RFC3339)
	}
	parsed, err := time.Parse(time.RFC3339, current)
	if err != nil || candidate.After(parsed) {
		return candidate.UTC().Format(time.RFC3339)
	}
	return current
}

func sortAPIIntegrationProviders(items []apiIntegrationCurrentProviderBreakdown) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Provider < items[j].Provider
	})
}

func sortAPIIntegrationAccounts(items []apiIntegrationCurrentAccountBreakdown) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Account < items[j].Account
	})
}

func sortAPIIntegrationModels(items []apiIntegrationCurrentModelBreakdown) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Model < items[j].Model
	})
}
