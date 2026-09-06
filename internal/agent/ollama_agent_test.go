package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestNewOllamaAgent_Basic(t *testing.T) {
	a := NewOllamaAgent(nil, nil, nil, nil, 60*time.Second, nil, nil)
	if a == nil {
		t.Fatal("nil agent")
	}
	a.SetPollingCheck(func() bool { return true })
	a.SetNotifier(nil)
}

func TestOllamaAgent_Poll_NoClientSafe(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	tr := tracker.NewOllamaTracker(st, nil)
	ag := NewOllamaAgent(nil, st, tr, nil, time.Second, nil, NewSessionManager(st, "ollama", 60*time.Second, nil))
	ag.poll(context.Background())
}

func TestOllamaAgent_Poll_FetchErrorNoInsert(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		OllamaAPIKey: "test-key",
	}
	client := &stubOllamaClient{err: errors.New("fetch failed")}
	tr := tracker.NewOllamaTracker(st, nil)
	ag := NewOllamaAgent(client, st, tr, cfg, time.Second, slog.Default(), NewSessionManager(st, "ollama", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestOllama()
	if err != nil {
		t.Fatalf("QueryLatestOllama: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot after fetch failure")
	}
}

func TestOllamaAgent_Poll_SuccessInsertsAndTracks(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	snapshot := &api.OllamaSnapshot{
		CapturedAt:  now,
		Plan:        "pro",
		AccountName: "Ollama Go",
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Utilization: 10, Format: api.OllamaQuotaFormatPercent, ResetsAt: &reset},
		},
	}

	client := &stubOllamaClient{snapshot: snapshot}
	tr := tracker.NewOllamaTracker(st, slog.Default())
	ag := NewOllamaAgent(client, st, tr, &config.Config{
		OllamaAPIKey: "test-key",
	}, time.Second, slog.Default(), NewSessionManager(st, "ollama", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestOllama()
	if err != nil {
		t.Fatalf("QueryLatestOllama: %v", err)
	}
	if latest == nil || len(latest.Quotas) != 1 {
		t.Fatalf("expected inserted snapshot, got %+v", latest)
	}

	cycle, err := st.QueryActiveOllamaCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOllamaCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle after tracker.Process")
	}
}

type stubOllamaClient struct {
	snapshot *api.OllamaSnapshot
	err      error
}

