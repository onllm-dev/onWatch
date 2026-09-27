package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// Issue #137: the DeepSeek and Moonshot tabs rendered no statistics.

func newBalanceTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	cfg := &config.Config{
		DeepSeekAPIKey: "sk-test",
		MoonshotAPIKey: "sk-test",
		PollInterval:   60 * time.Second,
		AdminUser:      "admin",
		AdminPass:      "test",
	}
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetDeepSeekTracker(tracker.NewDeepSeekTracker(s, nil))
	h.SetMoonshotTracker(tracker.NewMoonshotTracker(s, nil))
	return h, s
}

func insertDeepSeekUSD(t *testing.T, h *Handler, s *store.Store, at time.Time, total float64) {
	t.Helper()
	snap := &api.DeepSeekSnapshot{CapturedAt: at, IsAvailable: true, Currency: "USD", TotalBalance: total, ToppedUpBalance: total}
	if _, err := s.InsertDeepSeekSnapshot(snap); err != nil {
		t.Fatalf("insert DeepSeek snapshot: %v", err)
	}
	if err := h.deepseekTracker.Process(snap); err != nil {
		t.Fatalf("track DeepSeek snapshot: %v", err)
	}
}

func getJSON(t *testing.T, h *Handler, target string, out interface{}) {
	t.Helper()
	routes := map[string]http.HandlerFunc{
		"/api/insights":        h.Insights,
		"/api/summary":         h.Summary,
		"/api/cycles":          h.Cycles,
		"/api/cycle-overview":  h.CycleOverview,
		"/api/logging-history": h.LoggingHistory,
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	route, ok := routes[req.URL.Path]
	if !ok {
		t.Fatalf("no route for %s", req.URL.Path)
	}
	rec := httptest.NewRecorder()
	route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("GET %s: decode: %v", target, err)
	}
}

// A USD account must not be looked up as CNY when no currency is requested.
func TestDeepSeekDefaultsToTheTrackedCurrency(t *testing.T) {
	h, s := newBalanceTestHandler(t)
	now := time.Now().UTC()
	insertDeepSeekUSD(t, h, s, now.Add(-2*time.Hour), 4.10)
	insertDeepSeekUSD(t, h, s, now.Add(-time.Hour), 4.02)

	var insights insightsResponse
	getJSON(t, h, "/api/insights?provider=deepseek", &insights)
	if len(insights.Stats) == 0 || !strings.HasPrefix(insights.Stats[0].Value, "$") {
		t.Fatalf("insights stats = %+v, want USD balance stats", insights.Stats)
	}

	var summary map[string]map[string]interface{}
	getJSON(t, h, "/api/summary?provider=deepseek", &summary)
	if summary["balance"]["currency"] != "USD" || summary["balance"]["currentBalance"] != 4.02 {
		t.Fatalf("summary balance = %v, want the USD balance 4.02", summary["balance"])
	}

	var overview map[string]interface{}
	getJSON(t, h, "/api/cycle-overview?provider=deepseek&groupBy=balance", &overview)
	cyclesOut, _ := overview["cycles"].([]interface{})
	if len(cyclesOut) == 0 || overview["currency"] != "USD" {
		t.Fatalf("cycle overview = %v, want the USD cycle", overview)
	}
	if first, _ := cyclesOut[0].(map[string]interface{}); first["cycleId"] == nil {
		t.Fatalf("cycle row %v has no cycleId for the dashboard table", first)
	}
	// Balance cycles carry only a spend delta: no per-quota columns, and the
	// combined view must agree with the provider view.
	if names, _ := overview["quotaNames"].([]interface{}); len(names) != 0 {
		t.Fatalf("cycle overview quotaNames = %v, want none", names)
	}
	if both := h.deepseekCycleOverview(h.deepseekCurrency("")); len(both["quotaNames"].([]string)) != 0 {
		t.Fatalf("combined overview quotaNames = %v, want none", both["quotaNames"])
	}

	var logs map[string]interface{}
	getJSON(t, h, "/api/logging-history?provider=deepseek&range=1", &logs)
	if logs["currency"] != "USD" {
		t.Fatalf("logging history currency = %v, want USD", logs["currency"])
	}

	var cycles []interface{}
	getJSON(t, h, "/api/cycles?provider=deepseek", &cycles)
	if len(cycles) == 0 {
		t.Fatal("cycles empty, want the USD cycle")
	}
}

// Logging history must use the shared crossQuotas row shape the dashboard
// table reads.
func TestBalanceLoggingHistoryUsesCrossQuotas(t *testing.T) {
	h, s := newBalanceTestHandler(t)
	now := time.Now().UTC().Add(-time.Hour)
	insertDeepSeekUSD(t, h, s, now, 4.02)
	if _, err := s.InsertMoonshotSnapshot(&api.MoonshotSnapshot{CapturedAt: now, AvailableBalance: 19.47, VoucherBalance: 5, CashBalance: 14.47}); err != nil {
		t.Fatalf("insert Moonshot snapshot: %v", err)
	}

	for provider, want := range map[string]map[string]float64{
		"deepseek": {"total_balance": 4.02, "granted_balance": 0, "topped_up_balance": 4.02},
		"moonshot": {"available_balance": 19.47, "voucher_balance": 5, "cash_balance": 14.47},
	} {
		var resp struct {
			QuotaNames []string `json:"quotaNames"`
			Logs       []struct {
				CrossQuotas []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"crossQuotas"`
			} `json:"logs"`
		}
		getJSON(t, h, "/api/logging-history?provider="+provider+"&range=1", &resp)
		if len(resp.QuotaNames) != len(want) {
			t.Fatalf("%s quotaNames = %v, want %d balance fields", provider, resp.QuotaNames, len(want))
		}
		if len(resp.Logs) != 1 {
			t.Fatalf("%s logs = %d, want 1", provider, len(resp.Logs))
		}
		got := map[string]float64{}
		for _, cq := range resp.Logs[0].CrossQuotas {
			got[cq.Name] = cq.Value
		}
		for name, v := range want {
			if gv, ok := got[name]; !ok || gv != v {
				t.Errorf("%s %s = %v (present %v), want %v", provider, name, gv, ok, v)
			}
		}
	}
}

func TestBalanceProvidersAreWiredIntoTheDashboard(t *testing.T) {
	js := readStaticFile(t, "static/app.js")
	for _, want := range []string{
		"document.getElementById('quota-grid-deepseek')",
		"document.getElementById('quota-grid-moonshot')",
		"renderBalanceCards(",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js is missing %q", want)
		}
	}
}
