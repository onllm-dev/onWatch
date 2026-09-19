package notify

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// testWebhookConfig returns a config pointed at url with fast, deterministic timings.
func testWebhookConfig(url string) WebhookConfig {
	return WebhookConfig{
		URL:            url,
		TimeoutSeconds: 2,
		Retries:        0,
		Events:         WebhookEvents{Warning: true, Critical: true, Reset: true, AuthError: true, StarterSuccess: true, StarterFailure: true},
	}
}

func testPayload() WebhookPayload {
	util := 91.5
	limit := 100.0
	return WebhookPayload{
		Event:       "critical",
		Provider:    "anthropic",
		QuotaKey:    "five_hour",
		Utilization: &util,
		Limit:       &limit,
		Title:       "[CRITICAL] Anthropic quota five_hour at 91.5%",
		Message:     "quota is nearly exhausted",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
}

func TestWebhookSenderPostsJSONPayload(t *testing.T) {
	var gotMethod, gotContentType, gotUserAgent string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotUserAgent = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if !strings.Contains(gotUserAgent, "onWatch") {
		t.Errorf("User-Agent = %q, want it to identify onWatch", gotUserAgent)
	}

	var decoded WebhookPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v (body=%q)", err, gotBody)
	}
	if decoded.Event != "critical" || decoded.Provider != "anthropic" || decoded.QuotaKey != "five_hour" {
		t.Errorf("payload = %+v, want event/provider/quota_key preserved", decoded)
	}
	if decoded.Utilization == nil || *decoded.Utilization != 91.5 {
		t.Errorf("payload utilization = %v, want 91.5", decoded.Utilization)
	}
}

func TestWebhookSenderSendsCustomHeadersAndBearerToken(t *testing.T) {
	var gotAuth, gotTitle, gotPriority string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTitle = r.Header.Get("X-Title")
		gotPriority = r.Header.Get("X-Priority")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testWebhookConfig(srv.URL)
	cfg.BearerToken = "tk_secret"
	cfg.Headers = map[string]string{"X-Title": "onWatch alert", "X-Priority": "high"}

	sender := NewWebhookSender(cfg, slog.Default())
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	if gotAuth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tk_secret")
	}
	if gotTitle != "onWatch alert" || gotPriority != "high" {
		t.Errorf("custom headers = %q/%q, want onWatch alert/high", gotTitle, gotPriority)
	}
}

func TestWebhookSenderCustomHeadersCannotOverrideContentType(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testWebhookConfig(srv.URL)
	cfg.Headers = map[string]string{"Content-Type": "text/plain"}

	sender := NewWebhookSender(cfg, slog.Default())
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (body is always JSON)", gotContentType)
	}
}

func TestWebhookSenderRetriesOnServerErrorThenSucceeds(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testWebhookConfig(srv.URL)
	cfg.Retries = 2

	sender := NewWebhookSender(cfg, slog.Default())
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil after retries", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestWebhookSenderDoesNotRetryClientErrors(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testWebhookConfig(srv.URL)
	cfg.Retries = 3

	sender := NewWebhookSender(cfg, slog.Default())
	err := sender.Send(testPayload())
	if err == nil {
		t.Fatal("Send() error = nil, want error for 401")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (4xx is a config error, retrying cannot help)", got)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want it to mention the status code", err)
	}
}

func TestWebhookSenderRetriesOnTooManyRequests(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testWebhookConfig(srv.URL)
	cfg.Retries = 2

	sender := NewWebhookSender(cfg, slog.Default())
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil (429 is retryable)", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// The timeout is a total budget across all attempts, not a per-attempt timeout.
// Retries must never multiply how long a poll is blocked.
func TestWebhookSenderTimeoutIsATotalBudget(t *testing.T) {
	// release unblocks the handler at teardown; the client abandoning the request
	// does not reliably cancel the server-side request context.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	cfg := testWebhookConfig(srv.URL)
	cfg.TimeoutSeconds = 1
	cfg.Retries = 3

	sender := NewWebhookSender(cfg, slog.Default())

	start := time.Now()
	err := sender.Send(testPayload())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send() error = nil, want timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Send() blocked for %v, want <= ~1s total budget regardless of retries", elapsed)
	}
}

