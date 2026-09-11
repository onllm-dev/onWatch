package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func withIdleMuseCLI(t *testing.T) {
	t.Helper()
	prev := museCLIBusy
	museCLIBusy = func(string) bool { return false }
	t.Cleanup(func() { museCLIBusy = prev })
}

type stubMuseClient struct {
	snapshot *api.MuseSnapshot
	err      error
}

func (s *stubMuseClient) FetchSnapshot(ctx context.Context) (*api.MuseSnapshot, error) {
	return s.snapshot, s.err
}

func TestNewMuseAgent_Basic(t *testing.T) {
	a := NewMuseAgent(nil, nil, nil, 60*time.Second, nil, nil)
	if a == nil {
		t.Fatal("nil agent")
	}
	a.SetPollingCheck(func() bool { return true })
	a.SetNotifier(nil)
}

func TestMuseAgent_Poll_NoClientSafe(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	tr := tracker.NewMuseTracker(st, nil)
	ag := NewMuseAgent(nil, st, tr, time.Second, nil, NewSessionManager(st, "muse", 60*time.Second, nil))
	ag.poll(context.Background())
}

func TestMuseAgent_Poll_FetchErrorNoInsert(t *testing.T) {
	withIdleMuseCLI(t)
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := &stubMuseClient{err: errors.New("fetch failed")}
	tr := tracker.NewMuseTracker(st, nil)
	ag := NewMuseAgent(client, st, tr, time.Second, slog.Default(), NewSessionManager(st, "muse", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestMuse()
	if err != nil {
		t.Fatalf("QueryLatestMuse: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot after fetch failure")
	}
}

func TestMuseAgent_Poll_SuccessInsertsAndTracks(t *testing.T) {
	withIdleMuseCLI(t)
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	snapshot := &api.MuseSnapshot{
		CapturedAt: now,
		Tier:       "pro",
		Model:      "muse-spark-1.3",
		Quotas: []api.MuseQuota{
			{Name: api.MuseQuotaWindow5H, Used: 10, Limit: 100, Utilization: 10, Format: api.MuseQuotaFormatPercent, ResetsAt: &reset},
			{Name: api.MuseQuotaWeekly, Used: 5, Limit: 100, Utilization: 5, Format: api.MuseQuotaFormatPercent, ResetsAt: &reset},
		},
	}

	client := &stubMuseClient{snapshot: snapshot}
	tr := tracker.NewMuseTracker(st, slog.Default())
	ag := NewMuseAgent(client, st, tr, time.Second, slog.Default(), NewSessionManager(st, "muse", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestMuse()
	if err != nil {
		t.Fatalf("QueryLatestMuse: %v", err)
	}
	if latest == nil || len(latest.Quotas) != 2 {
		t.Fatalf("expected inserted snapshot, got %+v", latest)
	}

	cycle, err := st.QueryActiveMuseCycle(api.MuseQuotaWindow5H)
	if err != nil {
		t.Fatalf("QueryActiveMuseCycle: %v", err)
	}
	if cycle == nil {
		t.Fatal("expected active cycle")
	}
}

func TestMuseAgent_Poll_SkipsWhenCLIBusy(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	prev := museCLIBusy
	museCLIBusy = func(string) bool { return true }
	defer func() { museCLIBusy = prev }()

	called := false
	client := &stubMuseClient{snapshot: &api.MuseSnapshot{CapturedAt: time.Now().UTC()}}
	tr := tracker.NewMuseTracker(st, nil)
	ag := NewMuseAgent(&countingMuseClient{inner: client, called: &called}, st, tr, time.Second, slog.Default(), nil)
	ag.poll(context.Background())
	if called {
		t.Fatal("usage probe must not run while the muse CLI is active")
	}
	latest, err := st.QueryLatestMuse()
	if err != nil {
		t.Fatalf("QueryLatestMuse: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot when muse CLI is busy")
	}
}

func TestMuseAgent_Poll_RateLimitedNoInsert(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	prev := museCLIBusy
	museCLIBusy = func(string) bool { return false }
	defer func() { museCLIBusy = prev }()

	client := &stubMuseClient{err: api.ErrMuseRateLimited}
	ag := NewMuseAgent(client, st, tracker.NewMuseTracker(st, nil), time.Second, slog.Default(), nil)
	ag.poll(context.Background())
	latest, err := st.QueryLatestMuse()
	if err != nil {
		t.Fatalf("QueryLatestMuse: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshot after 429")
	}
}

type countingMuseClient struct {
	inner  *stubMuseClient
	called *bool
}

func (c *countingMuseClient) FetchSnapshot(ctx context.Context) (*api.MuseSnapshot, error) {
	*c.called = true
	return c.inner.FetchSnapshot(ctx)
}

func TestMuseAgent_Poll_DisabledByCheck(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	client := &stubMuseClient{snapshot: &api.MuseSnapshot{CapturedAt: now}}
	tr := tracker.NewMuseTracker(st, nil)
	ag := NewMuseAgent(client, st, tr, time.Second, slog.Default(), NewSessionManager(st, "muse", 60*time.Second, nil))
	ag.SetPollingCheck(func() bool { return false })

	ag.poll(context.Background())

	latest, err := st.QueryLatestMuse()
	if err != nil {
		t.Fatalf("QueryLatestMuse: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no poll when disabled")
	}
}
