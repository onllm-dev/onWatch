package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Webhook delivery is deliberately synchronous: notifications are deduplicated
// per cycle (see sendNotification), so a poll issues zero webhook requests in the
// steady state and at most a handful when a threshold is first crossed. Keeping
// it inline preserves the "log only after a channel succeeded" invariant that
// makes failed deliveries retry on the next poll. The cost is bounded by a total
// timeout budget that spans every retry, plus a circuit breaker that stops paying
// the timeout at all once an endpoint looks dead.
const (
	defaultWebhookTimeoutSeconds = 5
	maxWebhookTimeoutSeconds     = 15
	// A zero retry count is a valid choice, so there is no retry default here:
	// the settings UI seeds new configs with 2.
	maxWebhookRetries = 5

	// webhookBreakerThreshold consecutive failures pause delivery for
	// webhookBreakerCooldown, so an unreachable endpoint slows at most a few polls.
	webhookBreakerThreshold = 3
	webhookBreakerCooldown  = 5 * time.Minute

	// maxWebhookResponseBytes caps how much of a response we read. The body is
	// only used for error diagnostics, and reading it lets the connection be reused.
	maxWebhookResponseBytes = 4 << 10
	maxWebhookSnippetLen    = 200

	webhookUserAgent = "onWatch"
)

// ErrWebhookPaused is returned when the circuit breaker is open after repeated
// delivery failures. It is not a delivery error - no request was attempted.
var ErrWebhookPaused = errors.New("webhook delivery paused after repeated failures")

// headersOwnedByTransport cannot be overridden by user-supplied custom headers.
var headersOwnedByTransport = map[string]bool{
	"content-type":   true,
	"content-length": true,
	"host":           true,
}

// WebhookConfig holds the outbound HTTP notification settings.
// The bearer token is stored encrypted at rest and is never included in payloads.
type WebhookConfig struct {
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	BearerToken    string            `json:"bearer_token,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Retries        int               `json:"retries"`
	Events         WebhookEvents     `json:"events"`
}

// WebhookEvents controls which events are delivered to the webhook endpoint.
type WebhookEvents struct {
	Warning        bool `json:"warning"`
	Critical       bool `json:"critical"`
	Reset          bool `json:"reset"`
	AuthError      bool `json:"auth_error"`
	StarterSuccess bool `json:"starter_success"`
	StarterFailure bool `json:"starter_failure"`
}

// Enabled reports whether the named event should be delivered.
func (e WebhookEvents) Enabled(event string) bool {
	switch event {
	case EventWarning:
		return e.Warning
	case EventCritical:
		return e.Critical
	case EventReset:
		return e.Reset
	case EventAuthError:
		return e.AuthError
	case EventStarterSuccess:
		return e.StarterSuccess
	case EventStarterFailure:
		return e.StarterFailure
	default:
		return false
	}
}

// Event names used in webhook payloads and in the notification dedupe log.
const (
	EventWarning        = "warning"
	EventCritical       = "critical"
	EventReset          = "reset"
	EventAuthError      = "auth_error"
	EventStarterSuccess = "starter_success"
	EventStarterFailure = "starter_failure"
	EventTest           = "test"
)

// WebhookPayload is the JSON body POSTed to the configured endpoint.
// It carries no credentials: no OAuth tokens, API keys, or bearer tokens.
type WebhookPayload struct {
	Event       string   `json:"event"`
	Provider    string   `json:"provider"`
	QuotaKey    string   `json:"quota_key,omitempty"`
	AccountID   string   `json:"account_id,omitempty"`
	Utilization *float64 `json:"utilization,omitempty"`
	Limit       *float64 `json:"limit,omitempty"`
	Threshold   *float64 `json:"threshold,omitempty"`
	ResetAt     string   `json:"reset_at,omitempty"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Timestamp   string   `json:"timestamp"`
}

// WebhookSender delivers notification payloads to a user-configured HTTP endpoint.
type WebhookSender struct {
	cfg    WebhookConfig
	client *http.Client
	logger *slog.Logger

	mu           sync.Mutex
	failureCount int
	pausedUntil  time.Time
	pauseLogged  bool
	closed       atomic.Bool

	// now is overridable in tests so breaker behaviour is deterministic.
	now func() time.Time
}

// NewWebhookSender creates a sender with the given config, clamping the timeout
// and retry count so a misconfigured endpoint can never stall a poll loop.
func NewWebhookSender(cfg WebhookConfig, logger *slog.Logger) *WebhookSender {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultWebhookTimeoutSeconds
	}
	if cfg.TimeoutSeconds > maxWebhookTimeoutSeconds {
		cfg.TimeoutSeconds = maxWebhookTimeoutSeconds
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	if cfg.Retries > maxWebhookRetries {
		cfg.Retries = maxWebhookRetries
	}

	return &WebhookSender{
		cfg:    cfg,
		logger: logger,
		now:    time.Now,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
			// Redirects are not followed: the target is user-supplied and a
			// redirect could bounce the request to an unintended host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				MaxIdleConns:        2,
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

// ValidateWebhookURL checks that raw is a usable http(s) endpoint.
// Private and loopback addresses are allowed on purpose: a self-hosted ntfy or
// Gotify instance on the same host is the primary use case for this feature.
func ValidateWebhookURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("webhook URL is required")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("webhook URL must include a host")
	}
	return nil
}

