package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	s, _ := store.New(":memory:")
	defer s.Close()

	snap := sharedMiniMaxSnapshot(time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), 1)
	if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	body, err := json.Marshal(h.buildMiniMaxCurrent())
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
	if quota.Name != "MiniMax Coding Plan" || quota.DisplayName != "MiniMax Coding Plan" {
		t.Fatalf("unexpected merged quota identity: %+v", quota)
	}
	if quota.Used != 1 || quota.Remaining != 1499 || quota.Total != 1500 {
		t.Fatalf("unexpected merged counts: %+v", quota)
	}
}

func TestSessionsMiniMax_SharedQuotaFromSnapshots(t *testing.T) {
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
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshotWithWindow(base.Add(capture.offset), capture.used, windowStart, windowEnd)); err != nil {
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
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i)); err != nil {
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
		if _, ok := row["MiniMax Coding Plan"]; !ok {
			t.Fatalf("expected merged series key in row: %v", row)
		}
		if _, ok := row["MiniMax-M2"]; ok {
			t.Fatalf("did not expect per-model key in shared row: %v", row)
		}
	}
}

func TestBuildMiniMaxInsights_SharedQuota(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-3 * time.Hour)
	for i := 0; i < 4; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*45*time.Minute), 10+(i*4))); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot(%d): %v", i, err)
		}
	}

	h := NewHandler(s, nil, nil, nil, nil)
	resp := h.buildMiniMaxInsights(map[string]bool{}, 24*time.Hour)

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
			if insight.Title != "MiniMax Coding Plan: Healthy" {
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
	s, _ := store.New(":memory:")
	defer s.Close()

	snap := sharedMiniMaxSnapshot(time.Now().UTC().Add(-45*time.Minute), 1)
	if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	tr := tracker.NewMiniMaxTracker(s, nil)
	if err := tr.Process(snap); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	h.SetMiniMaxTracker(tr)
	resp := h.buildMiniMaxSummaryMap()

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
	if summary.ModelName != "MiniMax Coding Plan" || summary.DisplayName != "MiniMax Coding Plan" {
		t.Fatalf("unexpected summary identity: %+v", summary)
	}
	if summary.CurrentUsed != 1 || summary.CurrentRemain != 1499 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
}

func TestLoggingHistoryMiniMax_SharedQuota(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i+1)); err != nil {
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
	if len(resp.QuotaNames) != 1 || resp.QuotaNames[0] != "coding_plan" {
		t.Fatalf("unexpected quota names: %+v", resp.QuotaNames)
	}
	if len(resp.Logs) == 0 || len(resp.Logs[0].CrossQuotas) != 1 {
		t.Fatalf("expected merged MiniMax log rows, got %+v", resp.Logs)
	}
	if resp.Logs[0].CrossQuotas[0].Name != "coding_plan" {
		t.Fatalf("unexpected merged quota name: %+v", resp.Logs[0].CrossQuotas[0])
	}
}

func TestCycleOverviewMiniMax_SharedQuota(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-3 * time.Hour)
	snap := sharedMiniMaxSnapshot(base, 10)
	if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	tr := tracker.NewMiniMaxTracker(s, nil)
	if err := tr.Process(snap); err != nil {
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
	if len(resp.QuotaNames) != 1 || resp.QuotaNames[0] != "coding_plan" {
		t.Fatalf("unexpected quota names: %+v", resp.QuotaNames)
	}
	if len(resp.Cycles) == 0 || resp.Cycles[0].QuotaType != "coding_plan" {
		t.Fatalf("expected merged cycle rows, got %+v", resp.Cycles)
	}
	if len(resp.Cycles[0].CrossQuotas) != 1 || resp.Cycles[0].CrossQuotas[0].Name != "coding_plan" {
		t.Fatalf("expected merged cross quota entry, got %+v", resp.Cycles[0].CrossQuotas)
	}
}

func TestHistoryBoth_MiniMaxSharedQuotaSeries(t *testing.T) {
	s, _ := store.New(":memory:")
	defer s.Close()

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(base.Add(time.Duration(i)*15*time.Minute), i+1)); err != nil {
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
		if _, ok := row["MiniMax Coding Plan"]; !ok {
			t.Fatalf("expected merged coding-plan key in combined history row: %v", row)
		}
		if _, ok := row["MiniMax-M2"]; ok {
			t.Fatalf("did not expect per-model keys in combined history row: %v", row)
		}
	}
}
