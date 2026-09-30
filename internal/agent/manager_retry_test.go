package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

type managedRetryRunner struct {
	started, release, exited chan struct{}
	retries                  atomic.Int32
}

func (r *managedRetryRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	<-r.release
	close(r.exited)
	return nil
}
func (r *managedRetryRunner) RequestRetry() error { r.retries.Add(1); return nil }
func (r *managedRetryRunner) ConnectionState() api.MistralConnection {
	return api.MistralConnection{CanRetry: true}
}

func TestAgentManagerRetryUsesCurrentInstance(t *testing.T) {
	m := NewAgentManager(nil)
	makeRunner := func() *managedRetryRunner {
		return &managedRetryRunner{started: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	}
	old, current := makeRunner(), makeRunner()
	m.RegisterFactory("mistral", func() (AgentRunner, error) { return old, nil })
	if err := m.RequestRetry("mistral"); !errors.Is(err, ErrRetryUnavailable) {
		t.Fatal(err)
	}
	if err := m.Start("mistral"); err != nil {
		t.Fatal(err)
	}
	<-old.started
	if err := m.RequestRetry("mistral"); err != nil {
		t.Fatal(err)
	}
	m.Stop("mistral")
	m.RegisterFactory("mistral", func() (AgentRunner, error) { return current, nil })
	if err := m.Start("mistral"); err != nil {
		t.Fatal(err)
	}
	<-current.started
	defer func() { m.StopAll(); close(current.release); <-current.exited }()
	close(old.release)
	<-old.exited
	// Exercise retries throughout the old goroutine's deferred cleanup.
	for deadline := time.Now().Add(30 * time.Millisecond); time.Now().Before(deadline); {
		if err := m.RequestRetry("mistral"); err != nil {
			t.Fatalf("old exit removed replacement: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if old.retries.Load() != 1 || current.retries.Load() == 0 {
		t.Fatal("retry routed to stale instance")
	}
	if c, ok := m.ConnectionState("mistral"); !ok || !c.CanRetry {
		t.Fatal("missing running connection state")
	}
}
