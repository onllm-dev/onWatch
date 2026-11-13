package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAlibabaMissingCookies  = errors.New("alibaba: missing session cookies")
	ErrAlibabaUnauthorized    = errors.New("alibaba: unauthorized - session expired or SEC_TOKEN missing")
	ErrAlibabaRateLimited     = errors.New("alibaba: rate limited")
	ErrAlibabaServerError     = errors.New("alibaba: server error")
	ErrAlibabaNetwork         = errors.New("alibaba: network error")
	ErrAlibabaInvalidResponse = errors.New("alibaba: invalid response")
)

const (
	alibabaDefaultRegion   = "ap-southeast-1"
	alibabaConsoleOrigin   = "https://modelstudio.console.alibabacloud.com"
	alibabaZeldaGateway    = "https://bailian-singapore-cs.alibabacloud.com/data/api.json"
	alibabaPOPGateway      = alibabaConsoleOrigin + "/data/api.json"
	alibabaUsageAPI        = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
	alibabaSubscriptionAPI = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription"
	alibabaBodyLimit       = 2 << 20 // 2 MiB
	alibabaUserAgent       = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

// alibabaSecTokenRe matches the token in `SEC_TOKEN: "x"` and in the quoted
// JSON key form `"SEC_TOKEN":"x"`. A bare `SEC_TOKEN:?\s*['"]...` would capture
// the stray colon in the JSON form.
var alibabaSecTokenRe = regexp.MustCompile(`SEC_TOKEN["']?\s*[:=]?\s*['"]([^'"]+)['"]`)

// alibabaPageURL returns the Token Plan Personal console page for a region.
func alibabaPageURL(region string) string {
	return alibabaConsoleOrigin + "/" + url.PathEscape(region) + "/subscription/token-plan/personal"
}

// AlibabaClient polls the Model Studio console backend for Token Plan usage
// using a logged-in browser session.
//
// A poll is: GET the console page to read the SEC_TOKEN CSRF token, then two
// read-only gateway calls (usage, subscription) and one optional BSS call for
// the instance id. Cookies and the SEC_TOKEN are never logged.
//
// Cookies are attached per request host. Sending the full Netscape jar (or
// host-only modelstudio cookies) to bailian-singapore-cs triggers a Taobao
// WAF HTML redirect instead of JSON.
type AlibabaClient struct {
	httpClient   *http.Client
	logger       *slog.Logger
	jar          alibabaCookieJar
	region       string
	accountLabel string
	pageURL      string
	zeldaGateway string
	popGateway   string
	now          func() time.Time
}

// AlibabaOption configures an AlibabaClient.
type AlibabaOption func(*AlibabaClient)

// WithAlibabaRegion sets the console region (default ap-southeast-1).
func WithAlibabaRegion(region string) AlibabaOption {
	return func(c *AlibabaClient) {
		if region = strings.TrimSpace(region); region != "" {
			c.region = region
		}
	}
}

// WithAlibabaAccountLabel sets the label copied onto returned snapshots.
func WithAlibabaAccountLabel(label string) AlibabaOption {
	return func(c *AlibabaClient) {
		c.accountLabel = label
	}
}

// WithAlibabaPageURL sets the console page URL that carries the SEC_TOKEN.
func WithAlibabaPageURL(pageURL string) AlibabaOption {
	return func(c *AlibabaClient) {
		c.pageURL = strings.TrimSpace(pageURL)
	}
}

// WithAlibabaZeldaGateway sets the Zelda gateway URL (for testing).
func WithAlibabaZeldaGateway(gateway string) AlibabaOption {
	return func(c *AlibabaClient) {
		c.zeldaGateway = strings.TrimSpace(gateway)
	}
}

// WithAlibabaPOPGateway sets the BSS POP gateway URL (for testing).
func WithAlibabaPOPGateway(gateway string) AlibabaOption {
	return func(c *AlibabaClient) {
		c.popGateway = strings.TrimSpace(gateway)
	}
}

// WithAlibabaHTTPTransport overrides the HTTP transport (for testing).
func WithAlibabaHTTPTransport(rt http.RoundTripper) AlibabaOption {
	return func(c *AlibabaClient) {
		c.httpClient.Transport = rt
	}
}

// WithAlibabaTimeout sets a custom request timeout (for testing).
func WithAlibabaTimeout(timeout time.Duration) AlibabaOption {
	return func(c *AlibabaClient) {
		c.httpClient.Timeout = timeout
	}
}

func withAlibabaNow(now func() time.Time) AlibabaOption {
	return func(c *AlibabaClient) {
		c.now = now
	}
}

// NewAlibabaClient creates a client for one Model Studio session. cookies is
// a Netscape cookies.txt export or a Cookie header value.
func NewAlibabaClient(cookies string, logger *slog.Logger, opts ...AlibabaOption) *AlibabaClient {
	if logger == nil {
		logger = slog.Default()
	}

	c := &AlibabaClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
			// The Zelda gateway answers a bad Cookie jar with a 302 to
			// err.taobao.com. Following that produces an HTML body that looks
			// like an expired session; refuse the redirect instead.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				host := strings.ToLower(req.URL.Hostname())
				if strings.Contains(host, "taobao.com") || strings.Contains(host, "tmall.com") {
					return fmt.Errorf("%w: WAF redirect to %s", ErrAlibabaInvalidResponse, host)
				}
				if len(via) >= 5 {
					return fmt.Errorf("%w: too many redirects", ErrAlibabaNetwork)
				}
				return nil
			},
		},
		logger:       logger,
		jar:          parseAlibabaCookieJar(cookies),
		region:       alibabaDefaultRegion,
		zeldaGateway: alibabaZeldaGateway,
		popGateway:   alibabaPOPGateway,
		now:          time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.pageURL == "" {
		c.pageURL = alibabaPageURL(c.region)
	}
	return c
}

