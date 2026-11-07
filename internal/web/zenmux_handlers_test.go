package web

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func stringPointerZenmux(v string) *string { return &v }

func newZenmuxPublicFixture(t *testing.T) (*Handler, *config.Config) {
	t.Helper()
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UTC()
	expiry := now.Add(30 * 24 * time.Hour)
	zero, cap := 0.0, 34560.0
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: now, Plan: &api.ZenmuxPlan{Tier: stringPointerZenmux("ultra"), ExpiresAt: &expiry}, Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &cap}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", PollInterval: time.Minute}
	h := NewHandler(db, nil, nil, nil, cfg)
	return h, cfg
}

func TestZenmuxPublicCurrent(t *testing.T) {
	h, _ := newZenmuxPublicFixture(t)
	const cap = 34560.0
	current := httptest.NewRecorder()
	h.Current(current, httptest.NewRequest(http.MethodGet, "/api/current?provider=zenmux", nil))
	if current.Code != http.StatusOK {
		t.Fatalf("current status %d", current.Code)
	}
	var payload struct {
		Plan   *api.ZenmuxPlan          `json:"plan"`
		Quotas []map[string]interface{} `json:"quotas"`
	}
	if err := json.Unmarshal(current.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Plan == nil || payload.Plan.ExpiresAt == nil {
		t.Fatal("plan expiration missing")
	}
	byName := map[string]map[string]interface{}{}
	for _, q := range payload.Quotas {
		byName[q["name"].(string)] = q
	}
	if byName["five_hour"]["utilization"] != float64(0) {
		t.Fatal("known zero lost")
	}
	if byName["weekly"]["utilization"] != nil {
		t.Fatal("missing utilization became zero")
	}
	monthly := byName["monthly"]
	if monthly == nil || monthly["limit"] != cap {
		t.Fatal("monthly limit missing")
	}
	for _, k := range []string{"used", "remaining", "utilization", "resetsAt"} {
		if v, ok := monthly[k]; ok && v != nil {
			t.Fatalf("invented monthly %s", k)
		}
	}
}

func TestZenmuxPublicHistory(t *testing.T) {
	h, _ := newZenmuxPublicFixture(t)
	const cap = 34560.0
	hist := httptest.NewRecorder()
	h.History(hist, httptest.NewRequest(http.MethodGet, "/api/history?provider=zenmux&range=6h", nil))
	if hist.Code != http.StatusOK {
		t.Fatalf("history status %d", hist.Code)
	}
	var history []struct {
		Quotas []map[string]interface{} `json:"quotas"`
	}
	if err := json.Unmarshal(hist.Body.Bytes(), &history); err != nil || len(history) != 1 {
		t.Fatal("history missing")
	}
	var monthlyHistory map[string]interface{}
	for _, q := range history[0].Quotas {
		if q["name"] == "monthly" {
			monthlyHistory = q
		}
	}
	if monthlyHistory == nil || monthlyHistory["limit"] != cap {
		t.Fatal("monthly limit history missing")
	}
	for _, k := range []string{"used", "remaining", "utilization", "resetsAt"} {
		if v, ok := monthlyHistory[k]; ok && v != nil {
			t.Fatalf("invented monthly history %s", k)
		}
	}
}

func TestZenmuxPublicAggregateCurrent(t *testing.T) {
	h, cfg := newZenmuxPublicFixture(t)
	cfg.SyntheticAPIKey = "synthetic-provider"
	both := httptest.NewRecorder()
	h.Current(both, httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil))
	var aggregate map[string]json.RawMessage
	if both.Code != http.StatusOK || json.Unmarshal(both.Body.Bytes(), &aggregate) != nil || aggregate["zenmux"] == nil {
		t.Fatal("aggregate current omitted Zenmux")
	}
}

