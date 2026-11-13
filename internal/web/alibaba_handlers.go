package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// AlibabaAccountReloader is called to hot-reload Alibaba agents after account CRUD.
type AlibabaAccountReloader interface {
	Reload()
}

// SetAlibabaTracker sets the Alibaba tracker for usage summary enrichment.
func (h *Handler) SetAlibabaTracker(t *tracker.AlibabaTracker) {
	h.alibabaTracker = t
}

// SetAlibabaAgentManager sets the Alibaba agent manager for hot-reload on account changes.
func (h *Handler) SetAlibabaAgentManager(m AlibabaAccountReloader) {
	h.alibabaAgentMgr = m
}

// alibabaRegionPattern bounds the region to what is safe to embed in the
// console URL path (for example ap-southeast-1).
var alibabaRegionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// alibabaMaxCookieBytes bounds a pasted cookie export.
const alibabaMaxCookieBytes = 256 * 1024

var alibabaQuotaDisplayOrder = map[string]int{
	api.AlibabaQuotaFiveHour: 1,
	api.AlibabaQuotaWeekly:   2,
	api.AlibabaQuotaMonthly:  3,
}

var alibabaDisplayNames = map[string]string{
	api.AlibabaQuotaFiveHour: "5-Hour",
	api.AlibabaQuotaWeekly:   "Weekly",
	api.AlibabaQuotaMonthly:  "Monthly",
}

func alibabaDisplayName(name string) string {
	if dn, ok := alibabaDisplayNames[name]; ok {
		return dn
	}
	return name
}

func alibabaQuotaOrder(name string) int {
	if order, ok := alibabaQuotaDisplayOrder[name]; ok {
		return order
	}
	return 99
}

// alibabaQuotaNames lists the windows in display order.
func alibabaQuotaNames() []string {
	return []string{api.AlibabaQuotaFiveHour, api.AlibabaQuotaWeekly, api.AlibabaQuotaMonthly}
}

// alibabaInsightsResponse is the JSON payload for Alibaba insights.
type alibabaInsightsResponse struct {
	Stats    []alibabaInsightStat `json:"stats"`
	Insights []insightItem        `json:"insights"`
	// partial marks an answer built after a swallowed read error; it is
	// served but never cached.
	partial bool
}

func (r alibabaInsightsResponse) isPartial() bool { return r.partial }