func TestWebhookSenderDoesNotFollowRedirects(t *testing.T) {
	var finalHit atomic.Int32

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHit.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	sender := NewWebhookSender(testWebhookConfig(redirector.URL), slog.Default())
	err := sender.Send(testPayload())

	if err == nil {
		t.Fatal("Send() error = nil, want error for redirect response")
	}
	if got := finalHit.Load(); got != 0 {
		t.Errorf("redirect target hit %d times, want 0 (redirects must not be followed)", got)
	}
}

func TestWebhookSenderErrorIncludesResponseSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "topic name is invalid")
	}))
	defer srv.Close()

	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())
	err := sender.Send(testPayload())
	if err == nil {
		t.Fatal("Send() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "topic name is invalid") {
		t.Errorf("error = %v, want it to include the response body snippet", err)
	}
}

func TestWebhookSenderCircuitBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Now()
	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())
	sender.now = func() time.Time { return now }

	for i := 0; i < webhookBreakerThreshold; i++ {
		if err := sender.Send(testPayload()); err == nil {
			t.Fatalf("Send() #%d error = nil, want failure", i+1)
		}
	}
	hitsBeforeBreaker := attempts.Load()

	err := sender.Send(testPayload())
	if !errors.Is(err, ErrWebhookPaused) {
		t.Errorf("Send() error = %v, want ErrWebhookPaused once the breaker is open", err)
	}
	if got := attempts.Load(); got != hitsBeforeBreaker {
		t.Errorf("attempts = %d, want %d (no request while the breaker is open)", got, hitsBeforeBreaker)
	}

	// Once the pause window elapses the sender probes the endpoint again.
	now = now.Add(webhookBreakerCooldown + time.Second)
	if err := sender.Send(testPayload()); errors.Is(err, ErrWebhookPaused) {
		t.Error("Send() still paused after the cooldown elapsed, want a retry attempt")
	}
	if got := attempts.Load(); got <= hitsBeforeBreaker {
		t.Errorf("attempts = %d, want > %d after cooldown", got, hitsBeforeBreaker)
	}
}

func TestWebhookSenderCircuitBreakerResetsAfterSuccess(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())

	// One short of the threshold, then a success clears the failure streak.
	for i := 0; i < webhookBreakerThreshold-1; i++ {
		if err := sender.Send(testPayload()); err == nil {
			t.Fatalf("Send() #%d error = nil, want failure", i+1)
		}
	}
	fail.Store(false)
	if err := sender.Send(testPayload()); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	fail.Store(true)
	for i := 0; i < webhookBreakerThreshold-1; i++ {
		if err := sender.Send(testPayload()); errors.Is(err, ErrWebhookPaused) {
			t.Fatalf("Send() #%d was paused, want the failure streak to have been reset by the success", i+1)
		}
	}
}

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https endpoint", "https://ntfy.sh/my-topic", false},
		{"http endpoint", "http://example.com/hook", false},
		// Self-hosted ntfy/Gotify on the same machine is the primary use case.
		{"localhost with port", "http://localhost:8080/onwatch", false},
		{"private LAN address", "http://192.168.1.50:8080/hook", false},
		{"empty", "", true},
		{"no scheme", "ntfy.sh/my-topic", true},
		{"file scheme", "file:///etc/passwd", true},
		{"no host", "http://", true},
		{"not a url", "://nope", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateWebhookURL(%q) = nil, want error", tt.url)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateWebhookURL(%q) = %v, want nil", tt.url, err)
			}
		})
	}
}

func TestWebhookSenderRejectsInvalidURLWithoutSending(t *testing.T) {
	cfg := testWebhookConfig("file:///etc/passwd")
	sender := NewWebhookSender(cfg, slog.Default())
	if err := sender.Send(testPayload()); err == nil {
		t.Fatal("Send() error = nil, want rejection of a non-http(s) URL")
	}
}

func TestWebhookEventsEnabled(t *testing.T) {
	events := WebhookEvents{Warning: true, StarterFailure: true}

	if !events.Enabled("warning") {
		t.Error("Enabled(warning) = false, want true")
	}
	if events.Enabled("critical") {
		t.Error("Enabled(critical) = true, want false")
	}
	if !events.Enabled("starter_failure") {
		t.Error("Enabled(starter_failure) = false, want true")
	}
	if events.Enabled("unknown_event") {
		t.Error("Enabled(unknown_event) = true, want false")
	}
}