// Enabled reports whether the named event is enabled for this sender.
func (s *WebhookSender) Enabled(event string) bool {
	return s.cfg.Events.Enabled(event)
}

// Send POSTs the payload to the configured endpoint.
//
// All attempts share a single timeout budget, so retries extend reliability
// without extending how long the caller is blocked. Returns ErrWebhookPaused
// when the circuit breaker is open, in which case no request was made.
func (s *WebhookSender) Send(payload WebhookPayload) error {
	return s.deliver(payload, false)
}

// SendTest delivers a payload on the user's behalf, bypassing the circuit
// breaker. A test is a deliberate probe: it must reach the endpoint even while
// real delivery is paused, its failures say nothing about endpoint health so
// they do not count toward the breaker, and a success proves the endpoint is
// back and clears any pause.
func (s *WebhookSender) SendTest(payload WebhookPayload) error {
	return s.deliver(payload, true)
}

// Close releases idle connections held by the sender's transport. Called when
// a sender is replaced so reconfiguring does not leak keep-alive connections.
func (s *WebhookSender) Close() {
	if s.closed.CompareAndSwap(false, true) {
		s.client.CloseIdleConnections()
	}
}

func (s *WebhookSender) deliver(payload WebhookPayload, probe bool) error {
	if err := ValidateWebhookURL(s.cfg.URL); err != nil {
		return err
	}
	if !probe {
		if err := s.checkBreaker(); err != nil {
			return err
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify.Webhook: encode payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	var lastErr error
	backoff := 250 * time.Millisecond

	for attempt := 0; attempt <= s.cfg.Retries; attempt++ {
		if attempt > 0 {
			// Only wait if the remaining budget can still fit another attempt.
			select {
			case <-ctx.Done():
				s.recordFailure(probe)
				return lastErr
			case <-time.After(backoff):
			}
			backoff *= 3
		}

		retryable, err := s.attempt(ctx, body)
		if err == nil {
			s.recordSuccess()
			return nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			break
		}
	}

	s.recordFailure(probe)
	return lastErr
}

// attempt performs a single delivery. It reports whether the failure is worth
// retrying: transport errors, 429, and 5xx are; other non-2xx statuses are a
// configuration problem that a retry cannot fix.
func (s *WebhookSender) attempt(ctx context.Context, body []byte) (retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("notify.Webhook: build request: %w", err)
	}

	for name, value := range s.cfg.Headers {
		if headersOwnedByTransport[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webhookUserAgent)
	if s.cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.BearerToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return true, fmt.Errorf("notify.Webhook: request failed: %w", err)
	}
	defer resp.Body.Close()

	snippet := readSnippet(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}

	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	if snippet == "" {
		return retryable, fmt.Errorf("notify.Webhook: unexpected status %d", resp.StatusCode)
	}
	return retryable, fmt.Errorf("notify.Webhook: unexpected status %d: %s", resp.StatusCode, snippet)
}

// readSnippet drains a bounded amount of the response for error diagnostics.
func readSnippet(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, maxWebhookResponseBytes))
	snippet := strings.TrimSpace(string(data))
	if len(snippet) > maxWebhookSnippetLen {
		snippet = snippet[:maxWebhookSnippetLen] + "..."
	}
	return strings.ReplaceAll(snippet, "\n", " ")
}

// checkBreaker returns ErrWebhookPaused while the pause window is active.
func (s *WebhookSender) checkBreaker() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pausedUntil.IsZero() || !s.now().Before(s.pausedUntil) {
		return nil
	}
	if !s.pauseLogged {
		s.logger.Warn("webhook delivery paused after repeated failures",
			"failures", s.failureCount, "resumes_at", s.pausedUntil.UTC().Format(time.RFC3339))
		s.pauseLogged = true
	}
	return ErrWebhookPaused
}

func (s *WebhookSender) recordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureCount = 0
	s.pausedUntil = time.Time{}
	s.pauseLogged = false
}

// recordFailure advances the breaker. Probe (test-button) failures are ignored:
// they reflect the user's actions, not the endpoint's health during polling.
func (s *WebhookSender) recordFailure(probe bool) {
	if probe {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureCount++
	if s.failureCount >= webhookBreakerThreshold {
		s.pausedUntil = s.now().Add(webhookBreakerCooldown)
		s.pauseLogged = false
	}
}
