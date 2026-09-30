package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

const mistralRetryCooldown = 30 * time.Second

func (a *MistralAgent) RequestRetry() error {
	if !a.cfg.HasProvider("mistral") || a.pollingCheck != nil && !a.pollingCheck() {
		return ErrRetryUnavailable
	}
	a.retryMu.Lock()
	defer a.retryMu.Unlock()
	if a.retryPending || a.retryRunning {
		return nil
	}
	until := a.lastRetryRequest.Add(mistralRetryCooldown)
	if a.serverNotBefore.After(until) {
		until = a.serverNotBefore
	}
	if delay := time.Until(until); delay > 0 {
		return &RetryCooldownError{RetryAfter: delay}
	}
	a.lastRetryRequest = time.Now()
	a.retryPending = true
	a.retryCh <- struct{}{}
	return nil
}

func (a *MistralAgent) ConnectionState() api.MistralConnection {
	a.retryMu.Lock()
	c := a.connection
	c.Retrying = a.retryPending || a.retryRunning || a.pollActive
	c.CanRetry = !c.Retrying && time.Since(a.lastRetryRequest) >= mistralRetryCooldown && !time.Now().Before(a.serverNotBefore)
	a.retryMu.Unlock()
	c.CanRetry = c.CanRetry && a.cfg.HasProvider("mistral") && (a.pollingCheck == nil || a.pollingCheck())
	return c
}

// Called only by the polling loop. All state changes stay on that goroutine.
func (a *MistralAgent) retryPoll(ctx context.Context) {
	a.retryMu.Lock()
	a.retryPending = false
	a.retryRunning = true
	limited := time.Now().Before(a.serverNotBefore)
	a.retryMu.Unlock()
	defer func() { a.retryMu.Lock(); a.retryRunning = false; a.retryMu.Unlock(); a.persistConnection() }()
	if ctx.Err() != nil || limited {
		return
	}
	a.next = time.Time{}
	a.paused = false
	a.session = nil
	a.importFailures = 0
	a.failures = 0
	a.poll(ctx)
}

func (a *MistralAgent) connectionError(err error) {
	c := api.MistralConnection{}
	if err != nil {
		var diagnostic *api.MistralConnectionError
		if errors.As(err, &diagnostic) {
			c = diagnostic.Connection()
		} else if errors.Is(err, api.ErrMistralAuth) {
			c = (&api.MistralConnectionError{Reason: "session_rejected", Browser: a.cfg.MistralBrowser}).Connection()
		} else {
			c.Reason = "request_failed"
			c.Message = "Mistral usage is temporarily unavailable. Retrying automatically."
		}
	}
	a.retryMu.Lock()
	a.connection = c
	a.retryMu.Unlock()
}

func (a *MistralAgent) persistConnection() {
	a.retryMu.Lock()
	if a.next.After(time.Now()) {
		next := a.next
		a.connection.NextRetryAt = &next
	} else {
		a.connection.NextRetryAt = nil
	}
	a.retryMu.Unlock()
	data, err := json.Marshal(a.ConnectionState())
	if err == nil {
		_ = a.store.SetSetting("mistral_connection", string(data))
	}
}

func (a *MistralAgent) respectServerDelay(delay time.Duration) {
	if delay <= 0 {
		return
	}
	until := time.Now().Add(min(delay, time.Hour))
	a.retryMu.Lock()
	if until.After(a.serverNotBefore) {
		a.serverNotBefore = until
	}
	a.retryMu.Unlock()
}
