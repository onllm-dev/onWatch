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

// Custom errors for Moonshot API failures.
var (
	ErrMoonshotUnauthorized    = errors.New("moonshot: unauthorized - invalid API key")
	ErrMoonshotRateLimited     = errors.New("moonshot: rate limited")
	ErrMoonshotServerError     = errors.New("moonshot: server error")
	ErrMoonshotNetworkError    = errors.New("moonshot: network error")
	ErrMoonshotInvalidResponse = errors.New("moonshot: invalid response")
)

// MoonshotClient is an HTTP client for the Moonshot API.
type MoonshotClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	logger     *slog.Logger
}

// MoonshotOption configures a MoonshotClient.
type MoonshotOption func(*MoonshotClient)

// WithMoonshotBaseURL sets a custom base URL (for testing).
func WithMoonshotBaseURL(url string) MoonshotOption {
	return func(c *MoonshotClient) {
		c.baseURL = url
	}
}

// WithMoonshotTimeout sets a custom timeout (for testing).
func WithMoonshotTimeout(timeout time.Duration) MoonshotOption {
	return func(c *MoonshotClient) {
		c.httpClient.Timeout = timeout
	}
}

// NewMoonshotClient creates a new Moonshot API client.
func NewMoonshotClient(apiKey string, logger *slog.Logger, opts ...MoonshotOption) *MoonshotClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &MoonshotClient{
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
		baseURL: "https://api.moonshot.ai",
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchBalance retrieves the current balance information from the Moonshot API.
func (c *MoonshotClient) FetchBalance(ctx context.Context) (*MoonshotBalanceResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := c.baseURL + "/v1/users/me/balance"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("moonshot: creating request: %w", err)
	}

	// Set headers - Moonshot uses Bearer token authentication
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "onwatch/1.0")
	req.Header.Set("Accept", "application/json")

	// Log request (with redacted API key)
	c.logger.Debug("fetching Moonshot balance",
		"url", url,
		"api_key", redactMoonshotAPIKey(c.apiKey),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Check for context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrMoonshotNetworkError, err)
	}
	defer resp.Body.Close()

	// Log response status
	c.logger.Debug("Moonshot balance response received",
		"status", resp.StatusCode,
	)

	// Read response body (bounded to 64KB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrMoonshotInvalidResponse, err)
	}

	// Handle HTTP status codes
	switch {
	case resp.StatusCode == http.StatusOK:
		// continue
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrMoonshotUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrMoonshotRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrMoonshotServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("moonshot: unexpected status code %d", resp.StatusCode)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrMoonshotInvalidResponse)
	}

	balanceResp, err := ParseMoonshotResponse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMoonshotInvalidResponse, err)
	}

	// Log usage info
	c.logger.Debug("Moonshot balance fetched successfully",
		"available_balance", balanceResp.Data.AvailableBalance,
		"voucher_balance", balanceResp.Data.VoucherBalance,
		"cash_balance", balanceResp.Data.CashBalance,
	)

	return balanceResp, nil
}

// redactMoonshotAPIKey masks the API key for logging.
func redactMoonshotAPIKey(key string) string {
	if key == "" {
		return "(empty)"
	}

	if len(key) < 8 {
		return "***...***"
	}

	// Show first 4 chars and last 3 chars
	return key[:4] + "***...***" + key[len(key)-3:]
}
