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
	museDetailMaxRunes = 120       // cap for error details echoed into logs
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

	switch {
	case resp.StatusCode == http.StatusOK:
		// continue below - stream-parse so we can drop the connection as
		// soon as the subscription snapshot arrives. Holding the generation
		// open races the live Muse CLI on the same API key (HTTP 429).
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The body is deliberately dropped: vendors commonly echo the rejected
		// credential back in auth failures, and the agent logs this error
		// verbatim. "Never log API keys" (CLAUDE.md). The status is already
		// self-describing.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, museMaxBodyBytes))
		return nil, fmt.Errorf("%w: http %d (credential rejected)", ErrMuseUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrMuseRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: http %d", ErrMuseServerError, resp.StatusCode)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, museMaxBodyBytes))
		return nil, fmt.Errorf("%w: http %d: %s", ErrMuseInvalidResponse, resp.StatusCode, museErrorDetail(body))
	}

	sub, raw, err := readMuseSubscriptionStream(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMuseInvalidResponse, err)
	}

	return BuildMuseSnapshot(sub, c.model, raw, c.now()), nil
}

// readMuseSubscriptionStream scans an SSE body and returns at the first
// event that carries a usable subscription snapshot, so the HTTP stream can
// be closed instead of waiting for the model to finish the dummy probe.
func readMuseSubscriptionStream(r io.Reader) (*MuseSubscription, string, error) {
	// One byte over the cap: if the limiter is fully drained the body was at
	// least museMaxBodyBytes+1, which is the only reliable truncation signal.
	// raw.Len() cannot be used - ScanLines strips CR, so a CRLF stream rebuilds
	// shorter than it arrived.
	limited := &io.LimitedReader{R: r, N: museMaxBodyBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 4096), museMaxBodyBytes)
	var raw strings.Builder
	var snapshot *MuseSubscription
	for sc.Scan() {
		line := sc.Text()
		raw.WriteString(line)
		raw.WriteByte('\n')
		text := strings.TrimSpace(line)
		if !strings.HasPrefix(text, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(text, "data:"))
		if candidate := parseMuseSubscriptionData(payload); candidate != nil {
			snapshot = candidate
			break
		}
	}
	if snapshot != nil {
		return snapshot, raw.String(), nil
	}
	// A scanner failure (an SSE line over museMaxBodyBytes, or a dropped
	// connection) must not be reported as "Meta sent no usage": that sends the
	// operator looking at their plan instead of the transport.
	if err := sc.Err(); err != nil {
		return nil, raw.String(), fmt.Errorf("muse: reading usage stream: %w", err)
	}
	if limited.N <= 0 {
		return nil, raw.String(), fmt.Errorf("muse: usage stream exceeded %d bytes before a subscription frame arrived", museMaxBodyBytes)
	}
	return nil, raw.String(), fmt.Errorf("muse: stream carried no subscription usage")
}

// museErrorDetail extracts a short server message without echoing secrets.
func museErrorDetail(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return truncateMuseDetail(strings.Join(strings.Fields(envelope.Error.Message), " "))
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "unknown"
	}
	return truncateMuseDetail(s)
}

// truncateMuseDetail caps an error detail at museDetailMaxRunes. Slicing bytes
// would split a multi-byte rune in a localised message and leave an invalid
// UTF-8 fragment in the log line and the dashboard error body.
func truncateMuseDetail(s string) string {
	runes := []rune(s)
	if len(runes) <= museDetailMaxRunes {
		return s
	}
	return string(runes[:museDetailMaxRunes])
}

// IsMuseAuthError reports whether err means the credential was rejected.
func IsMuseAuthError(err error) bool {
	return errors.Is(err, ErrMuseUnauthorized)
}

// IsMuseRateLimited reports whether err is a rate-limit signal.
func IsMuseRateLimited(err error) bool {
	return errors.Is(err, ErrMuseRateLimited)
}
