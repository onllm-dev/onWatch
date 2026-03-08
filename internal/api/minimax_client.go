package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var (
	ErrMiniMaxUnauthorized    = errors.New("minimax: unauthorized - invalid api key")
	ErrMiniMaxAccessBlocked   = errors.New("minimax: upstream access blocked")
	ErrMiniMaxServerError     = errors.New("minimax: server error")
	ErrMiniMaxNetworkError    = errors.New("minimax: network error")
	ErrMiniMaxInvalidResponse = errors.New("minimax: invalid response")
)

// MiniMaxClient is an HTTP client for the MiniMax coding plan remains API.
type MiniMaxClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	logger     *slog.Logger
}

// MiniMaxOption configures MiniMaxClient.
type MiniMaxOption func(*MiniMaxClient)

// WithMiniMaxBaseURL sets a custom base URL (testing).
func WithMiniMaxBaseURL(url string) MiniMaxOption {
	return func(c *MiniMaxClient) {
		c.baseURL = url
	}
}

// WithMiniMaxTimeout sets a custom timeout (testing).
func WithMiniMaxTimeout(d time.Duration) MiniMaxOption {
	return func(c *MiniMaxClient) {
		c.httpClient.Timeout = d
	}
}

// NewMiniMaxClient creates a new MiniMax client.
func NewMiniMaxClient(apiKey string, logger *slog.Logger, opts ...MiniMaxOption) *MiniMaxClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &MiniMaxClient{
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
		baseURL: "https://api.minimax.io/v1/api/openplatform/coding_plan/remains",
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// FetchRemains fetches current model remains from MiniMax.
func (c *MiniMaxClient) FetchRemains(ctx context.Context) (*MiniMaxRemainsResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("minimax: creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	c.logger.Debug("fetching MiniMax remains", "url", c.baseURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrMiniMaxNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrMiniMaxInvalidResponse, err)
	}

	var remainsResp MiniMaxRemainsResponse
	parseErr := json.Unmarshal(body, &remainsResp)
	bodySnippet := minimaxBodySnippet(body)

	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("MiniMax non-OK response",
			"status", resp.StatusCode,
			"contentType", resp.Header.Get("Content-Type"),
			"server", resp.Header.Get("Server"),
			"bodySnippet", bodySnippet,
		)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrMiniMaxUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		if parseErr == nil && remainsResp.BaseResp.StatusCode == 1004 {
			return nil, ErrMiniMaxUnauthorized
		}
		if minimaxAccessBlocked(resp, body) {
			if bodySnippet == "" {
				return nil, ErrMiniMaxAccessBlocked
			}
			return nil, fmt.Errorf("%w: %s", ErrMiniMaxAccessBlocked, bodySnippet)
		}
		if bodySnippet == "" {
			return nil, fmt.Errorf("minimax: forbidden response")
		}
		return nil, fmt.Errorf("minimax: forbidden response: %s", bodySnippet)
	case resp.StatusCode >= 500:
		if bodySnippet == "" {
			return nil, fmt.Errorf("%w: status %d", ErrMiniMaxServerError, resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: status %d: %s", ErrMiniMaxServerError, resp.StatusCode, bodySnippet)
	default:
		if bodySnippet == "" {
			return nil, fmt.Errorf("minimax: unexpected status code %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("minimax: unexpected status code %d: %s", resp.StatusCode, bodySnippet)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrMiniMaxInvalidResponse)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrMiniMaxInvalidResponse, parseErr)
	}

	if remainsResp.BaseResp.StatusCode != 0 {
		if remainsResp.BaseResp.StatusCode == 1004 {
			return nil, ErrMiniMaxUnauthorized
		}
		return nil, fmt.Errorf("minimax: api error code=%d, msg=%s", remainsResp.BaseResp.StatusCode, remainsResp.BaseResp.StatusMsg)
	}

	c.logger.Debug("MiniMax remains fetched", "models", remainsResp.ActiveModelNames())
	return &remainsResp, nil
}

func minimaxAccessBlocked(resp *http.Response, body []byte) bool {
	server := strings.ToLower(resp.Header.Get("Server"))
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	snippet := strings.ToLower(minimaxBodySnippet(body))
	return strings.Contains(server, "cloudflare") ||
		strings.Contains(contentType, "text/html") ||
		strings.Contains(snippet, "attention required") ||
		strings.Contains(snippet, "please enable cookies") ||
		strings.Contains(snippet, "you have been blocked")
}

func minimaxBodySnippet(body []byte) string {
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
