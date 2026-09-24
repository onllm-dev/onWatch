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
	"strings"
	"time"
)

const (
	commandCodeDefaultBaseURL = "https://api.commandcode.ai"
	// commandCodeUserAgent identifies onWatch to the Command Code edge. The
	// API sits behind Cloudflare and rejects requests whose client signature
	// it dislikes (HTTP 403 error 1010), so a real product token is sent
	// rather than Go's default.
	commandCodeUserAgent = "onWatch"
	commandCodeTimeout   = 20 * time.Second
	commandCodeMaxBody   = 1 << 20 // 1 MiB; the largest response is a few KB
)

var (
	ErrCommandCodeMissingAPIKey   = errors.New("commandcode: missing API key")
	ErrCommandCodeUnauthorized    = errors.New("commandcode: unauthorized - invalid or expired API key")
	ErrCommandCodeRateLimited     = errors.New("commandcode: rate limited")
	ErrCommandCodeServerError     = errors.New("commandcode: server error")
	ErrCommandCodeNetworkError    = errors.New("commandcode: network error")
	ErrCommandCodeInvalidResponse = errors.New("commandcode: invalid response")
)

// CommandCodeClient polls the Command Code alpha API for the account's credit
// balance, rate-limit windows and billing-period usage.
//
// Every call is a read-only GET: unlike a usage probe against an inference
// endpoint, polling spends none of the user's credits.
type CommandCodeClient struct {
	httpClient *http.Client
	logger     *slog.Logger
	apiKey     string
	baseURL    string
	now        func() time.Time
}

// CommandCodeClientOption configures a CommandCodeClient.
type CommandCodeClientOption func(*CommandCodeClient)