// alibabaInsightStat carries linked forecast metadata for the dashboard.
type alibabaInsightStat struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Key      string `json:"key,omitempty"`
	Metric   string `json:"metric,omitempty"`
	Severity string `json:"severity,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

// ── Account helpers ──

// parseAlibabaAccountID extracts the Alibaba account ID from query params and
// falls back to the first active account.
func (h *Handler) parseAlibabaAccountID(r *http.Request) int64 {
	if accountStr := r.URL.Query().Get("account"); accountStr != "" {
		if id, err := strconv.ParseInt(accountStr, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return h.defaultAlibabaAccountID()
}

// defaultAlibabaAccountID returns the first active Alibaba account that has
// cookies, or the first active account when none has cookies yet.
func (h *Handler) defaultAlibabaAccountID() int64 {
	if h.store == nil {
		return 0
	}
	accounts, err := h.store.QueryActiveProviderAccounts("alibaba")
	if err != nil || len(accounts) == 0 {
		return 0
	}
	for _, acc := range accounts {
		if alibabaAccountHasCookies(acc.Metadata) {
			return acc.ID
		}
	}
	return accounts[0].ID
}

// alibabaAccountMetadata is the JSON stored in provider_accounts.metadata.
type alibabaAccountMetadata struct {
	Cookies string `json:"cookies,omitempty"`
	Region  string `json:"region,omitempty"`
}

func parseAlibabaAccountMetadata(raw string) alibabaAccountMetadata {
	var meta alibabaAccountMetadata
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &meta)
	}
	return meta
}

func alibabaAccountHasCookies(raw string) bool {
	return strings.TrimSpace(parseAlibabaAccountMetadata(raw).Cookies) != ""
}

// alibabaUsageAccounts returns current usage for all active Alibaba accounts.
func (h *Handler) alibabaUsageAccounts() []map[string]interface{} {
	usages, _ := h.alibabaUsageAccountsOK()
	accounts := make([]map[string]interface{}, 0, len(usages))
	for _, usage := range usages {
		accounts = append(accounts, usage.Usage)
	}
	return accounts
}

// alibabaUsageAccountsOK is alibabaUsageAccounts with an explicit enumeration
// outcome and the read outcome of each account's builder. enumerated is false
// only when the account list itself could not be read.
func (h *Handler) alibabaUsageAccountsOK() (accounts []menubarAccountUsage, enumerated bool) {
	if h.store == nil {
		return nil, true
	}

	rows, err := h.store.QueryActiveProviderAccounts("alibaba")
	if err != nil {
		h.logger.Error("failed to query Alibaba accounts", "error", err)
		return nil, false
	}

	// The migration seeds an empty "default" account for everyone. Hide it
	// while another account exists and it has neither cookies nor data, so a
	// UI-created account does not show next to a placeholder.
	if len(rows) > 1 {
		kept := make([]store.ProviderAccount, 0, len(rows))
		for _, acc := range rows {
			if acc.Name == "default" && !alibabaAccountHasCookies(acc.Metadata) {
				if latest, qErr := h.store.QueryLatestAlibaba(acc.ID); qErr == nil && latest == nil {
					continue
				}
			}
			kept = append(kept, acc)
		}
		if len(kept) > 0 {
			rows = kept
		}
	}

	accounts = make([]menubarAccountUsage, 0, len(rows))
	for _, acc := range rows {
		usage, readOK, hasData := h.buildAlibabaCurrentOK(acc.ID)
		usage["accountId"] = acc.ID
		usage["accountName"] = acc.Name
		usage["id"] = acc.ID
		usage["name"] = acc.Name
		accounts = append(accounts, menubarAccountUsage{Usage: usage, ReadOK: readOK, HasData: hasData})
	}
	return accounts, true
}

func alibabaUsageAccountID(usage map[string]interface{}) int64 {
	if usage == nil {
		return 0
	}
	switch v := usage["accountId"].(type) {
	case int64:
		if v > 0 {
			return v
		}
	case int:
		if v > 0 {
			return int64(v)
		}
	case float64:
		if v > 0 {
			return int64(v)
		}
	}
	return 0
}

func alibabaUsageAccountName(usage map[string]interface{}) string {
	if usage == nil {
		return ""
	}
	name, _ := usage["accountName"].(string)
	return name
}

func alibabaAccountTelemetryEnabled(visibility map[string]interface{}, accountID int64) bool {
	accountKey := fmt.Sprintf("alibaba:%d", accountID)
	if polling, exists := providerPollingValue(visibility[accountKey]); exists {
		return polling
	}
	return providerTelemetryEnabled(visibility, "alibaba")
}

// markAlibabaConfigured flags the provider as enabled after an account with
// cookies is saved, so the dashboard shows it without a restart.
func (h *Handler) markAlibabaConfigured() {
	if h.config == nil {
		return
	}
	h.config.WithWrite(func() {
		if !h.config.AlibabaDisabled && !h.config.AlibabaEnabled {
			h.config.AlibabaEnabled = true
		}
	})
}

// ── Account CRUD ──

// AlibabaAccounts handles Alibaba account management.
// GET    /api/alibaba/accounts        - list all accounts
// POST   /api/alibaba/accounts        - create account (body: {name, cookies, region})
// PUT    /api/alibaba/accounts?id=N   - update account (body: {name?, cookies?, region?, restore?})
// DELETE /api/alibaba/accounts?id=N   - soft-delete account
func (h *Handler) AlibabaAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.alibabaAccountsList(w, r)
	case http.MethodPost:
		h.alibabaAccountCreate(w, r)
	case http.MethodPut:
		h.alibabaAccountUpdate(w, r)
	case http.MethodDelete:
		h.alibabaAccountDelete(w, r)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) alibabaAccountsList(w http.ResponseWriter, _ *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"accounts": []interface{}{}})
		return
	}
	accounts, err := h.store.QueryProviderAccounts("alibaba")
	if err != nil {
		h.logger.Error("failed to query Alibaba accounts", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query accounts")
		return
	}
	result := make([]map[string]interface{}, 0, len(accounts))
	for _, acc := range accounts {
		meta := parseAlibabaAccountMetadata(acc.Metadata)
		// The cookies are never returned; only whether they are set.
		entry := map[string]interface{}{
			"id":         acc.ID,
			"name":       acc.Name,
			"createdAt":  acc.CreatedAt.Format(time.RFC3339),
			"hasCookies": strings.TrimSpace(meta.Cookies) != "",
		}
		if meta.Region != "" {
			entry["region"] = meta.Region
		}
		if acc.DeletedAt != nil {
			entry["deletedAt"] = acc.DeletedAt.Format(time.RFC3339)
		}
		result = append(result, entry)
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"accounts": result})
}

// normalizeAlibabaCookiesInput validates pasted cookies and returns the value
// to store. Netscape exports are kept as-is (domain columns matter for the
// Zelda gateway). Cookie-header pastes are normalised through
// ParseAlibabaCookies so a leading "Cookie:" prefix is stripped.
func normalizeAlibabaCookiesInput(raw string) (string, string) {
	if len(raw) > alibabaMaxCookieBytes {
		return "", "cookies are too large"
	}
	raw = strings.TrimSpace(raw)
	parsed := api.ParseAlibabaCookies(raw)
	if parsed == "" {
		return "", "no cookies found: paste a Cookie header or a Netscape cookies.txt export"
	}
	if api.AlibabaCookiesAreNetscape(raw) {
		return raw, ""
	}
	return parsed, ""
}

func (h *Handler) alibabaAccountCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Cookies string `json:"cookies"`
		Region  string `json:"region"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*alibabaMaxCookieBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "account name is required")
		return
	}
	if !validProfileName.MatchString(req.Name) {
		respondError(w, http.StatusBadRequest, "invalid account name: use only letters, numbers, hyphens, and underscores")
		return
	}
	req.Region = strings.TrimSpace(req.Region)
	if req.Region != "" && !alibabaRegionPattern.MatchString(req.Region) {
		respondError(w, http.StatusBadRequest, "invalid region")
		return
	}
	meta := alibabaAccountMetadata{Region: req.Region}
	if strings.TrimSpace(req.Cookies) != "" {
		cookies, msg := normalizeAlibabaCookiesInput(req.Cookies)
		if msg != "" {
			respondError(w, http.StatusBadRequest, msg)
			return
		}
		meta.Cookies = cookies
	}
	if h.store == nil {
		respondError(w, http.StatusInternalServerError, "store not available")
		return
	}

	acc, err := h.store.CreateOrRestoreProviderAccount("alibaba", req.Name)
	if err != nil {
		h.logger.Error("failed to create Alibaba account", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	if meta.Cookies != "" || meta.Region != "" {
		metaJSON, _ := json.Marshal(meta)
		if err := h.store.UpdateProviderAccountMetadata(acc.ID, string(metaJSON)); err != nil {
			h.logger.Error("failed to update Alibaba account metadata", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to save account")
			return
		}
	}
	if meta.Cookies != "" {
		h.markAlibabaConfigured()
	}

	if h.alibabaAgentMgr != nil {
		h.alibabaAgentMgr.Reload()
	}
	h.invalidateMenubarSnapshot()

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "account created",
		"id":      acc.ID,
		"name":    req.Name,
	})
}

