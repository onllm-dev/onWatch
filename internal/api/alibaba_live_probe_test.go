package api

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAlibabaLivePollWithThoriumCookies(t *testing.T) {
	if os.Getenv("ALIBABA_LIVE") != "1" {
		t.Skip("set ALIBABA_LIVE=1 and ALIBABA_LIVE_COOKIES to run the live Alibaba poll")
	}
	path := os.Getenv("ALIBABA_LIVE_COOKIES")
	if path == "" {
		t.Fatal("ALIBABA_LIVE_COOKIES must point at a Netscape cookies.txt export")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cookies file: %v", err)
	}
	c := NewAlibabaClient(string(raw), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	snap, err := c.FetchSnapshot(ctx)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) == 0 {
		t.Fatalf("expected at least one quota window, got %+v", snap)
	}
	t.Logf("quotas=%d spec=%s status=%s days=%d instance=%s", len(snap.Quotas), snap.SpecCode, snap.Status, snap.RemainingDays, snap.InstanceID)
	for _, q := range snap.Quotas {
		t.Logf("  %s util=%.4f", q.Name, q.Utilization)
	}
}
