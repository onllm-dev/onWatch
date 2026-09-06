package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	ollamaDefaultBaseURL = "https://ollama.com"
	ollamaUserAgent      = "onWatch"
	ollamaTimeout        = 20 * time.Second
	ollamaMaxBodyBytes   = 1 << 20 // 1 MiB
)

var (
	ErrOllamaMissingAPIKey   = errors.New("ollama: missing API key")
	ErrOllamaUnauthorized    = errors.New("ollama: unauthorized - invalid or revoked API key")
	ErrOllamaRateLimited     = errors.New("ollama: rate limited")
	ErrOllamaServerError     = errors.New("ollama: server error")
	ErrOllamaNetworkError    = errors.New("ollama: network error")
	ErrOllamaInvalidResponse = errors.New("ollama: invalid response")
)

// OllamaClient talks to ollama.com with an API key from ollama.com/settings/keys.
type OllamaClient struct {
	httpClient   *http.Client
	logger       *slog.Logger
	apiKey       string
	baseURL      string
	monthlyLimit float64
	resetDay     int
	now          func() time.Time
}

type OllamaClientOption func(*OllamaClient)

func WithOllamaBaseURL(baseURL string) OllamaClientOption {
	return func(c *OllamaClient) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

func WithOllamaTimeout(timeout time.Duration) OllamaClientOption {
	return func(c *OllamaClient) {
		c.httpClient.Timeout = timeout
	}
}

func WithOllamaHTTPTransport(rt http.RoundTripper) OllamaClientOption {
	return func(c *OllamaClient) {
		c.httpClient.Transport = rt
	}
}

// WithOllamaMonthlyLimit overrides the plan-derived included usage cap (USD).
func WithOllamaMonthlyLimit(usd float64) OllamaClientOption {
	return func(c *OllamaClient) {
		c.monthlyLimit = usd
	}
}

// WithOllamaResetDay overrides the reset day of month (1-31).
func WithOllamaResetDay(day int) OllamaClientOption {
	return func(c *OllamaClient) {
		c.resetDay = day
	}
}

func withOllamaNow(now func() time.Time) OllamaClientOption {
	return func(c *OllamaClient) {
		c.now = now
	}
}

func NewOllamaClient(apiKey string, logger *slog.Logger, opts ...OllamaClientOption) *OllamaClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &OllamaClient{
		httpClient: &http.Client{
			Timeout: ollamaTimeout,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: ollamaTimeout,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		logger:  logger,
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: ollamaDefaultBaseURL,
		now:     time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// FetchSnapshot polls /api/usage (required) and /api/me (best effort) and
// builds a snapshot. A failing /api/me only costs plan, account and derived
// reset information; usage is still recorded.
func (c *OllamaClient) FetchSnapshot(ctx context.Context) (*OllamaSnapshot, error) {
	if c.apiKey == "" {
		return nil, ErrOllamaMissingAPIKey
	}

	usageBody, err := c.do(ctx, http.MethodGet, "/api/usage")
	if err != nil {
		return nil, err
	}
	usage, err := ParseOllamaUsage(usageBody)
	if err != nil {
		return nil, fmt.Errorf("%w: usage: %v", ErrOllamaInvalidResponse, err)
	}

	var me *OllamaMeResponse
	meBody, err := c.do(ctx, http.MethodPost, "/api/me")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.logger.Warn("Ollama /api/me failed; plan and reset day unavailable this poll", "error", err)
	} else if parsed, perr := ParseOllamaMe(meBody); perr != nil {
		c.logger.Warn("Ollama /api/me returned unexpected JSON", "error", perr)
	} else {
		me = parsed
	}

	return BuildOllamaSnapshot(usage, me, string(usageBody), c.monthlyLimit, c.resetDay, c.now()), nil
}

func (c *OllamaClient) do(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrOllamaNetworkError, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ollamaUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOllamaNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, ollamaMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrOllamaNetworkError, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrOllamaUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrOllamaRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrOllamaServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: http %d: %s", ErrOllamaInvalidResponse, resp.StatusCode, sanitizeOllamaMessage(body))
	}
}

func sanitizeOllamaMessage(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "unknown"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// IsOllamaAuthError reports whether err means the key was rejected.
func IsOllamaAuthError(err error) bool {
	return errors.Is(err, ErrOllamaUnauthorized)
}
