package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	AntigravityQuotaGroupClaudeGPT   = "antigravity_claude_gpt"
	AntigravityQuotaGroupGeminiPro   = "antigravity_gemini_pro"
	AntigravityQuotaGroupGeminiFlash = "antigravity_gemini_flash"
)

var antigravityQuotaGroupOrder = []string{
	AntigravityQuotaGroupClaudeGPT,
	AntigravityQuotaGroupGeminiPro,
	AntigravityQuotaGroupGeminiFlash,
}

var antigravityQuotaGroupDisplayNames = map[string]string{
	AntigravityQuotaGroupClaudeGPT:   "Claude + GPT Quota",
	AntigravityQuotaGroupGeminiPro:   "Gemini Pro Quota",
	AntigravityQuotaGroupGeminiFlash: "Gemini Flash Quota",
}

var antigravityQuotaGroupColors = map[string]string{
	AntigravityQuotaGroupClaudeGPT:   "#D97757",
	AntigravityQuotaGroupGeminiPro:   "#10B981",
	AntigravityQuotaGroupGeminiFlash: "#3B82F6",
}

// AntigravityGroupedQuota represents one canonical logical quota group.
type AntigravityGroupedQuota struct {
	GroupKey          string
	DisplayName       string
	ModelIDs          []string
	Labels            []string
	RemainingFraction float64
	RemainingPercent  float64
	UsagePercent      float64
	IsExhausted       bool
	ResetTime         *time.Time
	TimeUntilReset    time.Duration
	Color             string
}

func AntigravityQuotaGroupOrder() []string {
	out := make([]string, len(antigravityQuotaGroupOrder))
	copy(out, antigravityQuotaGroupOrder)
	return out
}

func AntigravityQuotaGroupDisplayName(groupKey string) string {
	if name, ok := antigravityQuotaGroupDisplayNames[groupKey]; ok {
		return name
	}
	return groupKey
}

func AntigravityQuotaGroupColor(groupKey string) string {
	if color, ok := antigravityQuotaGroupColors[groupKey]; ok {
		return color
	}
	return "#6e40c9"
}

func AntigravityQuotaGroupForModel(modelID, label string) string {
	modelLower := strings.ToLower(strings.TrimSpace(modelID))
	labelLower := strings.ToLower(strings.TrimSpace(label))
	text := modelLower + " " + labelLower

	switch {
	case strings.Contains(text, "gemini") && strings.Contains(text, "flash"):
		return AntigravityQuotaGroupGeminiFlash
	case strings.Contains(text, "gemini"):
		return AntigravityQuotaGroupGeminiPro
	case strings.Contains(text, "claude"), strings.Contains(text, "gpt"):
		return AntigravityQuotaGroupClaudeGPT
	default:
		return AntigravityQuotaGroupClaudeGPT
	}
}

func GroupAntigravityModelsByLogicalQuota(models []AntigravityModelQuota) []AntigravityGroupedQuota {
	type accumulator struct {
		modelIDs      []string
		labels        []string
		remainingSum  float64
		remainingCnt  int
		anyExhausted  bool
		earliestReset *time.Time
	}

	accByGroup := map[string]*accumulator{}
	for _, key := range antigravityQuotaGroupOrder {
		accByGroup[key] = &accumulator{}
	}

	for _, m := range models {
		groupKey := AntigravityQuotaGroupForModel(m.ModelID, m.Label)
		acc := accByGroup[groupKey]
		if acc == nil {
			acc = &accumulator{}
			accByGroup[groupKey] = acc
		}

		acc.modelIDs = appendUniqueString(acc.modelIDs, m.ModelID)
		label := CleanAntigravityLabel(m.Label)
		if label == "" {
			label = AntigravityDisplayName(m.ModelID)
		}
		if label != "" {
			acc.labels = appendUniqueString(acc.labels, label)
		}

		acc.remainingSum += m.RemainingFraction
		acc.remainingCnt++
		acc.anyExhausted = acc.anyExhausted || m.IsExhausted || m.RemainingFraction <= 0

		if m.ResetTime != nil {
			if acc.earliestReset == nil || m.ResetTime.Before(*acc.earliestReset) {
				t := *m.ResetTime
				acc.earliestReset = &t
			}
		}
	}

	now := time.Now().UTC()
	groups := make([]AntigravityGroupedQuota, 0, len(antigravityQuotaGroupOrder))
	for _, key := range antigravityQuotaGroupOrder {
		acc := accByGroup[key]
		remaining := 1.0
		if acc != nil && acc.remainingCnt > 0 {
			remaining = acc.remainingSum / float64(acc.remainingCnt)
		}
		if remaining < 0 {
			remaining = 0
		}
		if remaining > 1 {
			remaining = 1
		}

		remainingPercent := remaining * 100
		usagePercent := 100 - remainingPercent
		if usagePercent < 0 {
			usagePercent = 0
		}
		if usagePercent > 100 {
			usagePercent = 100
		}

		group := AntigravityGroupedQuota{
			GroupKey:          key,
			DisplayName:       AntigravityQuotaGroupDisplayName(key),
			RemainingFraction: remaining,
			RemainingPercent:  remainingPercent,
			UsagePercent:      usagePercent,
			Color:             AntigravityQuotaGroupColor(key),
		}

		if acc != nil {
			group.ModelIDs = append(group.ModelIDs, acc.modelIDs...)
			group.Labels = append(group.Labels, acc.labels...)
			group.IsExhausted = acc.anyExhausted || (acc.remainingCnt > 0 && remaining <= 0)
			if acc.earliestReset != nil {
				group.ResetTime = acc.earliestReset
				d := acc.earliestReset.Sub(now)
				if d < 0 {
					d = 0
				}
				group.TimeUntilReset = d
			}
		}

		groups = append(groups, group)
	}

	return groups
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

// AntigravityModelOrAlias represents the model identifier structure.
type AntigravityModelOrAlias struct {
	Model string `json:"model"`
}

// AntigravityQuotaInfo represents quota information for a model.
type AntigravityQuotaInfo struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
}