func (s *stubOllamaClient) FetchSnapshot(_ context.Context) (*api.OllamaSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

func TestOllamaAgent_Poll_MissingConfig(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := api.NewOllamaClient("", nil)
	tr := tracker.NewOllamaTracker(st, nil)
	ag := NewOllamaAgent(client, st, tr, &config.Config{}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())

	latest, err := st.QueryLatestOllama()
	if err != nil {
		t.Fatalf("QueryLatestOllama: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot when config missing")
	}
}

func TestOllamaAgent_Poll_AuthError(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := &stubOllamaClient{err: api.ErrOllamaUnauthorized}
	tr := tracker.NewOllamaTracker(st, nil)
	ag := NewOllamaAgent(client, st, tr, &config.Config{
		OllamaAPIKey: "test-key",
	}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())

	latest, err := st.QueryLatestOllama()
	if err != nil {
		t.Fatalf("QueryLatestOllama: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot on auth error")
	}
}

var _ interface {
	FetchSnapshot(context.Context) (*api.OllamaSnapshot, error)
} = (*stubOllamaClient)(nil)

func TestOllamaAgent_Poll_FetchErrorTyped(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := &stubOllamaClient{err: errors.Join(api.ErrOllamaInvalidResponse, errors.New("details"))}
	tr := tracker.NewOllamaTracker(st, nil)
	ag := NewOllamaAgent(client, st, tr, &config.Config{
		OllamaAPIKey: "test-key",
	}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())
}

func ollamaSnapAt(captured time.Time, used float64, resetsAt time.Time) *api.OllamaSnapshot {
	r := resetsAt
	return &api.OllamaSnapshot{
		CapturedAt:     captured,
		Plan:           "pro",
		MonthlyUsedUSD: used,
		Quotas: []api.OllamaQuota{{
			Name: "monthly", Used: used, Limit: 60, Utilization: used / 60 * 100,
			Format: api.OllamaQuotaFormatCurrency, ResetsAt: &r,
		}},
	}
}

func TestOllamaAgent_LearnsResetDayFromUsageDrop(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	guess := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC) // account-anniversary guess
	stub := &stubOllamaClient{}
	cfg := &config.Config{OllamaAPIKey: "k"}
	ag := NewOllamaAgent(stub, st, tracker.NewOllamaTracker(st, nil), cfg, time.Second, slog.Default(), nil)

	// Poll 1: usage growing, no anchor learned, guess kept.
	stub.snapshot = ollamaSnapAt(time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC), 5.0, guess)
	ag.poll(context.Background())
	latest, _ := st.QueryLatestOllama()
	if latest == nil || !latest.Quotas[0].ResetsAt.Equal(guess) {
		t.Fatalf("poll 1 resetsAt = %v, want guess %v", latest.Quotas[0].ResetsAt, guess)
	}
	if v, _ := st.GetSetting("ollama_reset_anchor"); v != "" {
		t.Fatalf("anchor should not be learned yet, got %q", v)
	}

	// Poll 2: usage fell from 5.00 to 0.20 on the 20th -> real reset observed.
	observed := time.Date(2026, 9, 20, 0, 4, 0, 0, time.UTC)
	stub.snapshot = ollamaSnapAt(observed, 0.2, guess)
	ag.poll(context.Background())
	latest, _ = st.QueryLatestOllama()
	want := time.Date(2026, 10, 20, 0, 4, 0, 0, time.UTC)
	if latest == nil || !latest.Quotas[0].ResetsAt.Equal(want) {
		t.Fatalf("poll 2 resetsAt = %v, want learned %v", latest.Quotas[0].ResetsAt, want)
	}
	if v, _ := st.GetSetting("ollama_reset_anchor"); v != observed.Format(time.RFC3339) {
		t.Fatalf("anchor = %q, want %q", v, observed.Format(time.RFC3339))
	}

	// Poll 3: growing again, still anchored on the learned day.
	stub.snapshot = ollamaSnapAt(time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC), 0.9, guess)
	ag.poll(context.Background())
	latest, _ = st.QueryLatestOllama()
	if !latest.Quotas[0].ResetsAt.Equal(want) {
		t.Fatalf("poll 3 resetsAt = %v, want %v", latest.Quotas[0].ResetsAt, want)
	}

	// Tiny rounding dip (below half a cent) must not count as a reset.
	stub.snapshot = ollamaSnapAt(time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC), 0.897, guess)
	ag.poll(context.Background())
	if v, _ := st.GetSetting("ollama_reset_anchor"); v != observed.Format(time.RFC3339) {
		t.Fatalf("rounding dip changed anchor to %q", v)
	}
}

func TestOllamaAgent_ExplicitResetDayDisablesLearning(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	guess := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	stub := &stubOllamaClient{}
	cfg := &config.Config{OllamaAPIKey: "k", OllamaResetDay: 1}
	ag := NewOllamaAgent(stub, st, nil, cfg, time.Second, slog.Default(), nil)

	stub.snapshot = ollamaSnapAt(time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC), 5.0, guess)
	ag.poll(context.Background())
	stub.snapshot = ollamaSnapAt(time.Date(2026, 9, 20, 0, 4, 0, 0, time.UTC), 0.2, guess)
	ag.poll(context.Background())

	if v, _ := st.GetSetting("ollama_reset_anchor"); v != "" {
		t.Fatalf("explicit reset day should disable learning, got anchor %q", v)
	}
	latest, _ := st.QueryLatestOllama()
	if !latest.Quotas[0].ResetsAt.Equal(guess) {
		t.Fatalf("resetsAt = %v, want untouched %v", latest.Quotas[0].ResetsAt, guess)
	}
}
