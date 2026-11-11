package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func stringPointerZenmux(v string) *string { return &v }

func TestZenmuxStorePreservesUnknowns(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	zero, limit := 0.0, 34560.0
	expiry := time.Date(2027, 4, 12, 8, 26, 56, 0, time.UTC)
	now := time.Now().UTC().Truncate(time.Second)
	snap := &api.ZenmuxSnapshot{CapturedAt: now, Plan: &api.ZenmuxPlan{Tier: stringPointerZenmux("ultra"), ExpiresAt: &expiry}, Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &limit}}
	if _, err := s.InsertZenmuxSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryLatestZenmux()
	if err != nil || got == nil {
		t.Fatalf("latest: %v", err)
	}
	if got.Quota5Hour.UsagePercentage == nil || *got.Quota5Hour.UsagePercentage != 0 || got.Quota7Day.UsagePercentage != nil {
		t.Fatal("zero or missing changed")
	}
	if got.Plan.ExpiresAt == nil || !got.Plan.ExpiresAt.Equal(expiry) {
		t.Fatal("expiry missing")
	}
	b, _ := json.Marshal(got.QuotaMonthly)
	if strings.Contains(string(b), "used") || strings.Contains(string(b), "reset") || strings.Contains(string(b), "remaining") {
		t.Fatalf("invented monthly fields: %s", b)
	}
}

func TestZenmuxStoreLimitsHistory(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 250; i++ {
		if _, err := s.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.QueryZenmuxChartRange(now.Add(-time.Second), now.Add(251*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) > 200 || len(rows) == 0 || !rows[len(rows)-1].CapturedAt.Equal(now.Add(249*time.Second)) {
		t.Fatalf("bad sampling: %d", len(rows))
	}
}

func TestZenmuxHistoryIncludesWholeSecondWithinRange(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	_, err = s.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryZenmuxChartRange(at.Add(-time.Second), at.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal("whole-second snapshot disappeared within range")
	}
}
