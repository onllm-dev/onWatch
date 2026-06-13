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

// Custom errors for DeepSeek API failures.
var (
	ErrDeepSeekUnauthorized    = errors.New("deepseek: unauthorized - invalid API key")
	ErrDeepSeekRateLimited     = errors.New("deepseek: rate limited")
	ErrDeepSeekServerError     = errors.New("deepseek: server error")
	ErrDeepSeekNetworkError    = errors.New("deepseek: network error")
	ErrDeepSeekInvalidResponse = errors.New("deepseek: invalid response")
)

// DeepSeekClient is an HTTP client for the DeepSeek API.
type DeepSeekClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	logger     *slog.Logger
}

// DeepSeekOption configures a DeepSeekClient.
type DeepSeekOption func(*DeepSeekClient)

// WithDeepSeekBaseURL sets a custom base URL (for testing).
func WithDeepSeekBaseURL(url string) DeepSeekOption {
	return func(c *DeepSeekClient) {
		c.baseURL = url
	}
}

// WithDeepSeekTimeout sets a custom timeout (for testing).
func WithDeepSeekTimeout(timeout time.Duration) DeepSeekOption {
	return func(c *DeepSeekClient) {
		c.httpClient.Timeout = timeout
	}
}

// NewDeepSeekClient creates a new DeepSeek API client.
func NewDeepSeekClient(apiKey string, logger *slog.Logger, opts ...DeepSeekOption) *DeepSeekClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &DeepSeekClient{
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
		baseURL: "https://api.deepseek.com",
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchBalance retrieves the current balance information from the DeepSeek API.
func (c *DeepSeekClient) FetchBalance(ctx context.Context) (*DeepSeekBalanceResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := c.baseURL + "/user/balance"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("deepseek: creating request: %w", err)
	}

	// Set headers - DeepSeek uses Bearer token authentication
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "onwatch/1.0")
	req.Header.Set("Accept", "application/json")

	// Log request (with redacted API key)
	c.logger.Debug("fetching DeepSeek balance",
		"url", url,
		"api_key", redactDeepSeekAPIKey(c.apiKey),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Check for context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrDeepSeekNetworkError, err)
	}
	defer resp.Body.Close()

	// Log response status
	c.logger.Debug("DeepSeek balance response received",
		"status", resp.StatusCode,
	)

	// Read response body (bounded to 64KB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrDeepSeekInvalidResponse, err)
	}

	// Handle HTTP status codes
	switch {
	case resp.StatusCode == http.StatusOK:
		// continue
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrDeepSeekUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrDeepSeekRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrDeepSeekServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("deepseek: unexpected status code %d", resp.StatusCode)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrDeepSeekInvalidResponse)
	}

	balanceResp, err := ParseDeepSeekResponse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDeepSeekInvalidResponse, err)
	}

	// Log usage info
	c.logger.Debug("DeepSeek balance fetched successfully",
		"is_available", balanceResp.IsAvailable,
	)

	return balanceResp, nil
}

// redactDeepSeekAPIKey masks the API key for logging.
func redactDeepSeekAPIKey(key string) string {
	if key == "" {
		return "(empty)"
	}

	if len(key) < 8 {
		return "***...***"
	}

	// Show first 4 chars and last 3 chars
	return key[:4] + "***...***" + key[len(key)-3:]
}