func (h *Handler) alibabaAccountUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(w, http.StatusBadRequest, "valid account id is required")
		return
	}

	var req struct {
		Name    *string `json:"name"`
		Cookies *string `json:"cookies"`
		Region  *string `json:"region"`
		Restore *bool   `json:"restore"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*alibabaMaxCookieBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if h.store == nil {
		respondError(w, http.StatusInternalServerError, "store not available")
		return
	}

	acc, err := h.store.GetProviderAccountByID(id)
	if err != nil || acc == nil || acc.Provider != "alibaba" {
		respondError(w, http.StatusNotFound, "account not found")
		return
	}

	// Validate everything before the first write so a bad field never leaves
	// the account half updated.
	meta := parseAlibabaAccountMetadata(acc.Metadata)
	metaChanged := false
	if req.Cookies != nil && strings.TrimSpace(*req.Cookies) != "" {
		cookies, msg := normalizeAlibabaCookiesInput(*req.Cookies)
		if msg != "" {
			respondError(w, http.StatusBadRequest, msg)
			return
		}
		meta.Cookies = cookies
		metaChanged = true
	}
	if req.Region != nil {
		region := strings.TrimSpace(*req.Region)
		if region != "" && !alibabaRegionPattern.MatchString(region) {
			respondError(w, http.StatusBadRequest, "invalid region")
			return
		}
		meta.Region = region
		metaChanged = true
	}
	newName := ""
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		newName = strings.TrimSpace(*req.Name)
		if !validProfileName.MatchString(newName) {
			respondError(w, http.StatusBadRequest, "invalid account name")
			return
		}
	}

	if req.Restore != nil && *req.Restore {
		if err := h.store.UndeleteProviderAccountByID(id); err != nil {
			h.logger.Error("failed to restore Alibaba account", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to restore account")
			return
		}
	}
	if newName != "" {
		if err := h.store.RenameProviderAccount(id, newName); err != nil {
			h.logger.Error("failed to rename Alibaba account", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to rename account")
			return
		}
	}
	if metaChanged {
		metaJSON, _ := json.Marshal(meta)
		if err := h.store.UpdateProviderAccountMetadata(id, string(metaJSON)); err != nil {
			h.logger.Error("failed to update Alibaba account metadata", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to update account")
			return
		}
	}
	if meta.Cookies != "" {
		h.markAlibabaConfigured()
	}

	if h.alibabaAgentMgr != nil {
		h.alibabaAgentMgr.Reload()
	}
	h.invalidateMenubarSnapshot()

	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "account updated", "id": id})
}

func (h *Handler) alibabaAccountDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(w, http.StatusBadRequest, "valid account id is required")
		return
	}
	if h.store == nil {
		respondError(w, http.StatusInternalServerError, "store not available")
		return
	}

	acc, err := h.store.GetProviderAccountByID(id)
	if err != nil || acc == nil || acc.Provider != "alibaba" {
		respondError(w, http.StatusNotFound, "account not found")
		return
	}

	if err := h.store.MarkProviderAccountDeletedByID(id); err != nil {
		h.logger.Error("failed to delete Alibaba account", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	if h.alibabaAgentMgr != nil {
		h.alibabaAgentMgr.Reload()
	}
	h.invalidateMenubarSnapshot()

	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "account deleted", "id": id})
}

// AlibabaAccountsUsage returns current usage for all active Alibaba accounts.
func (h *Handler) AlibabaAccountsUsage(w http.ResponseWriter, _ *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"accounts": []interface{}{}})
		return
	}
	usages, enumerated := h.alibabaUsageAccountsOK()
	result := make([]map[string]interface{}, 0, len(usages))
	if enumerated {
		for _, usage := range usages {
			result = append(result, usage.Usage)
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"accounts": result})
}

// ── Current ──

func (h *Handler) currentAlibaba(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.buildAlibabaCurrent(h.parseAlibabaAccountID(r)))
}

// buildAlibabaCurrent builds the current Alibaba response for one account.
func (h *Handler) buildAlibabaCurrent(accountID int64) map[string]interface{} {
	response, _, _ := h.buildAlibabaCurrentOK(accountID)
	return response
}

// buildAlibabaCurrentOK is buildAlibabaCurrent with an explicit storage
// outcome. readOK is false only when the read that supplied the payload failed.
func (h *Handler) buildAlibabaCurrentOK(accountID int64) (response map[string]interface{}, readOK bool, hasData bool) {
	now := time.Now().UTC()
	response = map[string]interface{}{
		"capturedAt": now.Format(time.RFC3339),
		"quotas":     []interface{}{},
	}
	if h.store == nil {
		return response, true, false
	}

	latest, err := h.store.QueryLatestAlibaba(accountID)
	if err != nil {
		h.logger.Error("failed to query latest Alibaba snapshot", "error", err)
		return response, false, false
	}
	if latest == nil {
		return response, true, false
	}
	hasData = true

	response["capturedAt"] = latest.CapturedAt.Format(time.RFC3339)
	if latest.SpecCode != "" {
		response["plan"] = latest.SpecCode
	}
	if latest.Status != "" {
		response["subscriptionStatus"] = latest.Status
	}
	response["remainingDays"] = latest.RemainingDays
	response["autoRenew"] = latest.AutoRenew
	if latest.EndTime != nil {
		response["periodEnd"] = latest.EndTime.Format(time.RFC3339)
	}

	// One summary per quota per request: UsageSummary runs an active-cycle
	// query, a bounded history scan and a latest-snapshot read.
	summaries := map[string]*tracker.AlibabaSummary{}
	summaryFor := func(name string) *tracker.AlibabaSummary {
		if h.alibabaTracker == nil {
			return nil
		}
		if cached, ok := summaries[name]; ok {
			return cached
		}
		summary, sErr := h.alibabaTracker.UsageSummary(name, accountID)
		if sErr != nil {
			summary = nil
		}
		summaries[name] = summary
		return summary
	}

	quotas := append([]api.AlibabaQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left, right := alibabaQuotaOrder(quotas[i].Name), alibabaQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	age := now.Sub(latest.CapturedAt)
	out := make([]interface{}, 0, len(quotas))
	for _, q := range quotas {
		qMap := map[string]interface{}{
			"name":          q.Name,
			"displayName":   alibabaDisplayName(q.Name),
			"utilization":   q.Utilization,
			"limit":         100,
			"used":          q.Utilization,
			"format":        "percent",
			"status":        utilStatus(q.Utilization),
			"lastUpdatedAt": latest.CapturedAt.Format(time.RFC3339),
			"ageSeconds":    int64(age.Seconds()),
			"isStale":       age > 30*time.Minute,
		}
		if q.ResetsAt != nil {
			timeUntilReset := time.Until(*q.ResetsAt)
			qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			qMap["timeUntilReset"] = formatDuration(timeUntilReset)
			qMap["timeUntilResetSeconds"] = int64(timeUntilReset.Seconds())
		}
		if summary := summaryFor(q.Name); summary != nil {
			qMap["currentRate"] = summary.CurrentRate
			qMap["projectedUtil"] = summary.ProjectedUtil
			if !summary.TrackingSince.IsZero() {
				qMap["trackingSince"] = summary.TrackingSince.Format(time.RFC3339)
			}
		}
		out = append(out, qMap)
	}
	response["quotas"] = out
	applyDisplayModeToResponse(response, h.getDisplayMode("alibaba"))
	return response, true, hasData
}

// ── History ──

// alibabaHistoryRows converts snapshots to the { capturedAt, quotas: [...] }
// rows the single-provider chart reads.
func alibabaHistoryRows(snapshots []*api.AlibabaSnapshot) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(snapshots))
	for _, snap := range snapshots {
		quotas := make([]map[string]interface{}, 0, len(snap.Quotas))
		for _, q := range snap.Quotas {
			qMap := map[string]interface{}{
				"name":        q.Name,
				"utilization": q.Utilization,
			}
			if q.ResetsAt != nil {
				qMap["resetsAt"] = q.ResetsAt.Format(time.RFC3339)
			}
			quotas = append(quotas, qMap)
		}
		rows = append(rows, map[string]interface{}{
			"capturedAt": snap.CapturedAt.Format(time.RFC3339),
			"quotas":     quotas,
		})
	}
	return rows
}

// alibabaFlatHistory converts snapshots to the downsampled flat
// { capturedAt, <quota>: utilization } rows the combined view reads.
func alibabaFlatHistory(snapshots []*api.AlibabaSnapshot) []map[string]interface{} {
	step := downsampleStep(len(snapshots), maxChartPoints)
	last := len(snapshots) - 1
	data := make([]map[string]interface{}, 0, min(len(snapshots), maxChartPoints))
	for i, snap := range snapshots {
		if step > 1 && i != 0 && i != last && i%step != 0 {
			continue
		}
		entry := map[string]interface{}{"capturedAt": snap.CapturedAt.Format(time.RFC3339)}
		for _, q := range snap.Quotas {
			entry[q.Name] = q.Utilization
		}
		data = append(data, entry)
	}
	return data
}

func (h *Handler) historyAlibaba(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	start, end := parseProviderHistoryRange(r.URL.Query().Get("range"))
	snapshots, err := h.store.QueryAlibabaChartRange(start, end, h.parseAlibabaAccountID(r))
	if err != nil {
		h.logger.Error("failed to query Alibaba history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query history")
		return
	}
	respondJSON(w, http.StatusOK, alibabaHistoryRows(snapshots))
}

// ── Cycles ──

func alibabaCycleToMap(c *store.AlibabaResetCycle, active bool) map[string]interface{} {
	cycleMap := map[string]interface{}{
		"id":              c.ID,
		"quotaName":       c.QuotaName,
		"cycleStart":      c.CycleStart.Format(time.RFC3339),
		"cycleEnd":        nil,
		"peakUtilization": c.PeakUtilization,
		"totalDelta":      c.TotalDelta,
		"isActive":        active,
	}
	if c.CycleEnd != nil {
		cycleMap["cycleEnd"] = c.CycleEnd.Format(time.RFC3339)
	}
	if c.ResetsAt != nil {
		cycleMap["resetsAt"] = c.ResetsAt.Format(time.RFC3339)
		if active {
			cycleMap["timeUntilReset"] = formatDuration(time.Until(*c.ResetsAt))
		}
	}
	return cycleMap
}

func (h *Handler) cyclesAlibaba(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	accountID := h.parseAlibabaAccountID(r)
	quotaName := r.URL.Query().Get("type")
	if quotaName == "" {
		quotaName = api.AlibabaQuotaFiveHour
	}

	active, err := h.store.QueryActiveAlibabaCycle(quotaName, accountID)
	if err != nil {
		h.logger.Error("failed to query active Alibaba cycle", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}
	history, err := h.store.QueryAlibabaCycleHistory(quotaName, accountID, 50)
	if err != nil {
		h.logger.Error("failed to query Alibaba cycle history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycles")
		return
	}

	cycles := make([]map[string]interface{}, 0, len(history)+1)
	if active != nil {
		cycles = append(cycles, alibabaCycleToMap(active, true))
	}
	for _, c := range history {
		cycles = append(cycles, alibabaCycleToMap(c, false))
	}
	respondJSON(w, http.StatusOK, cycles)
}

func (h *Handler) cycleOverviewAlibaba(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"cycles": []interface{}{}})
		return
	}

	// The dashboard sends `groupBy`; accept `group_by` too.
	groupBy := r.URL.Query().Get("groupBy")
	if groupBy == "" {
		groupBy = r.URL.Query().Get("group_by")
	}
	if groupBy == "" {
		groupBy = api.AlibabaQuotaFiveHour
	}

	overview, err := h.store.QueryAlibabaCycleOverview(groupBy, cycleOverviewProviderLimit(r), h.parseAlibabaAccountID(r))
	if err != nil {
		h.logger.Error("failed to query Alibaba cycle overview", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query cycle overview")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groupBy":    groupBy,
		"provider":   "alibaba",
		"quotaNames": alibabaQuotaNames(),
		"cycles":     cycleOverviewRowsToJSON(overview),
	})
}

// ── Summary ──

func (h *Handler) summaryAlibaba(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	respondJSON(w, http.StatusOK, h.buildAlibabaSummaryMap(h.parseAlibabaAccountID(r)))
}

func (h *Handler) buildAlibabaSummaryMap(accountID int64) map[string]interface{} {
	if h.store == nil || h.alibabaTracker == nil {
		return map[string]interface{}{}
	}

	quotaNames, err := h.store.QueryAllAlibabaQuotaNames(accountID)
	if err != nil {
		h.logger.Error("failed to query Alibaba quota names", "error", err)
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})
	for _, name := range quotaNames {
		summary, err := h.alibabaTracker.UsageSummary(name, accountID)
		if err != nil || summary == nil {
			continue
		}
		entry := map[string]interface{}{
			"currentUtil":     summary.CurrentUtil,
			"completedCycles": summary.CompletedCycles,
			"peakCycle":       summary.PeakCycle,
			"avgPerCycle":     summary.AvgPerCycle,
			"totalTracked":    summary.TotalTracked,
		}
		if summary.ResetsAt != nil {
			entry["resetsAt"] = summary.ResetsAt.Format(time.RFC3339)
			entry["timeUntilReset"] = formatDuration(summary.TimeUntilReset)
		}
		result[name] = entry
	}
	return result
}

// ── Insights ──

type alibabaQuotaRate struct {
	Rate          float64
	HasRate       bool
	TimeToReset   time.Duration
	TimeToExhaust time.Duration
	ExhaustsFirst bool
	ProjectedPct  float64
}

func (h *Handler) computeAlibabaRate(quotaName string, currentUtil float64, summary *tracker.AlibabaSummary, accountID int64) alibabaQuotaRate {
	var result alibabaQuotaRate

	if summary != nil && summary.ResetsAt != nil {
		result.TimeToReset = time.Until(*summary.ResetsAt)
	}

	if h.store != nil {
		points, err := h.store.QueryAlibabaUtilizationSeries(quotaName, time.Now().Add(-30*time.Minute), accountID)
		if err == nil && len(points) >= 2 {
			first, last := points[0], points[len(points)-1]
			// A short burst cannot support an hourly projection.
			if elapsed := last.CapturedAt.Sub(first.CapturedAt); elapsed >= 20*time.Minute {
				result.Rate = math.Max(0, last.Utilization-first.Utilization) / elapsed.Hours()
				result.HasRate = true
			}
		}
	}

	if !result.HasRate && summary != nil && summary.CurrentRate > 0 {
		result.Rate = summary.CurrentRate
		result.HasRate = true
	}

	if result.HasRate && result.Rate > 0 {
		if remaining := 100 - currentUtil; remaining > 0 {
			result.TimeToExhaust = time.Duration(remaining / result.Rate * float64(time.Hour))
		}
		if result.TimeToReset > 0 {
			result.ProjectedPct = currentUtil + (result.Rate * result.TimeToReset.Hours())
			if result.ProjectedPct > 100 {
				result.ProjectedPct = 100
			}
			result.ExhaustsFirst = result.TimeToExhaust > 0 && result.TimeToExhaust < result.TimeToReset
		}
	}
	return result
}

func buildAlibabaBurnRateInsight(quota api.AlibabaQuota, rate alibabaQuotaRate) insightItem {
	item := insightItem{
		Key:   fmt.Sprintf("forecast_%s", quota.Name),
		Title: fmt.Sprintf("%s Burn Rate", alibabaDisplayName(quota.Name)),
	}
	if !rate.HasRate {
		return item
	}

	projected := quota.Utilization
	if rate.ProjectedPct > projected {
		projected = rate.ProjectedPct
	}
	sublabel := fmt.Sprintf("~%.0f%% by reset", projected)
	if rate.TimeToReset > 0 {
		sublabel = fmt.Sprintf("~%.0f%% by reset in %s", projected, formatDuration(rate.TimeToReset))
	}

	item.Type = "forecast"
	item.Sublabel = sublabel
	if rate.Rate < 0.01 {
		item.Severity = "positive"
		item.Metric = "Flat"
		item.Desc = fmt.Sprintf("Currently at %.0f%%. No measurable increase in the last 30 minutes, so this window is on pace to finish near %.0f%%.", quota.Utilization, projected)
		return item
	}

	item.Metric = fmt.Sprintf("%.1f%%/hr", rate.Rate)
	switch {
	case rate.ExhaustsFirst:
		item.Severity = "negative"
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset and likely to exhaust in %s before reset.", quota.Utilization, projected, formatDuration(rate.TimeToExhaust))
	case rate.ProjectedPct >= 80:
		item.Severity = "warning"
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset.", quota.Utilization, projected)
	default:
		item.Severity = "positive"
		item.Desc = fmt.Sprintf("Currently at %.0f%%. At this rate, projected %.0f%% by reset.", quota.Utilization, projected)
	}
	return item
}

func (h *Handler) insightsAlibaba(w http.ResponseWriter, r *http.Request, rangeDur time.Duration) {
	hidden := h.getHiddenInsightKeys()
	respondInsights(w, h.buildAlibabaInsights(h.parseAlibabaAccountID(r), hidden, rangeDur))
}

func (h *Handler) buildAlibabaInsights(accountID int64, hidden map[string]bool, _ time.Duration) alibabaInsightsResponse {
	resp := alibabaInsightsResponse{Stats: []alibabaInsightStat{}, Insights: []insightItem{}}
	if h.store == nil {
		return resp
	}

	latest, err := h.store.QueryLatestAlibaba(accountID)
	markPartial(&resp.partial, err)
	if err != nil || latest == nil || len(latest.Quotas) == 0 {
		return resp
	}

	if latest.SpecCode != "" {
		value := strings.ToUpper(latest.SpecCode[:1]) + latest.SpecCode[1:]
		if latest.Status != "" {
			value = fmt.Sprintf("%s (%s)", value, strings.ToLower(latest.Status))
		}
		resp.Stats = append(resp.Stats, alibabaInsightStat{Label: "Plan", Value: value})
	}
	if latest.EndTime != nil {
		label := "Expires"
		if latest.AutoRenew {
			label = "Renews"
		}
		resp.Stats = append(resp.Stats, alibabaInsightStat{
			Label:    label,
			Value:    latest.EndTime.UTC().Format("Jan 2"),
			Sublabel: fmt.Sprintf("%d days left", latest.RemainingDays),
		})
	}

	quotas := append([]api.AlibabaQuota(nil), latest.Quotas...)
	sort.SliceStable(quotas, func(i, j int) bool {
		left, right := alibabaQuotaOrder(quotas[i].Name), alibabaQuotaOrder(quotas[j].Name)
		if left != right {
			return left < right
		}
		return quotas[i].Name < quotas[j].Name
	})

	for _, quota := range quotas {
		var summary *tracker.AlibabaSummary
		if h.alibabaTracker != nil {
			if s, sErr := h.alibabaTracker.UsageSummary(quota.Name, accountID); sErr == nil {
				summary = s
			}
		}
		rate := h.computeAlibabaRate(quota.Name, quota.Utilization, summary, accountID)
		insightKey := fmt.Sprintf("forecast_%s", quota.Name)
		if hidden[insightKey] {
			continue
		}
		insight := buildAlibabaBurnRateInsight(quota, rate)
		if insight.Metric == "" {
			continue
		}
		resp.Stats = append(resp.Stats, alibabaInsightStat{
			Key:      insightKey,
			Label:    fmt.Sprintf("%s Burn Rate", alibabaDisplayName(quota.Name)),
			Value:    insight.Metric,
			Sublabel: insight.Sublabel,
			Metric:   insight.Metric,
			Severity: insight.Severity,
			Desc:     insight.Desc,
		})
		if insight.Severity == "negative" || insight.Severity == "warning" {
			resp.Insights = appendWindowInsight(resp.Insights, insight)
		}
	}

	resp.Stats, resp.Insights = boundWindowInsights(resp.Stats, resp.Insights)
	return resp
}

// ── Logging history ──

func (h *Handler) loggingHistoryAlibaba(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"logs": []interface{}{}})
		return
	}

	start, end, limit := h.loggingHistoryRangeAndLimit(r)
	snapshots, err := h.store.QueryAlibabaRange(start, end, h.parseAlibabaAccountID(r), limit)
	if err != nil {
		h.logger.Error("failed to query Alibaba snapshots", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to query logging history")
		return
	}

	quotaNames := alibabaQuotaNames()
	capturedAt := make([]time.Time, 0, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	series := make([]map[string]loggingHistoryCrossQuota, 0, len(snapshots))
	for _, snap := range snapshots {
		capturedAt = append(capturedAt, snap.CapturedAt)
		ids = append(ids, snap.ID)
		row := make(map[string]loggingHistoryCrossQuota, len(snap.Quotas))
		for _, q := range snap.Quotas {
			row[q.Name] = loggingHistoryCrossQuota{
				Format:   "percent",
				ResetsAt: q.ResetsAt,
				Name:     q.Name,
				Value:    q.Utilization,
				Limit:    100,
				Percent:  q.Utilization,
				HasValue: true,
				HasLimit: true,
			}
		}
		series = append(series, row)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider":   "alibaba",
		"quotaNames": quotaNames,
		"logs":       loggingHistoryRowsFromSnapshots(capturedAt, ids, quotaNames, series),
	})
}
