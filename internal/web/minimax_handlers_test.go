package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func sharedMiniMaxSnapshot(capturedAt time.Time, used int) *api.MiniMaxSnapshot {
	resetAt := capturedAt.Add(4 * time.Hour)
	windowStart := capturedAt.Add(-1 * time.Hour)
	windowEnd := resetAt

	return sharedMiniMaxSnapshotWithWindow(capturedAt, used, windowStart, windowEnd)
}

func sharedMiniMaxSnapshotWithWindow(capturedAt time.Time, used int, windowStart, windowEnd time.Time) *api.MiniMaxSnapshot {
	total := 1500
	remain := total - used
	resetAt := windowEnd

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
				TimeUntilReset: 4 * time.Hour,
			},
			{
				ModelName:      "MiniMax-M2.1",
				Total:          total,
				Used:           used,
				Remain:         remain,
				UsedPercent:    float64(used) / float64(total) * 100,
				ResetAt:        &resetAt,
				WindowStart:    &windowStart,
				WindowEnd:      &windowEnd,
				TimeUntilReset: 4 * time.Hour,
			},
			{
				ModelName:      "MiniMax-M2.5",
				Total:          total,
				Used:           used,
				Remain:         remain,
				UsedPercent:    float64(used) / float64(total) * 100,
				ResetAt:        &resetAt,
				WindowStart:    &windowStart,
				WindowEnd:      &windowEnd,
				TimeUntilReset: 4 * time.Hour,
			},
		},
	}
}

func TestBuildMiniMaxCurrent_SharedQuota(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	snap := sharedMiniMaxSnapshot(time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), 1)
	if _, err := s.InsertMiniMaxSnapshot(snap, 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	body, err := json.Marshal(h.buildMiniMaxCurrent(2))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var resp struct {
		SharedQuota bool `json:"sharedQuota"`
		Quotas      []struct {
			Name         string   `json:"name"`
			DisplayName  string   `json:"displayName"`
			Used         int      `json:"used"`
			Remaining    int      `json:"remaining"`
			Total        int      `json:"total"`
			UsagePercent float64  `json:"usagePercent"`
		} `json:"quotas"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !resp.SharedQuota {
		t.Fatal("expected sharedQuota=true")
	}
	if len(resp.Quotas) != 1 {
		t.Fatalf("quotas=%d, want 1", len(resp.Quotas))
	}
	quota := resp.Quotas[0]
	if quota.Name != "Coding" || quota.DisplayName != "Coding" {
		t.Fatalf("unexpected merged quota identity: %+v", quota)
	}
	if quota.Used != 1 || quota.Remaining != 1499 || quota.Total != 1500 {
		t.Fatalf("unexpected merged counts: %+v", quota)
	}
}

func TestSessionsMiniMax_SharedQuotaFromSnapshots(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-40 * time.Minute)
	windowStart := base.Add(-1 * time.Hour)
	windowEnd := base.Add(4 * time.Hour)
	captures := []struct {
		offset time.Duration
		used   int
	}{
		{0, 1},
		{5 * time.Minute, 1},
		{10 * time.Minute, 2},
		{25 * time.Minute, 26},
		{35 * time.Minute, 26},
	}
	for i, capture := range captures {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshotWithWindow(base.Add(capture.offset), capture.used, windowStart, windowEnd), 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	h.config = &config.Config{MiniMaxAPIKey: "test-key"}
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=minimax", nil)
	rr := httptest.NewRecorder()
	h.Sessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp []struct {
		ID               string  `json:"id"`
		EndedAt          *string `json:"endedAt"`
		MaxSubRequests   float64 `json:"maxSubRequests"`
		StartSubRequests float64 `json:"startSubRequests"`
		SnapshotCount    int     `json:"snapshotCount"`
		StartedAt        string  `json:"startedAt"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("len(resp)=%d, want 2: %s", len(resp), rr.Body.String())
	}
	if resp[0].SnapshotCount != 2 || resp[0].MaxSubRequests != 26 || resp[0].StartSubRequests != 26 {
		t.Fatalf("unexpected active shared session: %+v", resp[0])
	}
	if resp[0].EndedAt != nil {
		t.Fatalf("expected most recent session to remain active, got endedAt=%v", *resp[0].EndedAt)
	}
	if resp[1].SnapshotCount != 3 || resp[1].MaxSubRequests != 2 || resp[1].StartSubRequests != 1 {
		t.Fatalf("unexpected older shared session: %+v", resp[1])
	}
	if resp[1].EndedAt == nil {
		t.Fatalf("expected older session to be closed: %+v", resp[1])
	}
}

