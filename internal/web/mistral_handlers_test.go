package web

import (
	"context"
	"encoding/json"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMistralReporting(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now().UTC()
	amount := 1.25
	snap := &api.MistralSnapshot{Identity: "test", CapturedAt: now, Status: "ok", Quotas: []api.MistralQuota{{Name: "api_included", Used: 0, Limit: 10, Currency: "EUR", CapturedAt: now}, {Name: "vibe_included", Used: 50, Limit: 100, Currency: "EUR", Utilization: 50, CapturedAt: now}}, Billing: &api.MistralBilling{Amount: &amount, Currency: "GBP", Status: "ok", CapturedAt: now, PeriodStart: now}}
	if e = db.SaveMistral(context.Background(), snap); e != nil {
		t.Fatal(e)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true, SyntheticAPIKey: "syn_test", PollInterval: 120 * time.Second})
	current := h.buildMistralCurrent()
	qs := current["quotas"].([]interface{})
	if len(qs) != 2 || qs[0].(map[string]interface{})["currency"] != "EUR" {
		t.Fatalf("current=%v", current)
	}
	if b := current["billing"].(*api.MistralBilling); b.Currency != "GBP" || *b.Amount != 1.25 {
		t.Fatalf("billing=%+v", b)
	}
	meters := normalizeQuotas(current, 80, 95)
	if len(meters) != 2 || meters[0].Currency != "EUR" || meters[0].Key != "api_included" {
		t.Fatalf("meters=%+v", meters)
	}
	for name, fn := range map[string]http.HandlerFunc{"current": h.Current, "history": h.History, "cycles": h.Cycles, "summary": h.Summary, "insights": h.Insights, "logging-history": h.LoggingHistory, "cycle-overview": h.CycleOverview} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			fn(w, httptest.NewRequest("GET", "/api/"+name+"?provider=mistral", nil))
			if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for name, fn := range map[string]http.HandlerFunc{"current": h.Current, "history": h.History, "summary": h.Summary, "insights": h.Insights} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("GET", "/api/"+name+"?provider=both", nil))
		var result map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["mistral"] == nil {
			t.Errorf("aggregate %s omitted Mistral: %s", name, w.Body.String())
		}
	}

	if e = db.SetSetting("mistral_status", "reconnect"); e != nil {
		t.Fatal(e)
	}
	current = h.buildMistralCurrent()
	if current["status"] != "reconnect" || current["billing"].(*api.MistralBilling).Status != "stale" {
		t.Fatal(current)
	}
}

func TestMistralHideBillingKeepsAllowanceStatus(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	snap := &api.MistralSnapshot{Identity: "test", CapturedAt: now, Status: "partial", Quotas: []api.MistralQuota{
		{Name: "api_included", Used: 1, Limit: 10, Currency: "EUR", CapturedAt: now},
		{Name: "vibe_included", Used: 2, Limit: 20, Currency: "EUR", CapturedAt: now},
	}}
	if err := db.SaveMistral(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true})
	if current := h.buildMistralCurrent(); current["showBilling"] != true || current["status"] != "partial" {
		t.Fatalf("billing should be visible by default: %v", current)
	}
	if err := db.SetSetting("provider_settings", `{"mistral":{"show_payg":"false"}}`); err != nil {
		t.Fatal(err)
	}
	current := h.buildMistralCurrent()
	if current["showBilling"] != false || current["status"] != "ok" {
		t.Fatalf("hidden billing should not flag working allowances: %v", current)
	}
	// A hidden optional endpoint must not conceal missing allowance data.
	snap.Quotas = snap.Quotas[:1]
	snap.CapturedAt = now.Add(time.Second)
	snap.Quotas[0].CapturedAt = snap.CapturedAt
	if err := db.SaveMistral(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	if got := h.buildMistralCurrent()["status"]; got != "partial" {
		t.Fatalf("stale allowance hidden: %v", got)
	}
	if err := db.SetSetting("mistral_status", "reconnect"); err != nil {
		t.Fatal(err)
	}
	if got := h.buildMistralCurrent()["status"]; got != "reconnect" {
		t.Fatalf("real connection failure hidden: %v", got)
	}
}