// The payload must never carry credentials, and optional numeric fields must be
// omitted rather than reported as a misleading zero.
func TestWebhookPayloadOmitsUnsetFields(t *testing.T) {
	raw, err := json.Marshal(WebhookPayload{
		Event:     "auth_error",
		Provider:  "codex",
		Title:     "[AUTH ERROR] Codex - token refresh failed",
		Message:   "re-authenticate to resume tracking",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	for _, field := range []string{"utilization", "limit", "threshold", "reset_at", "quota_key", "account_id"} {
		if _, ok := fields[field]; ok {
			t.Errorf("payload contains %q for an auth error, want it omitted: %s", field, raw)
		}
	}
	for name := range fields {
		lower := strings.ToLower(name)
		for _, secret := range []string{"token", "bearer", "password", "authorization", "secret", "key"} {
			if strings.Contains(lower, secret) {
				t.Errorf("payload has field %q, want no credential fields in the body", name)
			}
		}
	}
}

func TestWebhookSenderDefaultsTimeoutAndMethod(t *testing.T) {
	sender := NewWebhookSender(WebhookConfig{URL: "https://ntfy.sh/topic"}, slog.Default())

	if sender.cfg.TimeoutSeconds != defaultWebhookTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want default %d", sender.cfg.TimeoutSeconds, defaultWebhookTimeoutSeconds)
	}
	if sender.client.Timeout != time.Duration(defaultWebhookTimeoutSeconds)*time.Second {
		t.Errorf("client timeout = %v, want %ds", sender.client.Timeout, defaultWebhookTimeoutSeconds)
	}
}

func TestWebhookSenderClampsExcessiveTimeout(t *testing.T) {
	cfg := WebhookConfig{URL: "https://ntfy.sh/topic", TimeoutSeconds: 600, Retries: 99}
	sender := NewWebhookSender(cfg, slog.Default())

	if sender.cfg.TimeoutSeconds > maxWebhookTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want it clamped to <= %d so a poll cannot stall", sender.cfg.TimeoutSeconds, maxWebhookTimeoutSeconds)
	}
	if sender.cfg.Retries > maxWebhookRetries {
		t.Errorf("Retries = %d, want it clamped to <= %d", sender.cfg.Retries, maxWebhookRetries)
	}
}

// --- NotificationEngine integration ---

// storeWebhookConfig saves webhook settings under the "webhook" key, matching
// the format that the handler's UpdateSettings uses.
func storeWebhookConfig(t *testing.T, s *store.Store, cfg WebhookConfig) {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	if err := s.SetSetting("webhook", string(raw)); err != nil {
		t.Fatalf("SetSetting(webhook) error = %v", err)
	}
}

// enableWebhookChannel saves notification settings with the webhook channel on.
func enableWebhookChannel(t *testing.T, s *store.Store, engine *NotificationEngine) {
	t.Helper()
	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyWarning:     true,
		NotifyCritical:    true,
		NotifyReset:       true,
		NotifyAuthError:   true,
		CooldownMinutes:   30,
		Channels:          &NotificationChannels{Webhook: true},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
}

// webhookCapture records payloads delivered to a test endpoint.
type webhookCapture struct {
	mu       sync.Mutex
	payloads []WebhookPayload
	srv      *httptest.Server
}

func (c *webhookCapture) all() []WebhookPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]WebhookPayload(nil), c.payloads...)
}

func (c *webhookCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.payloads)
}

func newWebhookCapture(t *testing.T) *webhookCapture {
	t.Helper()
	c := &webhookCapture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p WebhookPayload
		json.NewDecoder(r.Body).Decode(&p)
		c.mu.Lock()
		c.payloads = append(c.payloads, p)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// allWebhookEvents enables every event type.
func allWebhookEvents() WebhookEvents {
	return WebhookEvents{Warning: true, Critical: true, Reset: true, AuthError: true, StarterSuccess: true, StarterFailure: true}
}

func setupWebhookEngine(t *testing.T) (*NotificationEngine, *store.Store, *webhookCapture) {
	t.Helper()
	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)
	return engine, s, capture
}

// A webhook-only setup must still deliver: the engine previously bailed out when
// neither a mailer nor a push sender was configured.
func TestEngineSendsWebhookWithNoOtherChannelConfigured(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)

	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100})

	payloads := capture.all()
	if len(payloads) != 1 {
		t.Fatalf("delivered %d payloads, want 1", len(payloads))
	}
	if payloads[0].Event != EventCritical {
		t.Errorf("event = %q, want %q", payloads[0].Event, EventCritical)
	}
	if payloads[0].Provider != "anthropic" || payloads[0].QuotaKey != "five_hour" {
		t.Errorf("payload = %+v, want anthropic/five_hour", payloads[0])
	}
	if payloads[0].Threshold == nil || *payloads[0].Threshold != 95 {
		t.Errorf("threshold = %v, want 95", payloads[0].Threshold)
	}
	if payloads[0].Title == "" || payloads[0].Message == "" {
		t.Errorf("payload title/message = %q/%q, want both populated", payloads[0].Title, payloads[0].Message)
	}
}

