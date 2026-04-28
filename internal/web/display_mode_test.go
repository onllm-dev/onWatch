package web

import (
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// ═══════════════════════════════════════════════════════════════════
// ── applyDisplayModeToQuotaMap unit tests
// ═══════════════════════════════════════════════════════════════════

func TestApplyDisplayModeToQuotaMap_UsageMode_NoChange(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{
		"usagePercent": 45.0,
		"status":       "warning",
	}
	applyDisplayModeToQuotaMap(q, "usage")
	if _, ok := q["cardPercent"]; ok {
		t.Errorf("usage mode must not set cardPercent")
	}
	if _, ok := q["cardLabel"]; ok {
		t.Errorf("usage mode must not set cardLabel")
	}
}

func TestApplyDisplayModeToQuotaMap_AvailableMode_FlipsUsagePercent(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{"usagePercent": 30.0}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 70.0 {
		t.Errorf("cardPercent = %v, want 70", got)
	}
	if got := q["cardLabel"].(string); got != "Remaining" {
		t.Errorf("cardLabel = %q, want Remaining", got)
	}
	if got := q["remainingPercent"].(float64); got != 70.0 {
		t.Errorf("remainingPercent = %v, want 70", got)
	}
}

func TestApplyDisplayModeToQuotaMap_ReadsPercentField(t *testing.T) {
	t.Parallel()
	// Synthetic providers store their value under "percent"
	q := map[string]interface{}{"percent": 25.0}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 75.0 {
		t.Errorf("cardPercent = %v, want 75", got)
	}
}

func TestApplyDisplayModeToQuotaMap_ReadsUtilizationField(t *testing.T) {
	t.Parallel()
	// Anthropic / Cursor / Codex use "utilization"
	q := map[string]interface{}{"utilization": 80.0}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 20.0 {
		t.Errorf("cardPercent = %v, want 20", got)
	}
}

func TestApplyDisplayModeToQuotaMap_ClampsOver100(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{"usagePercent": 120.0}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 0.0 {
		t.Errorf("cardPercent should clamp to 0 when usage > 100, got %v", got)
	}
}

func TestApplyDisplayModeToQuotaMap_ClampsBelow0(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{"usagePercent": -5.0}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 100.0 {
		t.Errorf("cardPercent should clamp to 100 when usage < 0, got %v", got)
	}
}

func TestApplyDisplayModeToQuotaMap_PreservesExistingCardPercent(t *testing.T) {
	t.Parallel()
	// Codex sets cardPercent for code_review unconditionally; the helper must
	// not overwrite that value.
	q := map[string]interface{}{
		"utilization": 30.0,
		"cardPercent": 70.0,
		"cardLabel":   "Remaining",
	}
	applyDisplayModeToQuotaMap(q, "available")
	if got := q["cardPercent"].(float64); got != 70.0 {
		t.Errorf("must not overwrite existing cardPercent, got %v", got)
	}
}

func TestApplyDisplayModeToQuotaMap_NilMapNoCrash(t *testing.T) {
	t.Parallel()
	applyDisplayModeToQuotaMap(nil, "available") // must not panic
}

func TestApplyDisplayModeToQuotaMap_NoUsageFieldNoChange(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{"name": "no_data"}
	applyDisplayModeToQuotaMap(q, "available")
	if _, ok := q["cardPercent"]; ok {
		t.Errorf("must not set cardPercent when no usage value present")
	}
}

func TestApplyDisplayModeToQuotaMap_AcceptsIntUsage(t *testing.T) {
	t.Parallel()
	q := map[string]interface{}{"usagePercent": 50}
	applyDisplayModeToQuotaMap(q, "available")
	if got, ok := q["cardPercent"].(float64); !ok || got != 50.0 {
		t.Errorf("int usage should produce float cardPercent=50, got %v ok=%v", q["cardPercent"], ok)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── applyDisplayModeToResponse walker tests
// ═══════════════════════════════════════════════════════════════════

func TestApplyDisplayModeToResponse_QuotasArrayOfMaps(t *testing.T) {
	t.Parallel()
	resp := map[string]interface{}{
		"quotas": []map[string]interface{}{
			{"usagePercent": 25.0},
			{"usagePercent": 75.0},
		},
	}
	applyDisplayModeToResponse(resp, "available")
	q := resp["quotas"].([]map[string]interface{})
	if got := q[0]["cardPercent"].(float64); got != 75.0 {
		t.Errorf("q[0].cardPercent = %v, want 75", got)
	}
	if got := q[1]["cardPercent"].(float64); got != 25.0 {
		t.Errorf("q[1].cardPercent = %v, want 25", got)
	}
}

func TestApplyDisplayModeToResponse_QuotasArrayOfInterfaces(t *testing.T) {
	t.Parallel()
	resp := map[string]interface{}{
		"quotas": []interface{}{
			map[string]interface{}{"utilization": 40.0},
		},
	}
	applyDisplayModeToResponse(resp, "available")
	q := resp["quotas"].([]interface{})
	if got := q[0].(map[string]interface{})["cardPercent"].(float64); got != 60.0 {
		t.Errorf("cardPercent = %v, want 60", got)
	}
}

func TestApplyDisplayModeToResponse_TopLevelKeys(t *testing.T) {
	t.Parallel()
	// Synthetic + Z.ai shape: subscription/search/toolCalls/tokensLimit at top level
	resp := map[string]interface{}{
		"subscription": map[string]interface{}{"percent": 10.0},
		"search":       map[string]interface{}{"percent": 90.0},
		"tokensLimit":  map[string]interface{}{"usagePercent": 50.0},
	}
	applyDisplayModeToResponse(resp, "available")
	if got := resp["subscription"].(map[string]interface{})["cardPercent"].(float64); got != 90.0 {
		t.Errorf("subscription.cardPercent = %v, want 90", got)
	}
	if got := resp["search"].(map[string]interface{})["cardPercent"].(float64); got != 10.0 {
		t.Errorf("search.cardPercent = %v, want 10", got)
	}
	if got := resp["tokensLimit"].(map[string]interface{})["cardPercent"].(float64); got != 50.0 {
		t.Errorf("tokensLimit.cardPercent = %v, want 50", got)
	}
}

func TestApplyDisplayModeToResponse_UsageModeIsNoOp(t *testing.T) {
	t.Parallel()
	resp := map[string]interface{}{
		"quotas": []map[string]interface{}{{"usagePercent": 25.0}},
	}
	applyDisplayModeToResponse(resp, "usage")
	q := resp["quotas"].([]map[string]interface{})
	if _, ok := q[0]["cardPercent"]; ok {
		t.Errorf("usage mode must not set cardPercent")
	}
}

func TestApplyDisplayModeToResponse_NilNoCrash(t *testing.T) {
	t.Parallel()
	applyDisplayModeToResponse(nil, "available")
}

// ═══════════════════════════════════════════════════════════════════
// ── getDisplayMode precedence tests
// ═══════════════════════════════════════════════════════════════════

func TestHandler_GetDisplayMode_DefaultUsage(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	h := NewHandler(s, nil, nil, nil, &config.Config{})
	if got := h.getDisplayMode("anthropic"); got != "usage" {
		t.Errorf("default = %q, want usage", got)
	}
}

func TestHandler_GetDisplayMode_GlobalEnvAffectsAllProviders(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	cfg := &config.Config{DisplayMode: "available"}
	h := NewHandler(s, nil, nil, nil, cfg)

	for _, prov := range []string{"anthropic", "synthetic", "zai", "copilot", "minimax", "antigravity", "gemini", "cursor"} {
		if got := h.getDisplayMode(prov); got != "available" {
			t.Errorf("global env should apply to %q, got %q", prov, got)
		}
	}
}

func TestHandler_GetDisplayMode_GlobalDBAffectsAllProviders(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	s.SetSetting("provider_settings", `{"global":{"display_mode":"available"}}`)
	h := NewHandler(s, nil, nil, nil, &config.Config{})

	for _, prov := range []string{"anthropic", "synthetic", "zai", "copilot", "codex", "minimax", "antigravity", "gemini", "cursor"} {
		if got := h.getDisplayMode(prov); got != "available" {
			t.Errorf("global DB should apply to %q, got %q", prov, got)
		}
	}
}

func TestHandler_GetDisplayMode_PerProviderOverridesGlobal(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	// Global says "available" but Anthropic forces "usage"
	s.SetSetting("provider_settings",
		`{"global":{"display_mode":"available"},"anthropic":{"display_mode":"usage"}}`)
	h := NewHandler(s, nil, nil, nil, &config.Config{})

	if got := h.getDisplayMode("anthropic"); got != "usage" {
		t.Errorf("per-provider override should win, got %q", got)
	}
	// Other providers should still see global "available"
	if got := h.getDisplayMode("copilot"); got != "available" {
		t.Errorf("non-overridden provider should follow global, got %q", got)
	}
}

func TestHandler_GetDisplayMode_DBOverridesEnv(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	s.SetSetting("provider_settings", `{"global":{"display_mode":"usage"}}`)
	h := NewHandler(s, nil, nil, nil, &config.Config{DisplayMode: "available"})

	if got := h.getDisplayMode("anthropic"); got != "usage" {
		t.Errorf("DB should override env, got %q", got)
	}
}

func TestHandler_GetDisplayMode_CodexEnvVarStillRespected(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	// Backward compat: legacy CODEX_SHOW_AVAILABLE env var still flips Codex.
	cfg := &config.Config{CodexShowAvailable: "available"}
	h := NewHandler(s, nil, nil, nil, cfg)

	if got := h.getDisplayMode("codex"); got != "available" {
		t.Errorf("CODEX_SHOW_AVAILABLE env var should still flip codex, got %q", got)
	}
	// Non-codex providers must NOT inherit the legacy Codex-specific env var.
	if got := h.getDisplayMode("anthropic"); got != "usage" {
		t.Errorf("CODEX_SHOW_AVAILABLE must not leak to anthropic, got %q", got)
	}
}

func TestHandler_GetDisplayMode_InvalidValuesIgnored(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()
	s.SetSetting("provider_settings", `{"global":{"display_mode":"bogus"}}`)
	h := NewHandler(s, nil, nil, nil, &config.Config{})

	// Bogus values must fall through to the next layer (default "usage").
	if got := h.getDisplayMode("anthropic"); got != "usage" {
		t.Errorf("invalid db value should fall through to default, got %q", got)
	}
}

// ═══════════════════════════════════════════════════════════════════
// ── sanitizeProviderSettings: global enum field
// ═══════════════════════════════════════════════════════════════════

func TestSanitizeProviderSettings_GlobalDisplayMode_Valid(t *testing.T) {
	t.Parallel()
	settings := map[string]interface{}{
		"global": map[string]interface{}{"display_mode": "available"},
	}
	sanitizeProviderSettings(settings)
	got := settings["global"].(map[string]interface{})["display_mode"]
	if got != "available" {
		t.Errorf("valid value should pass through, got %v", got)
	}
}

func TestSanitizeProviderSettings_GlobalDisplayMode_InvalidResetsToFirst(t *testing.T) {
	t.Parallel()
	settings := map[string]interface{}{
		"global": map[string]interface{}{"display_mode": "garbage"},
	}
	sanitizeProviderSettings(settings)
	got := settings["global"].(map[string]interface{})["display_mode"]
	if got != "usage" {
		t.Errorf("invalid value should reset to usage, got %v", got)
	}
}
