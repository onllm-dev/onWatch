package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrOpenCodeGoUnauthorized   = errors.New("opencodego: unauthorized - invalid or expired cookie")
	ErrOpenCodeGoServerError    = errors.New("opencodego: server error")
	ErrOpenCodeGoInvalidResponse = errors.New("opencodego: invalid response")
)

const (
	OpenCodeGoBaseURL          = "https://opencode.ai"
	OpenCodeGoServerURL        = "https://opencode.ai/_server"
	OpenCodeGoWorkspaceServerID = "def39973159c7f0483d8793a822b8dbb10d067e12c65455fcb4608459ba0234f"
	OpenCodeGoUserAgent        = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"
)

// wrkPattern matches workspace IDs in responses.
var wrkPattern = regexp.MustCompile(`"wrk_[^"]*"`)

// OpenCodeGoClient handles HTTP communication with the OpenCode Go dashboard.
type OpenCodeGoClient struct {
	httpClient  *http.Client
	cookie      string
	cookieMu    sync.RWMutex
	baseURL     string
	workspaceID string // cached resolved workspace ID
	logger      *slog.Logger
}

// OpenCodeGoOption configures an OpenCodeGoClient.
type OpenCodeGoOption func(*OpenCodeGoClient)

// WithOpenCodeGoBaseURL sets a custom base URL for testing.
func WithOpenCodeGoBaseURL(url string) OpenCodeGoOption {
	return func(c *OpenCodeGoClient) {
		c.baseURL = url
	}
}

// WithOpenCodeGoTimeout sets a custom HTTP timeout.
func WithOpenCodeGoTimeout(timeout time.Duration) OpenCodeGoOption {
	return func(c *OpenCodeGoClient) {
		c.httpClient.Timeout = timeout
	}
}

// WithOpenCodeGoWorkspaceID sets a pre-resolved workspace ID (skips resolution).
func WithOpenCodeGoWorkspaceID(id string) OpenCodeGoOption {
	return func(c *OpenCodeGoClient) {
		c.workspaceID = id
	}
}

// SetWorkspaceID sets a pre-resolved workspace ID.
func (c *OpenCodeGoClient) SetWorkspaceID(id string) {
	c.workspaceID = id
}

// NewOpenCodeGoClient creates a new OpenCode Go client.
func NewOpenCodeGoClient(cookie string, logger *slog.Logger, opts ...OpenCodeGoOption) *OpenCodeGoClient {
	client := &OpenCodeGoClient{
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
		},
		cookie:  normalizeCookieValue(cookie),
		baseURL: OpenCodeGoBaseURL,
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// SetCookie updates the auth cookie. The value is normalized to extract only
// auth and __Host-auth cookies from a raw cookie header string.
func (c *OpenCodeGoClient) SetCookie(cookie string) {
	c.cookieMu.Lock()
	defer c.cookieMu.Unlock()
	c.cookie = normalizeCookieValue(cookie)
}

// normalizeCookieValue extracts only auth and __Host-auth cookies from a raw
// cookie header string (matching CodexBar's OpenCodeWebCookieSupport behavior).
func normalizeCookieValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, ";")
	var authParts []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if strings.HasPrefix(trimmed, "auth=") || strings.HasPrefix(trimmed, "__Host-auth=") {
			authParts = append(authParts, trimmed)
		}
	}
	if len(authParts) > 0 {
		return strings.Join(authParts, "; ")
	}

	// If no auth cookies found, return as-is (user may have passed just the value)
	return raw
}

func (c *OpenCodeGoClient) getCookie() string {
	c.cookieMu.RLock()
	defer c.cookieMu.RUnlock()
	return c.cookie
}

// GetCookie returns the current auth cookie.
func (c *OpenCodeGoClient) GetCookie() string {
	return c.getCookie()
}