// WithCommandCodeBaseURL sets a custom base URL (for testing).
func WithCommandCodeBaseURL(baseURL string) CommandCodeClientOption {
	return func(c *CommandCodeClient) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

// WithCommandCodeTimeout sets a custom timeout (for testing).
func WithCommandCodeTimeout(timeout time.Duration) CommandCodeClientOption {
	return func(c *CommandCodeClient) {
		c.httpClient.Timeout = timeout
	}
}

// WithCommandCodeHTTPTransport overrides the HTTP transport (for testing).
func WithCommandCodeHTTPTransport(rt http.RoundTripper) CommandCodeClientOption {
	return func(c *CommandCodeClient) {
		c.httpClient.Transport = rt
	}
}

func withCommandCodeNow(now func() time.Time) CommandCodeClientOption {
	return func(c *CommandCodeClient) {
		c.now = now
	}
}

// NewCommandCodeClient creates a new Command Code API client.
func NewCommandCodeClient(apiKey string, logger *slog.Logger, opts ...CommandCodeClientOption) *CommandCodeClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &CommandCodeClient{
		httpClient: &http.Client{
			Timeout: commandCodeTimeout,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: commandCodeTimeout,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		logger:  logger,
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: commandCodeDefaultBaseURL,
		now:     time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// FetchSnapshot polls the four alpha endpoints and builds a snapshot.
//
// whoami is required: it supplies the account identity and, when the key
// belongs to an organisation, the orgId every billing lookup is scoped to.
// The remaining three are independent, so each one that fails costs only its
// own section and is logged, mirroring how the reference provider reports
// unavailable sections instead of zeros.
func (c *CommandCodeClient) FetchSnapshot(ctx context.Context) (*CommandCodeSnapshot, error) {
	if c.apiKey == "" {
		return nil, ErrCommandCodeMissingAPIKey
	}

	whoamiBody, err := c.get(ctx, "/alpha/whoami")
	if err != nil {
		return nil, err
	}
	whoami, err := ParseCommandCodeWhoami(whoamiBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandCodeInvalidResponse, err)
	}

	orgID := ""
	if whoami.Org != nil {
		orgID = strings.TrimSpace(whoami.Org.ID)
	}

	// A blocking auth failure here means the key is unusable for billing, so
	// it is surfaced as a real error instead of a snapshot of empty sections.
	creditsBody, creditsErr := c.get(ctx, commandCodeOrgPath("/alpha/billing/credits", orgID))
	if creditsErr != nil && isCommandCodeAuthError(creditsErr) {
		return nil, creditsErr
	}
	subscriptionBody, subErr := c.get(ctx, commandCodeOrgPath("/alpha/billing/subscriptions", orgID))
	if subErr != nil && isCommandCodeAuthError(subErr) {
		return nil, subErr
	}

	var credits *commandCodeCreditsResponse
	if creditsErr == nil {
		if parsed, perr := ParseCommandCodeCredits(creditsBody); perr != nil {
			c.logger.Warn("Command Code credits response was not recognised", "error", perr)
		} else {
			credits = parsed
		}
	} else if ctx.Err() == nil {
		c.logger.Warn("Command Code credits lookup failed; credit windows unavailable this poll", "error", creditsErr)
	}

	var subscription *commandCodeSubscriptionResponse
	if subErr == nil {
		if parsed, perr := ParseCommandCodeSubscription(subscriptionBody); perr != nil {
			c.logger.Warn("Command Code subscription response was not recognised", "error", perr)
		} else {
			subscription = parsed
		}
	} else if ctx.Err() == nil {
		c.logger.Warn("Command Code subscription lookup failed; plan and reset unavailable this poll", "error", subErr)
	}

	// The usage summary is scoped to the billing period when the subscription
	// told us when it started; otherwise it returns the account's lifetime
	// totals, which is still a usable cost figure.
	since := ""
	if subscription != nil && subscription.Data.CurrentPeriodStart.t != nil {
		since = subscription.Data.CurrentPeriodStart.t.Format(time.RFC3339)
	}
	summaryBody, summaryErr := c.get(ctx, commandCodeSummaryPath(orgID, since))
	if summaryErr != nil && isCommandCodeAuthError(summaryErr) {
		return nil, summaryErr
	}
	var summary *commandCodeUsageSummaryResponse
	if summaryErr == nil {
		if parsed, perr := ParseCommandCodeSummary(summaryBody); perr != nil {
			c.logger.Warn("Command Code usage summary response was not recognised", "error", perr)
		} else {
			summary = parsed
		}
	} else if ctx.Err() == nil {
		c.logger.Warn("Command Code usage summary lookup failed; billing totals unavailable this poll", "error", summaryErr)
	}

	if credits == nil && subscription == nil && summary == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: no usage section could be read", ErrCommandCodeInvalidResponse)
	}

	raw := commandCodeRawJSON(whoamiBody, creditsBody, subscriptionBody, summaryBody)
	return BuildCommandCodeSnapshot(whoami, credits, subscription, summary, raw, c.now()), nil
}

// commandCodeOrgPath appends the orgId query parameter when the account
// belongs to an organisation.
func commandCodeOrgPath(path, orgID string) string {
	if orgID == "" {
		return path
	}
	return path + "?orgId=" + url.QueryEscape(orgID)
}

// commandCodeSummaryPath builds the usage-summary URL, scoping it to the
// billing period when one is known.
func commandCodeSummaryPath(orgID, since string) string {
	params := url.Values{}
	if orgID != "" {
		params.Set("orgId", orgID)
	}
	if since != "" {
		params.Set("since", since)
	}
	if len(params) == 0 {
		return "/alpha/usage/summary"
	}
	return "/alpha/usage/summary?" + params.Encode()
}

// commandCodeRawJSON keeps the four response bodies for the raw_json column so
// a schema change can be diagnosed from stored data. Bodies that failed to
// download contribute nothing.
func commandCodeRawJSON(bodies ...[]byte) string {
	labels := []string{"whoami", "credits", "subscriptions", "usage/summary"}
	var sb strings.Builder
	sb.WriteByte('{')
	first := true
	for i, body := range bodies {
		if len(body) == 0 {
			continue
		}
		if !first {
			sb.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&sb, "%q:", labels[i])
		sb.Write(body)
	}
	sb.WriteByte('}')
	return sb.String()
}

// get performs an authenticated GET and returns the body.
func (c *CommandCodeClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrCommandCodeNetworkError, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", commandCodeUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCommandCodeNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, commandCodeMaxBody))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: read body: %v", ErrCommandCodeNetworkError, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The body is deliberately dropped: auth failures often echo the
		// rejected credential, and the agent logs this error verbatim.
		// "Never log API keys" (CLAUDE.md). The status is self-describing.
		return nil, fmt.Errorf("%w: http %d (key rejected)", ErrCommandCodeUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrCommandCodeRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrCommandCodeServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: http %d: %s", ErrCommandCodeInvalidResponse, resp.StatusCode, commandCodeErrorDetail(body))
	}
}

// commandCodeErrorDetail extracts a short server message without echoing
// secrets. The alpha API reports failures as {"error":{"message":"..."}}.
func commandCodeErrorDetail(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if msg := strings.TrimSpace(envelope.Error.Message); msg != "" {
			return truncateCommandCodeDetail(msg)
		}
		if msg := strings.TrimSpace(envelope.Detail); msg != "" {
			return truncateCommandCodeDetail(msg)
		}
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "unknown"
	}
	return truncateCommandCodeDetail(s)
}

// commandCodeDetailMaxRunes caps an error detail. Slicing bytes would split a
// multi-byte rune and leave an invalid UTF-8 fragment in the log line.
const commandCodeDetailMaxRunes = 160

func truncateCommandCodeDetail(s string) string {
	runes := []rune(strings.Join(strings.Fields(s), " "))
	if len(runes) <= commandCodeDetailMaxRunes {
		return string(runes)
	}
	return string(runes[:commandCodeDetailMaxRunes])
}

func isCommandCodeAuthError(err error) bool {
	return errors.Is(err, ErrCommandCodeUnauthorized)
}

// IsCommandCodeAuthError reports whether err means the key was rejected.
func IsCommandCodeAuthError(err error) bool {
	return isCommandCodeAuthError(err)
}

// IsCommandCodeRateLimited reports whether err is a rate-limit signal.
func IsCommandCodeRateLimited(err error) bool {
	return errors.Is(err, ErrCommandCodeRateLimited)
}
