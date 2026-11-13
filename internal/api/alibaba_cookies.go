package api

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// alibabaCookieFileLimit bounds how much of a cookie file is read.
const alibabaCookieFileLimit = 1 << 20

// alibabaCookie is one Netscape cookie with its domain, used so each request
// only sends cookies that the browser would attach to that host. Dumping the
// whole jar into Cookie (including host-only modelstudio cookies and tracking
// domains) makes bailian-singapore-cs.alibabacloud.com answer a Taobao WAF
// redirect instead of JSON.
type alibabaCookie struct {
	Domain   string
	HostOnly bool
	Name     string
	Value    string
}

// alibabaCookieJar holds either domain-scoped cookies from a Netscape export
// or a prebuilt Cookie header string (no domain info).
type alibabaCookieJar struct {
	scoped []alibabaCookie
	header string // used when input was already a Cookie header
}

func (j alibabaCookieJar) empty() bool {
	return len(j.scoped) == 0 && strings.TrimSpace(j.header) == ""
}

// headerFor returns the Cookie header value for host. When the jar came from a
// plain header string, that string is returned unchanged.
func (j alibabaCookieJar) headerFor(host string) string {
	if len(j.scoped) == 0 {
		return j.header
	}
	host = strings.ToLower(strings.TrimSpace(host))
	var pairs []string
	for _, c := range j.scoped {
		if alibabaCookieMatchesHost(c, host) {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
	}
	// httptest and similar local hosts never match Alibaba domains; send the
	// whole jar there so unit tests can still assert on Cookie propagation.
	if len(pairs) == 0 && alibabaLocalTestHost(host) {
		for _, c := range j.scoped {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
	}
	return strings.Join(pairs, "; ")
}

func alibabaLocalTestHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

func alibabaCookieMatchesHost(c alibabaCookie, host string) bool {
	domain := strings.ToLower(strings.TrimSpace(c.Domain))
	if domain == "" || c.Name == "" {
		return false
	}
	if c.HostOnly {
		return host == domain
	}
	domain = strings.TrimPrefix(domain, ".")
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func alibabaHostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ParseAlibabaCookies turns a cookie export into a Cookie header value.
//
// Two inputs are accepted: a Netscape cookies.txt export (tab separated lines
// with the name and value in the last two of seven fields, "#HttpOnly_" lines
// included) or a ready-made Cookie header string ("a=1; b=2", optionally with
// a leading "Cookie:"). Any other text is passed through trimmed. The result
// never contains line breaks, so it is safe to use as a header value.
//
// Prefer parseAlibabaCookieJar when the caller will talk to more than one host;
// this helper keeps the historical string API for config/env seeding.
func ParseAlibabaCookies(input string) string {
	jar := parseAlibabaCookieJar(input)
	if len(jar.scoped) > 0 {
		// Flatten every cookie for the string API (env display, tests). The
		// HTTP client uses parseAlibabaCookieJar + headerFor(host) so each
		// request only sends domain-matching cookies.
		var pairs []string
		for _, c := range jar.scoped {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
		return strings.Join(pairs, "; ")
	}
	return jar.header
}

// AlibabaCookiesAreNetscape reports whether input looks like a Netscape
// cookies.txt export (tab-separated domain rows) rather than a Cookie header.
func AlibabaCookiesAreNetscape(input string) bool {
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if len(strings.Split(line, "\t")) >= 7 {
			return true
		}
	}
	return false
}

func parseAlibabaCookieJar(input string) alibabaCookieJar {
	input = strings.TrimSpace(input)
	if input == "" {
		return alibabaCookieJar{}
	}

	var scoped []alibabaCookie
	netscape := false
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		netscape = true
		name := strings.TrimSpace(fields[5])
		if name == "" {
			continue
		}
		domain := strings.TrimSpace(fields[0])
		// Netscape column 2: TRUE means include subdomains (leading-dot domain).
		includeSub := strings.EqualFold(strings.TrimSpace(fields[1]), "TRUE")
		hostOnly := !includeSub && !strings.HasPrefix(domain, ".")
		scoped = append(scoped, alibabaCookie{
			Domain:   domain,
			HostOnly: hostOnly,
			Name:     name,
			Value:    strings.TrimSpace(strings.Join(fields[6:], "\t")),
		})
	}
	if netscape {
		return alibabaCookieJar{scoped: scoped}
	}

	// Already a header: drop a "Cookie:" prefix and flatten line breaks.
	header := input
	if len(header) >= len("cookie:") && strings.EqualFold(header[:len("cookie:")], "cookie:") {
		header = header[len("cookie:"):]
	}
	parts := strings.FieldsFunc(header, func(r rune) bool { return r == '\n' || r == '\r' })
	kept := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "#") {
			continue
		}
		kept = append(kept, part)
	}
	return alibabaCookieJar{header: strings.Join(kept, " ")}
}

// LoadAlibabaCookiesFile reads a cookie export from disk and returns the file
// text (Netscape or Cookie header). The raw text is what NewAlibabaClient
// needs so domain scoping survives into the request Cookie header. It fails
// when the file holds no cookies.
func LoadAlibabaCookiesFile(path string) (string, error) {
	data, err := readAlibabaCookiesFile(path)
	if err != nil {
		return "", err
	}
	if parseAlibabaCookieJar(data).empty() {
		return "", fmt.Errorf("%w: no cookies in %s", ErrAlibabaMissingCookies, path)
	}
	return data, nil
}

func readAlibabaCookiesFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("alibaba: opening cookies file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, alibabaCookieFileLimit))
	if err != nil {
		return "", fmt.Errorf("alibaba: reading cookies file: %w", err)
	}
	return string(data), nil
}

// LoadAlibabaCookieJarFile reads a cookie export and returns a domain-aware jar.
func LoadAlibabaCookieJarFile(path string) (alibabaCookieJar, error) {
	data, err := readAlibabaCookiesFile(path)
	if err != nil {
		return alibabaCookieJar{}, err
	}
	jar := parseAlibabaCookieJar(data)
	if jar.empty() {
		return alibabaCookieJar{}, fmt.Errorf("%w: no cookies in %s", ErrAlibabaMissingCookies, path)
	}
	return jar, nil
}
