package api

import (
	"strings"
	"testing"
	"time"
)

const museTestSSESubscription = `{"subscription":{"tier":"pro","weekly":{"resets_at":"1789344000","used_percent":"12.5"},"window":{"resets_at":"1789078632","used_percent":"34","window_duration_mins":"300"}}}`

func TestParseMuseSubscriptionEventsPicksLastSnapshot(t *testing.T) {
	lines := []string{
		`{"type":"response.created"}`,
		`{"subscription":{"tier":"pro","weekly":{"resets_at":"1789344000","used_percent":"10"},"window":{"resets_at":"1789078632","used_percent":"20","window_duration_mins":"300"}}}`,
		museTestSSESubscription,
		"[DONE]",
		"",
	}
	sub, err := ParseMuseSubscriptionEvents(lines)
	if err != nil {
		t.Fatalf("ParseMuseSubscriptionEvents: %v", err)
	}
	if sub.Tier != "pro" {
		t.Fatalf("tier = %q, want pro", sub.Tier)
	}
	if sub.Window.UsedPercent != 34 {
		t.Fatalf("window used = %v, want 34", sub.Window.UsedPercent)
	}
	if sub.Weekly.UsedPercent != 12.5 {
		t.Fatalf("weekly used = %v, want 12.5", sub.Weekly.UsedPercent)
	}
	if sub.Window.WindowDurationMins != 300 {
		t.Fatalf("window mins = %v, want 300", sub.Window.WindowDurationMins)
	}
	wantReset := time.Unix(1789078632, 0).UTC()
	if sub.Window.ResetsAt == nil || !sub.Window.ResetsAt.Equal(wantReset) {
		t.Fatalf("window resets_at = %v, want %v", sub.Window.ResetsAt, wantReset)
	}
}

func TestParseMuseSubscriptionEventsNoSnapshot(t *testing.T) {
	_, err := ParseMuseSubscriptionEvents([]string{`{"type":"x"}`, "[DONE]"})
	if err == nil {
		t.Fatal("expected error when no subscription snapshot present")
	}
}

func TestParseMuseSubscriptionEventsNumericValues(t *testing.T) {
	lines := []string{
		`{"subscription":{"tier":"team","weekly":{"resets_at":1789344000,"used_percent":7},"window":{"resets_at":1789078632,"used_percent":99.5,"window_duration_mins":300}}}`,
	}
	sub, err := ParseMuseSubscriptionEvents(lines)
	if err != nil {
		t.Fatalf("ParseMuseSubscriptionEvents: %v", err)
	}
	if sub.Window.UsedPercent != 99.5 || sub.Weekly.UsedPercent != 7 {
		t.Fatalf("unexpected percents: %+v", sub)
	}
}

func TestParseMuseSubscriptionEventsMillisReset(t *testing.T) {
	lines := []string{
		`{"subscription":{"weekly":{"resets_at":"1789344000000","used_percent":"3"},"window":{"resets_at":"1789078632000","used_percent":"4","window_duration_mins":"300"}}}`,
	}
	sub, err := ParseMuseSubscriptionEvents(lines)
	if err != nil {
		t.Fatalf("ParseMuseSubscriptionEvents: %v", err)
	}
	want := time.Unix(1789078632, 0).UTC()
	if sub.Window.ResetsAt == nil || !sub.Window.ResetsAt.Equal(want) {
		t.Fatalf("millis resets_at = %v, want %v", sub.Window.ResetsAt, want)
	}
}

func TestBuildMuseSnapshotQuotas(t *testing.T) {
	sub, err := ParseMuseSubscriptionEvents([]string{museTestSSESubscription})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	snap := BuildMuseSnapshot(sub, "muse-spark-1.3", `{"raw":true}`, time.Unix(1789000000, 0).UTC())
	if snap.Model != "muse-spark-1.3" {
		t.Fatalf("model = %q", snap.Model)
	}
	if len(snap.Quotas) != 2 {
		t.Fatalf("quotas = %d, want 2", len(snap.Quotas))
	}
	byName := map[string]MuseQuota{}
	for _, q := range snap.Quotas {
		byName[q.Name] = q
	}
	w, ok := byName[MuseQuotaWindow5H]
	if !ok {
		t.Fatalf("missing quota %q", MuseQuotaWindow5H)
	}
	if w.Utilization != 34 || w.Limit != 100 || w.Format != MuseQuotaFormatPercent {
		t.Fatalf("window quota = %+v", w)
	}
	if w.ResetsAt == nil {
		t.Fatal("window quota missing ResetsAt")
	}
	wk, ok := byName[MuseQuotaWeekly]
	if !ok {
		t.Fatalf("missing quota %q", MuseQuotaWeekly)
	}
	if wk.Utilization != 12.5 {
		t.Fatalf("weekly quota = %+v", wk)
	}
	if snap.WeeklyUsedPct != 12.5 || snap.WindowUsedPct != 34 {
		t.Fatalf("snapshot fields = %+v", snap)
	}
}

func TestMuseDisplayTier(t *testing.T) {
	if got := MuseDisplayTier("pro"); got != "pro" {
		t.Fatalf("tier pro -> %q", got)
	}
	// Numeric account tier IDs carry no display meaning.
	if got := MuseDisplayTier("27681527378179523"); got != "" {
		t.Fatalf("numeric tier -> %q, want empty", got)
	}
	if got := MuseDisplayTier("  "); got != "" {
		t.Fatalf("blank tier -> %q, want empty", got)
	}
}

func TestMuseWindowLabel(t *testing.T) {
	if got := MuseWindowLabel(300); got != "5h prompts" {
		t.Fatalf("300m -> %q", got)
	}
	if got := MuseWindowLabel(30); got != "30m prompts" {
		t.Fatalf("30m -> %q", got)
	}
	if got := MuseWindowLabel(0); !strings.Contains(got, "prompts") {
		t.Fatalf("0m -> %q", got)
	}
}

func TestMuseUsedPercentClamp(t *testing.T) {
	lines := []string{
		`{"subscription":{"window":{"resets_at":"1789078632","used_percent":"140","window_duration_mins":"300"},"weekly":{"resets_at":"1789344000","used_percent":"-5"}}}`,
	}
	sub, err := ParseMuseSubscriptionEvents(lines)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	snap := BuildMuseSnapshot(sub, "m", "{}", time.Now().UTC())
	for _, q := range snap.Quotas {
		if q.Utilization < 0 || q.Utilization > 100 {
			t.Fatalf("quota %s unclamped: %v", q.Name, q.Utilization)
		}
	}
}
