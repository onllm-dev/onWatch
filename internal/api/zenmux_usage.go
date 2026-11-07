package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const zenmuxUsageMaxRows = 2000

var (
	ErrZenmuxInvalidUsageQuery = errors.New("zenmux: invalid account usage query")
	zenmuxDecimalPattern       = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

type ZenmuxUsageQuery struct {
	Dimension string
	Time      string
	BillType  string
	Model     string
}

// Validate only accepts the documented time dimensions and one billing type.
// An empty billing type asks for all types. Model selects the provider breakdown.
func (q ZenmuxUsageQuery) Validate() error {
	layout := map[string]string{"BIZ_MTH": "200601", "BIZ_DT": "20060102", "BIZ_HOUR": "2006010215"}[q.Dimension]
	if layout == "" || len(q.Time) != len(layout) {
		return ErrZenmuxInvalidUsageQuery
	}
	parsed, err := time.Parse(layout, q.Time)
	if err != nil || parsed.Year() < 1 || parsed.Format(layout) != q.Time {
		return ErrZenmuxInvalidUsageQuery
	}
	switch q.BillType {
	case "", "metered", "subscription", "fallbackMetered":
	default:
		return ErrZenmuxInvalidUsageQuery
	}
	if q.Model != "" && (!validZenmuxLabel(q.Model) || strings.Contains(q.Model, ",")) {
		return ErrZenmuxInvalidUsageQuery
	}
	return nil
}

func validZenmuxLabel(s string) bool {
	if s == "" || len(s) > 200 || !utf8.ValidString(s) || strings.TrimSpace(s) != s || strings.HasPrefix(s, "sk-") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type ZenmuxUsage struct {
	FetchedAt time.Time       `json:"fetchedAt"`
	Data      ZenmuxUsageData `json:"data"`
}

type ZenmuxUsageData struct {
	TokensTotal         *string               `json:"tokensTotal"`
	TokensPrompt        *string               `json:"tokensPrompt"`
	TokensCompletion    *string               `json:"tokensCompletion"`
	RequestCounts       *string               `json:"requestCounts"`
	TokensByModel       []ZenmuxUsageBucket   `json:"tokensByModel"`
	FastestEndpointSlug *string               `json:"fastestEndpointSlug"`
	MainEndpointSlug    *string               `json:"mainEndpointSlug"`
	Uptime              *string               `json:"uptime"`
	EndpointCount       *string               `json:"endpointCount"`
	EndpointUsages      []ZenmuxEndpointUsage `json:"endpointUsages"`
}

type ZenmuxUsageBucket struct {
	BizTime       string  `json:"bizTime"`
	ModelSlug     string  `json:"modelSlug"`
	Tokens        *string `json:"tokens"`
	RequestCounts *string `json:"requestCounts"`
}

type ZenmuxEndpointUsage struct {
	EndpointSlug     string  `json:"endpointSlug"`
	RequestCounts    *string `json:"requestCounts"`
	Tokens           *string `json:"tokens"`
	RequestRatio     *string `json:"requestRatio"`
	TokenRatio       *string `json:"tokenRatio"`
	MedianLatency    *string `json:"medianLatency"`
	MedianThroughput *string `json:"medianThroughput"`
	BillAmount       *string `json:"billAmount"`
	BillAmountRatio  *string `json:"billAmountRatio"`
}

// CloseIdleConnections releases connections for clients made for one UI request.
func (c *ZenmuxClient) CloseIdleConnections() {
	c.http.CloseIdleConnections()
	c.usageHTTP.CloseIdleConnections()
}

// FetchUsage only requests the official personal account analytics endpoint.
// Unknown fields are discarded and provider response bodies never enter errors.
func (c *ZenmuxClient) FetchUsage(ctx context.Context, q ZenmuxUsageQuery) (*ZenmuxUsage, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if c.key == "" {
		return nil, ErrZenmuxMissingKey
	}
	if q.Model != "" && strings.Contains(q.Model, c.key) {
		return nil, ErrZenmuxInvalidUsageQuery
	}
	endpoint := strings.TrimSuffix(c.url, "/subscription/detail") + "/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrZenmuxInvalidUsageQuery
	}
	params := req.URL.Query()
	params.Set("type", "usage")
	if q.Model != "" {
		params.Set("type", "provider")
		params.Set("model_slugs", q.Model)
	}
	params.Set("query_dimension", q.Dimension)
	params.Set("query_time", q.Time)
	if q.BillType != "" {
		params.Set("bill_types", q.BillType)
	}
	req.URL.RawQuery = params.Encode()
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.usageHTTP.Do(req)
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
	case http.StatusTooManyRequests, http.StatusUnprocessableEntity:
		return nil, ErrZenmuxRateLimited
	default:
		return nil, fmt.Errorf("zenmux: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, zenmuxMaxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrZenmuxInvalidResponse
	}
	if len(body) > zenmuxMaxBody {
		return nil, ErrZenmuxInvalidResponse
	}
	var envelope struct {
		Success *bool            `json:"success"`
		Data    *ZenmuxUsageData `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Success == nil || !*envelope.Success || envelope.Data == nil {
		return nil, ErrZenmuxInvalidResponse
	}
	d := envelope.Data
	if q.Model == "" && (d.TokensByModel == nil || len(d.TokensByModel) > zenmuxUsageMaxRows) {
		return nil, ErrZenmuxInvalidResponse
	}
	if q.Model != "" && (d.EndpointUsages == nil || len(d.EndpointUsages) > zenmuxUsageMaxRows) {
		return nil, ErrZenmuxInvalidResponse
	}
	validText := func(s string) bool {
		return validZenmuxLabel(s) && !strings.Contains(s, c.key)
	}
	validNum := func(s *string) bool {
		return s == nil || (len(*s) <= 64 && !strings.Contains(*s, c.key) && zenmuxDecimalPattern.MatchString(*s))
	}
	validTextPtr := func(s *string) bool { return s == nil || validText(*s) }
	if !validNum(d.TokensTotal) || !validNum(d.TokensPrompt) || !validNum(d.TokensCompletion) || !validNum(d.RequestCounts) || !validNum(d.Uptime) || !validNum(d.EndpointCount) || !validTextPtr(d.FastestEndpointSlug) || !validTextPtr(d.MainEndpointSlug) {
		return nil, ErrZenmuxInvalidResponse
	}
	bucketLayout := map[string]string{"BIZ_MTH": "20060102", "BIZ_DT": "2006010215", "BIZ_HOUR": "200601021504"}[q.Dimension]
	for _, b := range d.TokensByModel {
		tm, e := time.Parse(bucketLayout, b.BizTime)
		if e != nil || tm.Format(bucketLayout) != b.BizTime || !strings.HasPrefix(b.BizTime, q.Time) || !validText(b.ModelSlug) || !validNum(b.Tokens) || !validNum(b.RequestCounts) {
			return nil, ErrZenmuxInvalidResponse
		}
	}
	for _, e := range d.EndpointUsages {
		if !validText(e.EndpointSlug) || !validNum(e.RequestCounts) || !validNum(e.Tokens) || !validNum(e.RequestRatio) || !validNum(e.TokenRatio) || !validNum(e.MedianLatency) || !validNum(e.MedianThroughput) || !validNum(e.BillAmount) || !validNum(e.BillAmountRatio) {
			return nil, ErrZenmuxInvalidResponse
		}
	}
	return &ZenmuxUsage{FetchedAt: time.Now().UTC(), Data: *d}, nil
}