func TestMistralSettingsAndDiscovery(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cfg := &config.Config{MistralAuthCookie: "environment-secret"}
	if e = db.SetSetting("provider_settings", `{"mistral":{"enabled":false,"browser":"firefox","browser_profile":"/synthetic/profile::container=2","auth_mode":"automatic"}}`); e != nil {
		t.Fatal(e)
	}
	ApplyProviderSettingsFromDB(db, cfg, nil)
	if cfg.HasProvider("mistral") || cfg.MistralBrowser != "firefox" || cfg.MistralAuthMode != "automatic" {
		t.Fatalf("settings did not override environment")
	}
	h := NewHandler(db, nil, nil, nil, cfg)
	if !h.tryAutoDetect("mistral") || !cfg.HasProvider("mistral") {
		t.Fatal("enabling automatic discovery failed")
	}
	// This operation only schedules discovery; no importer/browser API is called.
	persisted, _ := db.GetSetting("provider_settings")
	if !strings.Contains(persisted, `"enabled":true`) {
		t.Fatal("enable not persisted")
	}
}

// The menubar shows quota.time_until_reset, falling back to the raw reset_at
// timestamp when it is absent. Without a formatted countdown Mistral rows
// displayed "2026-10-01T00:00:00Z" where every other provider shows "6d 11h".
func TestMistralQuotaCarriesFormattedCountdown(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now().UTC()
	reset := now.Add(50*time.Hour + 30*time.Minute)
	snap := &api.MistralSnapshot{Identity: "t", CapturedAt: now, Status: "ok", Quotas: []api.MistralQuota{
		{Name: "api_included", Used: 1, Limit: 10, Currency: "EUR", CapturedAt: now, ResetsAt: &reset},
	}}
	if e = db.SaveMistral(context.Background(), snap); e != nil {
		t.Fatal(e)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true})
	entry := h.buildMistralCurrent()["quotas"].([]interface{})[0].(map[string]interface{})
	got, _ := entry["timeUntilReset"].(string)
	if got != "2d 2h" {
		t.Fatalf("timeUntilReset=%q, want %q", got, "2d 2h")
	}
	if m := normalizeQuotas(h.buildMistralCurrent(), 80, 95); len(m) != 1 || m[0].TimeUntilReset != "2d 2h" {
		t.Fatalf("meter TimeUntilReset=%+v", m)
	}
}

// An unavailable pay-as-you-go balance is not worth a line of its own in the
// menubar: the spend card already reports it on the dashboard.
func TestMistralBillingSubtitleOmitsUnavailable(t *testing.T) {
	now := time.Now().UTC()
	if got := mistralBillingSubtitle(map[string]interface{}{"billing": &api.MistralBilling{Status: "unavailable"}}); got != "" {
		t.Fatalf("unavailable spend produced subtitle %q, want empty", got)
	}
	if got := mistralBillingSubtitle(map[string]interface{}{}); got != "" {
		t.Fatalf("missing billing produced subtitle %q, want empty", got)
	}
	amount := 1.5
	if got := mistralBillingSubtitle(map[string]interface{}{"showBilling": false, "billing": &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "ok", CapturedAt: now}}); got != "" {
		t.Fatalf("hidden spend produced subtitle %q", got)
	}
	if got := mistralBillingSubtitle(map[string]interface{}{"billing": &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "ok", CapturedAt: now}}); got != "Pay-as-you-go: 1.50 EUR" {
		t.Fatalf("subtitle=%q", got)
	}
	if got := mistralBillingSubtitle(map[string]interface{}{"billing": &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "stale", CapturedAt: now}}); got != "Pay-as-you-go: 1.50 EUR (stale)" {
		t.Fatalf("stale subtitle=%q", got)
	}
}

// The Mistral page carries a container for its one-time "how Mistral tracking
// works" panel. It starts hidden; the script fills and reveals it until the
// user dismisses it.
func TestMistralPageHasIntroPanel(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{MistralEnabled: true})
	rr := httptest.NewRecorder()
	h.Dashboard(rr, httptest.NewRequest(http.MethodGet, "/?provider=mistral", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="mistral-intro"`) {
		t.Fatal("Mistral page is missing the intro panel container")
	}
	if !strings.Contains(body, `id="mistral-intro" role="note" hidden`) {
		t.Fatal("intro panel must start hidden so a dismissed panel never flashes")
	}
}
