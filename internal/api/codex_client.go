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
	"sync"
	"time"
)

var (
	ErrCodexUnauthorized    = errors.New("codex: unauthorized")
	ErrCodexForbidden       = errors.New("codex: forbidden")
	ErrCodexAccessBlocked   = errors.New("codex: access blocked")
	ErrCodexServerError     = errors.New("codex: server error")
	ErrCodexNetworkError    = errors.New("codex: network error")
	ErrCodexInvalidResponse = errors.New("codex: invalid response")
)

// CodexClient is an HTTP client for Codex OAuth usage API.
type CodexClient struct {
	httpClient *http.Client
	baseURL    string
	logger     *slog.Logger

	token   string
	tokenMu sync.RWMutex
	account string
	acctMu  sync.RWMutex

	fallbackMu      sync.RWMutex
	fallbackBaseURL string

	// starterURL is the Codex Responses endpoint used by the auto quota-starter
	// (Beta). Defaults to codexResponsesURL; overridable for tests.
	starterURL string
}

// CodexOption configures a CodexClient.
type CodexOption func(*CodexClient)

// WithCodexBaseURL sets custom base URL.
func WithCodexBaseURL(url string) CodexOption {
	return func(c *CodexClient) {
		c.baseURL = url
	}
}

// WithCodexTimeout sets custom timeout.
func WithCodexTimeout(timeout time.Duration) CodexOption {
	return func(c *CodexClient) {
		c.httpClient.Timeout = timeout
	}
}

// WithCodexStarterURL overrides the Codex Responses endpoint used by the auto
// quota-starter ping. Primarily for tests.
func WithCodexStarterURL(url string) CodexOption {
	return func(c *CodexClient) {
		c.starterURL = url
	}
}

// NewCodexClient creates a Codex usage API client.
func NewCodexClient(token string, logger *slog.Logger, opts ...CodexOption) *CodexClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &CodexClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: 10 * time.Second,
				IdleConnTimeout:       10 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		token:      token,
		baseURL:    "https://chatgpt.com/backend-api/wham/usage",
		starterURL: codexResponsesURL,
		logger:     logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// SetToken updates bearer token for API calls. A changed token also releases
// the cached endpoint selection: new credentials may carry different endpoint
// access, so the next poll re-probes the default path (issue #127). An
// unchanged token is a no-op - the pre-poll credential re-read installs the
// same token every cycle and must not cost a probe each time.
func (c *CodexClient) SetToken(token string) {
	c.tokenMu.Lock()
	changed := c.token != token
	c.token = token
	c.tokenMu.Unlock()

	if changed {
		c.clearFallbackBaseURL()
	}
}

// SetAccountID updates account id metadata.
func (c *CodexClient) SetAccountID(accountID string) {
	c.acctMu.Lock()
	defer c.acctMu.Unlock()
	c.account = accountID
}

func (c *CodexClient) getToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *CodexClient) getAccountID() string {
	c.acctMu.RLock()
	defer c.acctMu.RUnlock()
	return c.account
}

func buildCodexFallbackBaseURL(rawBaseURL string) (string, bool) {
	u, err := url.Parse(rawBaseURL)
	if err != nil {
		return "", false
	}
	switch {
	case strings.Contains(u.Path, "/api/codex/usage"):
		u.Path = strings.Replace(u.Path, "/api/codex/usage", "/backend-api/wham/usage", 1)
	case strings.Contains(u.Path, "/backend-api/wham/usage"):
		u.Path = strings.Replace(u.Path, "/backend-api/wham/usage", "/api/codex/usage", 1)
	default:
		return "", false
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), true
}

func (c *CodexClient) getFallbackBaseURL() string {
	c.fallbackMu.RLock()
	defer c.fallbackMu.RUnlock()
	return c.fallbackBaseURL
}

func (c *CodexClient) setFallbackBaseURL(url string) {
	c.fallbackMu.Lock()
	defer c.fallbackMu.Unlock()
	c.fallbackBaseURL = url
}

