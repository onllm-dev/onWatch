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
