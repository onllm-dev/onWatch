package agent

import (
	"errors"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

var ErrRetryUnavailable = errors.New("provider retry is unavailable")

type RetryCooldownError struct{ RetryAfter time.Duration }

func (e *RetryCooldownError) Error() string { return "provider retry is cooling down" }

// RetryableRunner is optional; providers opt in without changing AgentRunner.
type RetryableRunner interface {
	RequestRetry() error
	ConnectionState() api.MistralConnection
}

func (m *AgentManager) RequestRetry(key string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry := m.running[key]
	if entry == nil {
		return ErrRetryUnavailable
	}
	runner, ok := entry.runner.(RetryableRunner)
	if !ok {
		return ErrRetryUnavailable
	}
	return runner.RequestRetry()
}

func (m *AgentManager) ConnectionState(key string) (api.MistralConnection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry := m.running[key]
	if entry != nil {
		if runner, ok := entry.runner.(RetryableRunner); ok {
			return runner.ConnectionState(), true
		}
	}
	return api.MistralConnection{}, false
}
