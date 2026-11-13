package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAlibabaPercent(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{0.25, 25},
		{1, 100},
		{1.5, 1.5}, // above 1 is already a percent
		{42, 42},
		{100, 100},
		{250, 100},
		{-0.2, 0},
		{-5, 0},
	}
	for _, tc := range tests {
		got := NormalizeAlibabaPercent(tc.in)
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("NormalizeAlibabaPercent(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseAlibabaUsage_AllWindows(t *testing.T) {
	raw := `{"per5HourPercentage":0.125,"per5HourResetTime":1790262790822,
		"per1WeekPercentage":0.4,"per1WeekResetTime":1790429616789,
		"per1MonthPercentage":7,"per1MonthResetTime":"1792000000000"}`
	quotas, err := ParseAlibabaUsage([]byte(raw))
	if err != nil {
		t.Fatalf("ParseAlibabaUsage: %v", err)
	}
	if len(quotas) != 3 {
		t.Fatalf("quotas = %d, want 3", len(quotas))
	}
	wantNames := []string{AlibabaQuotaFiveHour, AlibabaQuotaWeekly, AlibabaQuotaMonthly}
	wantUtil := []float64{12.5, 40, 7}
	for i, q := range quotas {
		if q.Name != wantNames[i] {
			t.Errorf("quota[%d].Name = %q, want %q", i, q.Name, wantNames[i])
		}
		if diff := q.Utilization - wantUtil[i]; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("quota[%d].Utilization = %v, want %v", i, q.Utilization, wantUtil[i])
		}
		if q.ResetsAt == nil {
			t.Errorf("quota[%d].ResetsAt = nil", i)
		}
	}
	if got := quotas[0].ResetsAt.UnixMilli(); got != 1790262790822 {
		t.Errorf("five_hour reset = %d", got)
	}
	if got := quotas[2].ResetsAt.UnixMilli(); got != 1792000000000 {
		t.Errorf("monthly reset from string = %d", got)
	}
}

func TestParseAlibabaUsage_OnlyPresentWindows(t *testing.T) {
	quotas, err := ParseAlibabaUsage([]byte(`{"per1WeekPercentage":0,"per5HourPercentage":null,"per1MonthResetTime":1792000000000}`))
	if err != nil {
		t.Fatalf("ParseAlibabaUsage: %v", err)
	}
	if len(quotas) != 1 || quotas[0].Name != AlibabaQuotaWeekly {
		t.Fatalf("quotas = %+v, want only weekly", quotas)
	}
	if quotas[0].Utilization != 0 {
		t.Errorf("weekly utilization = %v, want 0", quotas[0].Utilization)
	}
	if quotas[0].ResetsAt != nil {
		t.Errorf("weekly ResetsAt = %v, want nil", quotas[0].ResetsAt)
	}
}

func TestParseAlibabaUsage_Errors(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `"x"`, `{"per5HourPercentage":{}}`} {
		if _, err := ParseAlibabaUsage([]byte(raw)); err == nil {
			t.Errorf("ParseAlibabaUsage(%q) expected error", raw)
		}
	}
}

func TestParseAlibabaSubscription(t *testing.T) {
	raw := `{"remainingDays":21,"specCode":"lite","status":"VALID","startTime":1790000000000,
		"endTime":1792000000000,"autoRenewFlag":true,"instanceCode":"sfm_abc"}`
	sub, err := ParseAlibabaSubscription([]byte(raw))
	if err != nil {
		t.Fatalf("ParseAlibabaSubscription: %v", err)
	}
	if sub == nil {
		t.Fatal("expected subscription")
	}
	if sub.RemainingDays != 21 || sub.SpecCode != "lite" || sub.Status != "VALID" ||
		!sub.AutoRenew || sub.InstanceCode != "sfm_abc" {
		t.Errorf("unexpected subscription: %+v", sub)
	}
	if sub.StartTime == nil || sub.StartTime.UnixMilli() != 1790000000000 {
		t.Errorf("StartTime = %v", sub.StartTime)
	}
	if sub.EndTime == nil || sub.EndTime.UnixMilli() != 1792000000000 {
		t.Errorf("EndTime = %v", sub.EndTime)
	}
}