// FetchQuotas fetches the latest OpenCode Go usage data.
func (c *OpenCodeGoClient) FetchQuotas(ctx context.Context) (*OpenCodeGoSnapshot, error) {
	cookie := c.getCookie()
	if cookie == "" {
		return nil, fmt.Errorf("%w: no cookie configured", ErrOpenCodeGoUnauthorized)
	}

	// Step 1: Resolve workspace ID
	workspaceID, err := c.resolveWorkspaceID(ctx)
	if err != nil {
		return nil, fmt.Errorf("opencodego: resolve workspace: %w", err)
	}

	c.logger.Debug("opencodego: resolved workspace", "workspace_id", workspaceID)

	// Step 2: Fetch the usage page
	pageURL := fmt.Sprintf("%s/workspace/%s/go", c.baseURL, workspaceID)
	body, err := c.doGet(ctx, pageURL)
	if err != nil {
		return nil, err
	}

	// Step 3: Parse the response
	_, diag := DebugParse(body)
	c.logger.Debug("opencodego: parse diagnostics", "diag", diag)
	resp, err := ParseOpenCodeGoUsageResponse(body)
	if err != nil {
		if errors.Is(err, ErrOpenCodeGoNotSignedIn) {
			return nil, fmt.Errorf("%w: %v", ErrOpenCodeGoUnauthorized, err)
		}
		// Log raw response prefix for debugging
		bodyPreview := string(body)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		c.logger.Error("opencodego: parse failed, raw response prefix", "body_preview", bodyPreview)
		return nil, fmt.Errorf("opencodego: parse usage: %w", err)
	}

	now := time.Now().UTC()
	snapshot := resp.ToSnapshot(now)

	c.logger.Debug("opencodego: quotas fetched",
		"window_count", len(snapshot.Windows),
	)

	return snapshot, nil
}

// resolveWorkspaceID resolves the workspace ID from the OpenCode server endpoint.
func (c *OpenCodeGoClient) resolveWorkspaceID(ctx context.Context) (string, error) {
	if c.workspaceID != "" {
		return c.workspaceID, nil
	}

	rpcURL := OpenCodeGoServerURL + "?id=" + OpenCodeGoWorkspaceServerID
	body, err := c.doServerGet(ctx, rpcURL)
	if err != nil {
		id := extractWorkspaceID(body)
		if id != "" {
			c.workspaceID = id
			return id, nil
		}
		// Try fallback: POST with X-Server-Id header
		return c.resolveWorkspaceIDFallback(ctx)
	}

	id := extractWorkspaceID(body)
	if id != "" {
		c.workspaceID = id
		return id, nil
	}

	// Fallback via POST
	id, err = c.resolveWorkspaceIDFallback(ctx)
	if err == nil {
		c.workspaceID = id
	}
	return id, err
}

// doServerGet performs a GET request to the _server RPC endpoint with full headers.
func (c *OpenCodeGoClient) doServerGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setCommonHeaders(req)
	req.Header.Set("X-Server-Id", OpenCodeGoWorkspaceServerID)
	req.Header.Set("X-Server-Instance", "server-fn:"+newUUID())
	req.Header.Set("Origin", OpenCodeGoBaseURL)
	req.Header.Set("Referer", OpenCodeGoBaseURL)
	req.Header.Set("Accept", "text/javascript, application/json;q=0.9, */*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOpenCodeGoNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("server GET returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// resolveWorkspaceIDFallback tries the POST fallback for workspace resolution.
func (c *OpenCodeGoClient) resolveWorkspaceIDFallback(ctx context.Context) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, OpenCodeGoServerURL, bytes.NewReader([]byte("[]")))
	if err != nil {
		return "", fmt.Errorf("create workspace request: %w", err)
	}

	c.setCommonHeaders(req)
	req.Header.Set("X-Server-Id", OpenCodeGoWorkspaceServerID)
	req.Header.Set("X-Server-Instance", "server-fn:"+newUUID())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", OpenCodeGoBaseURL)
	req.Header.Set("Referer", OpenCodeGoBaseURL)
	req.Header.Set("Accept", "text/javascript, application/json;q=0.9, */*;q=0.8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrOpenCodeGoNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("read workspace response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workspace resolution returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	id := extractWorkspaceID(body)
	if id == "" {
		return "", fmt.Errorf("no workspace ID found in response")
	}

	return id, nil
}

// doGet performs a GET request with common headers and cookie auth.
func (c *OpenCodeGoClient) doGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOpenCodeGoNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP %d", ErrOpenCodeGoUnauthorized, resp.StatusCode)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: HTTP %d", ErrOpenCodeGoServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
}

// setCommonHeaders sets the common request headers including cookie auth.
func (c *OpenCodeGoClient) setCommonHeaders(req *http.Request) {
	cookie := c.getCookie()
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	req.Header.Set("User-Agent", OpenCodeGoUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/javascript,application/json;q=0.9,*/*;q=0.8")
}

// extractWorkspaceID extracts a workspace ID (wrk_ prefix) from response body.
func extractWorkspaceID(body []byte) string {
	matches := wrkPattern.FindAll(body, -1)
	for _, match := range matches {
		id := string(match)
		// Strip surrounding quotes
		id = trimQuotes(id)
		if len(id) > 4 {
			return id
		}
	}
	return ""
}

func trimQuotes(s string) string {
	for len(s) > 0 && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

func newUUID() string {
	// Simple UUID v4 generation - sufficient for request correlation
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(time.Now().UnixNano()>>((15-i)*4)) % 16
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