// AntigravityClientModelConfig represents a single model's configuration.
type AntigravityClientModelConfig struct {
	Label          string                   `json:"label"`
	ModelOrAlias   *AntigravityModelOrAlias `json:"modelOrAlias,omitempty"`
	QuotaInfo      *AntigravityQuotaInfo    `json:"quotaInfo,omitempty"`
	SupportsImages bool                     `json:"supportsImages,omitempty"`
	IsRecommended  bool                     `json:"isRecommended,omitempty"`
}

// AntigravityPlanInfo contains subscription plan details.
type AntigravityPlanInfo struct {
	PlanName             string `json:"planName"`
	TeamsTier            string `json:"teamsTier"`
	MonthlyPromptCredits int    `json:"monthlyPromptCredits"`
}

// AntigravityPlanStatus contains plan status with available credits.
type AntigravityPlanStatus struct {
	PlanInfo               *AntigravityPlanInfo `json:"planInfo,omitempty"`
	AvailablePromptCredits float64              `json:"availablePromptCredits"`
}

// AntigravityCascadeModelConfigData contains model configuration data.
type AntigravityCascadeModelConfigData struct {
	ClientModelConfigs []AntigravityClientModelConfig `json:"clientModelConfigs"`
}

// AntigravityUserStatus represents the user status from the API.
type AntigravityUserStatus struct {
	Name                   string                             `json:"name"`
	Email                  string                             `json:"email"`
	PlanStatus             *AntigravityPlanStatus             `json:"planStatus,omitempty"`
	CascadeModelConfigData *AntigravityCascadeModelConfigData `json:"cascadeModelConfigData,omitempty"`
}

// AntigravityUserStatusResponse is the full API response.
type AntigravityUserStatusResponse struct {
	UserStatus *AntigravityUserStatus `json:"userStatus"`
	Message    string                 `json:"message,omitempty"`
	Code       string                 `json:"code,omitempty"`
}

// AntigravityModelQuota represents a single normalized model quota for storage.
type AntigravityModelQuota struct {
	ModelID           string
	Label             string
	RemainingFraction float64
	RemainingPercent  float64
	IsExhausted       bool
	ResetTime         *time.Time
	TimeUntilReset    time.Duration
}

// Antigravity data sources. All variants share one Google-account quota, so
// the provider exposes a single card whose data may come from either source.
const (
	AntigravitySourceIDE  = "ide"
	AntigravitySourceCLI  = "cli"
	AntigravitySourceBoth = "both"
)

// NormalizeAntigravitySource validates a source preference, defaulting to "both".
func NormalizeAntigravitySource(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case AntigravitySourceIDE:
		return AntigravitySourceIDE
	case AntigravitySourceCLI:
		return AntigravitySourceCLI
	case AntigravitySourceBoth:
		return AntigravitySourceBoth
	default:
		return AntigravitySourceBoth
	}
}

