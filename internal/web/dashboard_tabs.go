package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	settingDashboardProvidersOrder  = "dashboard_providers_order"
	settingDashboardProviderLabels  = "dashboard_provider_labels"
	maxDashboardProviderLabelRunes  = 48
)

// defaultProviderTabLabel returns the built-in dashboard tab title for a provider key.
func defaultProviderTabLabel(key string) string {
	switch key {
	case "synthetic":
		return "Synthetic"
	case "zai":
		return "Z.ai"
	case "anthropic":
		return "Anthropic"
	case "copilot":
		return "Copilot"
	case "codex":
		return "Codex"
	case "antigravity":
		return "Antigravity"
	case "minimax":
		return "MiniMax"
	case "openrouter":
		return "OpenRouter"
	case "gemini":
		return "Gemini"
	case "cursor":
		return "Cursor"
	case "grok":
		return "Grok"
	case "kimi":
		return "Kimi"
	case "api-integrations":
		return "API Integrations"
	case "both":
		return "All"
	default:
		if key == "" {
			return ""
		}
		// Title-case fallback for unknown keys.
		r, size := utf8.DecodeRuneInString(key)
		if r == utf8.RuneError && size == 0 {
			return key
		}
		return strings.ToUpper(string(r)) + key[size:]
	}
}

// normalizeDashboardProviderLabel trims and caps custom tab labels.
// Empty input clears the override (caller should omit the key).
func normalizeDashboardProviderLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", false
	}
	// Drop control characters that would break HTML/layout.
	var b strings.Builder
	b.Grow(len(label))
	for _, r := range label {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	label = strings.TrimSpace(b.String())
	if label == "" {
		return "", false
	}
	if utf8.RuneCountInString(label) > maxDashboardProviderLabelRunes {
		runes := []rune(label)
		label = string(runes[:maxDashboardProviderLabelRunes])
	}
	return label, true
}

// isDashboardSpecialTab is true for composite/header-only tabs that should stay
// after real providers when a saved order omits newly added providers.
func isDashboardSpecialTab(key string) bool {
	return key == "both" || key == "api-integrations"
}

// orderDashboardProviders reorders available keys using a saved preference list.
// Unknown keys in order are dropped; available keys missing from order are inserted
// before special tabs (api-integrations / both) so a new provider is never buried
// after the All tab.
func orderDashboardProviders(available, preferred []string) []string {
	if len(available) == 0 {
		return []string{}
	}
	availSet := make(map[string]struct{}, len(available))
	for _, k := range available {
		availSet[k] = struct{}{}
	}
	seen := make(map[string]struct{}, len(available))
	regular := make([]string, 0, len(available))
	specials := make([]string, 0, 2)

	appendKey := func(k string) {
		if _, ok := availSet[k]; !ok {
			return
		}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		if isDashboardSpecialTab(k) {
			specials = append(specials, k)
			return
		}
		regular = append(regular, k)
	}

	for _, k := range preferred {
		appendKey(strings.TrimSpace(strings.ToLower(k)))
	}
	// Missing available keys (e.g. newly enabled Grok) join regular providers
	// in AvailableProviders order, still before specials.
	for _, k := range available {
		appendKey(k)
	}

	// Stable special order: api-integrations then both, regardless of discovery order.
	specialOrder := []string{"api-integrations", "both"}
	orderedSpecials := make([]string, 0, len(specials))
	specialSeen := map[string]struct{}{}
	for _, k := range specialOrder {
		for _, s := range specials {
			if s == k {
				orderedSpecials = append(orderedSpecials, s)
				specialSeen[s] = struct{}{}
			}
		}
	}
	for _, s := range specials {
		if _, ok := specialSeen[s]; !ok {
			orderedSpecials = append(orderedSpecials, s)
		}
	}

	out := make([]string, 0, len(regular)+len(orderedSpecials))
	out = append(out, regular...)
	out = append(out, orderedSpecials...)
	return out
}

