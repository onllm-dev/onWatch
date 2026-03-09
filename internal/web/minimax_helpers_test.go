package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestMiniMaxHelperFunctions(t *testing.T) {
	resetAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	quota := &api.MiniMaxModelQuota{
		ModelName:   "MiniMax-M2",
		Total:       1500,
		Used:        900,
		Remain:      600,
		UsedPercent: 60,
		ResetAt:     &resetAt,
	}

	if got := minimaxSharedModelSummary([]string{"MiniMax-M2", "MiniMax-M2.1"}); got != "M2, M2.1" {
		t.Fatalf("minimaxSharedModelSummary() = %q", got)
	}
	if minimaxUsageStatus(20) != "healthy" || minimaxUsageStatus(60) != "warning" || minimaxUsageStatus(85) != "danger" || minimaxUsageStatus(95) != "critical" {
		t.Fatal("unexpected minimaxUsageStatus mapping")
	}
	if minimaxInsightSeverity(10) != "positive" || minimaxInsightSeverity(60) != "warning" || minimaxInsightSeverity(95) != "critical" {
		t.Fatal("unexpected minimaxInsightSeverity mapping")
	}
	if minimaxStatusLabel(10) != "Healthy" || minimaxStatusLabel(60) != "Warning" || minimaxStatusLabel(85) != "High" || minimaxStatusLabel(95) != "Critical" {
		t.Fatal("unexpected minimaxStatusLabel mapping")
	}
	if !minimaxIsSharedGroup("coding_plan") || !minimaxIsSharedGroup("MiniMax Coding Plan") || minimaxIsSharedGroup("") {
		t.Fatal("unexpected minimaxIsSharedGroup result")
	}

	shared := minimaxSharedCrossQuota(quota)
	if shared.Name != minimaxSharedQuotaKey || shared.Value != 900 || shared.Limit != 1500 || shared.Percent != 60 {
		t.Fatalf("unexpected minimaxSharedCrossQuota result: %+v", shared)
	}
	if minimaxSharedCrossQuota(nil).Name != minimaxSharedQuotaKey {
		t.Fatal("expected nil shared cross quota to preserve shared key")
	}

	if got := minimaxProjectionSummary(950, 1500); got == "" || got == "950 requests" {
		t.Fatalf("unexpected minimaxProjectionSummary output: %q", got)
	}
	if got := minimaxProjectionSummary(950, 0); got != "950 requests" {
		t.Fatalf("minimaxProjectionSummary() with no total = %q", got)
	}
	if got := minimaxUsageRecommendation(950, 1500, time.Hour, 2*time.Hour); got == "" || got[:7] != "Current" {
		t.Fatalf("unexpected high-burn recommendation: %q", got)
	}
	if got := minimaxUsageRecommendation(1000, 1500, 0, 0); got == "" {
		t.Fatal("expected recommendation text")
	}
	if got := minimaxBurnStatus(100, 0, 0, 0); got == "" {
		t.Fatal("expected burn status text for empty totals")
	}
	if got := minimaxBurnStatus(1400, 1500, time.Hour, 2*time.Hour); got == "" {
		t.Fatal("expected burn status text for projected exhaustion")
	}
}

