package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	alibabaTestCookies = "cna=abc; login_ticket=secret"
	alibabaTestSec     = "sec-token-123"

	alibabaTestPage = `<html><script>window.ALIYUN_CONSOLE_CONFIG = {SEC_TOKEN: "sec-token-123", other: 1};</script></html>`

	alibabaTestUsage = `{"per5HourPercentage":0.1,"per5HourResetTime":1790262790822,
		"per1WeekPercentage":0.25,"per1WeekResetTime":1790429616789,
		"per1MonthPercentage":0.5,"per1MonthResetTime":1792000000000}`
	alibabaTestSubscription = `{"remainingDays":20,"specCode":"lite","status":"VALID",
		"startTime":1790000000000,"endTime":1792000000000,"autoRenewFlag":false,"instanceCode":"inst-1"}`
	alibabaTestInstances = `{"InstanceList":[{"InstanceId":"inst-1","ProductType":"sfm_tokenplansolo_public_intl","Status":"Normal"}]}`
)

func fixedAlibabaNow() time.Time {
	return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
}

func zeldaBody(inner string) string {
	return `{"code":"200","data":{"DataV2":{"data":{"success":true,"data":` + inner + `}}}}`
}

type alibabaTestServer struct {
	*httptest.Server
	pageHits  atomic.Int32
	zeldaHits atomic.Int32
	popHits   atomic.Int32
}

// newAlibabaTestServer serves the console page, the Zelda gateway and the BSS
// POP gateway from one server and checks cookie and token propagation.
func newAlibabaTestServer(t *testing.T, usage, subscription string, popBody string) *alibabaTestServer {
	t.Helper()
	ts := &alibabaTestServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		ts.pageHits.Add(1)
		if got := r.Header.Get("Cookie"); got != alibabaTestCookies {
			t.Errorf("page Cookie = %q", got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Chrome/130") {
			t.Errorf("User-Agent = %q", ua)
		}
		_, _ = w.Write([]byte(alibabaTestPage))
	})
	mux.HandleFunc("/zelda", func(w http.ResponseWriter, r *http.Request) {
		ts.zeldaHits.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("zelda method = %s", r.Method)
		}
		q := r.URL.Query()
		if q.Get("action") != "IntlBroadScopeAspnGateway" || q.Get("product") != "sfm_bailian" {
			t.Errorf("zelda query = %v", q)
		}
		if got := r.Header.Get("Cookie"); got != alibabaTestCookies {
			t.Errorf("zelda Cookie = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://modelstudio.console.alibabacloud.com" {
			t.Errorf("Origin = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("sec_token"); got != alibabaTestSec {
			t.Errorf("sec_token = %q", got)
		}
		if got := r.PostForm.Get("region"); got != "ap-southeast-1" {
			t.Errorf("region = %q", got)
		}
		params := r.PostForm.Get("params")
		for _, want := range []string{`"Api":"` + q.Get("api") + `"`, `"V":"1.0"`, `feTraceId`, `feURL`} {
			if !strings.Contains(params, want) {
				t.Errorf("params %q missing %q", params, want)
			}
		}
		switch q.Get("api") {
		case alibabaUsageAPI:
			_, _ = w.Write([]byte(zeldaBody(usage)))
		case alibabaSubscriptionAPI:
			if subscription == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(zeldaBody(subscription)))
		default:
			t.Errorf("unexpected api %q", q.Get("api"))
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/pop", func(w http.ResponseWriter, r *http.Request) {
		ts.popHits.Add(1)
		q := r.URL.Query()
		if q.Get("action") != "QueryAvailableInstances" || q.Get("product") != "BssOpenApi" {
			t.Errorf("pop query = %v", q)
		}
		_ = r.ParseForm()
		if got := r.PostForm.Get("sec_token"); got != alibabaTestSec {
			t.Errorf("pop sec_token = %q", got)
		}
		if popBody == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(popBody))
	})
	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func (ts *alibabaTestServer) client(opts ...AlibabaOption) *AlibabaClient {
	base := []AlibabaOption{
		WithAlibabaPageURL(ts.URL + "/page"),
		WithAlibabaZeldaGateway(ts.URL + "/zelda"),
		WithAlibabaPOPGateway(ts.URL + "/pop"),
		withAlibabaNow(fixedAlibabaNow),
	}
	return NewAlibabaClient(alibabaTestCookies, nil, append(base, opts...)...)
}

func TestAlibabaClientFetchSnapshot(t *testing.T) {
	ts := newAlibabaTestServer(t, alibabaTestUsage, alibabaTestSubscription,
		`{"data":{"Data":`+alibabaTestInstances+`}}`)

	snap, err := ts.client(WithAlibabaAccountLabel("work")).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if !snap.CapturedAt.Equal(fixedAlibabaNow()) {
		t.Errorf("CapturedAt = %v", snap.CapturedAt)
	}
	if snap.AccountLabel != "work" || snap.SpecCode != "lite" || snap.Status != "VALID" ||
		snap.RemainingDays != 20 || snap.InstanceID != "inst-1" || snap.AutoRenew {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Quotas) != 3 {
		t.Fatalf("quotas = %d, want 3", len(snap.Quotas))
	}
	if snap.Quotas[0].Name != AlibabaQuotaFiveHour || snap.Quotas[0].Utilization != 10 {
		t.Errorf("five_hour = %+v", snap.Quotas[0])
	}
	if snap.Quotas[2].Name != AlibabaQuotaMonthly || snap.Quotas[2].Utilization != 50 {
		t.Errorf("monthly = %+v", snap.Quotas[2])
	}
	if ts.pageHits.Load() != 1 || ts.zeldaHits.Load() != 2 || ts.popHits.Load() != 1 {
		t.Errorf("hits page=%d zelda=%d pop=%d", ts.pageHits.Load(), ts.zeldaHits.Load(), ts.popHits.Load())
	}
	if strings.Contains(snap.RawJSON, "secret") || strings.Contains(snap.RawJSON, alibabaTestSec) {
		t.Error("RawJSON must not contain credentials")
	}
}

func TestAlibabaClientAPIQueryIsEncoded(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			_, _ = w.Write([]byte(alibabaTestPage))
		case "/zelda":
			rawQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(zeldaBody(alibabaTestUsage)))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil,
		WithAlibabaPageURL(srv.URL+"/page"), WithAlibabaZeldaGateway(srv.URL+"/zelda"),
		WithAlibabaPOPGateway(srv.URL+"/pop"))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if !strings.Contains(rawQuery, "api="+url.QueryEscape(alibabaSubscriptionAPI)) {
		t.Errorf("raw query %q does not carry the url-encoded api", rawQuery)
	}
	if !strings.HasSuffix(rawQuery, "&_v=") {
		t.Errorf("raw query %q must end with &_v=", rawQuery)
	}
}

