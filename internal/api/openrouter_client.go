package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Custom errors for OpenRouter API failures.
var (
	ErrOpenRouterUnauthorized  = errors.New("openrouter: unauthorized - invalid API key")
	ErrOpenRouterRateLimited   = errors.New("openrouter: rate limited")
	ErrOpenRouterServerError   = errors.New("openrouter: server error")
	ErrOpenRouterNetworkError  = errors.New("openrouter: network error")
	ErrOpenRouterInvalidResponse = errors.New("openrouter: invalid response")
)

// OpenRouterClient is an HTTP client for the OpenRouter API.
type OpenRouterClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	logger     *slog.Logger
}

// OpenRouterOption configures an OpenRouterClient.
type OpenRouterOption func(*OpenRouterClient)

// WithOpenRouterBaseURL sets a custom base URL (for testing).
func WithOpenRouterBaseURL(url string) OpenRouterOption {
	return func(c *OpenRouterClient) {
		c.baseURL = url
	}
}

// WithOpenRouterTimeout sets a custom timeout (for testing).
func WithOpenRouterTimeout(timeout time.Duration) OpenRouterOption {
	return func(c *OpenRouterClient) {
		c.httpClient.Timeout = timeout
	}
}

// NewOpenRouterClient creates a new OpenRouter API client.
func NewOpenRouterClient(apiKey string, logger *slog.Logger, opts ...OpenRouterOption) *OpenRouterClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &OpenRouterClient{
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
		apiKey:  apiKey,
		baseURL: "https://openrouter.ai",
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchUsage retrieves the current usage information from the OpenRouter API.
func (c *OpenRouterClient) FetchUsage(ctx context.Context) (*OpenRouterAuthKeyResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := c.baseURL + "/api/v1/auth/key"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: creating request: %w", err)
	}

	// Set headers - OpenRouter uses Bearer token authentication
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "onwatch/1.0")
	req.Header.Set("Accept", "application/json")

	// Log request (with redacted API key)
	c.logger.Debug("fetching OpenRouter usage",
		"url", url,
		"api_key", redactOpenRouterAPIKey(c.apiKey),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Check for context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOpenRouterNetworkError, err)
	}
	defer resp.Body.Close()

	// Log response status
	c.logger.Debug("OpenRouter usage response received",
		"status", resp.StatusCode,
	)

	// Read response body (bounded to 64KB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrOpenRouterInvalidResponse, err)
	}

	// Handle HTTP status codes
	switch {
	case resp.StatusCode == http.StatusOK:
		// continue
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrOpenRouterUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrOpenRouterRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrOpenRouterServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("openrouter: unexpected status code %d", resp.StatusCode)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrOpenRouterInvalidResponse)
	}

	authKeyResp, err := ParseOpenRouterResponse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenRouterInvalidResponse, err)
	}

	// Log usage info
	c.logger.Debug("OpenRouter usage fetched successfully",
		"usage", authKeyResp.Data.Usage,
		"usage_daily", authKeyResp.Data.UsageDaily,
		"limit", authKeyResp.Data.Limit,
		"is_free_tier", authKeyResp.Data.IsFreeTier,
	)

	return authKeyResp, nil
}

// redactOpenRouterAPIKey masks the API key for logging.
func redactOpenRouterAPIKey(key string) string {
	if key == "" {
		return "(empty)"
	}

	if len(key) < 8 {
		return "***...***"
	}

	// Show first 4 chars and last 3 chars
	return key[:4] + "***...***" + key[len(key)-3:]
}