func TestParseAlibabaSubscription_FlexibleTypes(t *testing.T) {
	sub, err := ParseAlibabaSubscription([]byte(`{"remainingDays":"7","status":1,"autoRenewFlag":"1","endTime":"2026-12-01 00:00:00"}`))
	if err != nil {
		t.Fatalf("ParseAlibabaSubscription: %v", err)
	}
	if sub.RemainingDays != 7 || sub.Status != "1" || !sub.AutoRenew {
		t.Errorf("unexpected subscription: %+v", sub)
	}
	want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if sub.EndTime == nil || !sub.EndTime.Equal(want) {
		t.Errorf("EndTime = %v, want %v", sub.EndTime, want)
	}
}

func TestParseAlibabaSubscription_Empty(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`} {
		sub, err := ParseAlibabaSubscription([]byte(raw))
		if err != nil || sub != nil {
			t.Errorf("ParseAlibabaSubscription(%q) = %v, %v; want nil, nil", raw, sub, err)
		}
	}
}

func TestParseAlibabaInstance(t *testing.T) {
	raw := `{"InstanceList":[
		{"InstanceId":"other","ProductType":"something_else","Status":"Normal"},
		{"InstanceID":"inst-1","ProductType":"sfm_tokenplansolo_public_intl","Status":"Normal","EndTime":"2026-12-01T00:00:00Z"}
	]}`
	inst, err := ParseAlibabaInstance([]byte(raw))
	if err != nil {
		t.Fatalf("ParseAlibabaInstance: %v", err)
	}
	if inst == nil || inst.InstanceID != "inst-1" || inst.Status != "Normal" {
		t.Fatalf("instance = %+v", inst)
	}
	if inst.EndTime == nil || inst.EndTime.Year() != 2026 {
		t.Errorf("EndTime = %v", inst.EndTime)
	}

	none, err := ParseAlibabaInstance([]byte(`{"InstanceList":[]}`))
	if err != nil || none != nil {
		t.Errorf("empty list = %v, %v; want nil, nil", none, err)
	}
	if _, err := ParseAlibabaInstance([]byte(`{"InstanceList":"x"}`)); err == nil {
		t.Error("expected error for malformed InstanceList")
	}
}

func TestBuildAlibabaSnapshot(t *testing.T) {
	usage := `{"per5HourPercentage":0.1,"per5HourResetTime":1790262790822}`
	sub := `{"remainingDays":10,"specCode":"pro","status":"VALID","endTime":1792000000000}`
	inst := `{"InstanceList":[{"InstanceId":"inst-9","ProductType":"sfm_tokenplansolo_public_intl","Status":"Normal"}]}`
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.FixedZone("x", 3*3600))

	snap, err := BuildAlibabaSnapshot([]byte(usage), []byte(sub), []byte(inst), now)
	if err != nil {
		t.Fatalf("BuildAlibabaSnapshot: %v", err)
	}
	if !snap.CapturedAt.Equal(now) || snap.CapturedAt.Location() != time.UTC {
		t.Errorf("CapturedAt = %v", snap.CapturedAt)
	}
	if snap.SpecCode != "pro" || snap.Status != "VALID" || snap.RemainingDays != 10 {
		t.Errorf("subscription fields = %+v", snap)
	}
	if snap.InstanceID != "inst-9" {
		t.Errorf("InstanceID = %q, want fallback from BSS instance", snap.InstanceID)
	}
	if len(snap.Quotas) != 1 || snap.Quotas[0].Utilization != 10 {
		t.Errorf("quotas = %+v", snap.Quotas)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(snap.RawJSON), &raw); err != nil {
		t.Fatalf("RawJSON is not valid JSON: %v", err)
	}
	for _, key := range []string{"usage", "subscription", "instance"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("RawJSON missing %q", key)
		}
	}
}

func TestBuildAlibabaSnapshot_SubscriptionInstancePrecedence(t *testing.T) {
	sub := `{"instanceCode":"from-sub","status":"VALID","endTime":1792000000000}`
	inst := `{"InstanceList":[{"InstanceId":"from-bss","ProductType":"sfm_tokenplansolo_public_intl","Status":"Normal","EndTime":"2030-01-01T00:00:00Z"}]}`
	snap, err := BuildAlibabaSnapshot([]byte(`{}`), []byte(sub), []byte(inst), time.Now())
	if err != nil {
		t.Fatalf("BuildAlibabaSnapshot: %v", err)
	}
	if snap.InstanceID != "from-sub" || snap.Status != "VALID" || snap.EndTime.UnixMilli() != 1792000000000 {
		t.Errorf("subscription must win over BSS: %+v", snap)
	}
}

func TestBuildAlibabaSnapshot_Errors(t *testing.T) {
	if _, err := BuildAlibabaSnapshot([]byte(`{}`), nil, nil, time.Now()); err == nil {
		t.Error("expected error when usage and subscription are both empty")
	}
	if _, err := BuildAlibabaSnapshot([]byte(`nope`), nil, nil, time.Now()); err == nil {
		t.Error("expected error for invalid usage JSON")
	}
	// A broken BSS payload must not fail the snapshot.
	if _, err := BuildAlibabaSnapshot([]byte(`{"per1WeekPercentage":0.5}`), nil, []byte(`{"InstanceList":5}`), time.Now()); err != nil {
		t.Errorf("bad instance payload should be ignored, got %v", err)
	}
}

func TestParseAlibabaCookies_Netscape(t *testing.T) {
	input := "# Netscape HTTP Cookie File\n" +
		"# comment\n" +
		"\n" +
		".alibabacloud.com\tTRUE\t/\tTRUE\t1790000000\tcna\tabc123\n" +
		"modelstudio.console.alibabacloud.com\tFALSE\t/\tTRUE\t0\t_uab\tx=y\r\n" +
		"#HttpOnly_.alibabacloud.com\tTRUE\t/\tTRUE\t1790000000\tlogin_ticket\tsecret\n"
	got := ParseAlibabaCookies(input)
	want := "cna=abc123; _uab=x=y; login_ticket=secret"
	if got != want {
		t.Errorf("ParseAlibabaCookies = %q, want %q", got, want)
	}
}

func TestParseAlibabaCookies_HeaderPassThrough(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a=1; b=2", "a=1; b=2"},
		{"  a=1; b=2 \n", "a=1; b=2"},
		{"Cookie: a=1; b=2", "a=1; b=2"},
		{"cookie:a=1;\r\nb=2", "a=1; b=2"},
		{"", ""},
		{"   \n ", ""},
	}
	for _, tc := range tests {
		if got := ParseAlibabaCookies(tc.in); got != tc.want {
			t.Errorf("ParseAlibabaCookies(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ParseAlibabaCookies("a=1\r\nInjected: yes"); strings.ContainsAny(got, "\r\n") {
		t.Errorf("result must not contain line breaks: %q", got)
	}
}

func TestLoadAlibabaCookiesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.txt")
	raw := ".x.com\tTRUE\t/\tTRUE\t0\tk\tv\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAlibabaCookiesFile(path)
	if err != nil || got != raw {
		t.Fatalf("LoadAlibabaCookiesFile = %q, %v (want raw Netscape text)", got, err)
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("# only comments\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAlibabaCookiesFile(empty); err == nil {
		t.Error("expected error for a file without cookies")
	}
	if _, err := LoadAlibabaCookiesFile(filepath.Join(dir, "missing.txt")); err == nil {
		t.Error("expected error for a missing file")
	}
}
