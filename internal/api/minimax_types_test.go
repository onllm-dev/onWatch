package api

import (
	"testing"
	"time"
)

func TestParseMiniMaxResponse(t *testing.T) {
	raw := []byte(`{
		"base_resp": {"status_code": 0, "status_msg": "success"},
		"model_remains": [
			{
				"model_name": "MiniMax-M2",
				"start_time": 1771218000000,
				"end_time": 1771236000000,
				"remains_time": 205310,
				"current_interval_total_count": 15000,
				"current_interval_usage_count": 14077
			}
		]
	}`)

	resp, err := ParseMiniMaxResponse(raw)
	if err != nil {
		t.Fatalf("ParseMiniMaxResponse: %v", err)
	}
	if resp.BaseResp.StatusCode != 0 {
		t.Fatalf("status=%d", resp.BaseResp.StatusCode)
	}
	if len(resp.ModelRemains) != 1 {
		t.Fatalf("model_remains=%d", len(resp.ModelRemains))
	}
	if resp.ModelRemains[0].ModelName != "MiniMax-M2" {
		t.Fatalf("model_name=%q", resp.ModelRemains[0].ModelName)
	}
}

func TestMiniMaxRemainsResponse_ToSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	resp := MiniMaxRemainsResponse{
		BaseResp: MiniMaxBaseResp{StatusCode: 0, StatusMsg: "success"},
		ModelRemains: []MiniMaxModelRemain{
			{
				ModelName:                 "MiniMax-M2",
				StartTime:                 int64(1771218000000),
				EndTime:                   int64(1771236000000),
				RemainsTime:               60_000,
				CurrentIntervalTotalCount: 15000,
				CurrentIntervalUsageCount: 14000,
			},
		},
	}

	snap := resp.ToSnapshot(capturedAt)
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if len(snap.Models) != 1 {
		t.Fatalf("models=%d", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelName != "MiniMax-M2" {
		t.Fatalf("model=%q", m.ModelName)
	}
	if m.Total != 15000 || m.Used != 14000 || m.Remain != 1000 {
		t.Fatalf("unexpected totals total=%d used=%d remain=%d", m.Total, m.Used, m.Remain)
	}
	if m.UsedPercent <= 93 || m.UsedPercent >= 94 {
		t.Fatalf("unexpected percent=%f", m.UsedPercent)
	}
	if m.ResetAt == nil {
		t.Fatal("expected resetAt")
	}
	if m.WindowStart == nil || m.WindowEnd == nil {
		t.Fatal("expected window bounds")
	}
	if snap.RawJSON == "" {
		t.Fatal("expected raw json")
	}
}

func TestMiniMaxRemainsResponse_ActiveModelNames(t *testing.T) {
	resp := MiniMaxRemainsResponse{
		ModelRemains: []MiniMaxModelRemain{
			{ModelName: "MiniMax-M2.5-highspeed"},
			{ModelName: "MiniMax-M2"},
			{ModelName: "MiniMax-M2"},
			{ModelName: ""},
		},
	}

	names := resp.ActiveModelNames()
	if len(names) != 2 {
		t.Fatalf("names=%v", names)
	}
	if names[0] != "MiniMax-M2" || names[1] != "MiniMax-M2.5-highspeed" {
		t.Fatalf("unexpected names=%v", names)
	}
}

func TestParseMiniMaxTimestamp(t *testing.T) {
	ts := parseMiniMaxTimestamp("1771218000000")
	if ts == nil {
		t.Fatal("expected timestamp from string")
	}

	ts2 := parseMiniMaxTimestamp(float64(1771218000000))
	if ts2 == nil {
		t.Fatal("expected timestamp from float")
	}

	if parseMiniMaxTimestamp("") != nil {
		t.Fatal("expected nil for empty string")
	}
	if parseMiniMaxTimestamp(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}