func TestMiniMaxSamplesAndTrendHelpers(t *testing.T) {
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	windowStart := base.Add(-time.Hour)
	windowEnd := base.Add(4 * time.Hour)
	resetAt := windowEnd

	snapshots := []*api.MiniMaxSnapshot{
		nil,
		{
			CapturedAt: base,
			Models: []api.MiniMaxModelQuota{
				{ModelName: "MiniMax-M2", Total: 100, Used: 10, Remain: 90, UsedPercent: 10, ResetAt: &resetAt, WindowStart: &windowStart, WindowEnd: &windowEnd, TimeUntilReset: 4 * time.Hour},
			},
		},
		sharedMiniMaxSnapshotWithWindow(base.Add(30*time.Minute), 20, windowStart, windowEnd),
	}

	samples := minimaxMergedSamplesFromSnapshots(snapshots)
	if len(samples) != 2 {
		t.Fatalf("merged samples len = %d, want 2", len(samples))
	}
	if !samples[0].CapturedAt.Before(samples[1].CapturedAt) {
		t.Fatal("expected merged samples to be sorted")
	}

	filtered := minimaxCurrentWindowSamples(samples, samples[len(samples)-1])
	if len(filtered) != 2 {
		t.Fatalf("filtered samples len = %d, want 2", len(filtered))
	}

	days := minimaxDailyUsage([]minimaxMergedSample{
		{CapturedAt: base, Used: 10},
		{CapturedAt: base.Add(2 * time.Hour), Used: 25},
		{CapturedAt: base.Add(26 * time.Hour), Used: 30},
		{CapturedAt: base.Add(28 * time.Hour), Used: 45},
	})
	if len(days) != 2 {
		t.Fatalf("daily usage len = %d, want 2", len(days))
	}
	if got := minimaxTrendDirection(days); got == "" {
		t.Fatal("expected non-empty trend direction")
	}
	if minimaxTrendDirection(nil) != "Stable" {
		t.Fatal("expected Stable trend for empty series")
	}
}

func TestMiniMaxHandlersAndSummaryHelpers(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	tr := tracker.NewMiniMaxTracker(s, nil)
	h := NewHandler(s, nil, nil, nil, nil)
	h.SetMiniMaxTracker(tr)

	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	resetAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	snaps := []*api.MiniMaxSnapshot{
		sharedMiniMaxSnapshotWithWindow(base, 100, base.Add(-time.Hour), resetAt),
		sharedMiniMaxSnapshotWithWindow(base.Add(40*time.Minute), 250, base.Add(-time.Hour), resetAt),
		sharedMiniMaxSnapshotWithWindow(base.Add(80*time.Minute), 400, base.Add(-time.Hour), resetAt),
	}
	for _, snap := range snaps {
		if _, err := s.InsertMiniMaxSnapshot(snap); err != nil {
			t.Fatalf("InsertMiniMaxSnapshot: %v", err)
		}
		if err := tr.Process(snap); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/api/current/minimax", nil)
	currentRR := httptest.NewRecorder()
	h.currentMiniMax(currentRR, currentReq)
	if currentRR.Code != http.StatusOK {
		t.Fatalf("currentMiniMax status = %d, want 200", currentRR.Code)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/api/summary/minimax", nil)
	summaryRR := httptest.NewRecorder()
	h.summaryMiniMax(summaryRR, summaryReq)
	if summaryRR.Code != http.StatusOK {
		t.Fatalf("summaryMiniMax status = %d, want 200", summaryRR.Code)
	}
	summaryMap := h.buildMiniMaxSummaryMap()
	if _, ok := summaryMap["coding_plan"]; !ok {
		t.Fatalf("expected shared summary in response, got %+v", summaryMap)
	}

	insightsReq := httptest.NewRequest(http.MethodGet, "/api/insights/minimax", nil)
	insightsRR := httptest.NewRecorder()
	h.insightsMiniMax(insightsRR, insightsReq, 6*time.Hour)
	if insightsRR.Code != http.StatusOK {
		t.Fatalf("insightsMiniMax status = %d, want 200", insightsRR.Code)
	}

	cycle := &store.MiniMaxResetCycle{
		ID:         7,
		ModelName:  "MiniMax-M2",
		CycleStart: base,
		PeakUsed:   400,
		TotalDelta: 300,
		ResetAt:    &resetAt,
	}
	cycleMap := minimaxCycleToMap(cycle)
	if cycleMap["modelName"] != "MiniMax-M2" || cycleMap["peakUsed"] != 400 {
		t.Fatalf("unexpected minimaxCycleToMap result: %+v", cycleMap)
	}
}
