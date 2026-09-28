package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steipete/sweetcookie"
	"gopkg.in/ini.v1"
)

const (
	// Provider settings are stored at whatever length the dashboard sends, so
	// the values are bounded where they are parsed instead.
	maxMistralCookieHeaderLen = 8192
	maxMistralProfilePathLen  = 1024
)

// MistralSource contains metadata only. Cookies are never persisted in automatic mode.
type MistralSource struct {
	Browser   string `json:"browser"`
	Profile   string `json:"profile"`
	Container int    `json:"container"`
}

func (s MistralSource) Identity() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s.Browser+"\x00"+s.Profile+"\x00"+strconv.Itoa(s.Container))))
}

type MistralSession struct {
	Source  MistralSource
	Cookies []sweetcookie.Cookie
}
type MistralCookieReader func(context.Context, sweetcookie.Options) (sweetcookie.Result, error)

func MistralBrowserOrder(platform string) []string {
	if platform == "darwin" {
		return []string{"chrome", "firefox", "safari"}
	}
	return []string{"chrome", "firefox", "edge"}
}

// MistralSources enumerates metadata without reading/decrypting cookie databases.
// Every returned profile is an explicit store, preventing library-level merging.
func MistralSources(browser, profile string) ([]MistralSource, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	if len(profile) > maxMistralProfilePathLen {
		return nil, fmt.Errorf("mistral: browser profile exceeds %d bytes: %w", maxMistralProfilePathLen, ErrMistralParse)
	}
	order := MistralBrowserOrder(runtime.GOOS)
	if browser != "" && browser != "auto" {
		order = []string{browser}
	}
	var out []MistralSource
	var scanErrs []error
	for _, b := range order {
		if b != "chrome" && b != "firefox" && b != "edge" && b != "safari" {
			continue
		}
		if profile != "" {
			p := profile
			c := 0
			if i := strings.LastIndex(p, "::container="); i >= 0 {
				var err error
				c, err = strconv.Atoi(p[i+12:])
				if err != nil || c < 0 {
					return nil, ErrMistralParse
				}
				p = p[:i]
			}
			if filepath.IsAbs(p) {
				out = append(out, MistralSource{b, p, c})
			} else {
				discovered, err := MistralSources(b, "")
				if err != nil {
					return nil, err
				}
				for _, candidate := range discovered {
					base := filepath.Base(candidate.Profile)
					if base == p || strings.HasSuffix(base, "."+p) {
						candidate.Container = c
						out = append(out, candidate)
					}
				}
			}
			continue
		}
		var root string
		switch runtime.GOOS {
		case "darwin":
			switch b {
			case "chrome":
				root = filepath.Join(home, "Library/Application Support/Google/Chrome")
			case "edge":
				root = filepath.Join(home, "Library/Application Support/Microsoft Edge")
			case "firefox":
				root = filepath.Join(home, "Library/Application Support/Firefox")
			case "safari":
				// Only offer Safari when its store is really there: a blind
				// entry masks the failure of every other browser.
				safari := filepath.Join(home, "Library/Cookies/Cookies.binarycookies")
				if _, err := os.Stat(safari); err != nil {
					if !errors.Is(err, fs.ErrNotExist) {
						scanErrs = append(scanErrs, mistralScanError("safari", err))
					}
					continue
				}
				out = append(out, MistralSource{b, safari, 0})
				continue
			}
		case "windows":
			switch b {
			case "chrome":
				root = filepath.Join(os.Getenv("LOCALAPPDATA"), "Google/Chrome/User Data")
			case "edge":
				root = filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft/Edge/User Data")
			case "firefox":
				root = filepath.Join(os.Getenv("APPDATA"), "Mozilla/Firefox")
			}
		default:
			base := os.Getenv("XDG_CONFIG_HOME")
			if base == "" {
				base = filepath.Join(home, ".config")
			}
			switch b {
			case "chrome":
				root = filepath.Join(base, "google-chrome")
			case "edge":
				root = filepath.Join(base, "microsoft-edge")
			case "firefox":
				root = filepath.Join(home, ".mozilla/firefox")
			}
		}
		if root == "" {
			continue
		}
		var profiles []string
		if b == "firefox" {
			cfg, err := ini.Load(filepath.Join(root, "profiles.ini"))
			if err == nil {
				for _, section := range cfg.Sections() {
					if !strings.HasPrefix(section.Name(), "Profile") {
						continue
					}
					p := section.Key("Path").String()
					if p == "" {
						continue
					}
					if section.Key("IsRelative").String() == "1" {
						p = filepath.Join(root, p)
					}
					profiles = append(profiles, p)
				}
			}
		} else {
			entries, err := os.ReadDir(root)
			// A missing directory just means the browser is not installed.
			// Anything else (notably a macOS privacy denial for another app's
			// data) must be reported, or discovery fails with no explanation.
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				scanErrs = append(scanErrs, mistralScanError(b, err))
			}
			for _, entry := range entries {
				if entry.IsDir() && (entry.Name() == "Default" || strings.HasPrefix(entry.Name(), "Profile ")) {
					profiles = append(profiles, filepath.Join(root, entry.Name()))
				}
			}
		}
		sort.Strings(profiles)
		for _, p := range profiles {
			out = append(out, MistralSource{b, p, -1})
		}
	}
	if len(out) > 32 {
		out = out[:32]
	}
	// Sources and diagnostics are both returned: a browser that could not be
	// scanned must not hide the ones that could.
	return out, errors.Join(scanErrs...)
}