func TestAlibabaClientOptionalLookupsMayFail(t *testing.T) {
	// Subscription 500 and BSS 500 must not drop a good usage reading.
	ts := newAlibabaTestServer(t, alibabaTestUsage, "", "")
	snap, err := ts.client().FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if len(snap.Quotas) != 3 || snap.SpecCode != "" || snap.InstanceID != "" {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
}

func TestAlibabaClientMissingCookies(t *testing.T) {
	c := NewAlibabaClient("  \n", nil)
	if _, err := c.FetchSnapshot(context.Background()); !errors.Is(err, ErrAlibabaMissingCookies) {
		t.Fatalf("err = %v, want ErrAlibabaMissingCookies", err)
	}
}

func TestAlibabaClientNetscapeCookiesAreConverted(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewAlibabaClient(".a.com\tTRUE\t/\tTRUE\t0\tk1\tv1\n.a.com\tTRUE\t/\tTRUE\t0\tk2\tv2\n", nil,
		WithAlibabaPageURL(srv.URL))
	_, _ = c.FetchSnapshot(context.Background())
	if got != "k1=v1; k2=v2" {
		t.Errorf("Cookie header = %q", got)
	}
}

func TestAlibabaClientSecTokenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>please log in</html>`))
	}))
	defer srv.Close()
	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	if _, err := c.FetchSnapshot(context.Background()); !errors.Is(err, ErrAlibabaUnauthorized) {
		t.Fatalf("err = %v, want ErrAlibabaUnauthorized", err)
	}
}

func TestAlibabaClientSecTokenFormats(t *testing.T) {
	for _, page := range []string{
		`SEC_TOKEN: "tok-1"`,
		`SEC_TOKEN:'tok-1'`,
		`"SEC_TOKEN":"tok-1"`,
	} {
		m := alibabaSecTokenRe.FindStringSubmatch(page)
		if m == nil || m[1] != "tok-1" {
			t.Errorf("regex on %q = %v", page, m)
		}
	}
}

func TestAlibabaClientHTTPStatusErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, ErrAlibabaUnauthorized},
		{"forbidden", http.StatusForbidden, ErrAlibabaUnauthorized},
		{"rate limited", http.StatusTooManyRequests, ErrAlibabaRateLimited},
		{"server error", http.StatusBadGateway, ErrAlibabaServerError},
		{"unexpected", http.StatusTeapot, ErrAlibabaInvalidResponse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
			_, err := c.FetchSnapshot(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAlibabaClientGatewayErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"html login page", `<html>login</html>`, ErrAlibabaUnauthorized},
		{"not json", `plain text`, ErrAlibabaInvalidResponse},
		{"need login code", `{"code":"ConsoleNeedLogin","message":"please login"}`, ErrAlibabaUnauthorized},
		{"throttled", `{"code":"Throttling.User","message":"too many requests"}`, ErrAlibabaRateLimited},
		{"inner error", `{"data":{"DataV2":{"data":{"success":false,"errorCode":"Boom","errorMsg":"broken"}}}}`, ErrAlibabaInvalidResponse},
		{"empty data", `{"data":{"DataV2":{"data":{"data":{}}}}}`, ErrAlibabaInvalidResponse},
		{"inner login", `{"data":{"DataV2":{"data":{"success":false,"errorMsg":"SEC_TOKEN invalid"}}}}`, ErrAlibabaUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/page" {
					_, _ = w.Write([]byte(alibabaTestPage))
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewAlibabaClient(alibabaTestCookies, nil,
				WithAlibabaPageURL(srv.URL+"/page"), WithAlibabaZeldaGateway(srv.URL+"/zelda"),
				WithAlibabaPOPGateway(srv.URL+"/pop"))
			_, err := c.FetchSnapshot(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAlibabaClientUsageWithoutWindows(t *testing.T) {
	// An empty usage object with no subscription is not a Token Plan reading.
	ts := newAlibabaTestServer(t, `{"unrelated":1}`, "", "")
	_, err := ts.client().FetchSnapshot(context.Background())
	if !errors.Is(err, ErrAlibabaInvalidResponse) {
		t.Fatalf("err = %v, want ErrAlibabaInvalidResponse", err)
	}
}

func TestAlibabaClientNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	pageURL := srv.URL
	srv.Close()
	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(pageURL), WithAlibabaTimeout(2*time.Second))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrAlibabaNetwork) {
		t.Fatalf("err = %v, want ErrAlibabaNetwork", err)
	}
}

func TestAlibabaClientContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAlibabaClientBodyLimit(t *testing.T) {
	// The token sits past the 2 MiB cap, so it must not be found.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", alibabaBodyLimit+10)))
		_, _ = w.Write([]byte(`SEC_TOKEN: "late"`))
	}))
	defer srv.Close()
	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrAlibabaUnauthorized) {
		t.Fatalf("err = %v, want ErrAlibabaUnauthorized", err)
	}
}

func TestAlibabaClientDefaults(t *testing.T) {
	c := NewAlibabaClient("a=b", nil)
	if c.region != "ap-southeast-1" {
		t.Errorf("region = %q", c.region)
	}
	if c.pageURL != "https://modelstudio.console.alibabacloud.com/ap-southeast-1/subscription/token-plan/personal" {
		t.Errorf("pageURL = %q", c.pageURL)
	}
	if c.zeldaGateway != alibabaZeldaGateway || c.popGateway != alibabaPOPGateway {
		t.Errorf("gateways = %q, %q", c.zeldaGateway, c.popGateway)
	}

	c = NewAlibabaClient("a=b", nil, WithAlibabaRegion("us-east-1"))
	if !strings.Contains(c.pageURL, "/us-east-1/") {
		t.Errorf("region must feed the default page URL, got %q", c.pageURL)
	}
}

func TestIsAlibabaRateLimited(t *testing.T) {
	if !IsAlibabaRateLimited(ErrAlibabaRateLimited) || IsAlibabaRateLimited(ErrAlibabaNetwork) {
		t.Error("IsAlibabaRateLimited misclassifies")
	}
}
