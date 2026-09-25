package metrics

import (
	"context"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"testing"
	"time"
)

func TestMistralMetrics(t *testing.T) {
	s, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now().UTC()
	amount := 2.5
	if e = s.SaveMistral(context.Background(), &api.MistralSnapshot{Identity: "source", CapturedAt: now, Status: "ok", Quotas: []api.MistralQuota{{Name: "api_included", Utilization: 25, CapturedAt: now}}, Billing: &api.MistralBilling{Amount: &amount, Currency: "EUR", CapturedAt: now}}); e != nil {
		t.Fatal(e)
	}
	m := New()
	m.Scrape(s, time.Minute)
	families, e := m.Gather().Gather()
	if e != nil {
		t.Fatal(e)
	}
	assertGaugeValue(t, families, "onwatch_billing_spend", map[string]string{"provider": "mistral", "account_id": "source", "currency": "EUR"}, 2.5)
	if e = s.SetSetting("mistral_status", "reconnect"); e != nil {
		t.Fatal(e)
	}
	m.Scrape(s, time.Minute)
	families, e = m.Gather().Gather()
	if e != nil {
		t.Fatal(e)
	}
	assertGaugeValue(t, families, "onwatch_agent_healthy", map[string]string{"provider": "mistral", "account_id": "source"}, 0)
	if e = s.SetSetting("mistral_identity", "different"); e != nil {
		t.Fatal(e)
	}
	m.Scrape(s, time.Minute)
	families, e = m.Gather().Gather()
	if e != nil {
		t.Fatal(e)
	}
	assertMetricFamilyMissing(t, families, "onwatch_billing_spend")
}