// mistralScanError reports a browser profile that could not be scanned
// without the OS error's path, which would otherwise put the user's home
// directory into the daemon log. A permission failure stays detectable
// through fs.ErrPermission, so callers can still tell it apart.
func mistralScanError(browser string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%s: profile folder not readable: %w", browser, fs.ErrPermission)
	}
	return fmt.Errorf("%s: profile folder not readable", browser)
}

func ImportMistralSessions(ctx context.Context, source MistralSource, read MistralCookieReader) ([]MistralSession, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	restoreScope := read == nil
	if read == nil {
		read = sweetcookie.Get
	}
	// Generous but bounded: reading a Chromium store can raise a Keychain or
	// keyring prompt, and the budget has to cover a person finding and typing
	// their login password, not just the decryption itself.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	b := sweetcookie.Browser(source.Browser)
	if restoreScope && b == sweetcookie.BrowserSafari {
		if _, e := readMistralSafariScopes(ctx, source.Profile); e != nil {
			return nil, ErrMistralAuth
		}
	}
	// Cookie names and hosts are stored in the clear; only values are
	// encrypted. Checking them first means a profile that has never signed in
	// to Mistral is skipped without touching the platform credential store,
	// which is what raises a password prompt.
	if scopes, e := readMistralScopes(ctx, mistralStorePath(source), b); e == nil && len(scopes) == 0 {
		return nil, nil
	}
	result, err := read(ctx, sweetcookie.Options{URL: "https://admin.mistral.ai/subscription", Origins: []string{"https://admin.mistral.ai/api/billing/v2/usage", "https://console.mistral.ai/api-ui/trpc/billing.vibeUsage"}, Browsers: []sweetcookie.Browser{b}, Profiles: map[sweetcookie.Browser]string{b: source.Profile}, Timeout: 90 * time.Second})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, ErrMistralAuth
	}
	if restoreScope {
		result.Cookies, err = restoreMistralScopes(ctx, result.Cookies)
		if err != nil {
			return nil, err
		}
	}
	// Never include library warnings: OS errors may contain private paths/data.
	groups := map[int][]sweetcookie.Cookie{}
	for _, c := range result.Cookies {
		if !mistralCookieName(c.Name) || (c.Expires != nil && c.Expires.Before(time.Now())) {
			continue
		}
		if source.Container >= 0 && c.Container.ID != source.Container {
			continue
		}
		groups[c.Container.ID] = append(groups[c.Container.ID], c)
	}
	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var sessions []MistralSession
	for _, id := range ids {
		s := source
		s.Container = id
		sessions = append(sessions, MistralSession{s, groups[id]})
	}
	return sessions, nil
}