func TestEngineWebhookIncludesResetTime(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)
	resetAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	engine.Check(QuotaStatus{Provider: "codex", QuotaKey: "weekly", Utilization: 97, Limit: 100, ResetAt: resetAt})

	payloads := capture.all()
	if len(payloads) != 1 {
		t.Fatalf("delivered %d payloads, want 1", len(payloads))
	}
	if payloads[0].ResetAt != resetAt.Format(time.RFC3339) {
		t.Errorf("reset_at = %q, want %q", payloads[0].ResetAt, resetAt.Format(time.RFC3339))
	}
}

func TestEngineWebhookSkipsDisabledEvent(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	// Warning enabled, critical not.
	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2, Events: WebhookEvents{Warning: true}})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)

	engine.Check(QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 99, Limit: 100})

	if got := capture.count(); got != 0 {
		t.Errorf("delivered %d payloads, want 0 when the critical event is disabled", got)
	}
}

func TestEngineWebhookRespectsChannelToggle(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyCritical:    true,
		Channels:          &NotificationChannels{Webhook: false},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	engine.Check(QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 99, Limit: 100})

	if got := capture.count(); got != 0 {
		t.Errorf("delivered %d payloads, want 0 when the webhook channel is off", got)
	}
}

// A successful webhook delivery must satisfy the per-cycle dedupe on its own.
func TestEngineWebhookDeliveryIsDeduplicatedPerCycle(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)
	status := QuotaStatus{Provider: "kimi", QuotaKey: "daily", Utilization: 97, Limit: 100}

	engine.Check(status)
	engine.Check(status)

	if got := capture.count(); got != 1 {
		t.Errorf("delivered %d payloads, want 1 (second check is deduplicated)", got)
	}
}

func TestEngineWebhookResendsAfterReset(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)
	status := QuotaStatus{Provider: "kimi", QuotaKey: "daily", Utilization: 97, Limit: 100}

	engine.Check(status)
	engine.Check(QuotaStatus{Provider: "kimi", QuotaKey: "daily", ResetOccurred: true})
	engine.Check(status)

	events := []string{}
	for _, p := range capture.all() {
		events = append(events, p.Event)
	}
	want := []string{EventCritical, EventReset, EventCritical}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// A failed webhook must not mark the notification as sent, so the next poll retries.
func TestEngineWebhookFailureLeavesNotificationUnlogged(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	storeWebhookConfig(t, s, WebhookConfig{URL: srv.URL, TimeoutSeconds: 1, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)

	status := QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 97, Limit: 100}
	engine.Check(status)

	sentAt, _, err := s.GetLastNotification("grok", "daily", EventCritical)
	if err != nil {
		t.Fatalf("GetLastNotification() error = %v", err)
	}
	if !sentAt.IsZero() {
		t.Error("notification was logged despite delivery failure, want it retried next poll")
	}

	engine.Check(status)
	if got := hits.Load(); got < 2 {
		t.Errorf("endpoint hit %d times, want a retry on the next check", got)
	}
}

func TestEngineSendsWebhookOnAuthError(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)

	engine.SendAuthErrorNotification(AuthErrorAlert{
		Provider:  "codex",
		Title:     "Token refresh failed",
		Message:   "re-authenticate to resume tracking",
		AccountID: "2",
	})

	payloads := capture.all()
	if len(payloads) != 1 {
		t.Fatalf("delivered %d payloads, want 1", len(payloads))
	}
	if payloads[0].Event != EventAuthError {
		t.Errorf("event = %q, want %q", payloads[0].Event, EventAuthError)
	}
	if payloads[0].Provider != "codex" || payloads[0].AccountID != "2" {
		t.Errorf("payload = %+v, want codex/account 2", payloads[0])
	}
	if payloads[0].Utilization != nil {
		t.Errorf("utilization = %v, want omitted for an auth error", payloads[0].Utilization)
	}
}