func TestZenmuxPublicAggregateHistory(t *testing.T) {
	h, cfg := newZenmuxPublicFixture(t)
	cfg.SyntheticAPIKey = "synthetic-provider"
	both := httptest.NewRecorder()
	h.History(both, httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=6h", nil))
	var aggregate map[string]json.RawMessage
	if both.Code != http.StatusOK || json.Unmarshal(both.Body.Bytes(), &aggregate) != nil || aggregate["zenmux"] == nil {
		t.Fatal("aggregate history omitted Zenmux")
	}
}

func TestZenmuxPublicDashboard(t *testing.T) {
	h, _ := newZenmuxPublicFixture(t)
	dash := httptest.NewRecorder()
	h.Dashboard(dash, httptest.NewRequest(http.MethodGet, "/?provider=zenmux", nil))
	if dash.Code != http.StatusOK || !strings.Contains(dash.Body.String(), `id="quota-grid-zenmux"`) {
		t.Fatal("Zenmux dashboard unavailable")
	}
}

func TestZenmuxDisplayModeCurrentAndAggregate(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	used, limit := 0.92, 100.0
	reset := time.Now().Add(time.Hour).UTC()
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &used, MaxFlows: &limit, ResetsAt: &reset}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &limit}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", SyntheticAPIKey: "synthetic-provider", DisplayMode: "available"}
	h := NewHandler(db, nil, nil, nil, cfg)
	for _, provider := range []string{"zenmux", "both"} {
		rr := httptest.NewRecorder()
		h.Current(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider="+provider, nil))
		var body map[string]json.RawMessage
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &body) != nil {
			t.Fatalf("%s: %d", provider, rr.Code)
		}
		if provider == "both" {
			if err := json.Unmarshal(body["zenmux"], &body); err != nil {
				t.Fatal(err)
			}
		}
		var quotas []map[string]interface{}
		if err := json.Unmarshal(body["quotas"], &quotas); err != nil {
			t.Fatal(err)
		}
		pct, ok := quotas[0]["cardPercent"].(float64)
		if quotas[0]["utilization"] != float64(92) || !ok || pct < 7.9 || pct > 8.1 {
			t.Fatalf("%s: %+v", provider, quotas[0])
		}
		for _, q := range quotas[1:] {
			if _, ok := q["cardPercent"]; ok {
				t.Fatalf("invented percentage: %+v", q)
			}
		}
	}
}

