package api

import (
	"strings"
	"testing"
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
