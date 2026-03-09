package web

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

// Capabilities returns runtime capabilities for the current build.
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"version":           h.version,
		"platform":          runtime.GOOS,
		"variant":           menubarVariant(),
		"menubar_supported": menubar.IsSupported(),
		"menubar_running":   menubar.IsRunning(),
	})
}

// MenubarSummary returns the normalized data contract used by the menubar UI.
func (h *Handler) MenubarSummary(w http.ResponseWriter, r *http.Request) {
	if !menubar.IsSupported() && os.Getenv("ONWATCH_TEST_MODE") != "1" {
		http.NotFound(w, r)
		return
	}
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		h.logger.Error("failed to build menubar snapshot", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to build menubar snapshot")
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

// MenubarTest renders the same menubar UI in a browser page for automated testing.
func (h *Handler) MenubarTest(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ONWATCH_TEST_MODE") != "1" {
		http.NotFound(w, r)
		return
	}
	settings, _ := h.menubarSettings()
	view := normalizeMenubarView(r.URL.Query().Get("view"), settings.DefaultView)
	html, err := menubar.InlineHTML(view, settings)
	if err != nil {
		h.logger.Error("failed to render menubar test page", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to render menubar test page")
		return
	}
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self' 'unsafe-inline'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; "+
			"connect-src 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// BuildMenubarSnapshot constructs the shared menubar UI contract.
func (h *Handler) BuildMenubarSnapshot() (*menubar.Snapshot, error) {
	settings, err := h.menubarSettings()
	if err != nil {
		return nil, err
	}

	visibility := h.providerVisibilityMap()
	providers := make([]menubar.ProviderCard, 0, 8)
	latest := time.Time{}

	if h.config != nil && h.config.HasProvider("synthetic") && h.providerDashboardVisible("synthetic", visibility) {
		payload := h.buildSyntheticCurrent()
		if card := normalizeProviderCard("synthetic", "Synthetic", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("zai") && h.providerDashboardVisible("zai", visibility) {
		payload := h.buildZaiCurrent()
		if card := normalizeProviderCard("zai", "Z.ai", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("anthropic") && h.providerDashboardVisible("anthropic", visibility) {
		payload := h.buildAnthropicCurrent()
		if card := normalizeProviderCard("anthropic", "Anthropic", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("copilot") && h.providerDashboardVisible("copilot", visibility) {
		payload := h.buildCopilotCurrent()
		if card := normalizeProviderCard("copilot", "Copilot", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("codex") && h.providerDashboardVisible("codex", visibility) {
		for _, usage := range h.codexUsageAccounts() {
			accountID := codexUsageAccountID(usage)
			providerKey := fmt.Sprintf("codex:%d", accountID)
			if !providerDashboardVisibleForKey(visibility, providerKey, "codex") {
				continue
			}
			name := stringValue(usage, "accountName")
			if name == "" {
				name = "default"
			}
			subtitle := "ChatGPT account"
			if card := normalizeProviderCard(providerKey, "Codex - "+name, subtitle, usage, settings.WarningPercent, settings.CriticalPercent); card != nil {
				providers = append(providers, *card)
				if captured := parseCapturedAt(usage); captured.After(latest) {
					latest = captured
				}
			}
		}
	}
	if h.config != nil && h.config.HasProvider("antigravity") && h.providerDashboardVisible("antigravity", visibility) {
		payload := h.buildAntigravityCurrent()
		if card := normalizeProviderCard("antigravity", "Antigravity", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("minimax") && h.providerDashboardVisible("minimax", visibility) {
		payload := h.buildMiniMaxCurrent()
		if card := normalizeProviderCard("minimax", "MiniMax", "", payload, settings.WarningPercent, settings.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}

	sortProviderCards(providers, settings.ProvidersOrder)
	aggregate := buildAggregate(providers)
	return &menubar.Snapshot{
		GeneratedAt: time.Now().UTC(),
		UpdatedAgo:  timeAgo(latest),
		Aggregate:   aggregate,
		Providers:   providers,
	}, nil
}

func (h *Handler) menubarSettings() (*menubar.Settings, error) {
	if h.store == nil {
		return menubar.DefaultSettings(), nil
	}
	settings, err := h.store.GetMenubarSettings()
	if err != nil {
		return nil, err
	}
	return settings.Normalize(), nil
}

func normalizeProviderCard(id, label, subtitle string, payload map[string]interface{}, warningPercent, criticalPercent int) *menubar.ProviderCard {
	quotas := normalizeQuotas(payload, warningPercent, criticalPercent)
	if len(quotas) == 0 {
		return nil
	}
	status := "healthy"
	highest := 0.0
	trends := make([]menubar.TrendSeries, 0, len(quotas))
	for _, quota := range quotas {
		if quota.Percent > highest {
			highest = quota.Percent
		}
		status = worsenStatus(status, quota.Status)
		points := quota.SparklinePoints
		if len(points) == 0 {
			points = []float64{quota.Percent, quota.Percent, quota.Percent, quota.Percent}
		}
		trends = append(trends, menubar.TrendSeries{
			Key:    quota.Key,
			Label:  quota.Label,
			Status: quota.Status,
			Points: points,
		})
	}
	return &menubar.ProviderCard{
		ID:             id,
		BaseProvider:   providerKeyBase(id),
		Label:          label,
		Subtitle:       subtitle,
		Status:         status,
		HighestPercent: highest,
		UpdatedAt:      timeAgo(parseCapturedAt(payload)),
		Quotas:         quotas,
		Trends:         trends,
	}
}

func normalizeQuotas(payload map[string]interface{}, warningPercent, criticalPercent int) []menubar.QuotaMeter {
	var rawQuotas []interface{}
	switch typed := payload["quotas"].(type) {
	case []interface{}:
		rawQuotas = typed
	case []map[string]interface{}:
		rawQuotas = make([]interface{}, 0, len(typed))
		for _, item := range typed {
			rawQuotas = append(rawQuotas, item)
		}
	}

	if len(rawQuotas) == 0 {
		for _, key := range []string{"subscription", "search", "toolCalls", "tokensLimit", "timeLimit", "sharedQuota"} {
			if quotaMap, ok := payload[key].(map[string]interface{}); ok {
				rawQuotas = append(rawQuotas, quotaMap)
			}
		}
	}

	quotas := make([]menubar.QuotaMeter, 0, len(rawQuotas))
	for _, raw := range rawQuotas {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		label := stringValue(item, "displayName")
		if label == "" {
			label = stringValue(item, "label")
		}
		if label == "" {
			label = stringValue(item, "name")
		}
		if label == "" {
			label = stringValue(item, "quotaName")
		}
		if label == "" {
			continue
		}
		percent := firstFloat(item, "cardPercent", "usagePercent", "percent", "utilization", "remainingPercent")
		quotas = append(quotas, menubar.QuotaMeter{
			Key:            strings.ToLower(strings.ReplaceAll(label, " ", "_")),
			Label:          label,
			DisplayValue:   displayValue(item, percent),
			Percent:        percent,
			Status:         quotaStatus(item, percent, warningPercent, criticalPercent),
			Used:           firstFloat(item, "usage", "used", "currentUsage", "currentUsed"),
			Limit:          firstFloat(item, "limit", "total", "currentLimit", "entitlement"),
			ResetAt:        firstString(item, "renewsAt", "resetsAt", "resetDate", "resetTime", "resetAt"),
			TimeUntilReset: stringValue(item, "timeUntilReset"),
			ProjectedValue: firstFloat(item, "projectedUsage", "projectedUtil", "projectedValue"),
			CurrentRate:    firstFloat(item, "currentRate"),
		})
	}
	sort.SliceStable(quotas, func(i, j int) bool {
		if quotas[i].Percent != quotas[j].Percent {
			return quotas[i].Percent > quotas[j].Percent
		}
		return quotas[i].Label < quotas[j].Label
	})
	return quotas
}

func quotaStatus(item map[string]interface{}, percent float64, warningPercent, criticalPercent int) string {
	rawStatus := stringValue(item, "status")
	if _, ok := item["remainingPercent"]; ok || strings.EqualFold(stringValue(item, "cardLabel"), "Remaining") {
		if rawStatus != "" {
			return rawStatus
		}
		return statusFromRemaining(percent, warningPercent, criticalPercent)
	}
	return statusFromPercent(percent, warningPercent, criticalPercent)
}

func buildAggregate(providers []menubar.ProviderCard) menubar.Aggregate {
	aggregate := menubar.Aggregate{
		ProviderCount: len(providers),
		Status:        "healthy",
		Label:         "All Good",
	}
	for _, provider := range providers {
		if provider.HighestPercent > aggregate.HighestPercent {
			aggregate.HighestPercent = provider.HighestPercent
		}
		switch provider.Status {
		case "critical":
			aggregate.CriticalCount++
		case "danger", "warning":
			aggregate.WarningCount++
		}
		aggregate.Status = worsenStatus(aggregate.Status, provider.Status)
	}

	switch {
	case aggregate.CriticalCount > 0:
		aggregate.Label = fmt.Sprintf("%d Critical", aggregate.CriticalCount)
	case aggregate.WarningCount > 0:
		aggregate.Label = fmt.Sprintf("%d Warning", aggregate.WarningCount)
	default:
		aggregate.Label = "All Good"
	}
	return aggregate
}

func sortProviderCards(cards []menubar.ProviderCard, preferred []string) {
	if len(cards) == 0 {
		return
	}
	order := make(map[string]int, len(preferred))
	for idx, key := range preferred {
		order[key] = idx
	}
	sort.SliceStable(cards, func(i, j int) bool {
		leftOrder, leftOK := order[cards[i].ID]
		rightOrder, rightOK := order[cards[j].ID]
		switch {
		case leftOK && rightOK:
			return leftOrder < rightOrder
		case leftOK:
			return true
		case rightOK:
			return false
		case cards[i].BaseProvider == cards[j].BaseProvider:
			return cards[i].Label < cards[j].Label
		default:
			return cards[i].Label < cards[j].Label
		}
	})
}

func parseCapturedAt(payload map[string]interface{}) time.Time {
	value := stringValue(payload, "capturedAt")
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func menubarVariant() string {
	if runtime.GOOS == "darwin" {
		if menubar.IsSupported() {
			return "full"
		}
		return "lite"
	}
	return "lite"
}

func normalizeMenubarView(raw string, fallback menubar.ViewType) menubar.ViewType {
	switch menubar.ViewType(strings.ToLower(strings.TrimSpace(raw))) {
	case menubar.ViewMinimal:
		return menubar.ViewMinimal
	case menubar.ViewDetailed:
		return menubar.ViewDetailed
	case menubar.ViewStandard:
		return menubar.ViewStandard
	}
	if fallback != "" {
		return fallback
	}
	return menubar.ViewStandard
}

func providerDashboardVisibleForKey(vis map[string]map[string]bool, key, fallback string) bool {
	if pv, ok := vis[key]; ok {
		if dashboard, exists := pv["dashboard"]; exists {
			return dashboard
		}
	}
	if fallback == "" {
		return true
	}
	if pv, ok := vis[fallback]; ok {
		if dashboard, exists := pv["dashboard"]; exists {
			return dashboard
		}
	}
	return true
}

func worsenStatus(current, next string) string {
	rank := map[string]int{
		"healthy":  0,
		"warning":  1,
		"danger":   2,
		"critical": 3,
	}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func statusFromPercent(percent float64, warningPercent, criticalPercent int) string {
	warning := float64(warningPercent)
	if warning <= 0 {
		warning = 70
	}
	critical := float64(criticalPercent)
	if critical <= warning {
		critical = 90
	}
	switch {
	case percent >= critical:
		return "critical"
	case percent >= warning:
		return "warning"
	default:
		return "healthy"
	}
}

func statusFromRemaining(percent float64, warningPercent, criticalPercent int) string {
	warning := 100 - float64(warningPercent)
	critical := 100 - float64(criticalPercent)
	switch {
	case percent <= critical:
		return "critical"
	case percent <= warning:
		return "warning"
	default:
		return "healthy"
	}
}

func timeAgo(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	delta := time.Since(at)
	if delta < time.Minute {
		return "just now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
}

func displayValue(item map[string]interface{}, percent float64) string {
	if v := stringValue(item, "cardLabel"); v == "Remaining" {
		return fmt.Sprintf("%.0f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

func firstString(item map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(item, key); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(item map[string]interface{}, key string) string {
	switch value := item[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func firstFloat(item map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		switch value := item[key].(type) {
		case float64:
			return value
		case float32:
			return float64(value)
		case int:
			return float64(value)
		case int64:
			return float64(value)
		case uint64:
			return float64(value)
		case string:
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}