// FetchSnapshot reads the current Token Plan usage and subscription.
func (c *AlibabaClient) FetchSnapshot(ctx context.Context) (*AlibabaSnapshot, error) {
	if c.jar.empty() {
		return nil, ErrAlibabaMissingCookies
	}

	secToken, err := c.fetchSecToken(ctx)
	if err != nil {
		return nil, err
	}

	usageJSON, err := c.zeldaCall(ctx, alibabaUsageAPI, secToken)
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}

	// The subscription and instance lookups only add plan metadata, so a
	// failure there must not drop a good usage reading. Session and throttle
	// errors still end the poll because the next call would fail the same way.
	subscriptionJSON, err := c.zeldaCall(ctx, alibabaSubscriptionAPI, secToken)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrAlibabaUnauthorized) || errors.Is(err, ErrAlibabaRateLimited) {
			return nil, fmt.Errorf("subscription: %w", err)
		}
		c.logger.Warn("Alibaba subscription lookup failed", "error", err)
		subscriptionJSON = nil
	}

	instanceJSON, err := c.popQueryInstances(ctx, secToken)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.logger.Debug("Alibaba instance lookup failed", "error", err)
		instanceJSON = nil
	}

	snapshot, err := BuildAlibabaSnapshot(usageJSON, subscriptionJSON, instanceJSON, c.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAlibabaInvalidResponse, err)
	}
	snapshot.AccountLabel = c.accountLabel

	c.logger.Debug("Alibaba snapshot fetched", "quotas", len(snapshot.Quotas), "spec", snapshot.SpecCode)
	return snapshot, nil
}

// fetchSecToken loads the console page and extracts the CSRF SEC_TOKEN. A page
// without one means the session cookies no longer log in.
func (c *AlibabaClient) fetchSecToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("alibaba: creating page request: %w", err)
	}
	req.Header.Set("User-Agent", alibabaUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	c.setCookieHeader(req)

	body, err := c.do(ctx, req)
	if err != nil {
		return "", err
	}

	match := alibabaSecTokenRe.FindSubmatch(body)
	if match == nil {
		return "", fmt.Errorf("%w: SEC_TOKEN not found in console page", ErrAlibabaUnauthorized)
	}
	return string(match[1]), nil
}

// zeldaEnvelope is the gateway response: the payload sits at
// data.DataV2.data.data.
type zeldaEnvelope struct {
	Code    alibabaFlexValue `json:"code"`
	Message alibabaFlexValue `json:"message"`
	Success *bool            `json:"success"`
	Data    struct {
		DataV2 struct {
			Data struct {
				Success   *bool            `json:"success"`
				ErrorCode alibabaFlexValue `json:"errorCode"`
				ErrorMsg  alibabaFlexValue `json:"errorMsg"`
				Data      json.RawMessage  `json:"data"`
			} `json:"data"`
		} `json:"DataV2"`
	} `json:"data"`
}