func TestEngineSendsWebhookOnStarterEvents(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)

	engine.SendStarterEvent(StarterEvent{Provider: "codex", QuotaKey: "weekly", AccountID: "1", Success: true})
	engine.SendStarterEvent(StarterEvent{Provider: "codex", QuotaKey: "weekly", AccountID: "1", Success: false, Detail: "request timed out"})

	payloads := capture.all()
	if len(payloads) != 2 {
		t.Fatalf("delivered %d payloads, want 2", len(payloads))
	}
	if payloads[0].Event != EventStarterSuccess {
		t.Errorf("event[0] = %q, want %q", payloads[0].Event, EventStarterSuccess)
	}
	if payloads[1].Event != EventStarterFailure {
		t.Errorf("event[1] = %q, want %q", payloads[1].Event, EventStarterFailure)
	}
	if !strings.Contains(payloads[1].Message, "request timed out") {
		t.Errorf("failure message = %q, want it to include the detail", payloads[1].Message)
	}
}

// Starter events are operational pings, not quota-cycle alerts, so they must not
// consume the per-cycle dedupe slot used by warning/critical/reset.
func TestEngineStarterEventsAreNotDeduplicated(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)

	engine.SendStarterEvent(StarterEvent{Provider: "codex", QuotaKey: "weekly", Success: true})
	engine.SendStarterEvent(StarterEvent{Provider: "codex", QuotaKey: "weekly", Success: true})

	if got := capture.count(); got != 2 {
		t.Errorf("delivered %d payloads, want 2", got)
	}
}

func TestEngineStarterEventSkippedWhenWebhookDisabled(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2, Events: WebhookEvents{StarterFailure: true}})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)

	engine.SendStarterEvent(StarterEvent{Provider: "codex", QuotaKey: "weekly", Success: true})

	if got := capture.count(); got != 0 {
		t.Errorf("delivered %d payloads, want 0 when starter_success is disabled", got)
	}
}

func TestEngineConfigureWebhookClearsSenderWhenURLRemoved(t *testing.T) {
	engine, s, capture := setupWebhookEngine(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: "", Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	engine.Check(QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 99, Limit: 100})

	if got := capture.count(); got != 0 {
		t.Errorf("delivered %d payloads, want 0 after the URL was cleared", got)
	}
}

func TestEngineConfigureWebhookDecryptsBearerToken(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey() error = %v", err)
	}
	engine.SetEncryptionKey(key)

	encrypted, err := EncryptForStorage("tk_plaintext", key)
	if err != nil {
		t.Fatalf("EncryptForStorage() error = %v", err)
	}

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	storeWebhookConfig(t, s, WebhookConfig{URL: srv.URL, BearerToken: encrypted, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)

	engine.Check(QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 99, Limit: 100})

	if gotAuth != "Bearer tk_plaintext" {
		t.Errorf("Authorization = %q, want the decrypted token", gotAuth)
	}
}

func TestSendTestWebhook(t *testing.T) {
	engine, _, capture := setupWebhookEngine(t)

	if err := engine.SendTestWebhook(); err != nil {
		t.Fatalf("SendTestWebhook() error = %v", err)
	}

	payloads := capture.all()
	if len(payloads) != 1 {
		t.Fatalf("delivered %d payloads, want 1", len(payloads))
	}
	if payloads[0].Event != EventTest {
		t.Errorf("event = %q, want %q", payloads[0].Event, EventTest)
	}
}

// The test button must work before any event type is enabled, so users can
// verify the endpoint while setting it up.
func TestSendTestWebhookIgnoresEventToggles(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	if err := engine.SendTestWebhook(); err != nil {
		t.Fatalf("SendTestWebhook() error = %v", err)
	}
	if got := capture.count(); got != 1 {
		t.Errorf("delivered %d payloads, want 1", got)
	}
}

func TestSendTestWebhookErrorsWhenUnconfigured(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	if err := engine.SendTestWebhook(); err == nil {
		t.Error("SendTestWebhook() error = nil, want an error when no webhook is configured")
	}
}

// Reload must not silently disable a channel the user left enabled.
func TestReloadDefaultsWebhookChannelOffButPreservesExplicitValue(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		Channels:          &NotificationChannels{Email: true, Push: false, Webhook: true},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	channels := engine.Config().Channels
	if !channels.Webhook {
		t.Error("Channels.Webhook = false, want the saved value preserved")
	}
	if channels.Push {
		t.Error("Channels.Push = true, want the saved value preserved")
	}
}

// --- review follow-ups ---

