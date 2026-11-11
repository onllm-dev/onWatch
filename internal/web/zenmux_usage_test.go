package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestZenmuxUsagePublicEndpoint(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	h.zenmuxUsageFetch = func(ctx context.Context, key string, q api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		if key != "synthetic-management-key" || q.Dimension != "BIZ_DT" || q.Time != "20260903" || q.BillType != "subscription" {
			t.Errorf("wrong query %+v", q)
		}
		zero := "0"
		return &api.ZenmuxUsage{FetchedAt: time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC), Data: api.ZenmuxUsageData{TokensTotal: &zero, TokensByModel: []api.ZenmuxUsageBucket{{BizTime: "2026090312", ModelSlug: "sample/model", RequestCounts: &zero}}}}, nil
	}
	srv := httptest.NewServer(NewServer(9211, h, nil, "", "", "127.0.0.1", "", "", nil).httpServer.Handler)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/zenmux/usage?dimension=BIZ_DT&time=20260903&bill_type=subscription")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d cache %s", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), `"modelSlug":"sample/model"`) || !strings.Contains(string(body), `"requestCounts":"0"`) || strings.Contains(string(body), "synthetic-management-key") {
		t.Fatalf("public usage payload invalid: %s %v", body, err)
	}
	// Authentication middleware covers the new endpoint as it does all dashboard APIs.
	secure := httptest.NewServer(NewServer(9211, h, nil, "admin", "hash", "127.0.0.1", "", "", nil).httpServer.Handler)
	defer secure.Close()
	res, err := http.Get(secure.URL + "/api/zenmux/usage?dimension=BIZ_DT&time=20260903")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status %d", res.StatusCode)
	}
}

func TestZenmuxUsageRejectsInvalidQuery(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	calls := 0
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		calls++
		return nil, nil
	}
	rr := httptest.NewRecorder()
	h.ZenmuxUsage(rr, httptest.NewRequest("GET", "/api/zenmux/usage?dimension=BIZ_DT&time=bad", nil))
	if rr.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("bad query reached provider, code %d calls %d", rr.Code, calls)
	}
}