func TestHistoryMiniMax_SharedQuotaSeries(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i), 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/minimax/history?range=24h", nil)
	rr := httptest.NewRecorder()
	h.historyMiniMax(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected history rows")
	}
	for _, row := range rows {
		if _, ok := row["Coding"]; !ok {
			t.Fatalf("expected merged series key in row: %v", row)
		}
		if _, ok := row["MiniMax-M2"]; ok {
			t.Fatalf("did not expect per-model key in shared row: %v", row)
		}
	}
}

func TestBuildMiniMaxInsights_SharedQuota(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-3 * time.Hour)
	for i := 0; i < 4; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*45*time.Minute), 10+(i*4)), 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	resp := h.buildMiniMaxInsights(2, map[string]bool{}, 24*time.Hour)

	if len(resp.Stats) < 4 {
		t.Fatalf("expected rich stats, got %d", len(resp.Stats))
	}
	foundBurnRate := false
	for _, stat := range resp.Stats {
		if stat.Label == "Burn Rate" {
			foundBurnRate = true
			if stat.Value == "" {
				t.Fatal("expected burn-rate value")
			}
		}
	}
	if !foundBurnRate {
		t.Fatal("expected Burn Rate stat")
	}

	foundStatus := false
	foundBurnAnalysis := false
	for _, insight := range resp.Insights {
		switch insight.Key {
		case "shared_status":
			foundStatus = true
			if insight.Title != "Coding: Healthy" {
				t.Fatalf("insight.Title=%q", insight.Title)
			}
			if insight.Metric == "" || insight.Desc == "" {
				t.Fatalf("expected metric + desc on shared status insight: %+v", insight)
			}
		case "burn_rate":
			foundBurnAnalysis = true
			if insight.Sublabel == "" {
				t.Fatalf("expected burn-rate projection sublabel: %+v", insight)
			}
		}
	}
	if !foundStatus || !foundBurnAnalysis {
		t.Fatalf("expected shared status and burn-rate insights, got %+v", resp.Insights)
	}
}

