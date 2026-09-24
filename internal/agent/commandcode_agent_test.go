package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type stubCommandCodeClient struct {
	snapshot *api.CommandCodeSnapshot
	err      error
	calls    int
}

func (s *stubCommandCodeClient) FetchSnapshot(ctx context.Context) (*api.CommandCodeSnapshot, error) {
	s.calls++
	return s.snapshot, s.err
}

func newCommandCodeAgentStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func commandCodeAgentSnapshot(now time.Time) *api.CommandCodeSnapshot {
	reset := now.Add(4 * time.Hour)
	periodEnd := now.Add(25 * 24 * time.Hour)
	return &api.CommandCodeSnapshot{
		CapturedAt:       now,
		AccountName:      "prakersh",
		Plan:             "individual-goat",
		Status:           "active",
		RemainingCredits: 50.99,
		PeriodEnd:        &periodEnd,
		PeriodCostUSD:    18.99,
		PeriodReqs:       6190,
		PeriodTokens:     1_100_000_000,
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14, Utilization: 7.14, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
			{Name: api.CommandCodeQuotaWeekly, Used: 19, Limit: 35, Utilization: 54.29, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
			{Name: api.CommandCodeQuotaMonthly, Used: 18.99, Limit: 69.98, Utilization: 27.14, Format: api.CommandCodeQuotaFormatCurrency, Remaining: 50.99, ResetsAt: &periodEnd},
		},
	}
}

func TestNewCommandCodeAgentBasic(t *testing.T) {
	a := NewCommandCodeAgent(nil, nil, nil, 60*time.Second, nil, nil)
	if a == nil {
		t.Fatal("nil agent")
	}
	a.SetPollingCheck(func() bool { return true })
	a.SetNotifier(nil)
}

func TestCommandCodeAgentPollWithNilClientIsSafe(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	ag := NewCommandCodeAgent(nil, st, tracker.NewCommandCodeTracker(st, nil), time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))
	ag.poll(context.Background())
}

func TestCommandCodeAgentPollInsertsAndTracks(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	now := time.Now().UTC()
	client := &stubCommandCodeClient{snapshot: commandCodeAgentSnapshot(now)}
	tr := tracker.NewCommandCodeTracker(st, nil)
	ag := NewCommandCodeAgent(client, st, tr, time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestCommandCode()
	if err != nil {
		t.Fatalf("QueryLatestCommandCode: %v", err)
	}
	if latest == nil {
		t.Fatal("no snapshot inserted")
	}
	if latest.RemainingCredits != 50.99 || latest.Plan != "individual-goat" {
		t.Errorf("latest = %+v", latest)
	}
	for _, name := range []string{
		api.CommandCodeQuotaFiveHour,
		api.CommandCodeQuotaWeekly,
		api.CommandCodeQuotaMonthly,
	} {
		cycle, err := st.QueryActiveCommandCodeCycle(name)
		if err != nil {
			t.Fatalf("active cycle %s: %v", name, err)
		}
		if cycle == nil {
			t.Errorf("no active cycle for %s", name)
		}
	}
}

func TestCommandCodeAgentPollFetchErrorInsertsNothing(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	client := &stubCommandCodeClient{err: errors.New("fetch failed")}
	ag := NewCommandCodeAgent(client, st, tracker.NewCommandCodeTracker(st, nil), time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestCommandCode()
	if err != nil {
		t.Fatalf("QueryLatestCommandCode: %v", err)
	}
	if latest != nil {
		t.Fatalf("latest = %+v, want nothing after a fetch error", latest)
	}
}

func TestCommandCodeAgentPollRespectsPollingCheck(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	client := &stubCommandCodeClient{snapshot: commandCodeAgentSnapshot(time.Now().UTC())}
	ag := NewCommandCodeAgent(client, st, nil, time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))
	ag.SetPollingCheck(func() bool { return false })

	ag.poll(context.Background())

	if client.calls != 0 {
		t.Fatalf("client calls = %d, want 0 while polling is disabled", client.calls)
	}
}

func TestCommandCodeAgentPollCancelledContextIsSilent(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	client := &stubCommandCodeClient{err: context.Canceled}
	ag := NewCommandCodeAgent(client, st, nil, time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ag.poll(ctx)

	latest, err := st.QueryLatestCommandCode()
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatalf("latest = %+v, want nothing", latest)
	}
}

func TestCommandCodeAgentNotifierReceivesQuotas(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	now := time.Now().UTC()
	client := &stubCommandCodeClient{snapshot: commandCodeAgentSnapshot(now)}
	ag := NewCommandCodeAgent(client, st, tracker.NewCommandCodeTracker(st, nil), time.Second, nil,
		NewSessionManager(st, "commandcode", 60*time.Second, nil))
	// A real engine backed by an in-memory store; no channels are configured,
	// so Check only exercises the threshold bookkeeping.
	ag.SetNotifier(notify.New(st, nil))

	ag.poll(context.Background())

	latest, err := st.QueryLatestCommandCode()
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("snapshot not inserted")
	}
}

func TestCommandCodeAgentRunStopsOnContextCancel(t *testing.T) {
	st := newCommandCodeAgentStore(t)
	client := &stubCommandCodeClient{snapshot: commandCodeAgentSnapshot(time.Now().UTC())}
	ag := NewCommandCodeAgent(client, st, tracker.NewCommandCodeTracker(st, nil), 10*time.Millisecond, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ag.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.calls == 0 {
		t.Fatal("expected at least one poll before cancellation")
	}
}
