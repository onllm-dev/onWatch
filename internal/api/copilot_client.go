package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Custom errors for Copilot API failures.
var (
	ErrCopilotUnauthorized    = errors.New("copilot: unauthorized - invalid token")
	ErrCopilotForbidden       = errors.New("copilot: forbidden - token revoked or missing scope")
	ErrCopilotServerError     = errors.New("copilot: server error")
	ErrCopilotNetworkError    = errors.New("copilot: network error")
	ErrCopilotInvalidResponse = errors.New("copilot: invalid response")
)

// CopilotClient is an HTTP client for the GitHub Copilot internal API.
type CopilotClient struct {
	httpClient *http.Client
	token      string
	baseURL    string
	logger     *slog.Logger
}

// CopilotOption configures a CopilotClient.
type CopilotOption func(*CopilotClient)

// WithCopilotBaseURL sets a custom base URL (for testing).
func WithCopilotBaseURL(url string) CopilotOption {
	return func(c *CopilotClient) {
		c.baseURL = url
	}
}

// WithCopilotTimeout sets a custom timeout (for testing).
func WithCopilotTimeout(d time.Duration) CopilotOption {
	return func(c *CopilotClient) {
		c.httpClient.Timeout = d
	}
}

// NewCopilotClient creates a new Copilot API client.
func NewCopilotClient(token string, logger *slog.Logger, opts ...CopilotOption) *CopilotClient {
	client := &CopilotClient{
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
		token:   token,
		baseURL: "https://api.github.com/copilot_internal/user",
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchQuotas retrieves the current quota information from the Copilot API.
func (c *CopilotClient) FetchQuotas(ctx context.Context) (*CopilotUserResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot: creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "onwatch/1.0")

	// Log request (with redacted token)
	c.logger.Debug("fetching Copilot quotas",
		"url", c.baseURL,
		"token", redactCopilotToken(c.token),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCopilotNetworkError, err)
	}
	defer resp.Body.Close()

	// Log response status
	c.logger.Debug("Copilot quota response received",
		"status", resp.StatusCode,
	)

	// Handle HTTP status codes
	switch {
	case resp.StatusCode == http.StatusOK:
		// Continue to parse response
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrCopilotUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrCopilotForbidden
	case resp.StatusCode >= 500:
		return nil, ErrCopilotServerError
	default:
		return nil, fmt.Errorf("copilot: unexpected status code %d", resp.StatusCode)
	}

	// Read response body (bounded to 64KB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrCopilotInvalidResponse, err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrCopilotInvalidResponse)
	}

	var quotaResp CopilotUserResponse
	if err := json.Unmarshal(body, &quotaResp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCopilotInvalidResponse, err)
	}

	// Log active quota names on success
	if names := quotaResp.ActiveQuotaNames(); len(names) > 0 {
		c.logger.Debug("Copilot quotas fetched successfully",
			"active_quotas", names,
			"plan", quotaResp.CopilotPlan,
		)
	}

	return &quotaResp, nil
}

// redactCopilotToken masks the token for logging.
func redactCopilotToken(key string) string {
	if key == "" {
		return "(empty)"
	}

	if len(key) < 8 {
		return "***...***"
	}

	// Show first 4 chars and last 3 chars
	return key[:4] + "***...***" + key[len(key)-3:]
}
