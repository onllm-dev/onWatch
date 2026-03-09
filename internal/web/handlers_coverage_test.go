package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func testMiniMaxNonSharedSnapshot(capturedAt time.Time, usedA, usedB int) *api.MiniMaxSnapshot {
	totalA := 1500
	totalB := 900
	remainA := totalA - usedA
	remainB := totalB - usedB
	resetAt := capturedAt.Add(3 * time.Hour)
	windowStart := capturedAt.Add(-1 * time.Hour)
	windowEnd := resetAt

	return &api.MiniMaxSnapshot{
		CapturedAt: capturedAt,
		Models: []api.MiniMaxModelQuota{
			{
				ModelName:      "MiniMax-M2",
				Total:          totalA,
				Used:           usedA,
				Remain:         remainA,
				UsedPercent:    float64(usedA) / float64(totalA) * 100,
				ResetAt:        &resetAt,
				WindowStart:    &windowStart,
				WindowEnd:      &windowEnd,
				TimeUntilReset: 3 * time.Hour,
			},
			{
				ModelName:      "MiniMax-M2.1",
				Total:          totalB,
				Used:           usedB,
				Remain:         remainB,
				UsedPercent:    float64(usedB) / float64(totalB) * 100,
				ResetAt:        &resetAt,
				WindowStart:    &windowStart,
				WindowEnd:      &windowEnd,
				TimeUntilReset: 3 * time.Hour,
			},
		},
	}
}

func testMiniMaxSingleModelSnapshot(capturedAt time.Time, used int) *api.MiniMaxSnapshot {
	total := 1200
	remain := total - used
	resetAt := capturedAt.Add(2 * time.Hour)
	windowStart := capturedAt.Add(-1 * time.Hour)
	windowEnd := resetAt
	return &api.MiniMaxSnapshot{
		CapturedAt: capturedAt,
		Models: []api.MiniMaxModelQuota{
			{
				ModelName:      "MiniMax-M2",
				Total:          total,
				Used:           used,
				Remain:         remain,
				UsedPercent:    float64(used) / float64(total) * 100,
				ResetAt:        &resetAt,
				WindowStart:    &windowStart,
				WindowEnd:      &windowEnd,
				TimeUntilReset: 2 * time.Hour,
			},
		},
	}
}

func testAntigravitySnapshot(capturedAt time.Time, remain float64, resetAt time.Time) *api.AntigravitySnapshot {
	return &api.AntigravitySnapshot{
		CapturedAt: capturedAt,
		Models: []api.AntigravityModelQuota{
			{
				ModelID:           "claude-4-5-sonnet",
				Label:             "Claude Sonnet",
				RemainingFraction: remain,
				RemainingPercent:  remain * 100,
				IsExhausted:       remain <= 0,
				ResetTime:         &resetAt,
				TimeUntilReset:    resetAt.Sub(capturedAt),
			},
			{
				ModelID:           "gpt-4o",
				Label:             "GPT-4o",
				RemainingFraction: remain,
				RemainingPercent:  remain * 100,
				IsExhausted:       remain <= 0,
				ResetTime:         &resetAt,
				TimeUntilReset:    resetAt.Sub(capturedAt),
			},
		},
	}
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func TestProviderPollingValueCoverage(t *testing.T) {
	tests := []struct {
		name       string
		entry      interface{}
		wantValue  bool
		wantExists bool
	}{
		{
			name:       "map interface polling true",
			entry:      map[string]interface{}{"polling": true},
			wantValue:  true,
			wantExists: true,
		},
		{
			name:       "map interface polling false",
			entry:      map[string]interface{}{"polling": false},
			wantValue:  false,
			wantExists: true,
		},
		{
			name:       "map interface missing key",
			entry:      map[string]interface{}{"dashboard": false},
			wantValue:  true,
			wantExists: false,
		},
		{
			name:       "map interface bad type",
			entry:      map[string]interface{}{"polling": "yes"},
			wantValue:  false,
			wantExists: false,
		},
		{
			name:       "map bool polling false",
			entry:      map[string]bool{"polling": false},
			wantValue:  false,
			wantExists: true,
		},
		{
			name:       "map bool missing key",
			entry:      map[string]bool{"dashboard": true},
			wantValue:  false,
			wantExists: false,
		},
		{
			name:       "unsupported type",
			entry:      "invalid",
			wantValue:  true,
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotExists := providerPollingValue(tt.entry)
			if gotValue != tt.wantValue || gotExists != tt.wantExists {
				t.Fatalf("providerPollingValue() = (%v, %v), want (%v, %v)", gotValue, gotExists, tt.wantValue, tt.wantExists)
			}
		})
	}
}

func TestMinimaxSessionTimeChangedCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	almostSame := now.Add(500 * time.Millisecond)
	changed := now.Add(2 * time.Second)

	tests := []struct {
		name string
		a    *time.Time
		b    *time.Time
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: false},
		{name: "only a nil", a: nil, b: &now, want: true},
		{name: "only b nil", a: &now, b: nil, want: true},
		{name: "within one second", a: &now, b: &almostSame, want: false},
		{name: "greater than one second", a: &now, b: &changed, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := minimaxSessionTimeChanged(tt.a, tt.b); got != tt.want {
				t.Fatalf("minimaxSessionTimeChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerCodexProfilesCoverage(t *testing.T) {
	t.Run("nil store returns empty profiles", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
		rr := httptest.NewRecorder()
		h.CodexProfiles(rr, httptest.NewRequest(http.MethodGet, "/api/codex/profiles", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), `"profiles":[]`) {
			t.Fatalf("unexpected body: %s", rr.Body.String())
		}
	})

	t.Run("store query error", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		h := NewHandler(s, nil, nil, nil, createTestConfigWithSynthetic())
		rr := httptest.NewRecorder()
		h.CodexProfiles(rr, httptest.NewRequest(http.MethodGet, "/api/codex/profiles", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})
}

func TestHandlerSettingsPageTemplateError(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	// Missing layout.html template forces execution failure branch.
	h.settingsTmpl = template.New("broken")

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	h.SettingsPage(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

func TestHandlerTryAutoDetectAdditionalCoverage(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		if h.tryAutoDetect("codex") {
			t.Fatal("expected auto-detect to fail with nil config")
		}
	})

	t.Run("anthropic and codex miss", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CODEX_HOME", filepath.Join(home, "codex-home"))

		h := NewHandler(nil, nil, nil, nil, &config.Config{})
		if h.tryAutoDetect("anthropic") {
			t.Fatal("expected anthropic auto-detect miss")
		}
		if h.tryAutoDetect("codex") {
			t.Fatal("expected codex auto-detect miss")
		}
	})

	t.Run("anthropic success from credentials file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		credsDir := filepath.Join(home, ".claude")
		if err := os.MkdirAll(credsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credsDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"anth-auto-token"}}`), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg := &config.Config{}
		h := NewHandler(nil, nil, nil, nil, cfg)
		if !h.tryAutoDetect("anthropic") {
			t.Fatal("expected anthropic auto-detect success")
		}
		if cfg.AnthropicToken != "anth-auto-token" || !cfg.AnthropicAutoToken {
			t.Fatalf("unexpected anthropic config: %+v", cfg)
		}
	})

	t.Run("antigravity case path", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, &config.Config{})
		_ = h.tryAutoDetect("antigravity")
	})
}

func TestHandlerReloadProvidersCoverage(t *testing.T) {
	t.Run("rejects non-post", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, &config.Config{})
		rr := httptest.NewRecorder()
		h.ReloadProviders(rr, httptest.NewRequest(http.MethodGet, "/api/providers/reload", nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rr.Code)
		}
	})

	t.Run("fails without config", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.ReloadProviders(rr, httptest.NewRequest(http.MethodPost, "/api/providers/reload", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("config load failure", func(t *testing.T) {
		t.Setenv("SYNTHETIC_API_KEY", "bad-key")
		t.Setenv("ONWATCH_PORT", "9211")
		t.Setenv("ONWATCH_POLL_INTERVAL", "120")

		h := NewHandler(nil, nil, nil, nil, &config.Config{})
		rr := httptest.NewRecorder()
		h.ReloadProviders(rr, httptest.NewRequest(http.MethodPost, "/api/providers/reload", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("reloads and reconciles polling", func(t *testing.T) {
		oldWD, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		tmpWD := t.TempDir()
		if err := os.Chdir(tmpWD); err != nil {
			t.Fatalf("Chdir(tmp): %v", err)
		}
		defer func() {
			_ = os.Chdir(oldWD)
		}()

		home := t.TempDir()
		codexHome := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CODEX_HOME", codexHome)
		t.Setenv("SYNTHETIC_API_KEY", "syn_reload_key")
		t.Setenv("ZAI_API_KEY", "")
		t.Setenv("ANTHROPIC_TOKEN", "")
		t.Setenv("COPILOT_TOKEN", "")
		t.Setenv("CODEX_TOKEN", "")
		t.Setenv("MINIMAX_API_KEY", "")
		t.Setenv("ONWATCH_PORT", "9211")
		t.Setenv("ONWATCH_POLL_INTERVAL", "120")

		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		if err := s.SetSetting("provider_visibility", `{"synthetic":{"polling":true,"dashboard":true},"zai":{"polling":false,"dashboard":true}}`); err != nil {
			t.Fatalf("SetSetting(provider_visibility): %v", err)
		}

		controller := &mockProviderAgentController{running: map[string]bool{}}
		h := NewHandler(s, nil, nil, nil, &config.Config{})
		h.SetAgentManager(controller)

		rr := httptest.NewRecorder()
		h.ReloadProviders(rr, httptest.NewRequest(http.MethodPost, "/api/providers/reload", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
		}
		if !containsString(controller.started, "synthetic") {
			t.Fatalf("expected synthetic to start, started=%v", controller.started)
		}
		if !containsString(controller.stopped, "zai") {
			t.Fatalf("expected zai to be stopped, stopped=%v", controller.stopped)
		}
	})
}

func TestHandlerBuildAntigravitySummaryMapCoverage(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, createTestConfigWithAntigravity())
		got := h.buildAntigravitySummaryMap()
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %v", got)
		}
	})

	t.Run("snapshot without tracker", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		now := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
		reset := now.Add(3 * time.Hour)
		if _, err := s.InsertAntigravitySnapshot(testAntigravitySnapshot(now, 0.55, reset)); err != nil {
			t.Fatalf("InsertAntigravitySnapshot: %v", err)
		}

		h := NewHandler(s, nil, nil, nil, createTestConfigWithAntigravity())
		summary := h.buildAntigravitySummaryMap()
		if len(summary) == 0 {
			t.Fatal("expected non-empty antigravity summary")
		}

		raw, ok := summary[api.AntigravityQuotaGroupClaudeGPT]
		if !ok {
			t.Fatalf("missing %s group in summary: %v", api.AntigravityQuotaGroupClaudeGPT, summary)
		}
		item, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected item type: %T", raw)
		}
		if item["displayName"] == "" || item["resetTime"] == nil {
			t.Fatalf("expected displayName/resetTime in summary item: %+v", item)
		}
	})

	t.Run("tracker enriched summary", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		tr := tracker.NewAntigravityTracker(s, nil)
		base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Second)
		resetOne := base.Add(2 * time.Hour)
		resetTwo := base.Add(6 * time.Hour)

		snaps := []*api.AntigravitySnapshot{
			testAntigravitySnapshot(base, 0.70, resetOne),
			testAntigravitySnapshot(base.Add(40*time.Minute), 0.50, resetOne),
			testAntigravitySnapshot(base.Add(3*time.Hour), 0.90, resetTwo),
		}
		for i, snap := range snaps {
			if _, err := s.InsertAntigravitySnapshot(snap); err != nil {
				t.Fatalf("InsertAntigravitySnapshot(%d): %v", i, err)
			}
			if err := tr.Process(snap); err != nil {
				t.Fatalf("Process(%d): %v", i, err)
			}
		}

		h := NewHandler(s, nil, nil, nil, createTestConfigWithAntigravity())
		h.SetAntigravityTracker(tr)
		summary := h.buildAntigravitySummaryMap()

		raw, ok := summary[api.AntigravityQuotaGroupClaudeGPT]
		if !ok {
			t.Fatalf("missing %s group in summary: %v", api.AntigravityQuotaGroupClaudeGPT, summary)
		}
		item := raw.(map[string]interface{})
		completed, ok := item["completedCycles"].(int)
		if !ok {
			t.Fatalf("unexpected completedCycles type: %T", item["completedCycles"])
		}
		if completed < 1 {
			t.Fatalf("expected completedCycles >= 1, got %d", completed)
		}
		if item["trackingSince"] == nil {
			t.Fatalf("expected trackingSince in tracker-enriched summary: %+v", item)
		}
	})
}

func TestHandlerCyclesMiniMaxCoverage(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.cyclesMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/cycles?provider=minimax", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if strings.TrimSpace(rr.Body.String()) != "[]" {
			t.Fatalf("expected empty list, got %s", rr.Body.String())
		}
	})

	t.Run("query latest error", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		h := NewHandler(s, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.cyclesMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/cycles?provider=minimax", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("shared quota path", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		for i, used := range []int{10, 30, 55} {
			snap := sharedMiniMaxSnapshot(base.Add(time.Duration(i)*20*time.Minute), used)
			if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
				t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
			}
		}

		h := NewHandler(s, nil, nil, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=minimax&range=24h", nil)
		rr := httptest.NewRecorder()
		h.cyclesMiniMax(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
		}

		var rows []map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(rows) == 0 {
			t.Fatalf("expected shared cycles rows, body=%s", rr.Body.String())
		}
		if rows[0]["modelName"] != minimaxSharedQuotaDisplayName {
			t.Fatalf("unexpected modelName: %v", rows[0]["modelName"])
		}
	})

	t.Run("non-shared path with default model lookup", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		for i, used := range []int{40, 60, 120} {
			snap := testMiniMaxSingleModelSnapshot(base.Add(time.Duration(i)*15*time.Minute), used)
			if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
				t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
			}
		}

		h := NewHandler(s, nil, nil, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/cycles?provider=minimax&range=24h", nil)
		rr := httptest.NewRecorder()
		h.cyclesMiniMax(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
		}

		var rows []map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(rows) == 0 {
			t.Fatalf("expected non-shared cycles rows, body=%s", rr.Body.String())
		}
		if rows[0]["modelName"] != "MiniMax-M2" {
			t.Fatalf("unexpected modelName: %v", rows[0]["modelName"])
		}
	})
}

func TestHandlerLoggingHistoryMiniMaxCoverage(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.loggingHistoryMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=minimax", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("query error", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.loggingHistoryMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=minimax", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})

	t.Run("non-shared logs", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		for i := 0; i < 3; i++ {
			snap := testMiniMaxNonSharedSnapshot(base.Add(time.Duration(i)*20*time.Minute), 120+i*20, 80+i*15)
			if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
				t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
			}
		}

		h := NewHandler(s, nil, nil, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=minimax&range=1&limit=100", nil)
		rr := httptest.NewRecorder()
		h.loggingHistoryMiniMax(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
		}

		var resp struct {
			QuotaNames []string `json:"quotaNames"`
			Logs       []struct {
				CrossQuotas []struct {
					Name    string  `json:"name"`
					Value   float64 `json:"value"`
					Limit   float64 `json:"limit"`
					Percent float64 `json:"percent"`
				} `json:"crossQuotas"`
			} `json:"logs"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(resp.QuotaNames) == 0 || len(resp.Logs) == 0 {
			t.Fatalf("expected quota names and logs, got %+v", resp)
		}
	})
}