// mistralStorePath resolves a source to the cookie database inside it. A
// profile may already point straight at the store file.
func mistralStorePath(source MistralSource) string {
	if info, e := os.Stat(source.Profile); e != nil || !info.IsDir() {
		return source.Profile
	}
	switch sweetcookie.Browser(source.Browser) {
	case sweetcookie.BrowserFirefox:
		return filepath.Join(source.Profile, "cookies.sqlite")
	default:
		return filepath.Join(source.Profile, "Cookies")
	}
}

// CookieNames lists the cookie names held by the session. Names only: values
// are credentials and must never reach a log.
func (s MistralSession) CookieNames() []string {
	names := make([]string, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		names = append(names, c.Name+"@"+c.Domain)
	}
	sort.Strings(names)
	return names
}

func mistralCookieName(n string) bool {
	return strings.HasPrefix(n, "ory_session_") || n == "csrftoken"
}
func (s MistralSession) Header(destination string, now time.Time) string {
	u, e := url.Parse(destination)
	if e != nil || u.Scheme != "https" || (u.Host != "admin.mistral.ai" && u.Host != "console.mistral.ai") {
		return ""
	}
	req := &http.Request{Header: make(http.Header)}
	for _, c := range s.Cookies {
		if !mistralCookieName(c.Name) || (c.Expires != nil && !c.Expires.After(now)) {
			continue
		}
		domain := strings.TrimPrefix(c.Domain, ".")
		host := u.Hostname()
		if host != domain && !(strings.HasPrefix(c.Domain, ".") && strings.HasSuffix(host, "."+domain)) {
			continue
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		if u.Path != path && !(strings.HasPrefix(u.Path, path) && (strings.HasSuffix(path, "/") || len(u.Path) > len(path) && u.Path[len(path)] == '/')) {
			continue
		}
		// RFC 6265 allows a cookie value to be wrapped in double quotes, and
		// Ory session cookies are stored that way. Go rejects a raw '"' inside
		// Cookie.Value, so hand it the unquoted value and let it re-apply the
		// quoting on the wire; otherwise the session cookie is dropped and the
		// request is silently unauthenticated.
		value, quoted := c.Value, false
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value, quoted = value[1:len(value)-1], true
		}
		cookie := &http.Cookie{Name: c.Name, Value: value, Quoted: quoted}
		if cookie.Valid() != nil {
			continue
		}
		req.AddCookie(cookie)
	}
	return req.Header.Get("Cookie")
}
func ManualMistralSession(header string) (MistralSession, error) {
	s := MistralSession{Source: MistralSource{Browser: "manual", Profile: "manual"}}
	header = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(header), "Cookie:"))
	if len(header) > maxMistralCookieHeaderLen {
		return s, fmt.Errorf("mistral: cookie header exceeds %d bytes: %w", maxMistralCookieHeaderLen, ErrMistralAuth)
	}
	if strings.ContainsAny(header, "\r\n") {
		return s, ErrMistralAuth
	}
	req := http.Request{Header: http.Header{"Cookie": []string{header}}}
	hasSession := false
	for _, c := range req.Cookies() {
		if !mistralCookieName(c.Name) {
			continue
		}
		if strings.HasPrefix(c.Name, "ory_session_") {
			hasSession = true
		}
		// Parsing strips the quoting; keep it so the value is sent back in the
		// same form the browser would send it.
		value := c.Value
		if c.Quoted {
			value = `"` + value + `"`
		}
		s.Cookies = append(s.Cookies, sweetcookie.Cookie{Name: c.Name, Value: value, Domain: ".mistral.ai", Path: "/", Secure: true})
	}
	if !hasSession {
		return s, ErrMistralAuth
	}
	s.Source.Profile = fmt.Sprintf("%x", sha256.Sum256([]byte(header)))
	return s, nil
}