// clearFallbackBaseURL releases the cached endpoint so the next request starts
// from the default base URL again.
func (c *CodexClient) clearFallbackBaseURL() {
	c.fallbackMu.Lock()
	defer c.fallbackMu.Unlock()
	c.fallbackBaseURL = ""
}

// codexAccessBlocked reports whether a 403 is a bot challenge or edge block
// rather than an OAuth rejection. Challenges answer with HTML (or a Cloudflare
// banner) instead of the API's JSON error body; treating them as auth failures
// would burn the agent's failure budget and pause polling over a problem no
// re-authentication can fix.
func codexAccessBlocked(resp *http.Response, body []byte) bool {
	server := strings.ToLower(resp.Header.Get("Server"))
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	snippet := strings.ToLower(codexBodySnippet(body))
	return strings.Contains(server, "cloudflare") ||
		strings.Contains(contentType, "text/html") ||
		strings.Contains(snippet, "<html") ||
		strings.Contains(snippet, "attention required") ||
		strings.Contains(snippet, "just a moment") ||
		strings.Contains(snippet, "please enable cookies") ||
		strings.Contains(snippet, "you have been blocked")
}

// codexBodySnippet renders a bounded, single-line excerpt of a response body
// for logging and error messages.
func codexBodySnippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 240 {
		return text[:240] + "..."
	}
	return text
}

func (c *CodexClient) doUsageRequest(ctx context.Context, usageURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("codex: creating request: %w", err)
	}

	token := c.getToken()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "onwatch/1.0")
	if accountID := c.getAccountID(); accountID != "" {
		req.Header.Set("X-Account-Id", accountID)
		req.Header.Set("ChatClaude-Account-Id", accountID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCodexNetworkError, err)
	}
	return resp, nil
}

// FetchUsage fetches Codex OAuth usage state.
//
// Codex serves usage from one of two paths and the working one differs per
// account, so a 404 probes the alternate path. The probe result is cached only
// when it actually succeeds, and a cached endpoint is released as soon as it
// stops working: pinning an unsuccessful endpoint would keep polling away from
// a healthy primary until the process restarted (issue #127).
func (c *CodexClient) FetchUsage(ctx context.Context) (*CodexUsageResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	usageURL := c.baseURL
	usingCached := false
	if fallback := c.getFallbackBaseURL(); fallback != "" {
		usageURL = fallback
		usingCached = true
	}

	resp, err := c.doUsageRequest(reqCtx, usageURL)
	if err != nil {
		if usingCached {
			c.clearFallbackBaseURL()
		}
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		if altURL, ok := buildCodexFallbackBaseURL(usageURL); ok {
			resp.Body.Close()
			resp, err = c.doUsageRequest(reqCtx, altURL)
			if err != nil {
				c.clearFallbackBaseURL()
				return nil, err
			}
			// Cache the alternate path only once it has served a usable
			// response, and never cache the default - an empty selection
			// already means "use the default".
			if resp.StatusCode == http.StatusOK && altURL != c.baseURL {
				c.setFallbackBaseURL(altURL)
			} else {
				c.clearFallbackBaseURL()
			}
		}
	} else if usingCached && resp.StatusCode != http.StatusOK {
		c.clearFallbackBaseURL()
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrCodexUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		if readErr == nil && codexAccessBlocked(resp, body) {
			snippet := codexBodySnippet(body)
			c.logger.Warn("Codex access blocked (challenge response, not an auth failure)",
				"contentType", resp.Header.Get("Content-Type"),
				"server", resp.Header.Get("Server"),
				"bodySnippet", snippet)
			if snippet == "" {
				return nil, ErrCodexAccessBlocked
			}
			return nil, fmt.Errorf("%w: %s", ErrCodexAccessBlocked, snippet)
		}
		return nil, ErrCodexForbidden
	case resp.StatusCode >= 500:
		return nil, ErrCodexServerError
	default:
		return nil, fmt.Errorf("codex: unexpected status code %d", resp.StatusCode)
	}

	if readErr != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrCodexInvalidResponse, readErr)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrCodexInvalidResponse)
	}

	var usageResp CodexUsageResponse
	if err := json.Unmarshal(body, &usageResp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCodexInvalidResponse, err)
	}

	return &usageResp, nil
}
