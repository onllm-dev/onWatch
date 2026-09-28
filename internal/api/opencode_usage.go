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

// OpenCode Go usage via the console's Go status API.
//
// The Go dashboard moved to a single-page console and /workspace/<id>/go now
// redirects to login, so there is no HTML left to scrape. GET
// /console/api/go/status returns the subscription's own meters: the 5-hour,
// weekly and monthly used/limit amounts OpenCode enforces and the Go page
// shows. It accepts either a console service-account key (Bearer) or the
// browser's __Host-console_session cookie plus an x-org-id header. Amounts
// are micro-cents; onWatch stores them as USD. See anomalyco/opencode#50912.

const (
	openCodeGoStatusPath      = "/console/api/go/status"
	openCodeUsageUserAgent    = "onwatch-opencode-usage/1"
	openCodeUsageMaxBodyBytes = 1 << 20 // the status document is well under 1 KiB
	// openCodeUsageTimeout: go/status is a live billing lookup behind
	// Cloudflare rather than a cached page, so it gets some headroom; 20s
	// still fails a stuck request well before the next poll.
	openCodeUsageTimeout     = 20 * time.Second
	openCodeUsageMinInterval = 60 * time.Second
	openCodeMicroCentsPerUSD = 1e8 // 100 cents x 1e6 micro-cents
)

// ErrOpenCodeMissingAPIKey is returned when usage-API mode has no key.
var ErrOpenCodeMissingAPIKey = errors.New("opencode: missing usage api key")

// ErrOpenCodeRateLimited is returned when the status API answers 429.
var ErrOpenCodeRateLimited = errors.New("opencode: rate limited")

// openCodeGoStatus is the part of the go/status response onWatch reads. Other
// fields (subscriber and payment IDs, pricing) are deliberately not decoded.
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

// FetchUsageSnapshot reads the Go subscription meters of the key's account.
func (c *OpenCodeClient) FetchUsageSnapshot(ctx context.Context, apiKey string) (*OpenCodeSnapshot, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrOpenCodeMissingAPIKey
	}
	return c.goStatusSnapshot(ctx, "key\x00"+apiKey, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	})
}

// goStatusSnapshot fetches go/status with the given credential and maps it.
// credKey identifies the credential for the reuse window.
func (c *OpenCodeClient) goStatusSnapshot(ctx context.Context, credKey string, authorize func(*http.Request)) (*OpenCodeSnapshot, error) {
	status, err := c.goStatus(ctx, credKey, authorize)
	if err != nil {
		return nil, err
	}
	quotas, err := status.quotas()
	if err != nil {
		return nil, err
	}
	return &OpenCodeSnapshot{
		CapturedAt:  time.Now().UTC(),
		AccountType: OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas:      quotas,
	}, nil
}

// goStatus returns the status, reusing a recent one for the same credential so
// a short poll interval does not query the console every few seconds.
func (c *OpenCodeClient) goStatus(ctx context.Context, credKey string, authorize func(*http.Request)) (*openCodeGoStatus, error) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.usageStatus != nil && c.usageKey == credKey && time.Since(c.usageAt) < openCodeUsageMinInterval {
		return c.usageStatus, nil
	}
	status, err := c.fetchGoStatus(ctx, authorize)
	if err != nil {
		return nil, err
	}
	c.usageStatus, c.usageKey, c.usageAt = status, credKey, time.Now()
	return status, nil
}

func (c *OpenCodeClient) fetchGoStatus(ctx context.Context, authorize func(*http.Request)) (*openCodeGoStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.goStatusURL, nil)
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
	// Bodies are never logged or echoed: the document carries account IDs.
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
		return nil, fmt.Errorf("%w: go status exceeds %d bytes", ErrOpenCodeInvalidResponse, openCodeUsageMaxBodyBytes)
	}
	var status openCodeGoStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("%w: go status: %v", ErrOpenCodeParseFailed, err)
	}
	if _, err := status.quotas(); err != nil {
		return nil, err // never cache a document that cannot be shown
	}
	return &status, nil
}

// quotas maps the three meters to onWatch's quota names. Every
// meter must be present with a positive limit: a partial document is a format
// change, not zero usage.
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
