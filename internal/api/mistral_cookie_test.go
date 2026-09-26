package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steipete/sweetcookie"
	_ "modernc.org/sqlite"
)

func TestMistralCookieIsolation(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Hour)
	read := func(ctx context.Context, o sweetcookie.Options) (sweetcookie.Result, error) {
		if len(o.Browsers) != 1 || o.Profiles[o.Browsers[0]] != "profile-a" {
			t.Fatal("profile not isolated")
		}
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{
			{Name: "ory_session_a", Value: "default", Domain: "admin.mistral.ai", Path: "/", Source: sweetcookie.Source{Profile: "profile-a"}},
			{Name: "ory_session_a", Value: "container", Domain: ".mistral.ai", Path: "/", Container: sweetcookie.Container{ID: 2}},
			{Name: "ory_session_old", Value: "old", Domain: ".mistral.ai", Path: "/", Expires: &expired},
			{Name: "tracking", Value: "private", Domain: ".mistral.ai", Path: "/"},
		}}, nil
	}
	sessions, e := ImportMistralSessions(context.Background(), MistralSource{"firefox", "profile-a", -1}, read)
	if e != nil || len(sessions) != 2 {
		t.Fatalf("count=%d err=%v", len(sessions), e)
	}
	if got := sessions[0].Header("https://admin.mistral.ai/subscription", now); got != "ory_session_a=default" {
		t.Fatal(got)
	}
	if got := sessions[0].Header("https://console.mistral.ai/api-ui/trpc/billing.vibeUsage", now); got != "" {
		t.Fatal("host-only cookie escaped origin")
	}
	if got := sessions[1].Header("https://evil.mistral.ai/", now); got != "" {
		t.Fatal("invalid destination")
	}
	if got := sessions[1].Header("http://admin.mistral.ai/", now); got != "" {
		t.Fatal("insecure destination")
	}
}
func TestMistralImporterFailures(t *testing.T) {
	for _, err := range []error{errors.New("permission denied"), errors.New("locked database"), errors.New("unsupported app-bound encryption")} {
		_, e := ImportMistralSessions(context.Background(), MistralSource{"chrome", "Default", 0}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
			return sweetcookie.Result{}, err
		})
		if !errors.Is(e, ErrMistralAuth) {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := ImportMistralSessions(ctx, MistralSource{}, func(ctx context.Context, _ sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{}, ctx.Err()
	})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type mistralTransport func(*http.Request) (*http.Response, error)

func (f mistralTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMistralHTTPFailures(t *testing.T) {
	session, _ := ManualMistralSession("ory_session_test=secret; tracking=excluded")
	for _, status := range []int{302, 401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := NewMistralClient()
			c.http.Transport = mistralTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.Header.Get("Cookie"), "tracking") {
					t.Fatal("extra cookie")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"123"}}, Body: http.NoBody}, nil
			})
			_, err := c.get(context.Background(), session, "https://admin.mistral.ai/subscription")
			if err == nil {
				t.Fatal("missing error")
			}
			if status == 429 {
				var he *MistralHTTPError
				if !errors.As(err, &he) || he.RetryAfter != 123*time.Second {
					t.Fatal(err)
				}
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestMistralBrowserOrdering(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		order := MistralBrowserOrder(platform)
		if len(order) != 3 || order[0] != "chrome" || order[1] != "firefox" {
			t.Fatal(order)
		}
		if platform == "darwin" && order[2] != "safari" || platform != "darwin" && order[2] != "edge" {
			t.Fatal(order)
		}
	}
}

func TestMistralSyntheticFirefoxStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE moz_cookies(host TEXT,name TEXT,value TEXT,path TEXT,expiry INTEGER,isSecure INTEGER,isHttpOnly INTEGER,sameSite INTEGER,originAttributes TEXT)`)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct {
		value, attrs string
		expiry       int64
	}{{"default", "", time.Now().Add(time.Hour).Unix()}, {"container", "^userContextId=3", time.Now().Add(time.Hour).Unix()}, {"expired", "^userContextId=4", time.Now().Add(-time.Hour).Unix()}} {
		_, e = db.Exec(`INSERT INTO moz_cookies VALUES(?,?,?,?,?,1,1,0,?)`, ".mistral.ai", "ory_session_test", row.value, "/", row.expiry, row.attrs)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	sessions, e := ImportMistralSessions(context.Background(), MistralSource{"firefox", path, -1}, nil)
	if e != nil || len(sessions) != 2 {
		t.Fatalf("count=%d error=%v", len(sessions), e)
	}
	if sessions[0].Header("https://admin.mistral.ai/subscription", time.Now()) != "ory_session_test=default" || sessions[1].Source.Container != 3 {
		t.Fatal("container isolation failed")
	}
}

func TestMistralPartialRateLimit(t *testing.T) {
	s, _ := ManualMistralSession("ory_session_test=secret")
	client := NewMistralClient()
	client.http.Transport = mistralTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "billing") {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: http.NoBody}, nil
		}
		page := `<h2>Included API usage</h2><p>€1 €10</p>`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(page))}, nil
	})
	snap, e := client.FetchSnapshot(context.Background(), s)
	if e != nil || len(snap.Quotas) != 1 || snap.RetryAfter != time.Hour {
		t.Fatalf("snap=%+v e=%v", snap, e)
	}
}

// A profile that has never signed in to Mistral must not reach the platform
// credential store: that is what raises a macOS Keychain password prompt, and
// prompting for a profile that cannot possibly help is pure user annoyance.
func TestMistralProfileWithoutMistralCookiesIsNotDecrypted(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE cookies(host_key TEXT,name TEXT,path TEXT,value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cookies VALUES('example.com','sid','/','x')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	read := func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		t.Error("credential store was opened for a profile with no Mistral cookies")
		return sweetcookie.Result{}, nil
	}
	sessions, err := ImportMistralSessions(context.Background(), MistralSource{Browser: "chrome", Profile: dir, Container: -1}, read)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
}

// Ory session cookies are stored as quoted values, which RFC 6265 permits
// (DQUOTE *cookie-octet DQUOTE). Go rejects a raw '"' inside Cookie.Value, so
// passing the stored value through verbatim silently drops the session cookie
// and leaves only the CSRF cookie - a request that looks authenticated but is
// redirected straight to sign-in.
func TestMistralHeaderKeepsQuotedSessionCookie(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	s := MistralSession{Cookies: []sweetcookie.Cookie{
		{Name: "ory_session_abc", Value: `"MTc2NDU4+/=abc"`, Domain: ".mistral.ai", Path: "/", Expires: &exp},
		{Name: "csrftoken", Value: "plain", Domain: "admin.mistral.ai", Path: "/", Expires: &exp},
	}}
	h := s.Header("https://admin.mistral.ai/subscription", time.Now())
	if !strings.Contains(h, `ory_session_abc="MTc2NDU4+/=abc"`) {
		t.Fatalf("quoted session cookie dropped or mangled: %q", h)
	}
	if !strings.Contains(h, "csrftoken=plain") {
		t.Fatalf("csrf cookie missing: %q", h)
	}
}

// A pasted header keeps its quoting through the round trip.
func TestManualMistralSessionKeepsQuotedValue(t *testing.T) {
	s, err := ManualMistralSession(`ory_session_abc="MTc2NDU4+/=abc"; csrftoken=plain`)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Header("https://admin.mistral.ai/subscription", time.Now())
	if !strings.Contains(h, `ory_session_abc="MTc2NDU4+/=abc"`) {
		t.Fatalf("manual quoted cookie lost: %q", h)
	}
}

// Settings values are persisted at whatever length arrives - the only backstop
// is the 64KB request-body cap - so the parse boundary enforces its own limits,
// matching the byte-length reject style used for ingested fields.
func TestMistralRejectsOversizedInput(t *testing.T) {
	atLimit := "ory_session_a=" + strings.Repeat("x", maxMistralCookieHeaderLen-len("ory_session_a="))
	if _, err := ManualMistralSession(atLimit); err != nil {
		t.Fatalf("header exactly at the limit was rejected: %v", err)
	}
	if _, err := ManualMistralSession(atLimit + "x"); err == nil {
		t.Fatal("oversized cookie header was accepted")
	}

	// Absolute paths are used as-is, without scanning the host's real browser
	// profiles, so these cases stay independent of the machine running them.
	atLimitPath := "/" + strings.Repeat("p", maxMistralProfilePathLen-1)
	if _, err := MistralSources("chrome", atLimitPath+"p"); err == nil {
		t.Fatal("oversized browser profile was accepted")
	}
	if _, err := MistralSources("chrome", atLimitPath); err != nil {
		t.Fatalf("profile exactly at the limit was rejected: %v", err)
	}
}

// A browser profile that cannot be scanned is reported and logged, so the
// error must not carry the OS error's path: that would write the user's home
// directory into the daemon log. The permission cause stays detectable.
func TestMistralScanErrorOmitsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block listing on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := filepath.Join(home, ".config", "google-chrome")
	if runtime.GOOS == "darwin" {
		root = filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	_, err := MistralSources("chrome", "")
	if err == nil {
		t.Fatal("unreadable profile folder was not reported")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("permission cause lost: %v", err)
	}
	if strings.Contains(err.Error(), home) {
		t.Fatalf("error leaks a filesystem path: %v", err)
	}
}