func TestZenmuxCardsModeStatusAndReset(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');
const document={getElementById:()=>null,querySelector:()=>null,querySelectorAll:()=>[],addEventListener:()=>{},documentElement:{}};
const window={addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}};const sessionStorage=localStorage;const navigator={};
` + string(source) + `
const future=new Date(Date.now()+3600000).toISOString();
const past=new Date(Date.now()-3600000).toISOString();
const rows=[{name:'five_hour',utilization:92,cardPercent:8,cardLabel:'Remaining',resetsAt:future},{name:'weekly',resetsAt:past},{name:'monthly',limit:42}];
for (const html of [zenmuxCardsHTML(rows),renderProviderKPIHTML(normalizeBothQuotas('zenmux',{quotas:rows}),'zenmux')]) {
  assert(html.includes('8.0%')); assert(html.includes('Remaining')); assert(html.includes('data-status="critical"'));
  assert(html.includes('class="reset-time"')); assert(html.includes('data-reset-at="'+future+'"')); assert(html.includes('class="countdown zenmux-countdown"'));
  assert(!html.includes('data-reset-at="'+past+'"')); assert(!html.includes('id="countdown-zenmux'));
}
`
	runNodeScript(t, script)
}

func TestZenmuxEstimatePublicSafety(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		mutate func([]*api.ZenmuxSnapshot)
		want   bool
	}{
		{"valid", func(_ []*api.ZenmuxSnapshot) {}, true},
		{"same usage", func(s []*api.ZenmuxSnapshot) {
			for _, p := range s {
				v := 0.2
				p.Quota5Hour.UsagePercentage = &v
			}
		}, true},
		{"reset changed", func(s []*api.ZenmuxSnapshot) { v := now.Add(2 * time.Hour); s[2].Quota5Hour.ResetsAt = &v }, false},
		{"missing reset", func(s []*api.ZenmuxSnapshot) { s[2].Quota5Hour.ResetsAt = nil }, false},
		{"expired reset", func(s []*api.ZenmuxSnapshot) {
			v := now.Add(-time.Minute)
			for _, p := range s {
				p.Quota5Hour.ResetsAt = &v
			}
		}, false},
		{"changed limit", func(s []*api.ZenmuxSnapshot) { v := 200.0; s[2].Quota5Hour.MaxFlows = &v }, false},
		{"missing limit", func(s []*api.ZenmuxSnapshot) { s[2].Quota5Hour.MaxFlows = nil }, false},
		{"missing usage", func(s []*api.ZenmuxSnapshot) { s[2].Quota5Hour.UsagePercentage = nil }, false},
		{"gap", func(s []*api.ZenmuxSnapshot) {
			s[1].CapturedAt = s[0].CapturedAt.Add(time.Second)
			s[2].CapturedAt = s[0].CapturedAt.Add(2 * time.Second)
		}, false},
		{"stale", func(s []*api.ZenmuxSnapshot) {
			for _, p := range s {
				p.CapturedAt = p.CapturedAt.Add(-12 * time.Minute)
			}
		}, false},
		{"insufficient span", func(s []*api.ZenmuxSnapshot) {
			for i, p := range s {
				p.CapturedAt = now.Add(time.Duration(i-5) * time.Minute)
			}
		}, false},
		{"decreasing", func(s []*api.ZenmuxSnapshot) { v := 0.01; s[3].Quota5Hour.UsagePercentage = &v }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			reset := now.Add(time.Hour)
			limit := 100.0
			var snaps []*api.ZenmuxSnapshot
			for i := 0; i < 6; i++ {
				value := 0.2 + float64(i)*0.02
				snaps = append(snaps, &api.ZenmuxSnapshot{CapturedAt: now.Add(time.Duration(i-5) * 5 * time.Minute), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &value, ResetsAt: &reset, MaxFlows: &limit}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &limit}})
			}
			tc.mutate(snaps)
			for _, s := range snaps {
				if _, err := db.InsertZenmuxSnapshot(s); err != nil {
					t.Fatal(err)
				}
			}
			h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", PollInterval: 5 * time.Minute})
			rr := httptest.NewRecorder()
			h.Current(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider=zenmux", nil))
			var body struct {
				Quotas []map[string]interface{} `json:"quotas"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			_, got := body.Quotas[0]["ratePerHour"]
			if got != tc.want {
				t.Fatalf("estimate present %v, want %v: %+v", got, tc.want, body.Quotas[0])
			}
			for _, q := range body.Quotas[1:] {
				if _, ok := q["ratePerHour"]; ok {
					t.Fatalf("unexpected estimate %+v", q)
				}
			}
		})
	}
}

// These cases cover the serialized public response from real stored snapshots.
func TestZenmuxEstimatePublicNumbers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		first      float64
		last       float64
		resetAfter time.Duration
		wantRate   float64
		wantHours  float64
	}{
		{"exhaustion before reset", 0.50, 0.75, time.Hour, 60, 0.4166666666666667},
		{"exhaustion at reset", 0.50, 0.75, 25 * time.Minute, 60, 0},
		{"exhaustion after reset", 0.50, 0.75, 15 * time.Minute, 60, 0},
		{"zero rate", 0.50, 0.50, time.Hour, 0, 0},
		{"already fully consumed", 0.75, 1.00, time.Hour, 60, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			reset, limit := now.Add(tc.resetAfter), 100.0
			for i := 0; i < 6; i++ {
				value := tc.first + (tc.last-tc.first)*float64(i)/5
				window := &api.ZenmuxWindow{UsagePercentage: &value, MaxFlows: &limit, ResetsAt: &reset}
				_, err := db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: now.Add(time.Duration(i-5) * 5 * time.Minute), Quota5Hour: window, Quota7Day: window})
				if err != nil {
					t.Fatal(err)
				}
			}
			h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", PollInterval: 5 * time.Minute})
			rr := httptest.NewRecorder()
			h.Current(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider=zenmux", nil))
			var body struct {
				Quotas []map[string]interface{} `json:"quotas"`
			}
			if rr.Code != http.StatusOK {
				t.Fatalf("current status %d: %s", rr.Code, rr.Body.String())
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Quotas) != 2 {
				t.Fatalf("want both rolling windows, got %+v", body.Quotas)
			}
			for i, name := range []string{"five_hour", "weekly"} {
				q := body.Quotas[i]
				if q["name"] != name {
					t.Fatalf("quota %d: %+v", i, q)
				}
				rate, ok := q["ratePerHour"].(float64)
				if !ok || math.Abs(rate-tc.wantRate) > 1e-8 {
					t.Errorf("%s rate = %v, want %v", name, q["ratePerHour"], tc.wantRate)
				}
				hours, exists := q["exhaustHours"]
				if tc.wantHours == 0 {
					if exists {
						t.Errorf("%s should suppress exhaustHours, got %v", name, hours)
					}
				} else if value, ok := hours.(float64); !ok || math.Abs(value-tc.wantHours) > 1e-8 {
					t.Errorf("%s exhaustHours = %v, want %v", name, hours, tc.wantHours)
				}
			}
		})
	}
}

