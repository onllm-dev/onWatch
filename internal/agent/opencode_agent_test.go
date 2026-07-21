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

func TestNewOpenCodeAgent_Basic(t *testing.T) {
	a := NewOpenCodeAgent(nil, nil, nil, nil, 60*time.Second, nil, nil)
	if a == nil {
		t.Fatal("nil agent")
	}
	a.SetPollingCheck(func() bool { return true })
	a.SetNotifier(nil)
}

func TestOpenCodeAgent_Poll_NoClientSafe(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	tr := tracker.NewOpenCodeTracker(st, nil)
	ag := NewOpenCodeAgent(nil, st, tr, nil, time.Second, nil, NewSessionManager(st, "opencode", 60*time.Second, nil))
	ag.poll(context.Background())
}

func TestOpenCodeAgent_Poll_FetchErrorNoInsert(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		OpenCodeGoWorkspaceID: "ws",
		OpenCodeGoAuthCookie:  "cookie",
	}
	client := api.NewOpenCodeClient(nil)
	tr := tracker.NewOpenCodeTracker(st, nil)
	ag := NewOpenCodeAgent(client, st, tr, cfg, time.Second, slog.Default(), NewSessionManager(st, "opencode", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestOpenCode()
	if err != nil {
		t.Fatalf("QueryLatestOpenCode: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot after fetch failure")
	}
}

func TestOpenCodeAgent_Poll_SuccessInsertsAndTracks(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	snapshot := &api.OpenCodeSnapshot{
		CapturedAt:  now,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas: []api.OpenCodeQuota{
			{Name: "five_hour", Utilization: 10, Format: api.OpenCodeQuotaFormatPercent, ResetsAt: &reset},
		},
	}

	client := &stubOpenCodeClient{snapshot: snapshot}
	tr := tracker.NewOpenCodeTracker(st, slog.Default())
	ag := NewOpenCodeAgent(client, st, tr, &config.Config{
		OpenCodeGoWorkspaceID: "ws",
		OpenCodeGoAuthCookie:  "cookie",
	}, time.Second, slog.Default(), NewSessionManager(st, "opencode", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestOpenCode()
	if err != nil {
		t.Fatalf("QueryLatestOpenCode: %v", err)
	}
	if latest == nil || len(latest.Quotas) != 1 {
		t.Fatalf("expected inserted snapshot, got %+v", latest)
	}

	cycle, err := st.QueryActiveOpenCodeCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOpenCodeCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle after tracker.Process")
	}
}

type stubOpenCodeClient struct {
	snapshot *api.OpenCodeSnapshot
	err      error
}

func (s *stubOpenCodeClient) FetchSnapshot(_ context.Context, _, _ string) (*api.OpenCodeSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

func TestOpenCodeAgent_Poll_MissingConfig(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := api.NewOpenCodeClient(nil)
	tr := tracker.NewOpenCodeTracker(st, nil)
	ag := NewOpenCodeAgent(client, st, tr, &config.Config{}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())

	latest, err := st.QueryLatestOpenCode()
	if err != nil {
		t.Fatalf("QueryLatestOpenCode: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot when config missing")
	}
}

func TestOpenCodeAgent_Poll_AuthError(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := &stubOpenCodeClient{err: api.ErrOpenCodeUnauthorized}
	tr := tracker.NewOpenCodeTracker(st, nil)
	ag := NewOpenCodeAgent(client, st, tr, &config.Config{
		OpenCodeGoWorkspaceID: "ws",
		OpenCodeGoAuthCookie:  "cookie",
	}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())

	latest, err := st.QueryLatestOpenCode()
	if err != nil {
		t.Fatalf("QueryLatestOpenCode: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot on auth error")
	}
}

var _ interface {
	FetchSnapshot(context.Context, string, string) (*api.OpenCodeSnapshot, error)
} = (*stubOpenCodeClient)(nil)

func TestOpenCodeAgent_Poll_FetchErrorTyped(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := &stubOpenCodeClient{err: errors.Join(api.ErrOpenCodeParseFailed, errors.New("details"))}
	tr := tracker.NewOpenCodeTracker(st, nil)
	ag := NewOpenCodeAgent(client, st, tr, &config.Config{
		OpenCodeGoWorkspaceID: "ws",
		OpenCodeGoAuthCookie:  "cookie",
	}, time.Second, slog.Default(), nil)

	ag.poll(context.Background())
}
