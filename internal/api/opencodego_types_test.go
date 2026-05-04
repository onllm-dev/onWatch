package api

import (
	"testing"
	"time"
)

func TestParseOpenCodeGoUsageResponse_SerovalFormat(t *testing.T) {
	html := `window.__INITIAL_STATE__ = {
		rollingUsage:$R[30]={status:"ok",resetInSec:17562,usagePercent:2},
		weeklyUsage:$R[31]={status:"ok",resetInSec:533388,usagePercent:0},
		monthlyUsage:$R[32]={status:"ok",resetInSec:2485309,usagePercent:50}
	};`

	resp, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RollingUsage == nil {
		t.Fatal("expected rolling")
	}
	if resp.RollingUsage.UsagePercent != 2 {
		t.Errorf("rolling usagePercent = %v, want 2", resp.RollingUsage.UsagePercent)
	}
	if resp.RollingUsage.ResetInSec != 17562 {
		t.Errorf("rolling resetInSec = %v, want 17562", resp.RollingUsage.ResetInSec)
	}
	if resp.RollingUsage.Status != "ok" {
		t.Errorf("rolling status = %v, want ok", resp.RollingUsage.Status)
	}

	if resp.WeeklyUsage == nil {
		t.Fatal("expected weekly")
	}
	if resp.WeeklyUsage.UsagePercent != 0 {
		t.Errorf("weekly usagePercent = %v, want 0", resp.WeeklyUsage.UsagePercent)
	}

	if resp.MonthlyUsage == nil {
		t.Fatal("expected monthly")
	}
	if resp.MonthlyUsage.UsagePercent != 50 {
		t.Errorf("monthly usagePercent = %v, want 50", resp.MonthlyUsage.UsagePercent)
	}
}

func TestParseOpenCodeGoUsageResponse_SerovalWithSpaces(t *testing.T) {
	html := `window.__INITIAL_STATE__ = {
		rollingUsage : $R[30] = { status : "ok" , resetInSec : 17562 , usagePercent : 2 }
	};`

	resp, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RollingUsage == nil {
		t.Fatal("expected rolling")
	}
	if resp.RollingUsage.UsagePercent != 2 {
		t.Errorf("rolling usagePercent = %v, want 2", resp.RollingUsage.UsagePercent)
	}
	if resp.RollingUsage.ResetInSec != 17562 {
		t.Errorf("rolling resetInSec = %v, want 17562", resp.RollingUsage.ResetInSec)
	}
	if resp.RollingUsage.Status != "ok" {
		t.Errorf("rolling status = %v, want ok", resp.RollingUsage.Status)
	}
}

func TestParseOpenCodeGoUsageResponse_IntegerPercentNotMultiplied(t *testing.T) {
	// Value of 1 should mean 1%, not 100%
	html := `rollingUsage:$R[30]={status:"ok",resetInSec:17562,usagePercent:1}`

	resp, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RollingUsage == nil {
		t.Fatal("expected rolling")
	}
	if resp.RollingUsage.UsagePercent != 1 {
		t.Errorf("rolling usagePercent = %v, want 1", resp.RollingUsage.UsagePercent)
	}
}

func TestParseOpenCodeGoUsageResponse_FractionalPercent(t *testing.T) {
	// Value of 0.02 should be converted to 2%
	html := `rollingUsage:$R[30]={status:"ok",resetInSec:17562,usagePercent:0.02}`

	resp, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RollingUsage == nil {
		t.Fatal("expected rolling")
	}
	if resp.RollingUsage.UsagePercent != 2 { // 0.02 * 100 = 2
		t.Errorf("rolling usagePercent = %v, want 2", resp.RollingUsage.UsagePercent)
	}
}

func TestParseOpenCodeGoUsageResponse_StringWithBraces(t *testing.T) {
	// findMatchingBrace should not be confused by } inside strings
	html := `{"description": "Some {text} here", "rollingUsage": {"status": "ok", "resetInSec": 17562, "usagePercent": 2}}`

	resp, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.RollingUsage == nil {
		t.Fatal("expected rolling")
	}
	if resp.RollingUsage.UsagePercent != 2 {
		t.Errorf("rolling usagePercent = %v, want 2", resp.RollingUsage.UsagePercent)
	}
}

func TestParseOpenCodeGoUsageResponse_NotSignedIn(t *testing.T) {
	html := `<html><body><a href="/login">sign in</a></body></html>`

	_, err := ParseOpenCodeGoUsageResponse([]byte(html))
	if err != ErrOpenCodeGoNotSignedIn {
		t.Errorf("expected ErrOpenCodeGoNotSignedIn, got %v", err)
	}
}

func TestOpenCodeGoUsageResponse_ToSnapshot(t *testing.T) {
	resp := &OpenCodeGoUsageResponse{
		RollingUsage: &OpenCodeGoUsageWindow{
			Name:         "rolling",
			UsagePercent: 2.5,
			ResetInSec:   3600,
			Status:       "ok",
		},
		WeeklyUsage: &OpenCodeGoUsageWindow{
			Name:         "weekly",
			UsagePercent: 0,
			ResetInSec:   86400,
			Status:       "ok",
		},
	}

	now := time.Now().UTC()
	snap := resp.ToSnapshot(now)

	if snap.CapturedAt.IsZero() {
		t.Error("expected capturedAt to be set")
	}
	if len(snap.Windows) != 2 {
		t.Errorf("expected 2 windows, got %d", len(snap.Windows))
	}

	rolling := snap.GetWindow("rolling")
	if rolling == nil {
		t.Fatal("expected rolling window")
	}
	if rolling.UsagePercent != 2.5 {
		t.Errorf("rolling usagePercent = %v, want 2.5", rolling.UsagePercent)
	}
	if rolling.ResetInSec != 3600 {
		t.Errorf("rolling resetInSec = %v, want 3600", rolling.ResetInSec)
	}

	monthly := snap.GetWindow("monthly")
	if monthly != nil {
		t.Error("expected no monthly window")
	}
}

func TestOpenCodeGoSnapshot_HasMonthlyWindow(t *testing.T) {
	snap := &OpenCodeGoSnapshot{
		Windows: []OpenCodeGoWindowValue{
			{WindowName: "rolling"},
		},
	}
	if snap.HasMonthlyWindow() {
		t.Error("expected HasMonthlyWindow to be false")
	}

	snap.Windows = append(snap.Windows, OpenCodeGoWindowValue{WindowName: "monthly"})
	if !snap.HasMonthlyWindow() {
		t.Error("expected HasMonthlyWindow to be true")
	}
}

func TestFindMatchingBrace(t *testing.T) {
	tests := []struct {
		input string
		start int
		want  int
	}{
		{"{}", 0, 1},
		{"{{}}", 0, 3},
		{"{{}}", 1, 2},
		{"{\"a\": \"}\"}", 0, 9},
		{"{\"a\": \"{\"}", 0, 9},
		{"abc{def}ghi", 3, 7},
		{"abc", 0, -1},
		{"", 0, -1},
	}

	for _, tt := range tests {
		got := findMatchingBrace(tt.input, tt.start)
		if got != tt.want {
			t.Errorf("findMatchingBrace(%q, %d) = %d, want %d", tt.input, tt.start, got, tt.want)
		}
	}
}