func TestZenmuxUsageFailureKeepsQuotaAndSanitizesError(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	zero := 0.0
	if _, err := db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero}}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		return nil, errors.New("synthetic-management-key secret")
	}
	fail := httptest.NewRecorder()
	h.ZenmuxUsage(fail, httptest.NewRequest("GET", "/api/zenmux/usage?dimension=BIZ_DT&time=20260903", nil))
	if fail.Code != http.StatusBadGateway || strings.Contains(fail.Body.String(), "synthetic-management-key") {
		t.Fatalf("unsafe failure %d %s", fail.Code, fail.Body.String())
	}
	current := httptest.NewRecorder()
	h.Current(current, httptest.NewRequest("GET", "/api/current?provider=zenmux", nil))
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"utilization":0`) {
		t.Fatal("analytics failure hid the existing quota")
	}
}

func TestZenmuxUsageFailureEnforcesCooldown(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	calls := 0
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		calls++
		return nil, errors.New("synthetic upstream error")
	}
	request := func() *http.Request {
		return httptest.NewRequest("GET", "/api/zenmux/usage?dimension=BIZ_DT&time=20260903", nil)
	}
	h.ZenmuxUsage(httptest.NewRecorder(), request())
	blocked := httptest.NewRecorder()
	h.ZenmuxUsage(blocked, request())
	if blocked.Code != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("rate limit failed: %d %d", blocked.Code, calls)
	}
}

func TestZenmuxUsagePausedProvider(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSetting("provider_visibility", `{"zenmux":{"polling":false}}`); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	calls := 0
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		calls++
		return nil, nil
	}
	paused := httptest.NewRecorder()
	h.ZenmuxUsage(paused, httptest.NewRequest("GET", "/api/zenmux/usage?dimension=BIZ_DT&time=20260903", nil))
	if paused.Code != http.StatusServiceUnavailable || calls != 0 {
		t.Fatalf("paused provider contacted %d", calls)
	}
}

type zenmuxConcurrentSnapshot struct{ snapshot *api.ZenmuxSnapshot }

func (s zenmuxConcurrentSnapshot) FetchSnapshot(context.Context) (*api.ZenmuxSnapshot, error) {
	return s.snapshot, nil
}

type zenmuxStoredNotification struct{ stored chan struct{} }

func (n zenmuxStoredNotification) Check(notify.QuotaStatus) { n.stored <- struct{}{} }

func TestZenmuxQuotaPollCompletesWhileUsageBlocked(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	started := make(chan struct{})
	release := make(chan struct{})
	usageDone := make(chan *httptest.ResponseRecorder, 1)
	h.zenmuxUsageFetch = func(ctx context.Context, _ string, _ api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		close(started)
		select {
		case <-release:
			return &api.ZenmuxUsage{Data: api.ZenmuxUsageData{TokensByModel: []api.ZenmuxUsageBucket{}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		rr := httptest.NewRecorder()
		h.ZenmuxUsage(rr, httptest.NewRequest(http.MethodGet, "/api/zenmux/usage?dimension=BIZ_MTH&time=202609", nil).WithContext(ctx))
		usageDone <- rr
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("usage request did not start")
	}

	fraction := 0.42
	snapshot := &api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &fraction}}
	stored := make(chan struct{}, 1)
	pollDone := make(chan error, 1)
	poller := agent.NewZenmuxAgent(zenmuxConcurrentSnapshot{snapshot}, db, time.Hour, nil, zenmuxStoredNotification{stored})
	go func() { pollDone <- poller.Run(ctx) }()
	select {
	case <-stored:
	case <-ctx.Done():
		t.Fatal("fresh quota was not stored while usage was pending")
	}
	select {
	case <-usageDone:
		t.Fatal("usage unexpectedly completed before the fresh quota read")
	default:
	}
	current := httptest.NewRecorder()
	h.Current(current, httptest.NewRequest(http.MethodGet, "/api/current?provider=zenmux", nil))
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"utilization":42`) {
		t.Fatalf("fresh quota not readable while usage pending: status=%d body=%s", current.Code, current.Body.String())
	}
	close(release)
	select {
	case rr := <-usageDone:
		if rr.Code != http.StatusOK {
			t.Fatalf("usage status after release: %d", rr.Code)
		}
	case <-ctx.Done():
		t.Fatal("usage did not finish after release")
	}
	cancel()
	select {
	case err := <-pollDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poller did not stop")
	}
}

func TestZenmuxUsageAcceptedAfterFailureCooldown(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	var calls atomic.Int32
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("synthetic upstream failure")
		}
		return &api.ZenmuxUsage{Data: api.ZenmuxUsageData{TokensByModel: []api.ZenmuxUsageBucket{{ModelSlug: "sample/recovered"}}}}, nil
	}
	srv := httptest.NewServer(http.HandlerFunc(h.ZenmuxUsage))
	defer srv.Close()
	query := srv.URL + "?dimension=BIZ_MTH&time=202609"
	first, err := http.Get(query)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusBadGateway {
		t.Fatalf("first request status=%d", first.StatusCode)
	}
	// The real two-second cooldown begins before the failed fetch returns.
	<-time.After(2 * time.Second)
	second, err := http.Get(query)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	body, err := io.ReadAll(second.Body)
	if err != nil || second.StatusCode != http.StatusOK || !strings.Contains(string(body), `"modelSlug":"sample/recovered"`) || calls.Load() != 2 {
		t.Fatalf("retry status=%d calls=%d body=%s error=%v", second.StatusCode, calls.Load(), body, err)
	}
}