func TestBuildMiniMaxCycleOverviewRowsAdditionalCoverage(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		rows, names, groupBy, err := h.buildMiniMaxCycleOverviewRows("", 10)
		if err != nil {
			t.Fatalf("buildMiniMaxCycleOverviewRows: %v", err)
		}
		if rows != nil || names != nil || groupBy != "" {
			t.Fatalf("unexpected result: rows=%v names=%v groupBy=%q", rows, names, groupBy)
		}
	})

	t.Run("shared latest with empty representative model", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		now := time.Now().UTC().Truncate(time.Second)
		reset := now.Add(2 * time.Hour)
		windowStart := now.Add(-time.Hour)
		windowEnd := reset
		snap := &api.MiniMaxSnapshot{
			CapturedAt: now,
			Models: []api.MiniMaxModelQuota{
				{
					ModelName:      "",
					Total:          1200,
					Used:           100,
					Remain:         1100,
					UsedPercent:    100.0 / 1200.0 * 100,
					ResetAt:        &reset,
					WindowStart:    &windowStart,
					WindowEnd:      &windowEnd,
					TimeUntilReset: 2 * time.Hour,
				},
				{
					ModelName:      "",
					Total:          1200,
					Used:           100,
					Remain:         1100,
					UsedPercent:    100.0 / 1200.0 * 100,
					ResetAt:        &reset,
					WindowStart:    &windowStart,
					WindowEnd:      &windowEnd,
					TimeUntilReset: 2 * time.Hour,
				},
			},
		}
		if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot: %v", err)
		}

		h := NewHandler(s, nil, nil, nil, nil)
		rows, names, groupBy, err := h.buildMiniMaxCycleOverviewRows("MiniMax-M2", 10)
		if err != nil {
			t.Fatalf("buildMiniMaxCycleOverviewRows: %v", err)
		}
		if len(rows) != 0 {
			t.Fatalf("expected no rows, got %d", len(rows))
		}
		if len(names) != 1 || names[0] != minimaxSharedQuotaKey {
			t.Fatalf("unexpected names: %v", names)
		}
		if groupBy != minimaxSharedQuotaKey {
			t.Fatalf("groupBy = %q, want %q", groupBy, minimaxSharedQuotaKey)
		}
	})

	t.Run("non-shared rows and closed-store error", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}

		base := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
		snap := testMiniMaxNonSharedSnapshot(base, 220, 140)
		if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot: %v", err)
		}
		reset := base.Add(3 * time.Hour)
		if _, err := s.CreateMiniMaxCycle("MiniMax-M2", base.Add(-30*time.Minute), &reset); err != nil {
			t.Fatalf("CreateMiniMaxCycle: %v", err)
		}
		if err := s.UpdateMiniMaxCycle("MiniMax-M2", 260, 120); err != nil {
			t.Fatalf("UpdateMiniMaxCycle: %v", err)
		}

		h := NewHandler(s, nil, nil, nil, nil)
		rows, names, groupBy, err := h.buildMiniMaxCycleOverviewRows("MiniMax-M2", 10)
		if err != nil {
			t.Fatalf("buildMiniMaxCycleOverviewRows: %v", err)
		}
		if len(rows) == 0 {
			t.Fatalf("expected rows, got none")
		}
		if len(names) == 0 {
			t.Fatalf("expected quota names, got none")
		}
		if groupBy != "MiniMax-M2" {
			t.Fatalf("groupBy = %q, want MiniMax-M2", groupBy)
		}

		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		_, _, _, err = h.buildMiniMaxCycleOverviewRows("MiniMax-M2", 10)
		if err == nil {
			t.Fatal("expected error after closing store")
		}
	})
}

func TestHandlerCycleOverviewMiniMaxAdditionalCoverage(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		h := NewHandler(nil, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.cycleOverviewMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=minimax", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("closed store error", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.cycleOverviewMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=minimax", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})
}

func TestHandlerHistoryMiniMaxAdditionalCoverage(t *testing.T) {
	t.Run("invalid range", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		defer s.Close()

		h := NewHandler(s, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.historyMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/minimax/history?range=bad", nil))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("query error on closed store", func(t *testing.T) {
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		h := NewHandler(s, nil, nil, nil, nil)
		rr := httptest.NewRecorder()
		h.historyMiniMax(rr, httptest.NewRequest(http.MethodGet, "/api/minimax/history?range=24h", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
	})
}
