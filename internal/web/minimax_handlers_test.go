package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func sharedMiniMaxSnapshot(capturedAt time.Time, used int) *api.MiniMaxSnapshot {
	total := 1500
	remain := total - used
	resetAt := capturedAt.Add(4 * time.Hour)
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
			SharedModels []string `json:"sharedModels"`
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
	if len(quota.SharedModels) != 3 || quota.SharedModels[0] != "MiniMax-M2" || quota.SharedModels[1] != "MiniMax-M2.1" || quota.SharedModels[2] != "MiniMax-M2.5" {
		t.Fatalf("unexpected shared models: %v", quota.SharedModels)
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

	if _, err := s.InsertMiniMaxSnapshot(sharedMiniMaxSnapshot(time.Now().UTC(), 1)); err != nil {
		t.Fatalf("InsertMiniMaxSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, nil)
	resp := h.buildMiniMaxInsights(map[string]bool{}, 24*time.Hour)

	if len(resp.Stats) == 0 {
		t.Fatal("expected stats")
	}
	foundTotalUsage := false
	for _, stat := range resp.Stats {
		if stat.Label == "Total Usage" {
			foundTotalUsage = true
			if stat.Value != "1/1500" {
				t.Fatalf("stat.Value=%q, want 1/1500", stat.Value)
			}
		}
	}
	if !foundTotalUsage {
		t.Fatal("expected Total Usage stat")
	}

	foundShared := false
	for _, insight := range resp.Insights {
		if insight.Key != "shared_quota" {
			continue
		}
		foundShared = true
		if insight.Title != "MiniMax Coding Plan: Healthy" {
			t.Fatalf("insight.Title=%q", insight.Title)
		}
		want := "1 of 1500 requests used (0.1%)"
		if !strings.HasPrefix(insight.Desc, want) {
			t.Fatalf("insight.Desc=%q", insight.Desc)
		}
	}
	if !foundShared {
		t.Fatal("expected shared_quota insight")
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
		ModelName     string   `json:"modelName"`
		DisplayName   string   `json:"displayName"`
		SharedModels  []string `json:"sharedModels"`
		CurrentUsed   int      `json:"currentUsed"`
		CurrentRemain int      `json:"currentRemain"`
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
	if len(summary.SharedModels) != 3 {
		t.Fatalf("unexpected shared models: %+v", summary)
	}
}