// A token that cannot be decrypted must not be sent as a raw ciphertext bearer.
func TestEngineConfigureWebhookDropsUndecryptableToken(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	keyA, _ := GenerateEncryptionKey()
	keyB, _ := GenerateEncryptionKey()
	engine.SetEncryptionKey(keyB)

	encrypted, err := EncryptForStorage("tk_plaintext", keyA)
	if err != nil {
		t.Fatalf("EncryptForStorage() error = %v", err)
	}

	var gotAuth string
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	storeWebhookConfig(t, s, WebhookConfig{URL: srv.URL, BearerToken: encrypted, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	enableWebhookChannel(t, s, engine)

	engine.Check(QuotaStatus{Provider: "grok", QuotaKey: "daily", Utilization: 99, Limit: 100})

	if hits.Load() != 1 {
		t.Fatalf("endpoint hit %d times, want 1 (delivery continues without auth)", hits.Load())
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want no header rather than the ciphertext", gotAuth)
	}
}

// The test button must reach the endpoint even while the breaker is open, so a
// user who has just fixed their endpoint can verify it immediately.
func TestWebhookSenderSendTestBypassesOpenBreaker(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())
	for i := 0; i < webhookBreakerThreshold; i++ {
		sender.Send(testPayload())
	}
	if err := sender.Send(testPayload()); !errors.Is(err, ErrWebhookPaused) {
		t.Fatalf("precondition: breaker should be open, got %v", err)
	}
	before := hits.Load()

	fail.Store(false)
	if err := sender.SendTest(testPayload()); err != nil {
		t.Fatalf("SendTest() error = %v, want nil while breaker is open", err)
	}
	if hits.Load() != before+1 {
		t.Errorf("SendTest made %d requests, want 1", hits.Load()-before)
	}

	// A successful probe proves the endpoint is back, so real delivery resumes.
	if err := sender.Send(testPayload()); err != nil {
		t.Errorf("Send() after a successful test = %v, want the breaker cleared", err)
	}
}

// Test-button failures are the user's business, not evidence the endpoint is
// dead: they must not count toward opening the breaker for real alerts.
func TestWebhookSenderSendTestFailuresDoNotTripBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	sender := NewWebhookSender(testWebhookConfig(srv.URL), slog.Default())
	for i := 0; i < webhookBreakerThreshold+1; i++ {
		if err := sender.SendTest(testPayload()); err == nil {
			t.Fatalf("SendTest() #%d error = nil, want 401 failure", i+1)
		}
	}

	if err := sender.Send(testPayload()); errors.Is(err, ErrWebhookPaused) {
		t.Error("Send() is paused after repeated test failures, want the breaker untouched by SendTest")
	}
}

// Check must not do per-quota work when no channel can actually deliver, even
// if senders are configured but toggled off.
func TestChannelSetAnyRespectsToggles(t *testing.T) {
	sender := NewWebhookSender(WebhookConfig{URL: "https://ntfy.sh/topic"}, slog.Default())
	cases := []struct {
		name string
		set  channelSet
		want bool
	}{
		{"nothing configured", channelSet{enabled: NotificationChannels{Email: true, Push: true, Webhook: true}}, false},
		{"webhook configured but toggled off", channelSet{webhook: sender, enabled: NotificationChannels{Webhook: false}}, false},
		{"webhook configured and on", channelSet{webhook: sender, enabled: NotificationChannels{Webhook: true}}, true},
		{"mailer configured but toggled off", channelSet{mailer: &SMTPMailer{}, enabled: NotificationChannels{Email: false}}, false},
		{"mailer on", channelSet{mailer: &SMTPMailer{}, enabled: NotificationChannels{Email: true}}, true},
		{"push on", channelSet{push: &PushSender{}, enabled: NotificationChannels{Push: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.any(); got != tc.want {
				t.Errorf("any() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Reconfiguring must release the previous sender's idle connections rather
// than leaving them to age out on their own.
func TestEngineConfigureWebhookClosesPreviousSender(t *testing.T) {
	engine, s, capture := setupWebhookEngine(t)

	engine.mu.RLock()
	old := engine.webhookSender
	engine.mu.RUnlock()
	if old == nil {
		t.Fatal("precondition: sender should be configured")
	}

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL + "/v2", TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	engine.mu.RLock()
	current := engine.webhookSender
	engine.mu.RUnlock()
	if current == old {
		t.Fatal("sender was not replaced")
	}
	if !old.closed.Load() {
		t.Error("previous sender was not closed on reconfigure")
	}
}
