package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

type menubarQuotaOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type menubarProviderOption struct {
	ID           string               `json:"id"`
	BaseProvider string               `json:"base_provider"`
	Label        string               `json:"label"`
	Subtitle     string               `json:"subtitle,omitempty"`
	Visible      bool                 `json:"visible"`
	Quotas       []menubarQuotaOption `json:"quotas"`
}

// Capabilities returns runtime capabilities for the current build.
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"version":           h.version,
		"platform":          runtime.GOOS,
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

// MenubarPage renders the localhost-only browser UI used by the tray companion.
func (h *Handler) MenubarPage(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.NotFound(w, r)
		return
	}

	settings, _ := h.menubarSettings()
	view := normalizeMenubarView(r.URL.Query().Get("view"), settings.DefaultView)
	html, err := h.renderMenubarHTML(view, settings)
	if err != nil {
		h.logger.Error("failed to render menubar page", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to render menubar page")
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

// MenubarTest renders the same menubar UI in a browser page for automated testing.
func (h *Handler) MenubarTest(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ONWATCH_TEST_MODE") != "1" {
		http.NotFound(w, r)
		return
	}
	settings, _ := h.menubarSettings()
	view := normalizeMenubarView(r.URL.Query().Get("view"), settings.DefaultView)
	html, err := h.renderMenubarHTML(view, settings)
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

// MenubarPreferences returns or updates tray-specific settings for the local menubar surface.
func (h *Handler) MenubarPreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := h.menubarSettings()
		if err != nil {
			h.logger.Error("failed to load menubar preferences", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to load menubar preferences")
			return
		}
		providers, err := h.buildMenubarProviderOptions(settings)
		if err != nil {
			h.logger.Error("failed to build menubar provider options", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to load menubar preferences")
			return
		}
		respondJSON(w, http.StatusOK, menubarPreferencesResponse(settings, providers))
	case http.MethodPut:
		if h.store == nil {
			respondError(w, http.StatusInternalServerError, "store not available")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var settings menubar.Settings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			if err.Error() == "http: request body too large" {
				respondError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			respondError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		normalized := settings.Normalize()
		normalized.DefaultView = normalizeMenubarView(string(normalized.DefaultView), menubar.ViewStandard)
		if err := h.store.SetMenubarSettings(normalized); err != nil {
			h.logger.Error("failed to save menubar preferences", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to save menubar preferences")
			return
		}
		h.triggerMenubarRefresh()
		providers, err := h.buildMenubarProviderOptions(normalized)
		if err != nil {
			h.logger.Error("failed to rebuild menubar provider options", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to save menubar preferences")
			return
		}
		respondJSON(w, http.StatusOK, menubarPreferencesResponse(normalized, providers))
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) MenubarRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !isLoopbackRequest(r) {
		http.NotFound(w, r)
		return
	}
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		h.logger.Debug("menubar refresh snapshot rebuild failed", "error", err)
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	clientIP := net.ParseIP(host)
	return clientIP != nil && clientIP.IsLoopback()
}

func isLocalMenubarPublicPath(path string) bool {
	return path == "/menubar" || path == "/api/menubar/summary" || path == "/api/menubar/preferences" || path == "/api/menubar/refresh"
}

// BuildMenubarSnapshot constructs the shared menubar UI contract.
func (h *Handler) BuildMenubarSnapshot() (*menubar.Snapshot, error) {
	settings, err := h.menubarSettings()
	if err != nil {
		return nil, err
	}

	providers, latest := h.buildMenubarProviders(settings, false)
	aggregate := buildAggregate(providers)
	return &menubar.Snapshot{
		GeneratedAt: time.Now().UTC(),
		UpdatedAgo:  timeAgo(latest),
		Aggregate:   aggregate,
		Providers:   providers,
	}, nil
}

func (h *Handler) buildMenubarProviders(settings *menubar.Settings, includeHidden bool) ([]menubar.ProviderCard, time.Time) {
	normalized := settings.Normalize()

	visibility := h.providerVisibilityMap()
	providers := make([]menubar.ProviderCard, 0, 8)
	latest := time.Time{}

	if h.config != nil && h.config.HasProvider("synthetic") && h.providerDashboardVisible("synthetic", visibility) {
		payload := h.buildSyntheticCurrent()
		if card := normalizeProviderCard("synthetic", "Synthetic", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("zai") && h.providerDashboardVisible("zai", visibility) {
		payload := h.buildZaiCurrent()
		if card := normalizeProviderCard("zai", "Z.ai", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("anthropic") && h.providerDashboardVisible("anthropic", visibility) {
		payload := h.buildAnthropicCurrent()
		if card := normalizeProviderCard("anthropic", "Anthropic", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("copilot") && h.providerDashboardVisible("copilot", visibility) {
		payload := h.buildCopilotCurrent()
		if card := normalizeProviderCard("copilot", "Copilot", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
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
			if card := normalizeProviderCard(providerKey, "Codex - "+name, subtitle, usage, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
				providers = append(providers, *card)
				if captured := parseCapturedAt(usage); captured.After(latest) {
					latest = captured
				}
			}
		}
	}
	if h.config != nil && h.config.HasProvider("antigravity") && h.providerDashboardVisible("antigravity", visibility) {
		payload := h.buildAntigravityCurrent()
		if card := normalizeProviderCard("antigravity", "Antigravity", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}
	if h.config != nil && h.config.HasProvider("minimax") && h.providerDashboardVisible("minimax", visibility) {
		payload := h.buildMiniMaxCurrent()
		if card := normalizeProviderCard("minimax", "MiniMax", "", payload, normalized.WarningPercent, normalized.CriticalPercent); card != nil {
			providers = append(providers, *card)
			if captured := parseCapturedAt(payload); captured.After(latest) {
				latest = captured
			}
		}
	}

	sortProviderCards(providers, normalized.ProvidersOrder)
	if !includeHidden {
		providers = filterMenubarProviders(providers, normalized.VisibleProviders)
	}
	return providers, latest
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

func (h *Handler) renderMenubarHTML(view menubar.ViewType, settings *menubar.Settings) (string, error) {
	normalized := settings.Normalize()
	normalized.DefaultView = view
	bootstrap, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		return "", err
	}
	html := strings.Replace(string(page), "__ONWATCH_MENUBAR_BOOTSTRAP__", string(bootstrap), 1)
	version := strings.TrimSpace(h.version)
	if version == "" {
		version = "dev"
	}
	return strings.Replace(html, "__ONWATCH_MENUBAR_VERSION__", version, 1), nil
}

func (h *Handler) buildMenubarProviderOptions(settings *menubar.Settings) ([]menubarProviderOption, error) {
	normalized := settings.Normalize()
	providers, _ := h.buildMenubarProviders(normalized, true)
	visible := make(map[string]struct{}, len(normalized.VisibleProviders))
	for _, id := range normalized.VisibleProviders {
		visible[id] = struct{}{}
	}
	options := make([]menubarProviderOption, 0, len(providers))
	for _, provider := range providers {
		quotaOptions := make([]menubarQuotaOption, 0, len(provider.Quotas))
		for _, quota := range provider.Quotas {
			quotaOptions = append(quotaOptions, menubarQuotaOption{
				Key:   quota.Key,
				Label: quota.Label,
			})
		}
		_, isVisible := visible[provider.ID]
		if len(visible) == 0 {
			isVisible = true
		}
		options = append(options, menubarProviderOption{
			ID:           provider.ID,
			BaseProvider: provider.BaseProvider,
			Label:        provider.Label,
			Subtitle:     provider.Subtitle,
			Visible:      isVisible,
			Quotas:       quotaOptions,
		})
	}
	return options, nil
}

func menubarPreferencesResponse(settings *menubar.Settings, providers []menubarProviderOption) map[string]interface{} {
	normalized := settings.Normalize()
	return map[string]interface{}{
		"enabled":           normalized.Enabled,
		"default_view":      normalized.DefaultView,
		"refresh_seconds":   normalized.RefreshSeconds,
		"providers_order":   normalized.ProvidersOrder,
		"visible_providers": normalized.VisibleProviders,
		"warning_percent":   normalized.WarningPercent,
		"critical_percent":  normalized.CriticalPercent,
		"status_display":    resolvedStatusDisplay(normalized, providers),
		"theme":             normalized.Theme,
		"providers":         providers,
	}
}

func resolvedStatusDisplay(settings *menubar.Settings, providers []menubarProviderOption) menubar.StatusDisplay {
	normalized := settings.Normalize()
	display := normalized.StatusDisplay
	switch display.Mode {
	case menubar.StatusDisplayCriticalCount, menubar.StatusDisplayIconOnly:
		return display
	case menubar.StatusDisplayMultiProvider:
		selections := resolvedStatusSelections(display.SelectedQuotas, providers)
		if len(selections) == 0 {
			return menubar.StatusDisplay{Mode: menubar.StatusDisplayIconOnly}
		}
		return menubar.StatusDisplay{
			Mode:           menubar.StatusDisplayMultiProvider,
			SelectedQuotas: selections,
		}
	default:
		return menubar.StatusDisplay{Mode: menubar.StatusDisplayIconOnly}
	}
}

func resolvedStatusSelections(selections []menubar.StatusDisplaySelection, providers []menubarProviderOption) []menubar.StatusDisplaySelection {
	pool := preferredStatusProviders(providers)
	if len(pool) == 0 {
		pool = providers
	}
	if len(pool) == 0 {
		return []menubar.StatusDisplaySelection{}
	}
	allowed := make(map[string]menubarProviderOption, len(pool))
	for _, provider := range pool {
		allowed[provider.ID] = provider
	}
	out := make([]menubar.StatusDisplaySelection, 0, 3)
	seen := make(map[string]struct{}, 3)
	appendSelection := func(provider menubarProviderOption, quotaKey string) {
		resolvedQuota := providerQuotaKey(provider, quotaKey)
		if resolvedQuota == "" {
			return
		}
		key := provider.ID + "\x00" + resolvedQuota
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, menubar.StatusDisplaySelection{
			ProviderID: provider.ID,
			QuotaKey:   resolvedQuota,
		})
	}
	for _, selection := range selections {
		provider, ok := allowed[selection.ProviderID]
		if !ok {
			continue
		}
		appendSelection(provider, selection.QuotaKey)
		if len(out) == 3 {
			return out
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, provider := range pool {
		appendSelection(provider, "")
		if len(out) == 3 {
			break
		}
	}
	return out
}

func preferredStatusProviders(providers []menubarProviderOption) []menubarProviderOption {
	visible := make([]menubarProviderOption, 0, len(providers))
	for _, provider := range providers {
		if provider.Visible {
			visible = append(visible, provider)
		}
	}
	if len(visible) > 0 {
		return visible
	}
	return providers
}

func providerOptionByID(providerID string, providers []menubarProviderOption) *menubarProviderOption {
	for i := range providers {
		if providers[i].ID == providerID {
			return &providers[i]
		}
	}
	return nil
}

func providerHasQuota(provider menubarProviderOption, quotaKey string) bool {
	for _, quota := range provider.Quotas {
		if quota.Key == quotaKey {
			return true
		}
	}
	return false
}

func firstProviderQuotaKey(provider menubarProviderOption) string {
	if len(provider.Quotas) == 0 {
		return ""
	}
	return provider.Quotas[0].Key
}

func providerQuotaKey(provider menubarProviderOption, quotaKey string) string {
	if quotaKey != "" && providerHasQuota(provider, quotaKey) {
		return quotaKey
	}
	return firstProviderQuotaKey(provider)
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

func filterMenubarProviders(cards []menubar.ProviderCard, visible []string) []menubar.ProviderCard {
	if len(cards) == 0 || len(visible) == 0 {
		return cards
	}
	allowed := make(map[string]struct{}, len(visible))
	for _, id := range visible {
		allowed[id] = struct{}{}
	}
	filtered := make([]menubar.ProviderCard, 0, len(cards))
	for _, card := range cards {
		if _, ok := allowed[card.ID]; ok {
			filtered = append(filtered, card)
		}
	}
	return filtered
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

func normalizeMenubarView(raw string, fallback menubar.ViewType) menubar.ViewType {
	value := menubar.ViewType(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case menubar.ViewMinimal:
		return menubar.ViewMinimal
	case menubar.ViewDetailed:
		return menubar.ViewDetailed
	case menubar.ViewStandard:
		return menubar.ViewStandard
	}
	if fallback != "" {
		switch fallback {
		case menubar.ViewMinimal:
			return menubar.ViewMinimal
		case menubar.ViewDetailed:
			return menubar.ViewDetailed
		}
		return menubar.ViewStandard
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
