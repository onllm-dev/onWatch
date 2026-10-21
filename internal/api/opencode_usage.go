package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenCode Go usage.
//
// There are two data sources:
//
//   - The Go usage API at GET /zen/go/v1/usage, authenticated by a Go API key
//     (Bearer). This is the current, first-party route the CLI and console use,
//     and the one a service-account / Go key from Settings resolves to. Each
//     window reports an integer percent and a reset time.
//   - The console go/status document, used only by the browser-session mode
//     (workspace ID + __Host-console_session cookie). It reports micro-cents
//     per meter, which onWatch stores as USD.
//
// The older /console/api/go/status path is not reachable with a key: it answers
// 403 to any Bearer credential, which is why key mode used to fail silently.
// See anomalyco/opencode#53701 and #50912.

const (
	// openCodeGoUsagePath is the key-authenticated usage route. A Go API key is
	// only accepted here; the console status path below answers 403 to it.
	openCodeGoUsagePath = "/zen/go/v1/usage"
	// openCodeGoStatusPath is the browser-session route (workspace ID +
	// __Host-console_session cookie).
	openCodeGoStatusPath      = "/console/api/go/status"
	openCodeUsageUserAgent    = "onwatch-opencode-usage/1"
	openCodeUsageMaxBodyBytes = 1 << 20 // the status document is well under 1 KiB
	// openCodeUsageTimeout: both routes are live billing lookups behind
	// Cloudflare rather than cached pages, so they get some headroom; 20s
	// still fails a stuck request well before the next poll.
	openCodeUsageTimeout     = 20 * time.Second
	openCodeUsageMinInterval = 60 * time.Second
	openCodeMicroCentsPerUSD = 1e8 // 100 cents x 1e6 micro-cents
)

// ErrOpenCodeMissingAPIKey is returned when usage-API mode has no key.
var ErrOpenCodeMissingAPIKey = errors.New("opencode: missing usage api key")

// ErrOpenCodeRateLimited is returned when the usage API answers 429.
var ErrOpenCodeRateLimited = errors.New("opencode: rate limited")

// openCodeZenUsage is the /zen/go/v1/usage document. Each window is an integer
// percent (0-100) plus a reset time; monthly is absent on plans without one.
type openCodeZenUsage struct {
	Usage struct {
		Rolling *openCodeZenWindow `json:"rolling"`
		Weekly  *openCodeZenWindow `json:"weekly"`
		Monthly *openCodeZenWindow `json:"monthly"`
	} `json:"usage"`
}

type openCodeZenWindow struct {
	Status   string     `json:"status"`
	Percent  *float64   `json:"percent"`
	ResetsAt *time.Time `json:"resetsAt"`
}

// openCodeGoStatus is the part of the console go/status response onWatch reads.
// Other fields (subscriber and payment IDs, pricing) are deliberately not
// decoded. Used by browser-session mode.
type openCodeGoStatus struct {
	Access *struct {
		// EndsAt is the end of the subscription period, when the month renews.
		EndsAt *time.Time `json:"endsAt"`
		Meters *struct {
			FiveHour *openCodeGoMeter `json:"fiveHour"`
			Week     *openCodeGoMeter `json:"week"`
			Month    *openCodeGoMeter `json:"month"`
		} `json:"meters"`
	} `json:"access"`
}

// openCodeGoMeter is one window. ResetsAt is null for a 5-hour meter with no
// open session and absent for the month meter.
type openCodeGoMeter struct {
	ResetsAt *time.Time          `json:"resetsAt"`
	Limit    *openCodeMicroCents `json:"limitMicroCents"`
	Used     *openCodeMicroCents `json:"usedMicroCents"`
}

// openCodeMicroCents accepts the integer-as-string amounts go/status returns
// (and a bare JSON number, should it ever send one).
type openCodeMicroCents float64

func (m *openCodeMicroCents) UnmarshalJSON(b []byte) error {
	s := string(b)
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("micro-cents amount is not a number")
	}
	*m = openCodeMicroCents(v)
	return nil
}

// FetchUsageSnapshot reads the Go subscription windows of the key's account
// from the usage API.
func (c *OpenCodeClient) FetchUsageSnapshot(ctx context.Context, apiKey string) (*OpenCodeSnapshot, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrOpenCodeMissingAPIKey
	}
	usage, filledAt, err := c.zenUsage(ctx, "key\x00"+apiKey, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	})
	if err != nil {
		if IsOpenCodeAuthError(err) {
			// Name the setting: a wrong or non-Go key is the usual cause, and a
			// bare "forbidden" leaves the user guessing.
			return nil, fmt.Errorf("%w: check the OpenCode Go Usage API Key in Settings (a Go API key from opencode.ai/console)", err)
		}
		return nil, err
	}
	quotas, err := usage.quotas()
	if err != nil {
		return nil, err
	}
	return &OpenCodeSnapshot{
		// A document served from the 60s reuse window was fetched earlier;
		// stamping it now would make up to a minute-old data read as fresh.
		CapturedAt:  filledAt.UTC(),
		AccountType: OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas:      quotas,
	}, nil
}

