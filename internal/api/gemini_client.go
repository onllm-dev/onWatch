package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var (
	ErrGeminiUnauthorized    = errors.New("gemini: unauthorized")
	ErrGeminiForbidden       = errors.New("gemini: forbidden")
	ErrGeminiServerError     = errors.New("gemini: server error")
	ErrGeminiNetworkError    = errors.New("gemini: network error")
	ErrGeminiInvalidResponse = errors.New("gemini: invalid response")
)

const (
	geminiDefaultBaseURL = "https://cloudcode-pa.googleapis.com"
	geminiQuotaPath      = "/v1internal:retrieveUserQuota"
	geminiTierPath       = "/v1internal:loadCodeAssist"
)

// GeminiClient is an HTTP client for Gemini internal quota APIs.
type GeminiClient struct {
	httpClient *http.Client
	baseURL    string
	logger     *slog.Logger

	token     string
	tokenMu   sync.RWMutex
	projectID string
	projectMu sync.RWMutex
}

// GeminiOption configures a GeminiClient.
type GeminiOption func(*GeminiClient)

// WithGeminiBaseURL sets a custom base URL.
func WithGeminiBaseURL(url string) GeminiOption {
	return func(c *GeminiClient) {
		c.baseURL = url
	}
}

// WithGeminiTimeout sets a custom timeout.
func WithGeminiTimeout(timeout time.Duration) GeminiOption {
	return func(c *GeminiClient) {
		c.httpClient.Timeout = timeout
	}
}

// NewGeminiClient creates a Gemini quota API client.
func NewGeminiClient(token string, logger *slog.Logger, opts ...GeminiOption) *GeminiClient {
	if logger == nil {
		logger = slog.Default()
	}

	client := &GeminiClient{
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
		baseURL: geminiDefaultBaseURL,
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// SetToken updates the bearer token for API calls.
func (c *GeminiClient) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

func (c *GeminiClient) getToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

// SetProjectID sets the project ID for quota requests.
func (c *GeminiClient) SetProjectID(projectID string) {
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	c.projectID = projectID
}

func (c *GeminiClient) getProjectID() string {
	c.projectMu.RLock()
	defer c.projectMu.RUnlock()
	return c.projectID
}

// FetchQuotas fetches per-model quota data from the retrieveUserQuota endpoint.
func (c *GeminiClient) FetchQuotas(ctx context.Context) (*GeminiQuotaResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Build request body
	payload := map[string]interface{}{}
	if projectID := c.getProjectID(); projectID != "" {
		payload["project"] = projectID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := c.baseURL + geminiQuotaPath
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: creating request: %w", err)
	}

	token := c.getToken()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "onwatch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrGeminiNetworkError, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrGeminiUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrGeminiForbidden
	case resp.StatusCode >= 500:
		return nil, ErrGeminiServerError
	default:
		return nil, fmt.Errorf("gemini: unexpected status code %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrGeminiInvalidResponse, err)
	}
	if len(respBody) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrGeminiInvalidResponse)
	}

	var quotaResp GeminiQuotaResponse
	if err := json.Unmarshal(respBody, &quotaResp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGeminiInvalidResponse, err)
	}

	return &quotaResp, nil
}

// FetchTier fetches tier and project information from the loadCodeAssist endpoint.
func (c *GeminiClient) FetchTier(ctx context.Context) (*GeminiTierResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	payload := map[string]interface{}{
		"metadata": map[string]string{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal tier request: %w", err)
	}

	url := c.baseURL + geminiTierPath
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: creating tier request: %w", err)
	}

	token := c.getToken()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "onwatch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrGeminiNetworkError, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrGeminiUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrGeminiForbidden
	case resp.StatusCode >= 500:
		return nil, ErrGeminiServerError
	default:
		return nil, fmt.Errorf("gemini: unexpected tier status code %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading tier body: %v", ErrGeminiInvalidResponse, err)
	}

	var tierResp GeminiTierResponse
	if err := json.Unmarshal(respBody, &tierResp); err != nil {
		return nil, fmt.Errorf("%w: tier: %v", ErrGeminiInvalidResponse, err)
	}

	return &tierResp, nil
}
