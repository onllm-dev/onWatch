package api

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReadMuseSubscriptionStreamStopsAtFirstUsableFrame(t *testing.T) {
	body := strings.Join([]string{
		"data: {\"type\":\"response.created\"}",
		"data: {\"subscription\":{\"window\":{}}}",
		"data: " + museTestSSESubscription,
		"data: {\"subscription\":{\"tier\":\"pro\",\"window\":{\"resets_at\":\"1789078632\",\"used_percent\":\"99\",\"window_duration_mins\":\"300\"}}}",
		"data: [DONE]",
	}, "\n") + "\n"

	sub, _, err := readMuseSubscriptionStream(strings.NewReader(body))
	if err != nil {
		t.Fatalf("readMuseSubscriptionStream: %v", err)
	}
	if sub.Window.UsedPercent != 34 {
		t.Fatalf("window used = %v, want 34 from the first usable frame", sub.Window.UsedPercent)
	}
}

// A placeholder frame carries no reading; accepting it would store 0% and wipe
// the real value for the cycle.
func TestReadMuseSubscriptionStreamSkipsEmptyWindow(t *testing.T) {
	body := "data: {\"subscription\":{\"window\":{}}}\ndata: [DONE]\n"
	if _, _, err := readMuseSubscriptionStream(strings.NewReader(body)); err == nil {
		t.Fatal("expected an error when the stream carries only placeholder frames")
	}
}

// A truncated or oversized stream must not be reported as "Meta sent no usage".
func TestReadMuseSubscriptionStreamReportsTruncation(t *testing.T) {
	body := "data: {\"padding\":\"" + strings.Repeat("x", museMaxBodyBytes) + "\"}\n"
	_, _, err := readMuseSubscriptionStream(strings.NewReader(body))
	if err == nil {
		t.Fatal("expected an error for an oversized stream")
	}
	if strings.Contains(err.Error(), "carried no subscription usage") {
		t.Fatalf("transport failure misreported as missing usage: %v", err)
	}
}

func TestMuseHasQuotaRejectsEmptyWindows(t *testing.T) {
	if museHasQuota(&MuseSubscription{Window: &museWindow{}}) {
		t.Error("an empty window object must not count as a reading")
	}
	if !museHasQuota(&MuseSubscription{Window: &museWindow{UsedPercent: 12}}) {
		t.Error("a window with a percentage is a reading")
	}
	if !museHasQuota(&MuseSubscription{Weekly: &museWindow{WindowDurationMins: 300}}) {
		t.Error("a window with a duration is a reading")
	}
	if museHasQuota(nil) {
		t.Error("nil subscription is not a reading")
	}
}

// A frame with a real weekly reading but a placeholder window must not record
// the window at 0%: that wipes the cycle's true value and books the next real
// reading as one large delta.
func TestBuildMuseSnapshotSkipsPlaceholderWindow(t *testing.T) {
	sub, err := ParseMuseSubscriptionEvents([]string{
		`{"subscription":{"tier":"pro","weekly":{"resets_at":"1789344000","used_percent":"41"},"window":{}}}`,
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	snap := BuildMuseSnapshot(sub, "m", "{}", time.Unix(1789000000, 0).UTC())
	for _, q := range snap.Quotas {
		if q.Name == MuseQuotaWindow5H {
			t.Fatalf("placeholder window recorded as a quota: %+v", q)
		}
	}
	if snap.WindowUsedPct != 0 || snap.WindowResetsAt != nil {
		t.Fatalf("placeholder window leaked into the snapshot: used=%v resetsAt=%v",
			snap.WindowUsedPct, snap.WindowResetsAt)
	}
	if len(snap.Quotas) != 1 || snap.Quotas[0].Name != MuseQuotaWeekly {
		t.Fatalf("quotas = %+v, want the weekly reading alone", snap.Quotas)
	}
}

func TestTruncateMuseDetailIsRuneSafe(t *testing.T) {
	long := strings.Repeat("é", museDetailMaxRunes+20)
	got := truncateMuseDetail(long)
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a multi-byte rune")
	}
	if n := utf8.RuneCountInString(got); n != museDetailMaxRunes {
		t.Fatalf("rune count = %d, want %d", n, museDetailMaxRunes)
	}
	if short := truncateMuseDetail("ok"); short != "ok" {
		t.Fatalf("short string altered: %q", short)
	}
}

// ScanLines strips CR, so a CRLF stream rebuilds shorter than it arrived and
// truncation cannot be inferred from the rebuilt buffer.
func TestReadMuseSubscriptionStreamReportsCRLFTruncation(t *testing.T) {
	var b strings.Builder
	for b.Len() <= museMaxBodyBytes {
		b.WriteString("data: {\"delta\":\"" + strings.Repeat("x", 200) + "\"}\r\n")
	}
	_, _, err := readMuseSubscriptionStream(strings.NewReader(b.String()))
	if err == nil {
		t.Fatal("expected an error for a truncated CRLF stream")
	}
	if strings.Contains(err.Error(), "carried no subscription usage") {
		t.Fatalf("truncation misreported as missing usage: %v", err)
	}
}
