package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const zenmuxDetailURL = "https://zenmux.ai/api/v1/management/subscription/detail"
const zenmuxMaxBody = 1 << 20

var (
	ErrZenmuxNetwork         = errors.New("zenmux: network request failed")
	ErrZenmuxMissingKey      = errors.New("zenmux: missing management API key")
	ErrZenmuxUnauthorized    = errors.New("zenmux: management API key rejected")
	ErrZenmuxRateLimited     = errors.New("zenmux: rate limited")
	ErrZenmuxInvalidResponse = errors.New("zenmux: invalid subscription response")
)

type ZenmuxClient struct {
	key       string
	url       string
	http      *http.Client
	usageHTTP *http.Client
}

type ZenmuxClientOption func(*ZenmuxClient)

// withZenmuxBaseURL replaces the host for local tests.
func withZenmuxBaseURL(base string) ZenmuxClientOption {
	return func(c *ZenmuxClient) { c.url = strings.TrimRight(base, "/") + "/api/v1/management/subscription/detail" }
}

func NewZenmuxClient(key string, opts ...ZenmuxClientOption) *ZenmuxClient {
	transport := &http.Transport{MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}
	// Analytics can be slower than quota polling and must not change its transport.
	usageTransport := transport.Clone()
	usageTransport.ResponseHeaderTimeout = 60 * time.Second
	checkRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &ZenmuxClient{
		key: strings.TrimSpace(key), url: zenmuxDetailURL,
		http:      &http.Client{Timeout: 20 * time.Second, CheckRedirect: checkRedirect, Transport: transport},
		usageHTTP: &http.Client{Timeout: 60 * time.Second, CheckRedirect: checkRedirect, Transport: usageTransport},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// FetchSnapshot makes a single read-only management request; it never calls an
// inference endpoint. Errors do not contain response bodies or credentials.
func (c *ZenmuxClient) FetchSnapshot(ctx context.Context) (*ZenmuxSnapshot, error) {
	if c.key == "" {
		return nil, ErrZenmuxMissingKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, errors.New("zenmux: cannot construct request")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrZenmuxNetwork
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrZenmuxUnauthorized
	case http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return nil, ErrZenmuxRateLimited
	default:
		return nil, fmt.Errorf("zenmux: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, zenmuxMaxBody+1))
	if err != nil || len(body) > zenmuxMaxBody {
		return nil, ErrZenmuxInvalidResponse
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    *ZenmuxSnapshot `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Success == nil || !*envelope.Success || envelope.Data == nil {
		return nil, ErrZenmuxInvalidResponse
	}
	// Do not retain a management key if an upstream response echoes it in a
	// recognized string field. Check decoded strings, including escaped JSON.
	snap := envelope.Data
	if snap.Plan == nil && snap.Quota5Hour == nil && snap.Quota7Day == nil && snap.QuotaMonthly == nil {
		return nil, ErrZenmuxInvalidResponse
	}
	containsKey := func(value *string) bool { return value != nil && strings.Contains(*value, c.key) }
	if containsKey(snap.Currency) || containsKey(snap.AccountStatus) ||
		(snap.Plan != nil && (containsKey(snap.Plan.Tier) || containsKey(snap.Plan.Interval))) {
		return nil, ErrZenmuxInvalidResponse
	}
	envelope.Data.CapturedAt = time.Now().UTC()
	return envelope.Data, nil
}
