package api

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestMistralSubscription(t *testing.T) {
	page := `<script>self.__next_f.push([1,"1:{\"budget\":{\"api_budget\":{\"usage_percentage\":25,\"initial_budget\":10,\"currency\":\"EUR\",\"reset_at\":\"2026-10-01T00:00:00Z\"},\"vibe_budget\":{\"usage_percentage\":50,\"initial_budget\":100,\"currency\":\"EUR\"}}}\n"])</script>`
	q, err := ParseMistralSubscription([]byte(page), time.Now())
	if err != nil || len(q) != 2 {
		t.Fatalf("quotas=%v err=%v", q, err)
	}
	if q[0].Currency != "EUR" || q[0].Limit != 10 || q[0].ResetsAt == nil || q[1].ResetsAt != nil {
		t.Fatalf("incorrect quotas: %+v", q)
	}
}

func TestMistralBillingPaidOnly(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       float64
		valid      bool
	}{
		{"paid", `{"currency":"EUR","completion":{"models":{"x":{"input":[{"value":100,"value_paid":2,"billing_metric":"tokens","billing_group":"x"}]}}},"prices":[{"billing_metric":"tokens","billing_group":"x","price":"0.5"}]}`, 1, true},
		{"missing paid", `{"currency":"EUR","completion":{"models":{"x":{"input":[{"value":100,"billing_metric":"tokens","billing_group":"x"}]}}},"prices":[{"billing_metric":"tokens","billing_group":"x","price":"0.5"}]}`, 0, false},
		{"missing price", `{"currency":"USD","completion":{"models":{"x":{"input":[{"value_paid":2,"billing_metric":"tokens","billing_group":"x"}]}}}}`, 0, false},
		{"zero", `{"currency":"GBP","completion":{"models":{"x":{"input":[{"value_paid":0,"billing_metric":"tokens","billing_group":"x"}]}}},"prices":[{"billing_metric":"tokens","billing_group":"x","price":"0.5"}]}`, 0, true},
		{"signed adjustment", `{"currency":"EUR","completion":{"models":{"x":{"input":[{"value_paid":-2,"billing_metric":"tokens","billing_group":"x"}]}}},"prices":[{"billing_metric":"tokens","billing_group":"x","price":"0.5"}]}`, -1, true},
		{"ambiguous vibe", `{"currency":"EUR","vibe_usage":9}`, 0, false},
		{"empty", `{}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseMistralBilling([]byte(tc.body), time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("billing=%+v err=%v", b, err)
			}
			if tc.valid && (b.Amount == nil || *b.Amount != tc.want) {
				t.Fatalf("billing=%+v", b)
			}
		})
	}
}

func TestMistralVibeFallback(t *testing.T) {
	q, e := ParseMistralVibe([]byte(`[{"result":{"data":{"json":{"usage_percentage":25,"reset_at":"2026-10-01T00:00:00Z"}}}}]`), time.Now())
	if e != nil || q.Name != "vibe_included" || q.Utilization != 25 || !q.PercentOnly || q.Limit != 0 {
		t.Fatalf("q=%+v err=%v", q, e)
	}
}

func TestMistralRenderedFallback(t *testing.T) {
	page := `<html><script>fake: €999 €999</script><h2>Included API usage</h2><p>Resets on the first day of each calendar month</p><div>€0</div><div>€10.00</div><h2>Included Vibe Code usage</h2><div>€100.00</div><div>€100.00</div><h2>PAY-AS-YOU-GO</h2>€99</html>`
	now := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	q, e := ParseMistralSubscription([]byte(page), now)
	if e != nil || len(q) != 2 || q[0].Used != 0 || q[1].Utilization != 100 || q[0].ResetsAt.Year() != 2027 || q[1].ResetsAt != nil {
		t.Fatalf("%+v %v", q, e)
	}
}

func TestMistralExplicitBillingAndVibe(t *testing.T) {
	for _, tc := range []struct {
		body   string
		amount float64
	}{
		{`{"currency":"EUR","usage_charge_total":"1.25","vibe_usage":8,"start_date":"2026-09-01","end_date":"2026-10-01"}`, 1.25},
		{`{"currency":"GBP","vibe":{"input":[{"value_paid":3,"billing_metric":"vibe","billing_group":"overage"}]},"vibe_usage":8,"prices":[{"price":"0.5","billing_metric":"vibe","billing_group":"overage"}]}`, 1.5},
	} {
		b, e := ParseMistralBilling([]byte(tc.body), time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))
		if e != nil || b.Amount == nil || *b.Amount != tc.amount {
			t.Fatalf("%+v %v", b, e)
		}
	}
}

func TestMistralUnknownAmounts(t *testing.T) {
	data, e := json.Marshal(MistralQuota{Name: "vibe_included", PercentOnly: true, Utilization: 10})
	if e != nil || !bytes.Contains(data, []byte(`"used":null`)) || !bytes.Contains(data, []byte(`"limit":null`)) {
		t.Fatalf("unknown amounts serialized as zero: %s %v", data, e)
	}
}