// A fixed now at the existing estimator seam proves the exact equality boundary;
// a live handler clock has necessarily advanced by the time it handles a request.
func TestZenmuxEstimateAtExactReset(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	limit := 100.0
	for _, tc := range []struct {
		name      string
		shift     time.Duration
		wantHours float64
	}{
		{"before", time.Second, 0.4166666666666667},
		{"equal", 0, 0},
		{"after", -time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset := now.Add(25*time.Minute + tc.shift)
			var snapshots []*api.ZenmuxSnapshot
			for i := 5; i >= 0; i-- {
				value := 0.50 + float64(i)*0.05
				snapshots = append(snapshots, &api.ZenmuxSnapshot{CapturedAt: now.Add(time.Duration(i-5) * 5 * time.Minute), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &value, MaxFlows: &limit, ResetsAt: &reset}})
			}
			rate, hours, ok := zenmuxEstimate(snapshots, "five_hour", now, 5*time.Minute)
			if !ok || math.Abs(rate-60) > 1e-8 || math.Abs(hours-tc.wantHours) > 1e-8 {
				t.Fatalf("rate=%v exhaustHours=%v ok=%v", rate, hours, ok)
			}
		})
	}
}

func TestZenmuxEstimateWeeklyIndependentOfFiveHour(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	weeklyReset, shortReset, earlierReset := now.Add(48*time.Hour), now.Add(time.Hour), now.Add(30*time.Minute)
	limit := 100.0
	for i := 0; i < 6; i++ {
		value := 0.2 + float64(i)*0.01
		reset := shortReset
		if i == 2 {
			reset = earlierReset
		}
		_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: now.Add(time.Duration(i-5) * 5 * time.Minute), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &value, MaxFlows: &limit, ResetsAt: &reset}, Quota7Day: &api.ZenmuxWindow{UsagePercentage: &value, MaxFlows: &limit, ResetsAt: &weeklyReset}})
		if err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", SyntheticAPIKey: "synthetic-provider", PollInterval: 5 * time.Minute})
	for _, provider := range []string{"zenmux", "both"} {
		rr := httptest.NewRecorder()
		h.Current(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider="+provider, nil))
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if provider == "both" {
			if err := json.Unmarshal(body["zenmux"], &body); err != nil {
				t.Fatal(err)
			}
		}
		var quotas []map[string]interface{}
		if err := json.Unmarshal(body["quotas"], &quotas); err != nil {
			t.Fatal(err)
		}
		if _, ok := quotas[0]["ratePerHour"]; ok {
			t.Fatalf("unsafe 5h estimate: %+v", quotas[0])
		}
		rate, ok := quotas[1]["ratePerHour"].(float64)
		if !ok || rate < 11.9 || rate > 12.1 {
			t.Fatalf("weekly estimate: %+v", quotas[1])
		}
	}
}

func TestZenmuxProviderIconEmbedded(t *testing.T) {
	icon, err := staticFS.ReadFile("static/icons/zenmux.svg")
	if err != nil || !strings.Contains(string(icon), "<svg") {
		t.Fatalf("Zenmux provider icon not embedded: %v", err)
	}
}

