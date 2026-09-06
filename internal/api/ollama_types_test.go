package api

import (
	"testing"
	"time"
)

func TestOllamaPlanMonthlyLimit(t *testing.T) {
	cases := map[string]float64{"free": 0, "Pro": 60, "max": 300, "TEAM": 1000, "": 0, "enterprise": 0}
	for plan, want := range cases {
		if got := OllamaPlanMonthlyLimit(plan); got != want {
			t.Errorf("%q = %v, want %v", plan, got, want)
		}
	}
}

func TestOllamaNextReset(t *testing.T) {
	d := func(y int, m time.Month, day, h int) time.Time { return time.Date(y, m, day, h, 0, 0, 0, time.UTC) }
	cases := []struct {
		name        string
		anchor, now time.Time
		want        time.Time
	}{
		{"later this month", d(2026, 1, 15, 20), d(2026, 9, 6, 22), d(2026, 9, 15, 20)},
		{"already passed this month", d(2026, 1, 3, 10), d(2026, 9, 6, 22), d(2026, 10, 3, 10)},
		{"same day before time", d(2026, 1, 6, 23), d(2026, 9, 6, 22), d(2026, 9, 6, 23)},
		{"same day after time", d(2026, 1, 6, 21), d(2026, 9, 6, 22), d(2026, 10, 6, 21)},
		{"31st clamps in september", d(2026, 1, 31, 0), d(2026, 9, 6, 0), d(2026, 9, 30, 0)},
		{"31st into october after clamp passed", d(2026, 1, 31, 0), d(2026, 9, 30, 1), d(2026, 10, 31, 0)},
		{"december rolls to january", d(2026, 1, 10, 0), d(2026, 12, 20, 0), d(2027, 1, 10, 0)},
		{"29th in february leap-safe", d(2026, 1, 29, 0), d(2027, 2, 1, 0), d(2027, 2, 28, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OllamaNextReset(tc.anchor, tc.now); !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOllamaResetAnchor(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	if a := OllamaResetAnchor(0, &created, now); a == nil || !a.Equal(created) {
		t.Errorf("createdAt anchor = %v", a)
	}
	if a := OllamaResetAnchor(20, &created, now); a == nil || a.Day() != 20 {
		t.Errorf("override anchor = %v", a)
	}
	if a := OllamaResetAnchor(31, nil, now); a == nil || a.Day() != 31 {
		t.Errorf("day 31 anchor = %v", a)
	}
	if a := OllamaResetAnchor(0, nil, now); a != nil {
		t.Errorf("expected nil anchor, got %v", a)
	}
	if a := OllamaResetAnchor(40, nil, now); a != nil {
		t.Errorf("out of range day should be ignored, got %v", a)
	}
}

func TestOllamaFlexFloat(t *testing.T) {
	resp, err := ParseOllamaUsage([]byte(`{"activity":{"cost":"0.00000"},"limits":{"monthly":{"usage":0.001,"models":[{"name":"m","request_count":2,"cost":"$1,250.50"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if float64(resp.Activity.Cost) != 0 || float64(resp.Limits.Monthly.Usage) != 0.001 {
		t.Errorf("parsed %+v", resp)
	}
	if float64(resp.Limits.Monthly.Models[0].Cost) != 1250.5 {
		t.Errorf("model cost = %v", resp.Limits.Monthly.Models[0].Cost)
	}
	if _, err := ParseOllamaUsage([]byte(`{"limits":{"monthly":{"usage":"abc"}}}`)); err == nil {
		t.Error("expected error for non-numeric string")
	}
}

func TestBuildOllamaSnapshot_FreePlanUnknownLimit(t *testing.T) {
	usage, _ := ParseOllamaUsage([]byte(`{"limits":{"monthly":{"usage":0.4}}}`))
	created := time.Date(2026, 9, 6, 20, 53, 6, 0, time.UTC)
	me := &OllamaMeResponse{Plan: "free", Name: "n", CreatedAt: &created}
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	snap := BuildOllamaSnapshot(usage, me, "{}", 0, 0, now)
	q := snap.Quotas[0]
	if q.Limit != 0 || q.Utilization != 0 || q.Used != 0.4 {
		t.Errorf("free quota = %+v", q)
	}
	want := time.Date(2026, 10, 6, 20, 53, 6, 0, time.UTC)
	if q.ResetsAt == nil || !q.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v, want %v", q.ResetsAt, want)
	}
	if snap.Models != nil {
		t.Errorf("models should be nil when absent, got %v", snap.Models)
	}
}

func TestOllamaSnapshot_ApplyResetAnchor(t *testing.T) {
	captured := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 6, 8, 0, 0, 0, time.UTC)
	snap := BuildOllamaSnapshot(&OllamaUsageResponse{}, &OllamaMeResponse{Plan: "pro", CreatedAt: &created}, "", 0, 0, captured)
	if got := snap.Quotas[0].ResetsAt; got == nil || got.Day() != 6 {
		t.Fatalf("pre-anchor reset = %v", got)
	}
	anchor := time.Date(2026, 9, 20, 3, 4, 5, 0, time.UTC) // observed reset on the 20th
	snap.ApplyResetAnchor(anchor)
	want := time.Date(2026, 10, 20, 3, 4, 5, 0, time.UTC)
	if got := snap.Quotas[0].ResetsAt; got == nil || !got.Equal(want) {
		t.Fatalf("post-anchor reset = %v, want %v", got, want)
	}
	var nilSnap *OllamaSnapshot
	nilSnap.ApplyResetAnchor(anchor) // must not panic
	snap.ApplyResetAnchor(time.Time{})
	if got := snap.Quotas[0].ResetsAt; got == nil || !got.Equal(want) {
		t.Fatalf("zero anchor should be ignored, got %v", got)
	}
}
