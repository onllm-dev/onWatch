package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type managerTestRunner struct {
	started chan struct{}
	stopped chan struct{}
}

func newManagerTestRunner() *managerTestRunner {
	return &managerTestRunner{
		started: make(chan struct{}, 1),
		stopped: make(chan struct{}, 1),
	}
}

func (r *managerTestRunner) Run(ctx context.Context) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case r.stopped <- struct{}{}:
	default:
	}
	return nil
}

func TestAgentManager_StartStop(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	runner := newManagerTestRunner()
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) { return runner, nil })

	if err := mgr.Start("synthetic"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	if !mgr.IsRunning("synthetic") {
		t.Fatal("expected synthetic to be running")
	}

	mgr.Stop("synthetic")
	select {
	case <-runner.stopped:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	if mgr.IsRunning("synthetic") {
		t.Fatal("expected synthetic to be stopped")
	}
}

func TestAgentManager_StartUnknown(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	if err := mgr.Start("missing"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAgentManager_StartFactoryError(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) {
		return nil, errors.New("boom")
	})
	if err := mgr.Start("synthetic"); err == nil {
		t.Fatal("expected factory error")
	}
}

func TestAgentManager_StopAll(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	r1 := newManagerTestRunner()
	r2 := newManagerTestRunner()
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) { return r1, nil })
	mgr.RegisterFactory("zai", func() (AgentRunner, error) { return r2, nil })

	if err := mgr.Start("synthetic"); err != nil {
		t.Fatalf("Start synthetic: %v", err)
	}
	if err := mgr.Start("zai"); err != nil {
		t.Fatalf("Start zai: %v", err)
	}

	select {
	case <-r1.started:
	case <-time.After(time.Second):
		t.Fatal("synthetic not started")
	}
	select {
	case <-r2.started:
	case <-time.After(time.Second):
		t.Fatal("zai not started")
	}

	mgr.StopAll()
	select {
	case <-r1.stopped:
	case <-time.After(time.Second):
		t.Fatal("synthetic not stopped")
	}
	select {
	case <-r2.stopped:
	case <-time.After(time.Second):
		t.Fatal("zai not stopped")
	}

	if mgr.IsRunning("synthetic") || mgr.IsRunning("zai") {
		t.Fatal("expected all runners stopped")
	}
}

func TestAgentManager_RegisteredProviders(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	mgr.RegisterFactory("zai", func() (AgentRunner, error) { return newManagerTestRunner(), nil })
	mgr.RegisterFactory("anthropic", func() (AgentRunner, error) { return newManagerTestRunner(), nil })
	mgr.RegisterFactory("codex", func() (AgentRunner, error) { return newManagerTestRunner(), nil })

	got := mgr.RegisteredProviders()
	want := []string{"anthropic", "codex", "zai"}
	if len(got) != len(want) {
		t.Fatalf("RegisteredProviders length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RegisteredProviders[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}