// zenUsage returns the usage document, reusing a recent one for the same
// credential so a short poll interval does not query the API every few seconds.
func (c *OpenCodeClient) zenUsage(ctx context.Context, credKey string, authorize func(*http.Request)) (*openCodeZenUsage, time.Time, error) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.usageStatus != nil && c.usageKey == credKey && time.Since(c.usageAt) < openCodeUsageMinInterval {
		return c.usageStatus, c.usageAt, nil
	}
	usage, err := c.fetchZenUsage(ctx, authorize)
	if err != nil {
		return nil, time.Time{}, err
	}
	c.usageStatus, c.usageKey, c.usageAt = usage, credKey, time.Now()
	return usage, c.usageAt, nil
}

func (c *OpenCodeClient) fetchZenUsage(ctx context.Context, authorize func(*http.Request)) (*openCodeZenUsage, error) {
	body, err := c.getJSON(ctx, c.goUsageURL, authorize)
	if err != nil {
		return nil, err
	}
	var usage openCodeZenUsage
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("%w: go usage: %v", ErrOpenCodeParseFailed, err)
	}
	if _, err := usage.quotas(); err != nil {
		return nil, err // never cache a document that cannot be shown
	}
	return &usage, nil
}

// quotas maps the three windows to onWatch's quota names. rolling and weekly
// are required; monthly is optional (plans without one simply omit it). A
// missing required window, a missing percent, or a percent outside 0-100 is a
// format change, not zero usage.
func (u *openCodeZenUsage) quotas() ([]OpenCodeQuota, error) {
	windows := []struct {
		name, field string
		required    bool
		window      *openCodeZenWindow
	}{
		{"five_hour", "rolling", true, u.Usage.Rolling},
		{"weekly", "weekly", true, u.Usage.Weekly},
		{"monthly", "monthly", false, u.Usage.Monthly},
	}
	quotas := make([]OpenCodeQuota, 0, len(windows))
	for _, w := range windows {
		if w.window == nil {
			if w.required {
				return nil, fmt.Errorf("%w: go usage window %q missing", ErrOpenCodeParseFailed, w.field)
			}
			continue
		}
		if w.window.Percent == nil || math.IsNaN(*w.window.Percent) || math.IsInf(*w.window.Percent, 0) {
			return nil, fmt.Errorf("%w: go usage window %q has no percent", ErrOpenCodeParseFailed, w.field)
		}
		pct := *w.window.Percent
		if pct < 0 || pct > 100 {
			return nil, fmt.Errorf("%w: go usage window %q percent %.2f out of range", ErrOpenCodeParseFailed, w.field, pct)
		}
		var reset *time.Time
		if w.window.ResetsAt != nil {
			t := w.window.ResetsAt.UTC()
			reset = &t
		}
		rounded := math.Round(pct*10) / 10
		quotas = append(quotas, OpenCodeQuota{
			Name:        w.name,
			Used:        rounded,
			Limit:       100,
			Utilization: rounded,
			Format:      OpenCodeQuotaFormatPercent,
			// Status is passed through verbatim: an unknown value is shown as
			// given rather than dropped, and a missing status stays empty.
			Status:   strings.TrimSpace(w.window.Status),
			ResetsAt: reset,
		})
	}
	return quotas, nil
}

// FetchSnapshot reads the console Go meters with the browser's session. The
// console authenticates with the __Host-console_session cookie and needs the
// workspace ID as x-org-id; the old auth cookie and the /workspace/<id>/go
// page no longer work (issue #134).
func (c *OpenCodeClient) FetchSnapshot(ctx context.Context, workspaceID, sessionCookie string) (*OpenCodeSnapshot, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	sessionCookie = strings.TrimSpace(sessionCookie)
	if workspaceID == "" || sessionCookie == "" {
		return nil, ErrOpenCodeMissingConfig
	}
	cookieHeader := openCodeConsoleCookieHeader(sessionCookie)
	snap, err := c.goStatusSnapshot(ctx, "cookie\x00"+workspaceID+"\x00"+cookieHeader, func(req *http.Request) {
		req.Header.Set("Cookie", cookieHeader)
		req.Header.Set("x-org-id", workspaceID)
	})
	if errors.Is(err, ErrOpenCodeUnauthorized) {
		return nil, fmt.Errorf("%w: paste the __Host-console_session cookie from opencode.ai/console", err)
	}
	return snap, err
}