func TestZenmuxSettingsSecretAndLifecycle(t *testing.T) {
	cfg := &config.Config{}
	h, _, controller := newReconcileHandler(t, cfg, map[string]bool{})
	body := strings.NewReader(`{"provider_settings":{"zenmux":{"api_key":"synthetic-test-value"}}}`)
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, httptest.NewRequest(http.MethodPut, "/api/settings", body))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "synthetic-test-value") {
		t.Fatal("settings response leaked secret or failed")
	}
	status := httptest.NewRecorder()
	h.ProvidersStatus(status, httptest.NewRequest(http.MethodGet, "/api/providers/status", nil))
	var providers struct {
		Providers []ProviderStatus `json:"providers"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	configured, polling := false, false
	for _, provider := range providers.Providers {
		if provider.Key == "zenmux" {
			configured, polling = provider.Configured, provider.IsPolling
		}
	}
	if !configured || !polling {
		t.Fatal("public provider status did not report configured and polling")
	}
	get := httptest.NewRecorder()
	h.GetSettings(get, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if strings.Contains(get.Body.String(), "synthetic-test-value") {
		t.Fatal("settings GET leaked secret")
	}
	rotated := httptest.NewRecorder()
	h.UpdateSettings(rotated, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"provider_settings":{"zenmux":{"api_key":"synthetic-rotated"}}}`)))
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), "synthetic-rotated") {
		t.Fatal("key rotation failed or leaked key material")
	}
	// This mock controller cannot prove which credential a restarted agent uses.
	// Keep the lifecycle check at this boundary in addition to public status.
	if !containsString(controller.stopped, "zenmux") || !containsString(controller.started, "zenmux") {
		t.Fatal("key rotation did not restart the provider")
	}
}

func TestZenmuxDashboardRendersKnownAndUnknownValues(t *testing.T) {
	source, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert = require('node:assert/strict');
const document = {getElementById:()=>null,querySelector:()=>null,querySelectorAll:()=>[],addEventListener:()=>{},documentElement:{}};
const window = {addEventListener:()=>{},location:{},matchMedia:()=>({matches:false,addEventListener:()=>{}})};
const localStorage={getItem:()=>null,setItem:()=>{}}; const sessionStorage=localStorage; const navigator={};
` + string(source) + `
const rows = [
 {name:'five_hour',displayName:'5-Hour Flows',utilization:0,used:0,limit:800,remainingResets:0,usedValueUsd:1.88,maxValueUsd:26.27},
 {name:'weekly',displayName:'7-Day Flows',used:null,limit:null},
 {name:'monthly',displayName:'Monthly Flow Limit',limit:34560}
];
const html=zenmuxCardsHTML(rows);
assert(html.includes('0.0%')); assert(html.includes('Early resets remaining: 0'));
assert(html.includes('Usage unavailable')); assert(html.includes('34,560 flows')); assert(html.includes('Used value: $1.88')); assert(html.includes('Limit value: $26.27'));
assert(!html.includes('NaN')); assert(!html.includes('monthly reset'));
const charts=zenmuxHistoryDatasets([{capturedAt:'2026-11-01T00:00:00Z',quotas:[{name:'five_hour',utilization:0},{name:'weekly'}]},{capturedAt:'2026-11-01T00:01:00Z',quotas:[{name:'five_hour'}]}],'6h');
assert.equal(charts.length,1); assert.equal(charts[0].data[0].y,0); assert.equal(charts[0].data[1].y,null); assert.equal(charts[0].spanGaps,false);
const bothQuotas=normalizeBothQuotas('zenmux',{quotas:rows});
const bothHTML=renderProviderKPIHTML(bothQuotas,'zenmux');
assert(bothHTML.includes('Usage unavailable')); assert(!bothHTML.includes('NaN')); assert(bothHTML.includes('0.0%'));
const bothCharts=buildProviderCardDatasets('zenmux',[{capturedAt:'2026-11-01T00:00:00Z',quotas:[{name:'five_hour',utilization:0},{name:'weekly'}]}],'6h');
assert.equal(bothCharts.length,1); assert.equal(bothCharts[0].data[0].y,0);
`
	runNodeScript(t, script)
}
