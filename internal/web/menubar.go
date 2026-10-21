package web

import (
	"encoding/json"
	"fmt"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"html"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
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

// SetPollStatusRegistry wires the agent-side last-poll-outcome registry. The
// menubar reads it only to explain a stale card ("network error"); it never
// carries raw error text.
func (h *Handler) SetPollStatusRegistry(r *agent.PollStatusRegistry) {
	h.pollStatus = r
}

// Capabilities returns runtime capabilities for the current build.
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"version":           h.version,
		"platform":          runtime.GOOS,
		"menubar_supported": menubar.IsSupported(),
		"menubar_running":   menubar.IsRunning(),
		"menubar_session":   menubar.SessionAvailable(),
	})
}

// MenubarSummary returns the normalized data contract used by the menubar UI.
// Served to any consumer (native companions, GNOME extension, VS Code
// extension, browser). Loopback callers skip authentication in the middleware;
// remote callers go through the regular dashboard auth, so the handler itself
// no longer gates on the remote address.
// Does not require a native companion build (menubar.IsSupported).
func (h *Handler) MenubarSummary(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.menubarSnapshotForRequest(r.Context())
	if err != nil {
		h.logger.Error("failed to build menubar snapshot", "error", err)
		respondMenubarSnapshotError(w, err, "failed to build menubar snapshot")
		return
	}
	respondJSON(w, http.StatusOK, snapshot)
}

// MenubarTrayTitle returns the compact tray label (e.g. "6%·4%·1%") using the
// same rules as the macOS menubar companion, so GNOME can stay in sync without
// reimplementing formatting.
func (h *Handler) MenubarTrayTitle(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.menubarSnapshotForRequest(r.Context())
	if err != nil {
		h.logger.Error("failed to build menubar snapshot for tray title", "error", err)
		respondMenubarSnapshotError(w, err, "failed to build menubar tray title")
		return
	}
	// Never fail the tray title on a settings read error: the last settings
	// that were read successfully (or defaults) still format a usable title.
	settings := h.menubarSettingsWithFallback()
	title := menubar.TrayTitle(snapshot, settings)
	segments := menubar.TraySegments(snapshot, settings)
	tooltip := ""
	if snapshot != nil {
		if snapshot.Aggregate.ProviderCount == 0 {
			tooltip = "onWatch: no provider data"
		} else {
			tooltip = fmt.Sprintf("onWatch: %s across %d providers, updated %s",
				snapshot.Aggregate.Label, snapshot.Aggregate.ProviderCount, snapshot.UpdatedAgo)
		}
		// Prefer per-segment tooltip when available (shows who is who).
		if len(segments) > 0 {
			parts := make([]string, 0, len(segments))
			for _, seg := range segments {
				if seg.Label != "" && seg.Text != "" {
					parts = append(parts, seg.Label+": "+seg.Text)
				}
			}
			if len(parts) > 0 {
				tooltip = strings.Join(parts, " · ")
			}
			// Stale providers get their own lines; GNOME shows the tooltip and
			// dims the matching segments from the same flag.
			if lines := menubar.StaleProviderTooltipLines(snapshot, time.Now()); len(lines) > 0 {
				tooltip += "\n" + strings.Join(lines, "\n")
			}
		}
	}
	response := map[string]interface{}{
		"title":    title,
		"tooltip":  tooltip,
		"online":   snapshot != nil,
		"segments": segments,
	}
	// Surface LatestAt so tray companions can keep counting up even when the
	// daemon cannot be reached. Mirror the snapshot's omitzero behaviour:
	// never emit the zero time as "0001-01-01T00:00:00Z", which clients render
	// as hundreds of thousands of days.
	if snapshot != nil && !snapshot.LatestAt.IsZero() {
		response["latest_at"] = snapshot.LatestAt.UTC().Format(time.RFC3339)
	}
	respondJSON(w, http.StatusOK, response)
}