// resolveProviderTabLabel returns custom label if set, otherwise the default.
func resolveProviderTabLabel(key string, labels map[string]string) string {
	if labels != nil {
		if custom, ok := labels[key]; ok {
			if normalized, ok := normalizeDashboardProviderLabel(custom); ok {
				return normalized
			}
		}
	}
	return defaultProviderTabLabel(key)
}

// providerTabLabelsMap builds key→display label for the current tab set.
func providerTabLabelsMap(keys []string, labels map[string]string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = resolveProviderTabLabel(k, labels)
	}
	return out
}

func (h *Handler) loadDashboardProvidersOrder() []string {
	if h.store == nil {
		return nil
	}
	raw, err := h.store.GetSetting(settingDashboardProvidersOrder)
	if err != nil || raw == "" {
		return nil
	}
	var order []string
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		return nil
	}
	return order
}

func (h *Handler) loadDashboardProviderLabels() map[string]string {
	if h.store == nil {
		return map[string]string{}
	}
	raw, err := h.store.GetSetting(settingDashboardProviderLabels)
	if err != nil || raw == "" {
		return map[string]string{}
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return map[string]string{}
	}
	if labels == nil {
		return map[string]string{}
	}
	// Normalize on read so stale oversized values don't leak to UI.
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		k = strings.TrimSpace(strings.ToLower(k))
		if k == "" {
			continue
		}
		if normalized, ok := normalizeDashboardProviderLabel(v); ok {
			out[k] = normalized
		}
	}
	return out
}

func (h *Handler) saveDashboardProvidersOrder(order []string) error {
	if h.store == nil {
		return fmt.Errorf("store not available")
	}
	if order == nil {
		order = []string{}
	}
	// Normalize keys only; membership is validated against live providers at read time.
	normalized := make([]string, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, k := range order {
		k = strings.TrimSpace(strings.ToLower(k))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		normalized = append(normalized, k)
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return h.store.SetSetting(settingDashboardProvidersOrder, string(data))
}

func (h *Handler) saveDashboardProviderLabels(labels map[string]string) error {
	if h.store == nil {
		return fmt.Errorf("store not available")
	}
	if labels == nil {
		labels = map[string]string{}
	}
	normalized := make(map[string]string, len(labels))
	for k, v := range labels {
		k = strings.TrimSpace(strings.ToLower(k))
		if k == "" {
			continue
		}
		if label, ok := normalizeDashboardProviderLabel(v); ok {
			// Drop overrides that match the default name.
			if label == defaultProviderTabLabel(k) {
				continue
			}
			normalized[k] = label
		}
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return h.store.SetSetting(settingDashboardProviderLabels, string(data))
}

// filterDashboardProviders applies dashboard visibility, optional tools tab, and "both".
func (h *Handler) filterDashboardProviders(providers []string) []string {
	if h.store != nil {
		if visJSON, _ := h.store.GetSetting("provider_visibility"); visJSON != "" {
			var vis map[string]map[string]bool
			if json.Unmarshal([]byte(visJSON), &vis) == nil {
				filtered := make([]string, 0, len(providers))
				for _, p := range providers {
					if pv, ok := vis[p]; ok && !pv["dashboard"] {
						continue
					}
					filtered = append(filtered, p)
				}
				providers = filtered
			}
		}
	}
	return providers
}

// buildDashboardProviderTabs returns ordered provider keys for the dashboard header tabs.
func (h *Handler) buildDashboardProviderTabs() []string {
	providers := []string{}
	if h.config != nil {
		providers = h.config.AvailableProviders()
		providers = h.filterDashboardProviders(providers)

		hasTools := h.config.APIIntegrationsEnabled
		toolsVisible := hasTools && h.apiIntegrationsDashboardVisible()
		if toolsVisible {
			providers = append(providers, "api-integrations")
		}
		if h.config.HasMultipleProviders() {
			providers = append(providers, "both")
		}
	}
	return orderDashboardProviders(providers, h.loadDashboardProvidersOrder())
}
