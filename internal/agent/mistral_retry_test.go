package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/steipete/sweetcookie"
)

func TestMistralRetryRecoversWithoutRestart(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true, PollInterval: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.source = &api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	var permitted atomic.Bool
	var imports atomic.Int32
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		imports.Add(1)
		if !permitted.Load() {
			return sweetcookie.Result{Warnings: []string{"failed to copy cookies DB: permission denied"}}, nil
		}
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: "same-cookie", Domain: ".mistral.ai", Path: "/"}}}, nil
	}
	a.client = &fakeMistralFetcher{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitMistral(t, func() bool { c := a.ConnectionState(); return c.Reason == "browser_access_denied" && !c.Retrying })
	permitted.Store(true)
	if err := a.RequestRetry(); err != nil {
		t.Fatal(err)
	}
	waitMistral(t, func() bool {
		snap, _ := db.LatestMistral(context.Background())
		return snap != nil && !a.ConnectionState().Retrying
	})
	if imports.Load() != 2 {
		t.Fatalf("imports=%d", imports.Load())
	}
	if c := a.ConnectionState(); c.Reason != "" {
		t.Fatalf("stale diagnostic: %+v", c)
	}
	var cooldown *RetryCooldownError
	if err := a.RequestRetry(); !errors.As(err, &cooldown) {
		t.Fatalf("missing cooldown: %v", err)
	}
}

func waitMistral(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Mistral")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMistralRetryCoalescesAndRespectsRateLimit(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.RequestRetry(); err != nil {
				t.Errorf("coalesce: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(a.retryCh) != 1 {
		t.Fatalf("queued %d", len(a.retryCh))
	}
	<-a.retryCh
	a.retryMu.Lock()
	a.retryPending = false
	a.lastRetryRequest = time.Time{}
	a.retryMu.Unlock()
	a.backoff(&api.MistralHTTPError{Status: 429, RetryAfter: time.Hour})
	var cooldown *RetryCooldownError
	if err := a.RequestRetry(); !errors.As(err, &cooldown) || cooldown.RetryAfter < 59*time.Minute {
		t.Fatalf("rate limit bypassed: %v", err)
	}
	a.SetPollingCheck(func() bool { return false })
	if err := a.RequestRetry(); !errors.Is(err, ErrRetryUnavailable) {
		t.Fatalf("disabled polling: %v", err)
	}
}

func TestMistralRetryUnchangedRejectedCookie(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true, MistralAuthMode: "manual", MistralAuthCookie: "ory_session_test=same-cookie"}, nil)
	defer a.sm.Close()
	f := &fakeMistralFetcher{authFail: true}
	a.client = f
	a.poll(context.Background())
	if !a.paused || f.calls != 2 {
		t.Fatalf("paused=%v calls=%d", a.paused, f.calls)
	}
	f.authFail = false
	if err := a.RequestRetry(); err != nil {
		t.Fatal(err)
	}
	<-a.retryCh
	a.retryPoll(context.Background())
	if a.paused || f.calls != 3 || a.ConnectionState().Reason != "" {
		t.Fatalf("did not recover: %+v calls=%d", a.ConnectionState(), f.calls)
	}
}

func TestMistralRetryCancelledDoesNotImport(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true}, nil)
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		t.Fatal("cancelled retry imported cookies")
		return sweetcookie.Result{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.RequestRetry(); err != nil {
		t.Fatal(err)
	}
	<-a.retryCh
	a.retryPoll(ctx)
	if a.ConnectionState().Retrying {
		t.Fatal("cancelled request still pending")
	}
}