// MenubarPage renders the compact browser UI used by the tray companions and
// the VS Code quick view. Public on loopback, behind dashboard auth elsewhere.
func (h *Handler) MenubarPage(w http.ResponseWriter, r *http.Request) {

	settings, _ := h.menubarSettings()
	view := normalizeMenubarView(r.URL.Query().Get("view"), settings.DefaultView)
	html, err := h.renderMenubarHTML(view, settings)
	if err != nil {
		h.logger.Error("failed to render menubar page", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to render menubar page")
		return
	}
	w.Header().Set("Content-Security-Policy", menubarPageCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// menubarPageCSP lets the quick view be framed by the onWatch VS Code
// extension (sidebar webview and Simple Browser run inside vscode-webview://
// on desktop, vscode-cdn.net in the browser-based editors, under a
// vscode-file:// workbench) and by the dashboard origin itself, and by nobody
// else. The middleware skips X-Frame-Options for this page so this list is
// the whole policy.
const menubarPageCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'self' vscode-webview: vscode-file: https://*.vscode-cdn.net"

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
		// Preferences are derived from the same cached build as the summary:
		// the one build supplies the unfiltered card list, so opening the
		// popover never starts a second set of queries next to a summary
		// refresh. If the build fails and there is no cached data, fall back to
		// the last good settings with no options rather than 500.
		data, err := h.menubarDataForRequest(r.Context())
		if err != nil {
			h.logger.Error("failed to load menubar preferences", "error", err)
			respondMenubarSnapshotError(w, err, "failed to load menubar preferences")
			return
		}
		// Settings are read fresh: one cheap primary-key lookup, and it keeps
		// the response correct even when the cached build predates a save.
		effective := h.menubarSettingsWithFallback()
		respondJSON(w, http.StatusOK, menubarPreferencesResponse(effective, h.buildMenubarProviderOptions(effective, data.AllProviders)))
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
		if err := h.saveMenubarSettings(normalized); err != nil {
			h.logger.Error("failed to save menubar preferences", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to save menubar preferences")
			return
		}
		h.triggerMenubarRefresh()
		// Derive only the provider options from the shared build; the settings
		// in the response are always the ones just saved. When the rebuild
		// exceeds the wait budget the build data still carries the pre-save
		// settings, and echoing them here would make the popover persist them
		// into localStorage and revert the user's change.
		data, err := h.menubarDataForRequest(r.Context())
		if err != nil {
			h.logger.Error("failed to rebuild menubar provider options", "error", err)
			respondJSON(w, http.StatusOK, menubarPreferencesResponse(normalized, nil))
			return
		}
		respondJSON(w, http.StatusOK, menubarPreferencesResponse(normalized, h.buildMenubarProviderOptions(normalized, data.AllProviders)))
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) MenubarRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// A manual refresh must actually rebuild, even inside the fresh window,
	// but it keeps the last snapshot as the fallback: if the rebuild stalls
	// past the wait budget the request still answers with the previous data.
	h.markMenubarSnapshotStale()
	if _, err := h.menubarSnapshotForRequest(r.Context()); err != nil {
		h.logger.Debug("menubar refresh snapshot rebuild failed", "error", err)
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// respondMenubarSnapshotError maps a snapshot failure to a status. A wedged
// first build is temporary, so it answers 503 (retry later) rather than 500;
// the client keeps its last data either way.
func respondMenubarSnapshotError(w http.ResponseWriter, err error, message string) {
	if err == errMenubarSnapshotTimeout {
		respondError(w, http.StatusServiceUnavailable, message)
		return
	}
	respondError(w, http.StatusInternalServerError, message)
}

func isLoopbackRequest(r *http.Request) bool {
	// A reverse proxy on the same host connects from loopback on behalf of
	// remote clients. Forwarded requests are never treated as local.
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	clientIP := net.ParseIP(host)
	return clientIP != nil && clientIP.IsLoopback()
}

func isLocalMenubarPublicPath(path string) bool {
	return path == "/menubar" ||
		path == "/api/menubar/summary" ||
		path == "/api/menubar/preferences" ||
		path == "/api/menubar/refresh" ||
		path == "/api/menubar/mistral/retry" ||
		path == "/api/menubar/tray-title"
}

// BuildMenubarSnapshot constructs the shared menubar UI contract.
//
// When the stored settings cannot be read, the last settings that were read
// successfully stand in, so a transient store failure does not turn the whole
// menubar into a 500. Only when no settings have ever been read does it fail.
func (h *Handler) BuildMenubarSnapshot() (*menubar.Snapshot, error) {
	built, err := h.buildMenubarSnapshotWithAllProviders()
	if err != nil {
		return nil, err
	}
	return h.menubarStaleData(built).Snapshot, nil
}

// buildMenubarSnapshotWithAllProviders builds the filtered snapshot the
// clients see plus the unfiltered card list the preferences endpoint derives
// its options from. Building both in one pass keeps preferences off a second
// set of queries.
func (h *Handler) buildMenubarSnapshotWithAllProviders() (menubarBuiltData, error) {
	settings, err := h.reconcileMenubarSettings()
	if err != nil {
		settings = h.menubarLastSettingsSnapshot()
		if settings == nil {
			return menubarBuiltData{}, err
		}
		h.logger.Warn("menubar settings read failed; building from the last good settings", "error", err)
	}

	// Build with hidden cards included, then filter, so one pass supplies both
	// the snapshot and the options list.
	allProviders, latest := h.buildMenubarProviders(settings, true)
	normalized := settings.Normalize()
	providers := filterMenubarProviders(allProviders, normalized.VisibleProviders)
	if providers == nil {
		providers = []menubar.ProviderCard{}
	}
	aggregate := buildAggregate(providers)
	return menubarBuiltData{
		Snapshot: &menubar.Snapshot{
			GeneratedAt: time.Now().UTC(),
			// UpdatedAgo and LatestAt both describe the newest provider data, not
			// this build: a header age computed from them keeps growing when every
			// provider has stopped polling.
			UpdatedAgo:     timeAgo(latest),
			LatestAt:       latest.UTC(),
			Aggregate:      aggregate,
			Providers:      providers,
			MistralEnabled: h.config != nil && h.config.HasProvider("mistral"),
		},
		AllProviders: allProviders,
		Settings:     normalized,
	}, nil
}

// applyMenubarStaleness marks cards whose data is older than their provider's
// expected cadence. It mutates the cards in place (they were just built for
// this snapshot) and never changes Status: stale is a freshness flag, not a
// severity.
//
// The card's UpdatedAtTime comes from its honest capture time (oldest quota
// for multi-quota providers); the threshold comes from the ONE table in
// menubar_stale.go. The registry only supplies the reason class.
func (h *Handler) applyMenubarStaleness(providers []menubar.ProviderCard, visibility map[string]map[string]bool) {
	if len(providers) == 0 {
		return
	}
	now := h.menubarNowTime().UTC()
	interval := time.Duration(h.getPollIntervalSec()) * time.Second
	registry := h.pollStatus
	if registry == nil {
		registry = agent.GetPollStatusRegistry()
	}
	for i := range providers {
		card := &providers[i]
		base := card.BaseProvider
		if base == "" {
			base = providerKeyBase(card.ID)
		}
		polling := true
		if pv, ok := visibility[card.ID]; ok {
			if value, exists := pv["polling"]; exists {
				polling = value
			}
		} else if pv, ok := visibility[base]; ok {
			if value, exists := pv["polling"]; exists {
				polling = value
			}
		}
		var account string
		if idx := strings.IndexByte(card.ID, ':'); idx >= 0 {
			account = card.ID[idx+1:]
		}
		var outcome agent.PollOutcome
		if registry != nil {
			if got, ok := registry.Get(base, account); ok {
				outcome = got
			}
		}
		window := staleAfterFor(staleContext{
			Provider:        card.ID,
			Base:            base,
			Polling:         polling,
			PollInterval:    interval,
			Status:          card.ConnectionStatus,
			Outcome:         outcome,
			AnthropicSource: h.anthropicSource(),
		})
		if window <= 0 {
			// Manual (polling:false) providers never age out for network
			// reasons: their age grows by design until the dashboard refreshes
			// them. The card still carries its real age label.
			card.StaleAfterSeconds = 0
			continue
		}
		card.StaleAfterSeconds = int64(window.Seconds())
		source := card.UpdatedAtTime
		if card.StaleCheckedAt != "" {
			// Staleness is judged from a different timestamp than the age label
			// (Anthropic hybrid: primary windows only, see
			// applyMenubarOldestCapturedAt).
			source = card.StaleCheckedAt
		}
		captured, err := time.Parse(time.RFC3339, source)
		if err != nil || captured.IsZero() {
			continue
		}
		age := now.Sub(captured)
		if age <= window {
			continue
		}
		card.Stale = true
		if outcome != "" {
			card.StaleReason = outcome.Reason()
		}
		if card.StaleReason == "" {
			card.StaleReason = menubar.StaleReasonText(age)
		}
	}
}

func (h *Handler) buildMenubarProviders(settings *menubar.Settings, includeHidden bool) ([]menubar.ProviderCard, time.Time) {
	normalized := settings.Normalize()
	if h.menubarProvidersBuildHook != nil {
		h.menubarProvidersBuildHook()
	}
	// Reuse dashboard tab renames (Settings → Tab name) for menubar cards.
	labels := h.loadDashboardProviderLabels()

	visibility := h.providerVisibilityMap()
	providers := make([]menubar.ProviderCard, 0, 10)
	latest := time.Time{}

	// attempted holds every card ID this build considered (configured and
	// dashboard-visible). After the build, cached cards outside this set are
	// evicted, so a removed provider or deleted account does not linger.
	attempted := make(map[string]struct{}, 10)
	defer h.pruneMenubarLastGood(attempted)

	// add appends a resolved card and folds its data time into latest. A cached
	// card keeps its original timestamp, which is a real data time, so LatestAt
	// still comes only from real data.
	add := func(card *menubar.ProviderCard, captured time.Time) {
		if card == nil {
			return
		}
		providers = append(providers, *card)
		if captured.After(latest) {
			latest = captured
		}
	}

	if h.config != nil && h.config.HasProvider("synthetic") && h.providerDashboardVisible("synthetic", visibility) {
		attempted["synthetic"] = struct{}{}
		payload, readOK, hasData := h.buildSyntheticCurrentOK()
		add(h.menubarResolveCard("synthetic", resolveProviderTabLabel("synthetic", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("zai") && h.providerDashboardVisible("zai", visibility) {
		attempted["zai"] = struct{}{}
		payload, readOK, hasData := h.buildZaiCurrentOK()
		add(h.menubarResolveCard("zai", resolveProviderTabLabel("zai", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("anthropic") && h.providerDashboardVisible("anthropic", visibility) {
		attempted["anthropic"] = struct{}{}
		payload, readOK, hasData := h.buildAnthropicCurrentOK()
		add(h.menubarResolveCard("anthropic", resolveProviderTabLabel("anthropic", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("copilot") && h.providerDashboardVisible("copilot", visibility) {
		attempted["copilot"] = struct{}{}
		payload, readOK, hasData := h.buildCopilotCurrentOK()
		add(h.menubarResolveCard("copilot", resolveProviderTabLabel("copilot", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("codex") && h.providerDashboardVisible("codex", visibility) {
		codexAccounts, enumerated := h.codexUsageAccountsOK()
		if enumerated {
			multiCodex := len(codexAccounts) > 1
			for _, account := range codexAccounts {
				usage := account.Usage
				accountID := codexUsageAccountID(usage)
				providerKey := fmt.Sprintf("codex:%d", accountID)
				if !providerDashboardVisibleForKey(visibility, providerKey, "codex") {
					continue
				}
				attempted[providerKey] = struct{}{}
				name := stringValue(usage, "accountName")
				if name == "" {
					name = "default"
				}
				subtitle := "ChatGPT account"
				label := menubarAccountLabel("codex", labels, name, multiCodex)
				add(h.menubarResolveCard(providerKey, label, subtitle, usage, account.ReadOK, account.HasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
			}
		} else {
			// The account list could not be read. Keep every known account card
			// and leave the cache untouched: an enumeration failure says nothing
			// about which accounts still exist.
			for _, entry := range h.menubarCachedAccountCards("codex", attempted, visibility) {
				add(entry.card, entry.captured)
			}
		}
	}
	if h.config != nil && h.config.HasProvider("antigravity") && h.providerDashboardVisible("antigravity", visibility) {
		attempted["antigravity"] = struct{}{}
		payload, readOK, hasData := h.buildAntigravityCurrentOK()
		add(h.menubarResolveCard("antigravity", resolveProviderTabLabel("antigravity", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("minimax") && h.providerDashboardVisible("minimax", visibility) {
		minimaxAccounts, enumerated := h.minimaxUsageAccountsOK()
		if enumerated {
			multiMiniMax := len(minimaxAccounts) > 1
			for _, account := range minimaxAccounts {
				usage := account.Usage
				accountID := minimaxUsageAccountID(usage)
				providerKey := fmt.Sprintf("minimax:%d", accountID)
				if !providerDashboardVisibleForKey(visibility, providerKey, "minimax") {
					continue
				}
				attempted[providerKey] = struct{}{}
				name := stringValue(usage, "accountName")
				if name == "" {
					name = "default"
				}
				subtitle := "MiniMax account"
				label := menubarAccountLabel("minimax", labels, name, multiMiniMax)
				add(h.menubarResolveCard(providerKey, label, subtitle, usage, account.ReadOK, account.HasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
			}
		} else {
			// The account list could not be read: keep the known account cards
			// and do not prune them.
			for _, entry := range h.menubarCachedAccountCards("minimax", attempted, visibility) {
				add(entry.card, entry.captured)
			}
		}
	}
	if h.config != nil && h.config.HasProvider("openrouter") && h.providerDashboardVisible("openrouter", visibility) {
		attempted["openrouter"] = struct{}{}
		payload, readOK, hasData := h.buildOpenRouterCurrentOK()
		add(h.menubarResolveCard("openrouter", resolveProviderTabLabel("openrouter", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("gemini") && h.providerDashboardVisible("gemini", visibility) {
		attempted["gemini"] = struct{}{}
		payload, readOK, hasData := h.buildGeminiCurrentOK()
		add(h.menubarResolveCard("gemini", resolveProviderTabLabel("gemini", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("grok") && h.providerDashboardVisible("grok", visibility) {
		attempted["grok"] = struct{}{}
		payload, readOK, hasData := h.buildGrokCurrentOK()
		add(h.menubarResolveCard("grok", resolveProviderTabLabel("grok", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("kimi") && h.providerDashboardVisible("kimi", visibility) {
		attempted["kimi"] = struct{}{}
		payload, readOK, hasData := h.buildKimiCurrentOK()
		add(h.menubarResolveCard("kimi", "Kimi Code", "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("mistral") && h.providerDashboardVisible("mistral", visibility) {
		attempted["mistral"] = struct{}{}
		payload, readOK, hasData := h.buildMistralCurrentOK()
		add(h.menubarResolveCard("mistral", resolveProviderTabLabel("mistral", labels), mistralBillingSubtitle(payload), payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("opencode") && h.providerDashboardVisible("opencode", visibility) {
		attempted["opencode"] = struct{}{}
		payload, readOK, hasData := h.buildOpenCodeCurrentOK()
		add(h.menubarResolveCard("opencode", resolveProviderTabLabel("opencode", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, openCodePeakDecorator))
	}
	if h.config != nil && h.config.HasProvider("ollama") && h.providerDashboardVisible("ollama", visibility) {
		attempted["ollama"] = struct{}{}
		payload, readOK, hasData := h.buildOllamaCurrentOK()
		add(h.menubarResolveCard("ollama", resolveProviderTabLabel("ollama", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("muse") && h.providerDashboardVisible("muse", visibility) {
		attempted["muse"] = struct{}{}
		payload, readOK, hasData := h.buildMuseCurrentOK()
		add(h.menubarResolveCard("muse", resolveProviderTabLabel("muse", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}
	if h.config != nil && h.config.HasProvider("commandcode") && h.providerDashboardVisible("commandcode", visibility) {
		attempted["commandcode"] = struct{}{}
		payload, readOK, hasData := h.buildCommandCodeCurrentOK()
		add(h.menubarResolveCard("commandcode", resolveProviderTabLabel("commandcode", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, commandCodePeakDecorator))
	}
	if h.config != nil && h.config.HasProvider("cursor") && h.providerDashboardVisible("cursor", visibility) {
		attempted["cursor"] = struct{}{}
		payload, readOK, hasData := h.buildCursorCurrentOK()
		add(h.menubarResolveCard("cursor", resolveProviderTabLabel("cursor", labels), "", payload, readOK, hasData, normalized.WarningPercent, normalized.CriticalPercent, nil))
	}

	sortProviderCards(providers, normalized.ProvidersOrder)
	if !includeHidden {
		providers = filterMenubarProviders(providers, normalized.VisibleProviders)
	}
	if providers == nil {
		// An empty build still serialises as [] rather than null.
		providers = []menubar.ProviderCard{}
	}
	return providers, latest
}

// menubarStaleData returns a copy of the built data with staleness freshly
// evaluated at serve time. The cached build itself is immutable and may be up
// to menubarFreshWindow old; staleness depends on the wall clock, so it is
// recomputed on every read. That is what makes a card turn stale while the
// daemon is otherwise healthy but one provider stopped polling.
func (h *Handler) menubarStaleData(data menubarBuiltData) menubarBuiltData {
	if data.Snapshot == nil {
		return data
	}
	visibility := h.providerVisibilityMap()
	fresh := make([]menubar.ProviderCard, len(data.Snapshot.Providers))
	copy(fresh, data.Snapshot.Providers)
	h.applyMenubarStaleness(fresh, visibility)

	snapshotCopy := *data.Snapshot
	snapshotCopy.Providers = fresh
	return menubarBuiltData{
		Snapshot:     &snapshotCopy,
		AllProviders: data.AllProviders,
		Settings:     data.Settings,
	}
}

// menubarResolveCard normalizes one provider payload into a card and applies
// the last-good cache. decorate, when non-nil, runs on a freshly built card
// before it is cached.
//
// readOK reports whether the provider's storage read succeeded; hasData reports
// whether it returned stored rows. A read failure is distinct from a genuine
// "no data yet": on failure the cached card is served unchanged (or the
// provider is omitted when nothing was cached). A successful read with no rows
// means the history is genuinely gone, so any cached card is evicted; the
// provider is omitted, except for Mistral's connection-only placeholder, which
// carries retry state but must never claim a data time.
func (h *Handler) menubarResolveCard(id, label, subtitle string, payload map[string]interface{}, readOK, hasData bool, warningPercent, criticalPercent int, decorate func(*menubar.ProviderCard)) (*menubar.ProviderCard, time.Time) {
	if !readOK {
		return h.menubarCachedCard(id)
	}
	if !hasData {
		h.forgetMenubarCard(id)
		if providerKeyBase(id) != "mistral" {
			return nil, time.Time{}
		}
		// Mistral still shows a connection-only placeholder so the user can see
		// the reconnect/browser-access state and retry. It must not carry a
		// capturedAt: that would fabricate a fresh age and feed LatestAt.
		timeless := make(map[string]interface{}, len(payload))
		for key, value := range payload {
			if key == "capturedAt" {
				continue
			}
			timeless[key] = value
		}
		card := normalizeProviderCard(id, label, subtitle, timeless, warningPercent, criticalPercent)
		if card == nil {
			return nil, time.Time{}
		}
		if decorate != nil {
			decorate(card)
		}
		return card, time.Time{}
	}
	card := normalizeProviderCard(id, label, subtitle, payload, warningPercent, criticalPercent)
	if card == nil {
		// Stored rows that produce nothing recognizable are not evidence that
		// the history is gone (some polls store a snapshot with no quota
		// values). Serve the last good card instead of dropping it: with
		// return nil here the card vanished now and reappeared later with
		// older numbers.
		return h.menubarCachedCard(id)
	}
	if decorate != nil {
		decorate(card)
	}
	captured := h.applyMenubarOldestCapturedAt(card, id, payload)
	h.storeMenubarCard(id, card)
	return card, captured
}

// applyMenubarOldestCapturedAt sets the card's age source to the OLDEST quota
// capture time for the providers whose builders merge per-quota snapshots.
// Their payload capturedAt is the newest quota's time, so a fresh quota hides
// a days-old one and the card reads as fresh. This is menubar-only: dashboard
// responses keep reporting the newest time (the API contract there).
//
// Age and staleness share one rule: a quota's time comes from the CURRENT
// payload while that quota is present. Only a quota missing from the payload
// (aged out of the builder's query window) contributes its last-seen time,
// and that memory is dropped once every quota is back - otherwise the card
// would stay pinned to its first old reading forever.
//
// Providers without per-quota timestamps keep the payload capturedAt. The
// returned time feeds LatestAt, so it is the same honest capture time the age
// label shows.
func (h *Handler) applyMenubarOldestCapturedAt(card *menubar.ProviderCard, id string, payload map[string]interface{}) time.Time {
	current := parseCapturedAt(payload)
	base := providerKeyBase(id)
	switch base {
	case "anthropic", "mistral", "cursor":
	default:
		return current
	}
	present := quotaCapturedTimes(payload)
	// Cursor's builder reads only the newest snapshot, so a quota missing from
	// the payload is gone, not windowed out. Remembering it would pin the card
	// stale for up to menubarQuotaMemoryBound after the quota disappeared.
	// Anthropic and Mistral read a window (per-quota merge and per-quota latest
	// snapshots), where a missing quota may simply have aged out.
	oldest := h.mergeQuotaTimes(id, present, base != "cursor")
	if oldest.IsZero() {
		return current
	}
	if !current.IsZero() && current.Before(oldest) {
		// The builder's own time is older still (for example a statusline
		// snapshot newer quotas do not cover); keep the older one.
		return current
	}
	card.UpdatedAtTime = oldest.UTC().Format(time.RFC3339)
	card.UpdatedAt = timeAgo(oldest)
	if base == "anthropic" && h.anthropicSource() != "api" {
		// Anthropic's statusline supplies the primary windows every few
		// minutes while API-only quotas (Sonnet, extra usage) refresh only
		// every tenth poll in hybrid mode. Judging staleness from the oldest
		// quota would flap the card for a quota that is expected to be 20m
		// old. Staleness follows the newest PRIMARY window; the age label
		// still shows the honest oldest time.
		if primary, ok := anthropicPrimaryCapturedAt(present); ok && !primary.IsZero() {
			card.StaleCheckedAt = primary.UTC().Format(time.RFC3339)
		}
	}
	return oldest
}

// anthropicSource returns the configured Anthropic data source. Empty means
// the default ("auto", hybrid statusline + API).
func (h *Handler) anthropicSource() string {
	if h.config == nil {
		return "auto"
	}
	source := strings.ToLower(strings.TrimSpace(h.config.AnthropicSource))
	if source == "" {
		return "auto"
	}
	return source
}

// anthropicPrimaryCapturedAt returns the newest capture time among the two
// primary windows the statusline feeds. They are what the card and tray show,
// so their freshness is what the card's staleness means. A payload without
// them (API-only mode before the first poll) reports false and the age source
// is used.
func anthropicPrimaryCapturedAt(times map[string]time.Time) (time.Time, bool) {
	var newest time.Time
	found := false
	for _, name := range []string{"five_hour", "seven_day"} {
		at, ok := times[name]
		if !ok || at.IsZero() {
			continue
		}
		if !found || at.After(newest) {
			newest = at
			found = true
		}
	}
	return newest, found
}

// menubarQuotaMemoryBound is how long a quota remembered from an earlier
// payload keeps pinning a card's age after it disappears from the payload.
//
// The bound is 24h, not 2x the stale threshold. A quota that vanishes because
// the provider stopped reporting it (extra usage switched off, a renamed
// window) can never come back through the builder's query window (Cursor reads
// only the newest snapshot, Anthropic the last 50), so remembering it forever
// pins the age until restart. The card still shows its real (old) data until
// the next successful build of the remaining quotas, and the age it reports is
// the real capture time of the last data that still exists. A 24h horizon
// covers a quota that merely aged out of the window while the provider was
// idle (the builders query at most a few hours of history), while anything
// older than that is not data the card is still displaying.
const menubarQuotaMemoryBound = 24 * time.Hour

// mergeQuotaTimes folds the payload's per-quota times into the remembered set
// and returns the oldest time the card should show. A present quota always
// updates its entry to the current time, so a refreshed quota moves forward;
// a quota absent from the payload contributes its last-seen time until it has
// been missing longer than menubarQuotaMemoryBound, at which point the entry
// is dropped. Without that bound a permanently removed quota would pin the
// card's age (and its stale flag) until the daemon restarted.
//
// rememberMissing must be false for providers whose builder reads only the
// latest snapshot (Cursor): a quota absent there is gone, so it stops counting
// immediately instead of pinning the card until the bound expires.
func (h *Handler) mergeQuotaTimes(id string, present map[string]time.Time, rememberMissing bool) time.Time {
	h.menubarLastGoodMu.Lock()
	defer h.menubarLastGoodMu.Unlock()
	if len(present) == 0 && rememberMissing {
		// Nothing in the payload to age; leave the remembered set untouched
		// for a later pass that can tell which quotas are missing.
		return time.Time{}
	}
	if h.menubarQuotas == nil {
		h.menubarQuotas = make(map[string]map[string]time.Time, 8)
	}
	lastSeen := h.menubarQuotas[id]
	if lastSeen == nil {
		lastSeen = make(map[string]time.Time, len(present))
		h.menubarQuotas[id] = lastSeen
	}
	if h.menubarQuotaMissing == nil {
		h.menubarQuotaMissing = make(map[string]map[string]time.Time, 8)
	}
	missing := h.menubarQuotaMissing[id]
	if missing == nil {
		missing = make(map[string]time.Time, 2)
		h.menubarQuotaMissing[id] = missing
	}
	now := h.menubarNowTime().UTC()
	for name, at := range present {
		if !at.IsZero() {
			lastSeen[name] = at
			delete(missing, name)
		}
	}
	// Drop quotas that have been missing from the payload for longer than the
	// bound, measured from the first pass that did not carry them. A quota
	// that is merely absent from one payload (aged out of the builder's
	// window) stays remembered; one that is gone for good stops pinning the
	// card's age. Latest-snapshot builders have no window: a missing quota is
	// dropped at once.
	for name := range lastSeen {
		if _, ok := present[name]; ok {
			continue
		}
		if !rememberMissing {
			delete(lastSeen, name)
			delete(missing, name)
			continue
		}
		since, seen := missing[name]
		if !seen {
			missing[name] = now
			continue
		}
		if now.Sub(since) > menubarQuotaMemoryBound {
			delete(lastSeen, name)
			delete(missing, name)
		}
	}
	if len(lastSeen) == 0 {
		delete(h.menubarQuotas, id)
		delete(h.menubarQuotaMissing, id)
		return time.Time{}
	}
	var oldest time.Time
	for _, at := range lastSeen {
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	return oldest
}

// quotaCapturedTimes maps quota name to its lastUpdatedAt from a payload.
func quotaCapturedTimes(payload map[string]interface{}) map[string]time.Time {
	var rawQuotas []interface{}
	switch typed := payload["quotas"].(type) {
	case []interface{}:
		rawQuotas = typed
	case []map[string]interface{}:
		rawQuotas = make([]interface{}, 0, len(typed))
		for _, item := range typed {
			rawQuotas = append(rawQuotas, item)
		}
	default:
		return nil
	}
	out := make(map[string]time.Time, len(rawQuotas))
	for _, raw := range rawQuotas {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := stringValue(item, "name")
		if name == "" {
			name = stringValue(item, "label")
		}
		value := stringValue(item, "lastUpdatedAt")
		if name == "" || value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil || parsed.IsZero() {
			continue
		}
		out[name] = parsed
	}
	return out
}

// oldestQuotaCapturedAt scans the payload quotas for the earliest
// lastUpdatedAt. It reports false when no quota carries a timestamp.
func oldestQuotaCapturedAt(payload map[string]interface{}) (time.Time, bool) {
	times := quotaCapturedTimes(payload)
	var oldest time.Time
	for _, at := range times {
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	if oldest.IsZero() {
		return time.Time{}, false
	}
	return oldest, true
}

// menubarCachedCard serves the last good card for a provider whose storage read
// failed. With nothing cached the provider is omitted rather than fabricated
// with a zero-value card stamped now.
func (h *Handler) menubarCachedCard(id string) (*menubar.ProviderCard, time.Time) {
	h.menubarLastGoodMu.Lock()
	cached, ok := h.menubarLastGood[id]
	h.menubarLastGoodMu.Unlock()
	if !ok {
		return nil, time.Time{}
	}
	card := cached
	captured, _ := time.Parse(time.RFC3339, card.UpdatedAtTime)
	return &card, captured
}

func (h *Handler) forgetMenubarCard(id string) {
	h.menubarLastGoodMu.Lock()
	defer h.menubarLastGoodMu.Unlock()
	delete(h.menubarLastGood, id)
	delete(h.menubarQuotas, id)
	delete(h.menubarQuotaMissing, id)
}

func (h *Handler) storeMenubarCard(id string, card *menubar.ProviderCard) {
	h.menubarLastGoodMu.Lock()
	defer h.menubarLastGoodMu.Unlock()
	if h.menubarLastGood == nil {
		h.menubarLastGood = make(map[string]menubar.ProviderCard, 8)
	}
	h.menubarLastGood[id] = *card
}

// pruneMenubarLastGood drops cached cards for providers this build did not
// consider, so a removed provider, a deleted account or a disabled provider
// does not linger in the menubar.
func (h *Handler) pruneMenubarLastGood(attempted map[string]struct{}) {
	h.menubarLastGoodMu.Lock()
	defer h.menubarLastGoodMu.Unlock()
	for id := range h.menubarLastGood {
		if _, ok := attempted[id]; !ok {
			delete(h.menubarLastGood, id)
			delete(h.menubarQuotas, id)
			delete(h.menubarQuotaMissing, id)
		}
	}
}

// menubarCachedAccountEntry is one cached card handed back while a
// multi-account provider's account enumeration is failing.
type menubarCachedAccountEntry struct {
	card     *menubar.ProviderCard
	captured time.Time
}

// menubarCachedAccountCards serves every cached card for a multi-account
// provider whose account list could not be read. The entries are marked
// attempted so deferred pruning keeps them; pruning waits for a successful
// enumeration, which is the only evidence that an account was deleted.
func (h *Handler) menubarCachedAccountCards(base string, attempted map[string]struct{}, visibility map[string]map[string]bool) []menubarCachedAccountEntry {
	h.menubarLastGoodMu.Lock()
	defer h.menubarLastGoodMu.Unlock()
	entries := make([]menubarCachedAccountEntry, 0, len(h.menubarLastGood))
	for id, card := range h.menubarLastGood {
		if providerKeyBase(id) != base {
			continue
		}
		if !providerDashboardVisibleForKey(visibility, id, base) {
			continue
		}
		attempted[id] = struct{}{}
		stored := card
		captured, _ := time.Parse(time.RFC3339, stored.UpdatedAtTime)
		entries = append(entries, menubarCachedAccountEntry{card: &stored, captured: captured})
	}
	// Stable order so tests and clients see the same account order even though
	// the cache is a map.
	sort.Slice(entries, func(i, j int) bool { return entries[i].card.ID < entries[j].card.ID })
	return entries
}

// menubarCardAccountID extracts the account ID from an account-scoped card ID
// ("codex:2", "minimax:3"). Providers without a scope return 0.
func menubarCardAccountID(cardID string) int64 {
	idx := strings.IndexByte(cardID, ':')
	if idx < 0 {
		return 0
	}
	id, err := strconv.ParseInt(cardID[idx+1:], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// updateMenubarProviderHistory changes one settings value so provider history
// and automatic visibility are saved together. A nil history marks settings
// written before provider discovery was tracked.
func updateMenubarProviderHistory(settings *menubar.Settings, configured []string) bool {
	if settings == nil {
		return false
	}
	changed := settings.KnownProviders == nil
	if settings.KnownProviders == nil {
		settings.KnownProviders = []string{}
		for _, provider := range configured {
			if provider != "commandcode" {
				settings.KnownProviders = append(settings.KnownProviders, provider)
			}
		}
	}
	for _, provider := range configured {
		if menubarSettingsKeyAllows(provider, settings.KnownProviders) {
			continue
		}
		settings.KnownProviders = append(settings.KnownProviders, provider)
		if len(settings.VisibleProviders) > 0 && !menubarSettingsKeyAllows(provider, settings.VisibleProviders) {
			settings.VisibleProviders = append(settings.VisibleProviders, provider)
		}
		changed = true
	}
	return changed
}

func (h *Handler) configuredMenubarProviders() []string {
	if h.config == nil {
		return nil
	}
	configured := make([]string, 0, len(providerCatalog()))
	for _, provider := range providerCatalog() {
		if h.config.HasProvider(provider.Key) {
			configured = append(configured, provider.Key)
		}
	}
	return configured
}

func (h *Handler) reconcileMenubarSettings() (*menubar.Settings, error) {
	h.menubarSettingsMu.Lock()
	defer h.menubarSettingsMu.Unlock()
	settings, err := h.menubarSettings()
	if err != nil {
		return settings, err
	}
	// Record the read that succeeded before any write below can fail; the
	// values themselves are valid even if persisting the discovery history
	// does not work.
	h.recordMenubarLastSettings(settings)
	if h.store == nil {
		return settings, nil
	}
	if updateMenubarProviderHistory(settings, h.configuredMenubarProviders()) {
		if err := h.store.SetMenubarSettings(settings); err != nil {
			return settings, err
		}
	}
	return settings, nil
}

// saveMenubarSettings keeps discovery history when either settings API writes
// a client supplied settings object. The submitted visibility is explicit.
func (h *Handler) saveMenubarSettings(settings *menubar.Settings) error {
	h.menubarSettingsMu.Lock()
	defer h.menubarSettingsMu.Unlock()
	current, err := h.menubarSettings()
	if err != nil {
		return err
	}
	updateMenubarProviderHistory(current, h.configuredMenubarProviders())
	settings.KnownProviders = append([]string{}, current.KnownProviders...)
	if err := h.store.SetMenubarSettings(settings); err != nil {
		return err
	}
	// Menubar output just changed: the next summary/tray-title request must
	// rebuild rather than serve the previous settings' snapshot.
	h.invalidateMenubarSnapshot()
	return nil
}

// menubarAccountLabel builds multi-account menubar titles using the dashboard
// tab rename for the base provider. Single-account cards drop the "- default"
// suffix so a renamed "❄️ Codex" is not forced to "❄️ Codex - default".
func menubarAccountLabel(baseKey string, labels map[string]string, accountName string, multi bool) string {
	base := resolveProviderTabLabel(baseKey, labels)
	if base == "" {
		base = defaultProviderTabLabel(baseKey)
	}
	if !multi {
		return base
	}
	name := strings.TrimSpace(accountName)
	if name == "" {
		name = "default"
	}
	return base + " - " + name
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
	content := strings.Replace(string(page), "__ONWATCH_MENUBAR_BOOTSTRAP__", string(bootstrap), 1)
	content = strings.ReplaceAll(content, "__ONWATCH_MISTRAL_ASSET_VERSION__", mistralRecoveryAssetVersion)
	content = strings.ReplaceAll(content, "__ONWATCH_PEAK_ASSET_VERSION__", peakHoursAssetVersion)
	basePath := h.getBasePath()
	baseJSON, _ := json.Marshal(basePath)
	content = strings.ReplaceAll(content, "__ONWATCH_BASE_PATH_JSON__", string(baseJSON))
	content = strings.ReplaceAll(content, "__ONWATCH_BASE_PATH__", html.EscapeString(basePath))
	version := strings.TrimSpace(h.version)
	if version == "" {
		version = "dev"
	}
	return strings.Replace(content, "__ONWATCH_MENUBAR_VERSION__", version, 1), nil
}

func (h *Handler) buildMenubarProviderOptions(settings *menubar.Settings, providers []menubar.ProviderCard) []menubarProviderOption {
	normalized := settings.Normalize()
	visibleKeys := normalized.VisibleProviders
	options := make([]menubarProviderOption, 0, len(providers))
	for _, provider := range providers {
		quotaOptions := make([]menubarQuotaOption, 0, len(provider.Quotas))
		for _, quota := range provider.Quotas {
			quotaOptions = append(quotaOptions, menubarQuotaOption{
				Key:   quota.Key,
				Label: quota.Label,
			})
		}
		isVisible := len(visibleKeys) == 0 || menubarSettingsKeyAllows(provider.ID, visibleKeys)
		options = append(options, menubarProviderOption{
			ID:           provider.ID,
			BaseProvider: provider.BaseProvider,
			Label:        provider.Label,
			Subtitle:     provider.Subtitle,
			Visible:      isVisible,
			Quotas:       quotaOptions,
		})
	}
	return options
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
	var connection *api.ProviderConnection
	if id == "mistral" {
		if c, ok := payload["connection"].(api.ProviderConnection); ok {
			connection = &c
		}
	}
	if len(quotas) == 0 && (connection == nil || connection.Reason == "" && !connection.CanRetry && !connection.Retrying) {
		return nil
	}
	status := "healthy"
	connectionStatus := stringValue(payload, "status")
	// reconnect and partial describe a connection that needs action (some or
	// all data unavailable), so they stay a warning floor. Plain staleness does
	// not: see the isStale branch below.
	if connectionStatus == "reconnect" || connectionStatus == "partial" || connectionStatus == "paused" {
		status = "warning"
	}
	// A paused account needs re-authentication. That is a warning floor, not a
	// ceiling: the quota loop below still worsens the status, so a paused
	// account at 100% keeps showing critical while the tooltip signals re-auth
	// (#147 review, item 6).
	paused, _ := payload["paused"].(bool)
	if paused {
		status = worsenStatus(status, "warning")
		connectionStatus = "paused"
	} else if stale, _ := payload["isStale"].(bool); stale {
		// Staleness is shown only through the per-card age label. It must not
		// raise the dot severity: a healthy card that is merely old stays
		// healthy. The connection_status still marks the state for the clients
		// that need it (Mistral recovery text); it does not drive color here.
		if connectionStatus == "" {
			connectionStatus = "stale"
		}
	}
	if connection != nil && connection.Reason != "" {
		status = "warning"
	}
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
	// Age is computed from the provider's own last stored snapshot time
	// (capturedAt), not the summary build time. That way it keeps growing when
	// this provider's polls stop, even though the daemon is still serving
	// snapshots for other providers.
	updatedAt := ""
	updatedAtTime := ""
	if captured := parseCapturedAt(payload); !captured.IsZero() {
		updatedAt = timeAgo(captured)
		updatedAtTime = captured.UTC().Format(time.RFC3339)
	}
	return &menubar.ProviderCard{
		ID:               id,
		BaseProvider:     providerKeyBase(id),
		Label:            label,
		Subtitle:         subtitle,
		ConnectionStatus: connectionStatus,
		Connection:       connection,
		Status:           status,
		HighestPercent:   highest,
		UpdatedAt:        updatedAt,
		UpdatedAtTime:    updatedAtTime,
		Quotas:           quotas,
		Trends:           trends,
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
		for _, key := range []string{"subscription", "search", "toolCalls", "tokensLimit", "timeLimit", "sharedQuota", "credits"} {
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
		statusLabel := stringValue(item, "statusLabel")
		percentOnly := false
		if v, ok := item["percentOnly"].(bool); ok {
			percentOnly = v
		}
		meter := menubar.QuotaMeter{
			Key:            strings.ToLower(strings.ReplaceAll(label, " ", "_")),
			Label:          label,
			DisplayValue:   displayValue(item, percent),
			Percent:        percent,
			Status:         quotaStatus(item, percent, warningPercent, criticalPercent),
			StatusLabel:    statusLabel,
			PercentOnly:    percentOnly,
			Used:           firstFloat(item, "usage", "used", "currentUsage", "currentUsed"),
			Limit:          firstFloat(item, "limit", "total", "currentLimit", "entitlement"),
			Format:         stringValue(item, "format"),
			Currency:       stringValue(item, "currency"),
			ResetAt:        firstString(item, "renewsAt", "resetsAt", "resetDate", "resetTime", "resetAt"),
			TimeUntilReset: stringValue(item, "timeUntilReset"),
			ProjectedValue: firstFloat(item, "projectedUsage", "projectedUtil", "projectedValue"),
			CurrentRate:    firstFloat(item, "currentRate"),
			Source:         stringValue(item, "source"),
			AgeSeconds:     int64(firstFloat(item, "ageSeconds")),
		}
		if name := stringValue(item, "name"); name == "api_included" || name == "vibe_included" {
			meter.Key = name
		}
		if v, ok := item["isStale"]; ok {
			if b, ok := v.(bool); ok {
				meter.IsStale = b
			}
		}
		quotas = append(quotas, meter)
	}
	return quotas
}

func quotaStatus(item map[string]interface{}, percent float64, warningPercent, criticalPercent int) string {
	// An upstream window status that is not "ok" (OpenCode key mode) is
	// authoritative: a blocked window is critical whatever its percent.
	if label := stringValue(item, "statusLabel"); label != "" && !strings.EqualFold(label, "ok") {
		return "critical"
	}
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

// commandCodePeakDecorator attaches the DeepSeek peak schedule to the Command
// Code card. Command Code serves the DeepSeek models, so the provider-level
// card shows the schedule, model note, and peak pricing together.
func commandCodePeakDecorator(card *menubar.ProviderCard) {
	card.Peak = menubar.NewDeepSeekPeak("commandcode", "Command Code DeepSeek models", menubar.DeepSeekPeakPricing)
}

// openCodePeakDecorator attaches the DeepSeek peak schedule to the OpenCode Go
// card. The schedule applies to the DeepSeek models the Go plan exposes.
func openCodePeakDecorator(card *menubar.ProviderCard) {
	card.Peak = menubar.NewDeepSeekPeak("opencode", menubar.DeepSeekPeakModelNote, "")
}

func sortProviderCards(cards []menubar.ProviderCard, preferred []string) {
	if len(cards) == 0 {
		return
	}
	order := make(map[string]int, len(preferred))
	for idx, key := range preferred {
		order[key] = idx
	}
	rank := func(cardID string) (int, bool) {
		if idx, ok := order[cardID]; ok {
			return idx, true
		}
		// Settings UI may store base keys ("codex") while cards use
		// account-scoped IDs ("codex:1").
		base := providerKeyBase(cardID)
		if base != cardID {
			if idx, ok := order[base]; ok {
				return idx, true
			}
		}
		return 0, false
	}
	sort.SliceStable(cards, func(i, j int) bool {
		leftOrder, leftOK := rank(cards[i].ID)
		rightOrder, rightOK := rank(cards[j].ID)
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
	filtered := make([]menubar.ProviderCard, 0, len(cards))
	for _, card := range cards {
		if menubarSettingsKeyAllows(card.ID, visible) {
			filtered = append(filtered, card)
		}
	}
	return filtered
}

// menubarSettingsKeyAllows reports whether a menubar card ID is covered by a
// settings key list (providers_order / visible_providers). Exact match wins;
// bare base keys like "codex" also match account-scoped cards like "codex:1".
// Distinct accounts ("codex:1" vs "codex:2") do not match each other.
func menubarSettingsKeyAllows(cardID string, keys []string) bool {
	for _, key := range keys {
		if menubarProviderKeysMatch(cardID, key) {
			return true
		}
	}
	return false
}

func menubarProviderKeysMatch(cardID, settingsKey string) bool {
	cardID = strings.TrimSpace(cardID)
	settingsKey = strings.TrimSpace(settingsKey)
	if cardID == "" || settingsKey == "" {
		return false
	}
	if cardID == settingsKey {
		return true
	}
	cardBase := providerKeyBase(cardID)
	settingsBase := providerKeyBase(settingsKey)
	if cardBase != settingsBase {
		return false
	}
	// Same family: bare "codex" matches any "codex:N"; specific accounts
	// only match themselves (handled above).
	return cardID == cardBase || settingsKey == settingsBase
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
	// Currency quotas with an unknown cap (Ollama Free plan) have no
	// meaningful percent; show the dollars used instead of a misleading 0%.
	if unknown, ok := item["limitUnknown"].(bool); ok && unknown && stringValue(item, "format") == "currency" {
		return formatUsdShort(firstFloat(item, "used", "usage")) + " used"
	}
	if v := stringValue(item, "cardLabel"); v == "Remaining" {
		return fmt.Sprintf("%.0f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

// formatUsdShort keeps sub-cent amounts visible ($0.001) and rounds the rest
// to cents.
func formatUsdShort(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
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

// mistralBillingSubtitle labels the provider card with pay-as-you-go spend.
// An unavailable balance gets no subtitle: the dashboard's spend card already
// reports that, and a permanent "unavailable" line is only clutter in a
// compact menu.
func mistralBillingSubtitle(payload map[string]interface{}) string {
	if payload["showBilling"] == false {
		return ""
	}
	b, ok := payload["billing"].(*api.MistralBilling)
	if !ok || b == nil || b.Amount == nil {
		return ""
	}
	subtitle := fmt.Sprintf("Pay-as-you-go: %.2f %s", *b.Amount, b.Currency)
	if b.Status != "" && b.Status != "ok" {
		subtitle += " (" + b.Status + ")"
	}
	return subtitle
}
