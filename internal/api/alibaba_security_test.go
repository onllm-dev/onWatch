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
)

// --- Redirect policy ---

func TestAlibabaRedirectAllowedHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"modelstudio.console.alibabacloud.com", true},
		{"bailian-singapore-cs.alibabacloud.com", true},
		{"alibabacloud.com", true},
		{"ALIBABACLOUD.COM", true},
		{"evilalibabacloud.com", false},
		{"alibabacloud.com.evil.example", false},
		{"taobao.com", false},
		{"err.taobao.com", false},
		{"tmall.com", false},
		{"127.0.0.1", false},
		{"localhost", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := alibabaRedirectHostAllowed(tc.host); got != tc.want {
			t.Errorf("alibabaRedirectHostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// A redirect to a local httptest server must be refused by the production
// policy, so the test seam below is the only way tests may follow redirects.
func TestAlibabaClientRefusesNonAlibabaRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(alibabaTestPage))
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/page", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected the redirect to be refused")
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d times, want 0", targetHits.Load())
	}
}

func TestAlibabaClientFollowsRedirectAllowedByTestSeam(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		switch r.URL.Path {
		case "/page":
			_, _ = w.Write([]byte(alibabaTestPage))
		case "/zelda":
			_, _ = w.Write([]byte(zeldaBody(alibabaTestUsage)))
		case "/pop":
			_, _ = w.Write([]byte(`{"data":{"Data":{"InstanceList":[]}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil,
		WithAlibabaPageURL(srv.URL+"/page"),
		WithAlibabaZeldaGateway(srv.URL+"/zelda"),
		WithAlibabaPOPGateway(srv.URL+"/pop"),
		withAlibabaRedirectPolicy(func(*http.Request) bool { return true }),
		withAlibabaNow(fixedAlibabaNow))
	if _, err := c.FetchSnapshot(context.Background()); err != nil {
		t.Fatalf("FetchSnapshot through the test seam: %v", err)
	}
	if targetHits.Load() == 0 {
		t.Fatal("test seam must allow the redirect")
	}
}

// --- Cookie host scoping ---

func TestAlibabaCookieJarDoesNotSpecialCaseLocalhost(t *testing.T) {
	jar := parseAlibabaCookieJar(".alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n")
	for _, host := range []string{"127.0.0.1", "localhost", "::1", "app.localhost"} {
		if got := jar.headerFor(host); got != "" {
			t.Errorf("headerFor(%q) = %q, want no cookies", host, got)
		}
	}
}

// --- Import filtering ---

func TestFilterAlibabaCookiesKeepsOnlyAlibabaDomains(t *testing.T) {
	input := "# Netscape HTTP Cookie File\n" +
		".alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc\n" +
		"modelstudio.console.alibabacloud.com\tFALSE\t/\tTRUE\t0\thostonly\tx\n" +
		"#HttpOnly_.alibabacloud.com\tTRUE\t/\tTRUE\t0\tlogin_ticket\tsecret\n" +
		".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1\n" +
		"example.com\tFALSE\t/\tTRUE\t0\tother\t2\n"

	out, err := FilterAlibabaCookies(input)
	if err != nil {
		t.Fatalf("FilterAlibabaCookies: %v", err)
	}
	for _, want := range []string{"cna", "hostonly", "login_ticket"} {
		if !strings.Contains(out, "\t"+want+"\t") {
			t.Errorf("filtered export missing %q:\n%s", want, out)
		}
	}
	for _, dropped := range []string{"mmstat.com", "example.com", "track\t1", "other\t2"} {
		if strings.Contains(out, dropped) {
			t.Errorf("filtered export still carries %q:\n%s", dropped, out)
		}
	}
	// The filtered export must still parse and scope cookies to Alibaba hosts.
	jar := parseAlibabaCookieJar(out)
	if got := jar.headerFor("bailian-singapore-cs.alibabacloud.com"); got != "cna=abc; login_ticket=secret" {
		t.Errorf("zelda cookies after filtering = %q", got)
	}
}

func TestFilterAlibabaCookiesErrorsWhenNothingRelevant(t *testing.T) {
	input := "# Netscape HTTP Cookie File\n" +
		".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1\n" +
		"example.com\tFALSE\t/\tTRUE\t0\tother\t2\n"
	if _, err := FilterAlibabaCookies(input); !errors.Is(err, ErrAlibabaMissingCookies) {
		t.Fatalf("err = %v, want ErrAlibabaMissingCookies", err)
	}
}

func TestFilterAlibabaCookiesLeavesHeaderInputAlone(t *testing.T) {
	for _, in := range []string{"a=1; b=2", "Cookie: a=1; b=2", "  x=y \n"} {
		out, err := FilterAlibabaCookies(in)
		if err != nil {
			t.Fatalf("FilterAlibabaCookies(%q): %v", in, err)
		}
		if out != strings.TrimSpace(in) {
			t.Errorf("FilterAlibabaCookies(%q) = %q, want unchanged", in, out)
		}
	}
}

func TestAlibabaCookieHostFromURL(t *testing.T) {
	// Guards the filter host list against a malformed constant.
	for _, raw := range []string{alibabaConsoleOrigin, alibabaZeldaGateway} {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			t.Fatalf("constant URL %q does not parse", raw)
		}
		if !alibabaRedirectHostAllowed(u.Hostname()) {
			t.Errorf("request host %q must be an allowed Alibaba host", u.Hostname())
		}
	}
}

// An off-domain redirect (the console sending an expired session to a login
// host) must be refused without following it and classified as an expired
// session, not as an invalid response. The target server is never contacted and
// the error carries no path or query string.
func TestAlibabaClientOffDomainRedirectIsSessionExpired(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(alibabaTestPage))
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/signin?redirect=x&token=secret", http.StatusFound)
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected the redirect to be refused")
	}
	if !errors.Is(err, ErrAlibabaUnauthorized) {
		t.Fatalf("err = %v, want ErrAlibabaUnauthorized (session expired)", err)
	}
	if errors.Is(err, ErrAlibabaInvalidResponse) {
		t.Fatalf("err = %v, must not classify as an invalid response", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d times, want 0", targetHits.Load())
	}
	if text := err.Error(); strings.Contains(text, "redirect=x") || strings.Contains(text, "secret") || strings.Contains(text, "signin") {
		t.Fatalf("error leaked the redirect path or query: %q", text)
	}
}

// A non-HTTPS redirect is refused for the same reason and classified as an
// expired session rather than an invalid response.
func TestAlibabaClientNonHTTPSRedirectIsSessionExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://modelstudio.console.alibabacloud.com/login")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if !errors.Is(err, ErrAlibabaUnauthorized) {
		t.Fatalf("err = %v, want ErrAlibabaUnauthorized", err)
	}
}

// The Taobao/Tmall WAF redirect keeps the contributor's invalid-response
// classification and is not turned into a session-expired error.
func TestAlibabaClientTaobaoRedirectStaysInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://err.taobao.com/x")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := NewAlibabaClient(alibabaTestCookies, nil, WithAlibabaPageURL(srv.URL))
	_, err := c.FetchSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected the taobao redirect to be refused")
	}
	if !strings.Contains(err.Error(), "WAF redirect") {
		t.Fatalf("err = %v, want the WAF invalid-response message", err)
	}
	if errors.Is(err, ErrAlibabaUnauthorized) {
		t.Fatalf("err = %v, a taobao WAF redirect must not read as an expired session", err)
	}
}