// zeldaCall invokes one zeldaHttp API through the gateway and returns the
// inner payload.
func (c *AlibabaClient) zeldaCall(ctx context.Context, apiName, secToken string) ([]byte, error) {
	params, err := json.Marshal(map[string]interface{}{
		"Api": apiName,
		"V":   "1.0",
		"Data": map[string]interface{}{
			"cornerstoneParam": map[string]string{
				"feTraceId": uuid.NewString(),
				"feURL":     c.pageURL,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("alibaba: encoding params: %w", err)
	}
	form := url.Values{
		"params":    {string(params)},
		"sec_token": {secToken},
		"region":    {c.region},
	}
	endpoint := c.zeldaGateway + "?action=IntlBroadScopeAspnGateway&product=sfm_bailian&api=" + url.QueryEscape(apiName) + "&_v="

	body, err := c.postForm(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var env zeldaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, c.nonJSONError(body, err)
	}

	inner := env.Data.DataV2.Data
	if len(inner.Data) > 0 && string(inner.Data) != "null" && alibabaIsJSONObject(inner.Data) && (inner.Success == nil || *inner.Success) {
		return inner.Data, nil
	}

	detail := strings.TrimSpace(strings.Join([]string{
		env.Code.text(), env.Message.text(), inner.ErrorCode.text(), inner.ErrorMsg.text(),
	}, " "))
	if classified := classifyAlibabaMessage(detail); classified != nil {
		return nil, fmt.Errorf("%w: %s", classified, alibabaSnippet([]byte(detail)))
	}
	if detail == "" {
		detail = alibabaSnippet(body)
	}
	return nil, fmt.Errorf("%w: gateway returned no data: %s", ErrAlibabaInvalidResponse, detail)
}

// popEnvelope is the BSS POP response: the payload sits at data.Data.
type popEnvelope struct {
	Code    alibabaFlexValue `json:"code"`
	Message alibabaFlexValue `json:"message"`
	Data    struct {
		Data json.RawMessage `json:"Data"`
	} `json:"data"`
}

// popQueryInstances lists the account's Token Plan instances through the BSS
// POP gateway.
func (c *AlibabaClient) popQueryInstances(ctx context.Context, secToken string) ([]byte, error) {
	params, err := json.Marshal(map[string]interface{}{
		"Product":     AlibabaCommodityCode,
		"ProductType": AlibabaCommodityCode,
		"RegionId":    c.region,
		"PageSize":    5,
		"PageNum":     1,
	})
	if err != nil {
		return nil, fmt.Errorf("alibaba: encoding params: %w", err)
	}
	form := url.Values{
		"product":   {"BssOpenApi"},
		"action":    {"QueryAvailableInstances"},
		"params":    {string(params)},
		"sec_token": {secToken},
		"region":    {c.region},
	}
	endpoint := c.popGateway + "?action=QueryAvailableInstances&product=BssOpenApi&_tag="

	body, err := c.postForm(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}

	var env popEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, c.nonJSONError(body, err)
	}
	if len(env.Data.Data) == 0 || string(env.Data.Data) == "null" {
		return nil, fmt.Errorf("%w: BSS returned no data", ErrAlibabaInvalidResponse)
	}
	return env.Data.Data, nil
}

func (c *AlibabaClient) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("alibaba: creating request: %w", err)
	}
	req.Header.Set("User-Agent", alibabaUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.setCookieHeader(req)
	req.Header.Set("Origin", alibabaConsoleOrigin)
	req.Header.Set("Referer", c.pageURL)
	return c.do(ctx, req)
}

// setCookieHeader attaches only the cookies that match the request host.
func (c *AlibabaClient) setCookieHeader(req *http.Request) {
	host := ""
	if req.URL != nil {
		host = req.URL.Hostname()
	}
	if cookie := c.jar.headerFor(host); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}

// do executes a request and returns the (size limited) body for a 200
// response, or a typed error otherwise.
func (c *AlibabaClient) do(ctx context.Context, req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrAlibabaNetwork, redactAlibabaURLError(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, alibabaBodyLimit))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: reading body: %v", ErrAlibabaNetwork, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: http %d", ErrAlibabaUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrAlibabaRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrAlibabaServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: http %d: %s", ErrAlibabaInvalidResponse, resp.StatusCode, alibabaSnippet(body))
	}
}

// nonJSONError classifies a 200 response that is not JSON. The gateway
// answers an expired session with an HTML login page.
func (c *AlibabaClient) nonJSONError(body []byte, parseErr error) error {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "<") {
		return fmt.Errorf("%w: html response instead of JSON", ErrAlibabaUnauthorized)
	}
	return fmt.Errorf("%w: %v", ErrAlibabaInvalidResponse, parseErr)
}

// classifyAlibabaMessage maps a gateway error code or message to a typed
// error, or nil when it is not recognised.
func classifyAlibabaMessage(msg string) error {
	lower := strings.ToLower(msg)
	switch {
	case lower == "":
		return nil
	case strings.Contains(lower, "throttl") || strings.Contains(lower, "ratelimit") ||
		strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many"):
		return ErrAlibabaRateLimited
	case strings.Contains(lower, "login") || strings.Contains(lower, "sec_token") ||
		strings.Contains(lower, "sectoken") || strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "unauthor") || strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "session"):
		return ErrAlibabaUnauthorized
	}
	return nil
}

func alibabaSnippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}

// redactAlibabaURLError drops the request URL from a transport error; it
// carries no secrets today (they travel in the body and headers) but the
// message should stay short and stable.
func redactAlibabaURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// IsAlibabaRateLimited reports whether err is an Alibaba rate-limit error.
func IsAlibabaRateLimited(err error) bool {
	return errors.Is(err, ErrAlibabaRateLimited)
}
