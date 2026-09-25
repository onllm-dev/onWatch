package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrMistralAuth = errors.New("mistral: reconnect in the selected browser or update manual cookies")

type MistralHTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *MistralHTTPError) Error() string { return fmt.Sprintf("mistral: HTTP %d", e.Status) }

type MistralClient struct{ http *http.Client }

func NewMistralClient() *MistralClient {
	return &MistralClient{http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 2, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, ForceAttemptHTTP2: true}}}
}
func (c *MistralClient) get(ctx context.Context, s MistralSession, url string) ([]byte, error) {
	header := s.Header(url, time.Now())
	if header == "" {
		return nil, fmt.Errorf("no cookie matched %s: %w", url, ErrMistralAuth)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, ErrMistralParse
	}
	req.Header.Set("Cookie", header)
	req.Header.Set("User-Agent", "onWatch")
	req.Header.Set("Accept", "text/html, application/json")
	if strings.HasPrefix(url, "https://console.mistral.ai/") {
		for _, cookie := range req.Cookies() {
			if cookie.Name == "csrftoken" {
				req.Header.Set("X-CSRFTOKEN", cookie.Value)
			}
		}
	}
	res, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("mistral: network request failed")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, fmt.Errorf("mistral rejected the session (HTTP %d): %w", res.StatusCode, ErrMistralAuth)
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return nil, fmt.Errorf("redirected to sign-in (HTTP %d): %w", res.StatusCode, ErrMistralAuth)
	}
	if res.StatusCode != 200 {
		retry := time.Duration(0)
		if seconds, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil {
			retry = time.Duration(max(0, min(seconds, 3600))) * time.Second
		} else if date, e := http.ParseTime(res.Header.Get("Retry-After")); e == nil {
			retry = time.Until(date)
		}
		return nil, &MistralHTTPError{res.StatusCode, retry}
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(data) > 2<<20 {
		return nil, fmt.Errorf("response from %s was unreadable or oversized: %w", url, ErrMistralParse)
	}
	return data, nil
}
func (c *MistralClient) FetchSnapshot(ctx context.Context, s MistralSession) (*MistralSnapshot, error) {
	now := time.Now().UTC()
	snap := &MistralSnapshot{Identity: s.Source.Identity(), CapturedAt: now, Quotas: []MistralQuota{}, Status: "ok"}
	page, subErr := c.get(ctx, s, "https://admin.mistral.ai/subscription")
	if subErr == nil {
		snap.Quotas, subErr = ParseMistralSubscription(page, now)
	}
	hasVibe := false
	for _, q := range snap.Quotas {
		if q.Name == "vibe_included" {
			hasVibe = true
		}
	}
	if !hasVibe && !errors.Is(subErr, ErrMistralAuth) {
		const vibeURL = "https://console.mistral.ai/api-ui/trpc/billing.vibeUsage?batch=1&input=%7B%220%22%3A%7B%22json%22%3Anull%2C%22meta%22%3A%7B%22values%22%3A%5B%22undefined%22%5D%2C%22v%22%3A1%7D%7D%7D"
		if strings.Contains(s.Header(vibeURL, now), "csrftoken=") {
			if data, e := c.get(ctx, s, vibeURL); e == nil {
				if q, e := ParseMistralVibe(data, now); e == nil {
					snap.Quotas = append(snap.Quotas, q)
					subErr = nil
				}
			}
		}
	}
	data, billErr := c.get(ctx, s, fmt.Sprintf("https://admin.mistral.ai/api/billing/v2/usage?month=%d&year=%d", now.Month(), now.Year()))
	if billErr == nil {
		snap.Billing, billErr = ParseMistralBilling(data, now)
	}
	for _, err := range []error{subErr, billErr} {
		var httpErr *MistralHTTPError
		if errors.As(err, &httpErr) {
			snap.RetryAfter = max(snap.RetryAfter, httpErr.RetryAfter)
			if httpErr.Status >= 500 {
				snap.RetryAfter = max(snap.RetryAfter, 2*time.Minute)
			}
		}
		if errors.Is(err, ErrMistralAuth) {
			snap.AuthFailed = true
		}
	}
	if subErr != nil && billErr != nil {
		// Return the underlying error, not the bare sentinel: the wrapped
		// message says whether the session was rejected, redirected to
		// sign-in, or simply had no cookie that matched the request.
		if errors.Is(subErr, ErrMistralAuth) {
			return nil, subErr
		}
		if errors.Is(billErr, ErrMistralAuth) {
			return nil, billErr
		}
		var he *MistralHTTPError
		if errors.As(billErr, &he) && (he.Status == 429 || he.Status >= 500) {
			return nil, billErr
		}
		return nil, subErr
	}
	if subErr != nil || billErr != nil || len(snap.Quotas) < 2 {
		snap.Status = "partial"
	}
	if snap.Billing == nil {
		snap.Billing = &MistralBilling{Status: "unavailable"}
	}
	return snap, nil
}