func TestBuildMiniMaxSummaryMap_SharedQuota(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	snap := sharedMiniMaxSnapshot(time.Now().UTC().Add(-45*time.Minute), 1)
	if _, err := s.InsertMiniMaxSnapshot(snap, 2); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	tr := tracker.NewMiniMaxTracker(s, nil)
	if err := tr.Process(snap, 2); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	h.SetMiniMaxTracker(tr)
	resp := h.buildMiniMaxSummaryMap(2)

	if len(resp) != 1 {
		t.Fatalf("len(resp)=%d, want 1", len(resp))
	}

	raw, ok := resp["coding_plan"]
	if !ok {
		t.Fatalf("expected coding_plan key, got %v", resp)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var summary struct {
		ModelName     string `json:"modelName"`
		DisplayName   string `json:"displayName"`
		CurrentUsed   int    `json:"currentUsed"`
		CurrentRemain int    `json:"currentRemain"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if summary.ModelName != "Coding" || summary.DisplayName != "Coding" {
		t.Fatalf("unexpected summary identity: %+v", summary)
	}
	if summary.CurrentUsed != 1 || summary.CurrentRemain != 1499 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
}

func TestLoggingHistoryMiniMax_SharedQuota(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i+1), 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=minimax&range=1&limit=10", nil)
	rr := httptest.NewRecorder()
	h.LoggingHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
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
	if len(resp.QuotaNames) != 1 || resp.QuotaNames[0] != "Coding" {
		t.Fatalf("unexpected quota names: %+v", resp.QuotaNames)
	}
	if len(resp.Logs) == 0 || len(resp.Logs[0].CrossQuotas) != 1 {
		t.Fatalf("expected merged MiniMax log rows, got %+v", resp.Logs)
	}
	if resp.Logs[0].CrossQuotas[0].Name != "Coding" {
		t.Fatalf("unexpected merged quota name: %+v", resp.Logs[0].CrossQuotas[0])
	}
}

func TestCycleOverviewMiniMax_SharedQuota(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	acc, _ := s.GetOrCreateProviderAccount("minimax", "default")
	accountID := acc.ID

	base := time.Now().UTC().Add(-3 * time.Hour)
	snap := sharedMiniMaxSnapshot(base, 10)
	if _, err := s.InsertMiniMaxSnapshot(snap, accountID); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	tr := tracker.NewMiniMaxTracker(s, nil)
	if err := tr.Process(snap, accountID); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=minimax&groupBy=coding_plan&limit=10", nil)
	rr := httptest.NewRecorder()
	h.cycleOverviewMiniMax(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		GroupBy    string   `json:"groupBy"`
		QuotaNames []string `json:"quotaNames"`
		Cycles     []struct {
			QuotaType   string `json:"quotaType"`
			CrossQuotas []struct {
				Name string `json:"name"`
			} `json:"crossQuotas"`
		} `json:"cycles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.GroupBy != "coding_plan" {
		t.Fatalf("groupBy=%q, want coding_plan", resp.GroupBy)
	}
	if len(resp.QuotaNames) != 1 || resp.QuotaNames[0] != "Coding" {
		t.Fatalf("unexpected quota names: %+v", resp.QuotaNames)
	}
	if len(resp.Cycles) == 0 || resp.Cycles[0].QuotaType != "coding_plan" {
		t.Fatalf("expected merged cycle rows, got %+v", resp.Cycles)
	}
	if len(resp.Cycles[0].CrossQuotas) != 1 || resp.Cycles[0].CrossQuotas[0].Name != "Coding" {
		t.Fatalf("expected merged cross quota entry, got %+v", resp.Cycles[0].CrossQuotas)
	}
}

func TestHistoryBoth_MiniMaxSharedQuotaSeries(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i+1), 2); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	h.config = &config.Config{MiniMaxAPIKey: "test-key", SyntheticAPIKey: "syn-test"}
	req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=24h", nil)
	rr := httptest.NewRecorder()
	h.History(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		MiniMax []map[string]interface{} `json:"minimax"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(resp.MiniMax) == 0 {
		t.Fatal("expected combined MiniMax history")
	}
	for _, row := range resp.MiniMax {
		if _, ok := row["Coding"]; !ok {
			t.Fatalf("expected merged coding-plan key in combined history row: %v", row)
		}
		if _, ok := row["MiniMax-M2"]; ok {
			t.Fatalf("did not expect per-model keys in combined history row: %v", row)
		}
	}
}

// ── Account CRUD Handler Tests ──

func TestMiniMaxAccounts_ListEmpty(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/minimax/accounts", nil)
	w := httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	accounts, ok := resp["accounts"].([]interface{})
	if !ok {
		t.Fatal("expected accounts array")
	}
	// There should be at least the default "minimax" account from migration
	if len(accounts) == 0 {
		t.Fatal("expected at least the default minimax account")
	}
}

func TestMiniMaxAccounts_CreateAndList(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, nil)

	// Create a new account
	body := `{"name":"work","api_key":"sk_test","region":"global"}`
	req := httptest.NewRequest("POST", "/api/minimax/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List and verify
	req = httptest.NewRequest("GET", "/api/minimax/accounts", nil)
	w = httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	accounts := resp["accounts"].([]interface{})
	found := false
	for _, a := range accounts {
		acc := a.(map[string]interface{})
		if acc["name"] == "work" {
			found = true
			if acc["hasKey"] != true {
				t.Error("expected hasKey=true for created account")
			}
			if acc["region"] != "global" {
				t.Errorf("expected region='global', got %v", acc["region"])
			}
		}
	}
	if !found {
		t.Error("created account 'work' not found in list")
	}
}

func TestMiniMaxAccounts_Update(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, nil)

	// Create account first
	body := `{"name":"test","api_key":"sk_old","region":"global"}`
	req := httptest.NewRequest("POST", "/api/minimax/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	var createResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&createResp)
	id := createResp["id"]

	// Update name and region
	updateBody := `{"name":"renamed","region":"cn"}`
	req = httptest.NewRequest("PUT", "/api/minimax/accounts?id="+formatID(id), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify update
	req = httptest.NewRequest("GET", "/api/minimax/accounts", nil)
	w = httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	var listResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&listResp)
	for _, a := range listResp["accounts"].([]interface{}) {
		acc := a.(map[string]interface{})
		if formatID(acc["id"]) == formatID(id) {
			if acc["name"] != "renamed" {
				t.Errorf("expected name 'renamed', got %v", acc["name"])
			}
			if acc["region"] != "cn" {
				t.Errorf("expected region 'cn', got %v", acc["region"])
			}
		}
	}
}

func TestMiniMaxAccounts_Delete(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, nil)

	// Create account
	body := `{"name":"todelete","api_key":"sk_del","region":"global"}`
	req := httptest.NewRequest("POST", "/api/minimax/accounts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	var createResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&createResp)
	id := createResp["id"]

	// Delete
	req = httptest.NewRequest("DELETE", "/api/minimax/accounts?id="+formatID(id), nil)
	w = httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify deletion (soft-delete - should still appear in list with deletedAt)
	req = httptest.NewRequest("GET", "/api/minimax/accounts", nil)
	w = httptest.NewRecorder()
	h.MiniMaxAccounts(w, req)

	var listResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&listResp)
	for _, a := range listResp["accounts"].([]interface{}) {
		acc := a.(map[string]interface{})
		if formatID(acc["id"]) == formatID(id) {
			if _, hasDeletedAt := acc["deletedAt"]; !hasDeletedAt {
				t.Error("expected deletedAt to be set after soft-delete")
			}
		}
	}
}

func TestMiniMaxAccounts_ValidationErrors(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, nil)

	tests := []struct {
		name   string
		method string
		url    string
		body   string
		want   int
	}{
		{"empty name", "POST", "/api/minimax/accounts", `{"name":"","api_key":"sk_test"}`, 400},
		{"invalid name", "POST", "/api/minimax/accounts", `{"name":"bad name!","api_key":"sk_test"}`, 400},
		{"invalid region on create", "POST", "/api/minimax/accounts", `{"name":"test","api_key":"sk_test","region":"invalid"}`, 400},
		{"invalid id on update", "PUT", "/api/minimax/accounts?id=abc", `{"name":"new"}`, 400},
		{"invalid id on delete", "DELETE", "/api/minimax/accounts?id=0", ``, 400},
		{"not found on update", "PUT", "/api/minimax/accounts?id=99999", `{"name":"new"}`, 404},
		{"not found on delete", "DELETE", "/api/minimax/accounts?id=99999", ``, 404},
		{"bad method", "PATCH", "/api/minimax/accounts", ``, 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			}
			w := httptest.NewRecorder()
			h.MiniMaxAccounts(w, req)
			if w.Code != tt.want {
				t.Errorf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestMiniMaxAccountsUsage_MultiAccount(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cfg := &config.Config{MiniMaxAPIKey: "sk_placeholder"}
	tr := tracker.NewMiniMaxTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, cfg)
	h.minimaxTracker = tr

	// Create two accounts and insert snapshots for each
	acc1, _ := s.GetOrCreateProviderAccount("minimax", "work")
	s.UpdateProviderAccountMetadata(acc1.ID, `{"api_key":"sk1","region":"global"}`)
	acc2, _ := s.GetOrCreateProviderAccount("minimax", "personal")
	s.UpdateProviderAccountMetadata(acc2.ID, `{"api_key":"sk2","region":"cn"}`)

	now := time.Now().UTC().Truncate(time.Second)
	snap1 := sharedMiniMaxSnapshot(now, 500)
	snap2 := sharedMiniMaxSnapshot(now, 800)
	s.InsertMiniMaxSnapshot(snap1, acc1.ID)
	s.InsertMiniMaxSnapshot(snap2, acc2.ID)

	req := httptest.NewRequest("GET", "/api/minimax/accounts/usage", nil)
	w := httptest.NewRecorder()
	h.MiniMaxAccountsUsage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	accounts, ok := resp["accounts"].([]interface{})
	if !ok || len(accounts) < 2 {
		t.Fatalf("expected at least 2 accounts in usage response, got %v", resp)
	}
}

func formatID(v interface{}) string {
	switch id := v.(type) {
	case float64:
		return fmt.Sprintf("%.0f", id)
	case int64:
		return fmt.Sprintf("%d", id)
	case int:
		return fmt.Sprintf("%d", id)
	case string:
		return id
	default:
		return fmt.Sprintf("%v", v)
	}
}