// goStatusSnapshot fetches the console go/status document with the given
// credential and maps it. credKey identifies the credential for the reuse
// window.
func (c *OpenCodeClient) goStatusSnapshot(ctx context.Context, credKey string, authorize func(*http.Request)) (*OpenCodeSnapshot, error) {
	status, filledAt, err := c.goStatus(ctx, credKey, authorize)
	if err != nil {
		return nil, err
	}
	quotas, err := status.quotas()
	if err != nil {
		return nil, err
	}
	return &OpenCodeSnapshot{
		// Fill time, not serve time: see FetchUsageSnapshot.
		CapturedAt:  filledAt.UTC(),
		AccountType: OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas:      quotas,
	}, nil
}

// goStatus returns the console status, reusing a recent one for the same
// credential so a short poll interval does not query the console every few
// seconds.
func (c *OpenCodeClient) goStatus(ctx context.Context, credKey string, authorize func(*http.Request)) (*openCodeGoStatus, time.Time, error) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.goStatusCache != nil && c.usageKey == credKey && time.Since(c.usageAt) < openCodeUsageMinInterval {
		return c.goStatusCache, c.usageAt, nil
	}
	body, err := c.getJSON(ctx, c.goStatusURL, authorize)
	if err != nil {
		return nil, time.Time{}, err
	}
	var status openCodeGoStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, time.Time{}, fmt.Errorf("%w: go status: %v", ErrOpenCodeParseFailed, err)
	}
	if _, err := status.quotas(); err != nil {
		return nil, time.Time{}, err // never cache a document that cannot be shown
	}
	c.goStatusCache, c.usageKey, c.usageAt = &status, credKey, time.Now()
	return &status, c.usageAt, nil
}

// getJSON performs a GET with the shared headers, applies authorize, and maps
// HTTP status to a typed error. The body is never logged or echoed: both
// documents carry account identifiers.
func (c *OpenCodeClient) getJSON(ctx context.Context, url string, authorize func(*http.Request)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrOpenCodeNetworkError, err)
	}
	// Cloudflare in front of opencode.ai rejects default client User-Agents.
	req.Header.Set("User-Agent", openCodeUsageUserAgent)
	req.Header.Set("Accept", "application/json")
	authorize(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOpenCodeNetworkError, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode >= 300 && resp.StatusCode < 400: // redirect to login
		return nil, ErrOpenCodeUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrOpenCodeForbidden
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrOpenCodeRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrOpenCodeServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: http %d", ErrOpenCodeInvalidResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeUsageMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrOpenCodeNetworkError, err)
	}
	if len(body) > openCodeUsageMaxBodyBytes {
		return nil, fmt.Errorf("%w: go usage exceeds %d bytes", ErrOpenCodeInvalidResponse, openCodeUsageMaxBodyBytes)
	}
	return body, nil
}

// quotas maps the console meters to onWatch's quota names. Every meter must be
// present with a positive limit: a partial document is a format change, not
// zero usage.
func (s *openCodeGoStatus) quotas() ([]OpenCodeQuota, error) {
	if s.Access == nil || s.Access.Meters == nil {
		return nil, fmt.Errorf("%w: go status has no access meters", ErrOpenCodeParseFailed)
	}
	m := s.Access.Meters
	windows := []struct {
		name, field string
		meter       *openCodeGoMeter
	}{
		{"five_hour", "fiveHour", m.FiveHour},
		{"weekly", "week", m.Week},
		{"monthly", "month", m.Month},
	}
	quotas := make([]OpenCodeQuota, 0, len(windows))
	for _, w := range windows {
		if w.meter == nil || w.meter.Limit == nil || w.meter.Used == nil {
			return nil, fmt.Errorf("%w: go status meter %q missing or incomplete", ErrOpenCodeParseFailed, w.field)
		}
		limit, used := float64(*w.meter.Limit), float64(*w.meter.Used)
		if limit <= 0 || used < 0 {
			return nil, fmt.Errorf("%w: go status meter %q has an invalid limit or usage", ErrOpenCodeParseFailed, w.field)
		}
		resetsAt := w.meter.ResetsAt
		if resetsAt == nil && w.name == "monthly" {
			resetsAt = s.Access.EndsAt // the month renews with the subscription
		}
		var reset *time.Time
		if resetsAt != nil {
			t := resetsAt.UTC()
			reset = &t
		}
		quotas = append(quotas, OpenCodeQuota{
			Name:        w.name,
			Used:        used / openCodeMicroCentsPerUSD,
			Limit:       limit / openCodeMicroCentsPerUSD,
			Utilization: math.Round(used/limit*1000) / 10,
			Format:      OpenCodeQuotaFormatCurrency,
			ResetsAt:    reset,
		})
	}
	return quotas, nil
}
