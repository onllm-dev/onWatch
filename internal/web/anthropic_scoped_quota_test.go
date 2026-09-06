package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestCurrentAnthropic_ScopedQuotaReachesDashboard walks a real capture of
// GET /api/oauth/usage all the way to /api/current, which is the path issue
// #121 reports as broken: the per-model weekly bucket exists upstream but never
// reaches a card.
func TestCurrentAnthropic_ScopedQuotaReachesDashboard(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../api/testdata/anthropic_usage_weekly_scoped.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	parsed, err := api.ParseAnthropicResponse(raw)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse: %v", err)
	}

	// The capture has the scoped bucket at 0%. Nudge it so the assertions below
	// distinguish "rendered" from "defaulted to zero".
	util := 12.0
	(*parsed)["seven_day_scoped_fable"].Utilization = &util

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertAnthropicSnapshot(parsed.ToSnapshot(time.Now().UTC())); err != nil {
		t.Fatalf("InsertAnthropicSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var response struct {
		Quotas []struct {
			Name        string  `json:"name"`
			DisplayName string  `json:"displayName"`
			Utilization float64 `json:"utilization"`
		} `json:"quotas"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	byName := make(map[string]float64, len(response.Quotas))
	labels := make(map[string]string, len(response.Quotas))
	order := make([]string, 0, len(response.Quotas))
	for _, q := range response.Quotas {
		byName[q.Name] = q.Utilization
		labels[q.Name] = q.DisplayName
		order = append(order, q.Name)
	}

	if _, ok := byName["seven_day_scoped_fable"]; !ok {
		t.Fatalf("per-model weekly quota missing from the dashboard payload, got %v", order)
	}
	if got := byName["seven_day_scoped_fable"]; got != 12 {
		t.Errorf("seven_day_scoped_fable utilization = %v, want 12", got)
	}
	if got := labels["seven_day_scoped_fable"]; got != "Weekly Fable" {
		t.Errorf("seven_day_scoped_fable displayName = %q, want %q", got, "Weekly Fable")
	}

	// The primary quotas from the same capture must be unaffected.
	if got := byName["five_hour"]; got != 32 {
		t.Errorf("five_hour utilization = %v, want 32", got)
	}
	if got := byName["seven_day"]; got != 3 {
		t.Errorf("seven_day utilization = %v, want 3", got)
	}

	// Experimental keys stay hidden.
	for _, name := range order {
		switch name {
		case "seven_day_omelette", "seven_day_cowork", "seven_day_oauth_apps", "nimbus_quill":
			t.Errorf("experimental quota %q rendered on the dashboard", name)
		}
	}

	// A binding per-model limit should sit with the weekly quotas, not last.
	var scopedIdx, extraIdx = -1, -1
	for i, name := range order {
		switch name {
		case "seven_day_scoped_fable":
			scopedIdx = i
		case "extra_usage":
			extraIdx = i
		}
	}
	if extraIdx >= 0 && scopedIdx > extraIdx {
		t.Errorf("scoped quota ordered after extra_usage: %v", order)
	}
}

// zaiCard mirrors the per-quota object in the Z.ai /api/current payload.
type zaiCard struct {
	Name           string  `json:"name"`
	Usage          float64 `json:"usage"`
	Limit          float64 `json:"limit"`
	Percent        float64 `json:"percent"`
	TimeUntilReset string  `json:"timeUntilReset"`
}

// TestCurrentZai_CreditPlanCards walks the payload from issue #122 through the
// store to /api/current, where every card previously read 0.
func TestCurrentZai_CreditPlanCards(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"code": 200, "msg": "Operation successful", "success": true,
		"data": {"limits": [
			{"type": "CREDIT_LIMIT", "unit": 3, "number": 5, "usage": 2000, "currentValue": 402, "remaining": 1597, "percentage": 20, "nextResetTime": 1788351145586},
			{"type": "CREDIT_LIMIT", "unit": 6, "number": 1, "usage": 10000, "currentValue": 5207, "remaining": 4792, "percentage": 52, "nextResetTime": 1788784466996}
		], "level": "lite"}
	}`)

	parsed, err := api.ParseZaiResponse(payload)
	if err != nil {
		t.Fatalf("ParseZaiResponse: %v", err)
	}

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertZaiSnapshot(parsed.ToSnapshot(time.Now().UTC())); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithZai())
	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var response struct {
		TimeLimit   zaiCard `json:"timeLimit"`
		TokensLimit zaiCard `json:"tokensLimit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	timeCard := response.TimeLimit
	if timeCard.Percent != 20 || timeCard.Usage != 402 || timeCard.Limit != 2000 {
		t.Errorf("timeLimit card = %.0f%% (%v/%v), want 20%% (402/2000)",
			timeCard.Percent, timeCard.Usage, timeCard.Limit)
	}
	if timeCard.Name != "5-Hour Credits" {
		t.Errorf("timeLimit card name = %q, want %q", timeCard.Name, "5-Hour Credits")
	}

	tokensCard := response.TokensLimit
	if tokensCard.Percent != 52 || tokensCard.Usage != 5207 || tokensCard.Limit != 10000 {
		t.Errorf("tokensLimit card = %.0f%% (%v/%v), want 52%% (5207/10000)",
			tokensCard.Percent, tokensCard.Usage, tokensCard.Limit)
	}
	if tokensCard.Name != "Weekly Credits" {
		t.Errorf("tokensLimit card name = %q, want %q", tokensCard.Name, "Weekly Credits")
	}
}

// TestCurrentZai_LegacyPlanCardsUnchanged pins the labelling for accounts that
// still receive TIME_LIMIT / TOKENS_LIMIT.
func TestCurrentZai_LegacyPlanCardsUnchanged(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"code": 200, "success": true,
		"data": {"limits": [
			{"type": "TIME_LIMIT", "unit": 5, "number": 1, "usage": 1000, "currentValue": 19, "remaining": 981, "percentage": 1},
			{"type": "TOKENS_LIMIT", "unit": 5, "number": 1, "usage": 300000000, "currentValue": 200112618, "remaining": 99887382, "percentage": 66, "nextResetTime": 1788784466996}
		]}
	}`)

	parsed, err := api.ParseZaiResponse(payload)
	if err != nil {
		t.Fatalf("ParseZaiResponse: %v", err)
	}

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertZaiSnapshot(parsed.ToSnapshot(time.Now().UTC())); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithZai())
	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=zai", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	var response struct {
		TimeLimit   zaiCard `json:"timeLimit"`
		TokensLimit zaiCard `json:"tokensLimit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.TimeLimit.Name != "Time Limit" || response.TimeLimit.Percent != 1 {
		t.Errorf("timeLimit card = %q at %.0f%%, want \"Time Limit\" at 1%%",
			response.TimeLimit.Name, response.TimeLimit.Percent)
	}
	// TIME_LIMIT carries no reset, so the card keeps its historical placeholder.
	if response.TimeLimit.TimeUntilReset != "N/A" {
		t.Errorf("timeLimit timeUntilReset = %q, want %q", response.TimeLimit.TimeUntilReset, "N/A")
	}
	if response.TokensLimit.Name != "Tokens Limit" || response.TokensLimit.Percent != 66 {
		t.Errorf("tokensLimit card = %q at %.0f%%, want \"Tokens Limit\" at 66%%",
			response.TokensLimit.Name, response.TokensLimit.Percent)
	}
}