// AntigravitySnapshot represents a point-in-time capture of Antigravity quotas.
type AntigravitySnapshot struct {
	ID             int64
	CapturedAt     time.Time
	Email          string
	PlanName       string
	PromptCredits  float64
	MonthlyCredits int
	Models         []AntigravityModelQuota
	RawJSON        string
	// Source records which probe produced this snapshot: "ide" or "cli".
	Source string
}

// --- agy CLI: RetrieveUserQuotaSummary ------------------------------------
// The agy CLI's language server exposes a richer, bucket-based quota summary
// than the IDE probe: per logical group (Gemini, Claude+GPT) it reports a
// weekly and a 5-hour bucket, each with a remaining fraction and reset time.

// AntigravityQuotaBucket is a single weekly/5h limit within a quota group.
type AntigravityQuotaBucket struct {
	BucketID          string  `json:"bucketId"`
	DisplayName       string  `json:"displayName"`
	Window            string  `json:"window"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
}

// AntigravityQuotaGroup is a set of models sharing weekly + 5h buckets.
type AntigravityQuotaGroup struct {
	DisplayName string                   `json:"displayName"`
	Description string                   `json:"description"`
	Buckets     []AntigravityQuotaBucket `json:"buckets"`
}

// AntigravityQuotaSummaryResponse is the agy RetrieveUserQuotaSummary payload.
type AntigravityQuotaSummaryResponse struct {
	Response *struct {
		Groups      []AntigravityQuotaGroup `json:"groups"`
		Description string                  `json:"description"`
	} `json:"response"`
}

// agyBucketMeta provides stable display ordering and colors for known buckets.
// Unknown bucket IDs still render via parser fallbacks.
//
// Order prefers 5h buckets before weekly: the shorter window is usually hit
// first, so it leads each family on the dashboard card strip.
var agyBucketMeta = map[string]struct {
	short string
	color string
	order int
}{
	"gemini-5h":     {"Gemini 5h", "#34D399", 0},
	"gemini-weekly": {"Gemini Weekly", "#10B981", 1},
	"3p-5h":         {"Claude + GPT 5h", "#E8A38C", 2},
	"3p-weekly":     {"Claude + GPT Weekly", "#D97757", 3},
}

// AgyBucketColor returns a stable card color for a bucket ID.
func AgyBucketColor(bucketID string) string {
	if m, ok := agyBucketMeta[bucketID]; ok {
		return m.color
	}
	return "#6e40c9"
}

// AgyBucketOrder returns a stable sort index for a bucket ID (unknown last).
func AgyBucketOrder(bucketID string) int {
	if m, ok := agyBucketMeta[bucketID]; ok {
		return m.order
	}
	return 1000
}

// agyBucketLabel builds a human label like "Gemini Weekly Limit" from the
// group display name and the bucket's own display name.
func agyBucketLabel(groupDisplay, bucketID, bucketDisplay string) string {
	if m, ok := agyBucketMeta[bucketID]; ok {
		return m.short
	}
	short := strings.TrimSpace(strings.TrimSuffix(groupDisplay, " models"))
	short = strings.TrimSpace(strings.TrimSuffix(short, " Models"))
	if short == "" {
		return strings.TrimSpace(bucketDisplay)
	}
	return strings.TrimSpace(short + " " + bucketDisplay)
}

// ParseAgyQuotaSummary converts a RetrieveUserQuotaSummary payload into
// normalized model-quota rows (one row per bucket), reusing the existing
// storage/tracking pipeline which is generic over model_id + fraction + reset.
func ParseAgyQuotaSummary(data []byte) ([]AntigravityModelQuota, error) {
	var resp AntigravityQuotaSummaryResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Response == nil {
		return nil, fmt.Errorf("agy quota summary: missing response body")
	}

	now := time.Now()
	var rows []AntigravityModelQuota
	for _, g := range resp.Response.Groups {
		for _, b := range g.Buckets {
			frac := b.RemainingFraction
			if frac < 0 {
				frac = 0
			}
			if frac > 1 {
				frac = 1
			}
			row := AntigravityModelQuota{
				ModelID:           b.BucketID,
				Label:             agyBucketLabel(g.DisplayName, b.BucketID, b.DisplayName),
				RemainingFraction: frac,
				RemainingPercent:  frac * 100,
				IsExhausted:       frac <= 0,
			}
			if b.ResetTime != "" {
				if t, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
					row.ResetTime = &t
					if d := t.Sub(now); d > 0 {
						row.TimeUntilReset = d
					}
				}
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("agy quota summary: no buckets found")
	}
	return rows, nil
}

// AntigravityQuotaPool represents a group of models sharing the same quota.
// Models with identical remainingFraction and resetTime share a quota pool.
type AntigravityQuotaPool struct {
	PoolID            string   // Unique signature based on fraction+resetTime
	Name              string   // Combined name of models in this pool
	Models            []string // Model labels in this pool
	RemainingFraction float64
	RemainingPercent  float64
	IsExhausted       bool
	ResetTime         *time.Time
	TimeUntilReset    time.Duration
}

// GroupModelsByQuota groups models that share the same quota pool.
// Models with identical remainingFraction and resetTime are considered shared.
func GroupModelsByQuota(models []AntigravityModelQuota) []AntigravityQuotaPool {
	if len(models) == 0 {
		return nil
	}

	// Group by signature (fraction_resetTime)
	type poolData struct {
		models []AntigravityModelQuota
	}
	poolMap := make(map[string]*poolData)
	poolOrder := []string{} // Preserve order

	for _, m := range models {
		// Build signature from fraction (6 decimals) and reset time
		var resetStr string
		if m.ResetTime != nil {
			resetStr = m.ResetTime.Format(time.RFC3339)
		}
		sig := formatPoolSignature(m.RemainingFraction, resetStr)

		if poolMap[sig] == nil {
			poolMap[sig] = &poolData{}
			poolOrder = append(poolOrder, sig)
		}
		poolMap[sig].models = append(poolMap[sig].models, m)
	}

	// Build quota pools
	var pools []AntigravityQuotaPool
	for _, sig := range poolOrder {
		pd := poolMap[sig]
		if len(pd.models) == 0 {
			continue
		}

		// Use first model as reference for quota values
		ref := pd.models[0]

		// Build pool name from model labels
		var names []string
		for _, m := range pd.models {
			label := CleanAntigravityLabel(m.Label)
			if label == "" {
				label = AntigravityDisplayName(m.ModelID)
			}
			// Extract just the model family name (e.g., "Claude Sonnet" from "Claude Sonnet 4.6")
			names = append(names, label)
		}
		poolName := buildPoolName(names)

		pools = append(pools, AntigravityQuotaPool{
			PoolID:            sig,
			Name:              poolName,
			Models:            names,
			RemainingFraction: ref.RemainingFraction,
			RemainingPercent:  ref.RemainingPercent,
			IsExhausted:       ref.IsExhausted,
			ResetTime:         ref.ResetTime,
			TimeUntilReset:    ref.TimeUntilReset,
		})
	}

	return pools
}

// formatPoolSignature creates a unique signature for quota pool grouping.
func formatPoolSignature(fraction float64, resetTime string) string {
	// Use 6 decimal precision for fraction to catch small differences
	// Format: "0.600000_2026-02-23T20:00:00Z"
	return fmt.Sprintf("%.6f_%s", fraction, resetTime)
}

// buildPoolName creates a concise name for a quota pool from model names.
func buildPoolName(names []string) string {
	if len(names) == 0 {
		return "Unknown"
	}
	if len(names) == 1 {
		return names[0]
	}

	// Try to find common prefix (e.g., "Gemini 3.1 Pro" from "Gemini 3.1 Pro (High)" and "Gemini 3.1 Pro (Low)")
	// First, check if they're similar models that should be combined
	combined := combineModelNames(names)
	if combined != "" {
		return combined
	}

	// Otherwise, join with "/"
	if len(names) <= 3 {
		return strings.Join(names, " / ")
	}
	return names[0] + " + " + string(rune('0'+len(names)-1)) + " more"
}

// combineModelNames tries to combine similar model names into a shorter form.
func combineModelNames(names []string) string {
	if len(names) < 2 {
		return ""
	}

	// Check for common patterns like "Model (High)" and "Model (Low)"
	// or "Model 4.5" and "Model 4.6"
	baseCounts := make(map[string]int)
	for _, name := range names {
		base := extractModelBase(name)
		baseCounts[base]++
	}

	// If all names share the same base, use that base
	if len(baseCounts) == 1 {
		for base := range baseCounts {
			return base
		}
	}

	return ""
}

// extractModelBase extracts the base model name without version/tier suffixes.
func extractModelBase(name string) string {
	// Remove common suffixes like "(High)", "(Low)", "(Medium)", "(Thinking)"
	suffixes := []string{
		" (High)", " (Low)", " (Medium)",
		" (Thinking)", " (Fast)", " (Slow)",
		" 4.6", " 4.5", " 4.0", " 3.5", " 3.0",
	}
	result := name
	for _, suffix := range suffixes {
		result = strings.TrimSuffix(result, suffix)
	}
	return strings.TrimSpace(result)
}

// antigravityDisplayNames maps model IDs to human-readable labels.
var antigravityDisplayNames = map[string]string{
	"claude-4-5-sonnet":          "Claude 4.5 Sonnet",
	"claude-4-5-sonnet-thinking": "Claude 4.5 Sonnet",
	"gemini-3-pro":               "Gemini 3 Pro",
	"gemini-3-flash":             "Gemini 3 Flash",
}

// CleanAntigravityLabel removes unnecessary suffixes like "(Thinking)" from model labels.
// Everyone knows Sonnet/Opus models support thinking, so we simplify the display.
func CleanAntigravityLabel(label string) string {
	// Remove "(Thinking)" suffix - it's redundant for Claude models
	label = strings.TrimSuffix(label, " (Thinking)")
	// Also handle other common patterns
	label = strings.TrimSuffix(label, "(Thinking)")
	return strings.TrimSpace(label)
}

// AntigravityDisplayName returns the human-readable name for a model ID.
func AntigravityDisplayName(modelID string) string {
	if name, ok := antigravityDisplayNames[modelID]; ok {
		return name
	}
	return modelID
}

// ActiveModelIDs returns sorted model IDs present in the response.
func (r AntigravityUserStatusResponse) ActiveModelIDs() []string {
	if r.UserStatus == nil || r.UserStatus.CascadeModelConfigData == nil {
		return nil
	}

	var ids []string
	for _, cfg := range r.UserStatus.CascadeModelConfigData.ClientModelConfigs {
		if cfg.QuotaInfo != nil && cfg.ModelOrAlias != nil {
			ids = append(ids, cfg.ModelOrAlias.Model)
		}
	}
	sort.Strings(ids)
	return ids
}

// ToSnapshot converts an AntigravityUserStatusResponse to an AntigravitySnapshot.
func (r AntigravityUserStatusResponse) ToSnapshot(capturedAt time.Time) *AntigravitySnapshot {
	snapshot := &AntigravitySnapshot{
		CapturedAt: capturedAt,
	}

	if r.UserStatus == nil {
		return snapshot
	}

	snapshot.Email = r.UserStatus.Email

	if r.UserStatus.PlanStatus != nil {
		snapshot.PromptCredits = r.UserStatus.PlanStatus.AvailablePromptCredits
		if r.UserStatus.PlanStatus.PlanInfo != nil {
			snapshot.PlanName = r.UserStatus.PlanStatus.PlanInfo.PlanName
			snapshot.MonthlyCredits = r.UserStatus.PlanStatus.PlanInfo.MonthlyPromptCredits
		}
	}

	if r.UserStatus.CascadeModelConfigData != nil {
		now := time.Now()
		for _, cfg := range r.UserStatus.CascadeModelConfigData.ClientModelConfigs {
			if cfg.QuotaInfo == nil {
				continue
			}

			modelID := ""
			if cfg.ModelOrAlias != nil {
				modelID = cfg.ModelOrAlias.Model
			}

			quota := AntigravityModelQuota{
				ModelID:           modelID,
				Label:             cfg.Label,
				RemainingFraction: cfg.QuotaInfo.RemainingFraction,
				RemainingPercent:  cfg.QuotaInfo.RemainingFraction * 100,
				IsExhausted:       cfg.QuotaInfo.RemainingFraction == 0,
			}

			if cfg.QuotaInfo.ResetTime != "" {
				if t, err := time.Parse(time.RFC3339, cfg.QuotaInfo.ResetTime); err == nil {
					quota.ResetTime = &t
					quota.TimeUntilReset = t.Sub(now)
					if quota.TimeUntilReset < 0 {
						quota.TimeUntilReset = 0
					}
				}
			}

			snapshot.Models = append(snapshot.Models, quota)
		}
	}

	// Store raw JSON for debugging/auditing
	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// ParseAntigravityResponse parses raw JSON bytes into an AntigravityUserStatusResponse.
func ParseAntigravityResponse(data []byte) (*AntigravityUserStatusResponse, error) {
	var resp AntigravityUserStatusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