func TestZenmuxUsageInFlightAfterTimeGate(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	var calls atomic.Int32
	h.zenmuxUsageFetch = func(ctx context.Context, key string, q api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &api.ZenmuxUsage{Data: api.ZenmuxUsageData{TokensByModel: []api.ZenmuxUsageBucket{}}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := func() *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/zenmux/usage?dimension=BIZ_MTH&time=202609", nil).WithContext(ctx)
	}
	go func() {
		rr := httptest.NewRecorder()
		h.ZenmuxUsage(rr, request())
		done <- rr
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first fetch did not start")
	}
	// Advance only the existing time-gate state, leaving the fetch in flight.
	h.zenmuxUsageMu.Lock()
	h.zenmuxUsageLast = time.Now().Add(-3 * time.Second)
	h.zenmuxUsageMu.Unlock()
	blocked := httptest.NewRecorder()
	h.ZenmuxUsage(blocked, request())
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") != "2" || calls.Load() != 1 {
		t.Fatalf("in-flight request not serialized: status=%d retry=%q calls=%d", blocked.Code, blocked.Header().Get("Retry-After"), calls.Load())
	}
	release <- struct{}{}
	select {
	case first := <-done:
		if first.Code != http.StatusOK {
			t.Fatalf("first request status=%d: %s", first.Code, first.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("first fetch did not finish")
	}
	next := httptest.NewRecorder()
	h.ZenmuxUsage(next, request())
	if next.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("request not released: status=%d calls=%d", next.Code, calls.Load())
	}
}

func TestZenmuxUsageUpstreamRateLimited(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"})
	h.zenmuxUsageFetch = func(context.Context, string, api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		return nil, api.ErrZenmuxRateLimited
	}
	rr := httptest.NewRecorder()
	h.ZenmuxUsage(rr, httptest.NewRequest(http.MethodGet, "/api/zenmux/usage?dimension=BIZ_MTH&time=202609", nil))
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "60" {
		t.Fatalf("upstream rate limit: status=%d retry=%q", rr.Code, rr.Header().Get("Retry-After"))
	}
}

func TestZenmuxUsageRendering(t *testing.T) {
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
assert.equal(zenmuxUsageNumber(null),'Unknown'); assert.equal(zenmuxUsageNumber('0'),'0');
assert.equal(zenmuxUsageNumber('9007199254740993'),'9,007,199,254,740,993');
assert.equal(zenmuxUsageBucketLabel('202609031215'),'2026-09-03 12:15');
let html=zenmuxUsageRowsHTML([{bizTime:'2026090312',modelSlug:'x/<img>',tokens:null,requestCounts:'0'}]);
assert(html.includes('x/&lt;img&gt;'));assert(!html.includes('x/<img>'));assert(html.includes('Unknown'));assert(html.includes('>0<'));
let endpoints=zenmuxEndpointRowsHTML([{endpointSlug:'edge',requestCounts:'0',tokens:null,medianLatency:'12.5',medianThroughput:null}]);
assert(endpoints.includes('edge'));assert(endpoints.includes('Unknown'));assert(endpoints.includes('12.5'));
`
	runNodeScript(t, script)
}

func TestZenmuxUsageModelViewAndEndpoints(t *testing.T) {
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
const rows=[
 {modelSlug:'large',bizTime:'2026090112',requestCounts:'9007199254740993',tokens:'0.2'},
 {modelSlug:'large',bizTime:'2026090113',requestCounts:'0.25',tokens:'0.1'},
 {modelSlug:'less',bizTime:'2026090112',requestCounts:'9007199254740992',tokens:'999999999999999999999'},
 {modelSlug:'unknown',bizTime:'2026090112',requestCounts:'1',tokens:'0'},
 {modelSlug:'unknown',bizTime:'2026090113',requestCounts:null,tokens:null},
 {modelSlug:'zero',bizTime:'2026090112',requestCounts:'0',tokens:'0'}
];
assert(!zenmuxUsageRowsHTML(rows,'model').includes('Time bucket'));
const grouped=zenmuxGroupUsage(rows);
assert.deepEqual(grouped.map(r=>r.modelSlug),['large','less','zero','unknown']);
assert.equal(grouped[0].requestCounts,'9007199254740993.25'); assert.equal(grouped[0].tokens,'0.3');
assert.equal(grouped[3].requestCounts,null); assert.equal(grouped[3].tokens,null);
const html=zenmuxUsageRowsHTML(grouped,'model'); assert(!html.includes('Time bucket')); assert(html.includes('9,007,199,254,740,993.25'));
const intervals=zenmuxUsageRowsHTML(rows,'interval'); assert(intervals.includes('Time bucket')); assert(intervals.includes('2026-09-01 12:00'));
const endpoint={endpointSlug:'edge/<img>',requestCounts:'0',tokens:null,medianLatency:'12.5',medianThroughput:null,requestRatio:'0'};
let detail=zenmuxEndpointRowsHTML([endpoint],{mainEndpointSlug:'edge/<img>',fastestEndpointSlug:'edge/<img>',uptime:'0'});
assert(detail.includes('edge/&lt;img&gt;'));assert(!detail.includes('edge/<img>'));
assert(detail.includes('Main'));assert(detail.includes('Fastest'));assert(detail.includes('Success rate'));assert(detail.includes('0%'));assert(detail.includes('12.5'));
detail=zenmuxEndpointRowsHTML([{...endpoint,requestRatio:null}],{uptime:null});
assert(!detail.includes('Success rate'));assert(!detail.includes('Main'));assert(detail.includes('Unknown'));
assert.equal(zenmuxFractionPercent('0.875'),'87.5%');assert.equal(zenmuxFractionPercent('1.01'),'Unknown');
`
	runNodeScript(t, script)
}

func TestZenmuxUsageModelOrderAndRenderedValues(t *testing.T) {
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
const rows=[
 {modelSlug:'zeta',requestCounts:'7',tokens:'9007199254740993'},
 {modelSlug:'beta',requestCounts:'7',tokens:'9007199254740992'},
 {modelSlug:'delta',requestCounts:'3',tokens:'9007199254740992'},
 {modelSlug:'alpha',requestCounts:'7',tokens:'9007199254740993'},
 {modelSlug:'delta',requestCounts:'4',tokens:'1'}
];
const html=zenmuxUsageRowsHTML(zenmuxGroupUsage(rows),'model');
const rendered=[...html.matchAll(/<tr><td>(.*?)<\/td><td>(.*?)<\/td><td>(.*?)<\/td><\/tr>/g)].map(match=>match.slice(1));
assert.deepEqual(rendered,[
 ['alpha','7','9,007,199,254,740,993'],
 ['delta','7','9,007,199,254,740,993'],
 ['zeta','7','9,007,199,254,740,993'],
 ['beta','7','9,007,199,254,740,992']
]);
`
	runNodeScript(t, script)
}

func TestZenmuxUsageConfigChangeDuringFetch(t *testing.T) {
	cfg := &config.Config{ZenmuxManagementAPIKey: "synthetic-management-key"}
	h := NewHandler(nil, nil, nil, nil, cfg)
	h.zenmuxUsageFetch = func(ctx context.Context, key string, q api.ZenmuxUsageQuery) (*api.ZenmuxUsage, error) {
		cfg.WriteLock()
		cfg.ZenmuxManagementAPIKey = "synthetic-new-key"
		cfg.WriteUnlock()
		return &api.ZenmuxUsage{Data: api.ZenmuxUsageData{TokensByModel: []api.ZenmuxUsageBucket{{ModelSlug: "old/account"}}}}, nil
	}
	rr := httptest.NewRecorder()
	h.ZenmuxUsage(rr, httptest.NewRequest("GET", "/api/zenmux/usage?dimension=BIZ_MTH&time=202609", nil))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "old/account") {
		t.Fatal("returned previous account data")
	}
}
