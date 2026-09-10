package api

import (
	"bufio"
	"bytes"
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

const (
	museDefaultBaseURL = "https://api.meta.ai"
	museUserAgent      = "onwatch/1.0"
	museTimeout        = 60 * time.Second
	museMaxBodyBytes   = 64 * 1024 // 64 KiB: one probe response is a few KB
	museProbeInput     = "ping"
	museProbeMaxTokens = 16
)

var (
	ErrMuseMissingAPIKey   = errors.New("muse: missing API key")
	ErrMuseUnauthorized    = errors.New("muse: unauthorized - invalid or expired credential")
	ErrMuseRateLimited     = errors.New("muse: rate limited")
	ErrMuseServerError     = errors.New("muse: server error")
	ErrMuseNetworkError    = errors.New("muse: network error")
	ErrMuseInvalidResponse = errors.New("muse: invalid response")
)

// MuseClient polls the Meta Model API for the Muse coding-plan subscription
// snapshot. Meta publishes no aggregate usage endpoint, so each poll sends
// one minimal streamed probe (the same snapshot `muse /usage` shows).
type MuseClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	model      string
	logger     *slog.Logger
	now        func() time.Time
}

// MuseClientOption configures a MuseClient.
type MuseClientOption func(*MuseClient)

// WithMuseBaseURL sets a custom base URL (for testing).
func WithMuseBaseURL(url string) MuseClientOption {
	return func(c *MuseClient) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(url), "/")
	}
}

// WithMuseTimeout sets a custom timeout (for testing).
func WithMuseTimeout(timeout time.Duration) MuseClientOption {
	return func(c *MuseClient) {
		c.httpClient.Timeout = timeout
	}
}

// WithMuseHTTPTransport overrides the HTTP transport (for testing).
func WithMuseHTTPTransport(rt http.RoundTripper) MuseClientOption {
	return func(c *MuseClient) {
		c.httpClient.Transport = rt
	}
}

func withMuseNow(now func() time.Time) MuseClientOption {
	return func(c *MuseClient) {
		c.now = now
	}
}

// NewMuseClient creates a new Muse API client. An empty model falls back to
// DefaultMuseModel.
func NewMuseClient(apiKey, model string, logger *slog.Logger, opts ...MuseClientOption) *MuseClient {
	if logger == nil {
		logger = slog.Default()
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultMuseModel
	}
	c := &MuseClient{
		httpClient: &http.Client{
			Timeout: museTimeout,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: museTimeout,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: museDefaultBaseURL,
		model:   model,
		logger:  logger,
		now:     time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type museProbeRequest struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

// FetchSnapshot sends one minimal streamed probe and extracts the
// subscription usage snapshot from its SSE events.
func (c *MuseClient) FetchSnapshot(ctx context.Context) (*MuseSnapshot, error) {
	if c.apiKey == "" {
		return nil, ErrMuseMissingAPIKey
	}

	reqBody, err := json.Marshal(museProbeRequest{
		Model:           c.model,
		Input:           museProbeInput,
		Stream:          true,
		MaxOutputTokens: museProbeMaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode probe: %v", ErrMuseNetworkError, err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, museTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrMuseNetworkError, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", museUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrMuseNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, museMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrMuseNetworkError, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		// continue below
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrMuseUnauthorized, museErrorDetail(body))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrMuseRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrMuseServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: http %d: %s", ErrMuseInvalidResponse, resp.StatusCode, museErrorDetail(body))
	}

	lines := museSSEDataLines(body)
	sub, err := ParseMuseSubscriptionEvents(lines)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMuseInvalidResponse, err)
	}

	return BuildMuseSnapshot(sub, c.model, string(body), c.now()), nil
}

// museSSEDataLines returns the payloads of SSE "data:" lines.
func museSSEDataLines(body []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 4096), museMaxBodyBytes)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(text, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(text, "data:")))
		}
	}
	return lines
}

// museErrorDetail extracts a short server message without echoing secrets.
func museErrorDetail(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		s := strings.Join(strings.Fields(envelope.Error.Message), " ")
		if len(s) > 120 {
			s = s[:120]
		}
		return s
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "unknown"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// IsMuseAuthError reports whether err means the credential was rejected.
func IsMuseAuthError(err error) bool {
	return errors.Is(err, ErrMuseUnauthorized)
}

// IsMuseRateLimited reports whether err is a rate-limit signal.
func IsMuseRateLimited(err error) bool {
	return errors.Is(err, ErrMuseRateLimited)
}
