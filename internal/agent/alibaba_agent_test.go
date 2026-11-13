package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type fakeAlibabaFetcher struct {
	calls atomic.Int32
	fn    func() (*api.AlibabaSnapshot, error)
}

func (f *fakeAlibabaFetcher) FetchSnapshot(ctx context.Context) (*api.AlibabaSnapshot, error) {
	f.calls.Add(1)
	return f.fn()
}

func newAlibabaAgentTestStore(t *testing.T) (*store.Store, *tracker.AlibabaTracker, int64) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	acc, err := s.GetOrCreateProviderAccount("alibaba", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	return s, tracker.NewAlibabaTracker(s, nil), acc.ID
}

func alibabaAgentSnapshot() *api.AlibabaSnapshot {
	reset := time.Now().UTC().Add(3 * time.Hour)
	return &api.AlibabaSnapshot{
		CapturedAt:    time.Now().UTC(),
		SpecCode:      "lite",
		Status:        "VALID",
		RemainingDays: 12,
		Quotas: []api.AlibabaQuota{
			{Name: api.AlibabaQuotaFiveHour, Utilization: 15, ResetsAt: &reset},
			{Name: api.AlibabaQuotaWeekly, Utilization: 35},
		},
	}
}

func TestAlibabaAgent_SinglePoll(t *testing.T) {
	t.Parallel()
	s, tr, accountID := newAlibabaAgentTestStore(t)
	fetcher := &fakeAlibabaFetcher{fn: func() (*api.AlibabaSnapshot, error) { return alibabaAgentSnapshot(), nil }}
	sm := NewSessionManager(s, "alibaba", 600*time.Second, slog.Default())
	ag := NewAlibabaAgentWithAccount(fetcher, s, tr, 100*time.Millisecond, nil, sm, accountID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ag.Run(ctx) }()

	var latest *api.AlibabaSnapshot
	waitUntil(t, 10*time.Second, func() bool {
		var err error
		latest, err = s.QueryLatestAlibaba(accountID)
		return err == nil && latest != nil
	}, "a stored Alibaba snapshot")
	cancel()

	if len(latest.Quotas) != 2 || latest.SpecCode != "lite" || latest.RemainingDays != 12 {
		t.Errorf("unexpected snapshot: %+v", latest)
	}
	// The tracker runs right after the insert, so wait for it instead of
	// assuming it already finished.
	waitUntil(t, 10*time.Second, func() bool {
		cycle, err := s.QueryActiveAlibabaCycle(api.AlibabaQuotaFiveHour, accountID)
		return err == nil && cycle != nil && cycle.PeakUtilization == 15
	}, "a tracked Alibaba cycle with the first reading as peak")

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestAlibabaAgent_FetchErrorStoresNothing(t *testing.T) {
	t.Parallel()
	s, tr, accountID := newAlibabaAgentTestStore(t)
	fetcher := &fakeAlibabaFetcher{fn: func() (*api.AlibabaSnapshot, error) { return nil, api.ErrAlibabaUnauthorized }}
	ag := NewAlibabaAgentWithAccount(fetcher, s, tr, time.Hour, nil, nil, accountID)

	ag.poll(context.Background())

	if fetcher.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", fetcher.calls.Load())
	}
	if latest, _ := s.QueryLatestAlibaba(accountID); latest != nil {
		t.Errorf("failed poll must not store a snapshot: %+v", latest)
	}
}

func TestAlibabaAgent_PublishesPollStatus(t *testing.T) {
	// Not parallel: the poll status registry is process wide.
	s, tr, accountID := newAlibabaAgentTestStore(t)
	registry := NewPollStatusRegistry()
	SetPollStatusRegistry(registry)
	t.Cleanup(func() { SetPollStatusRegistry(nil) })

	var failure error = fmt.Errorf("wrapped: %w", api.ErrAlibabaRateLimited)
	fetcher := &fakeAlibabaFetcher{fn: func() (*api.AlibabaSnapshot, error) {
		if failure != nil {
			return nil, failure
		}
		return alibabaAgentSnapshot(), nil
	}}
	ag := NewAlibabaAgentWithAccount(fetcher, s, tr, time.Hour, nil, nil, accountID)
	key := pollStatusAccountKey(accountID)

	ag.poll(context.Background())
	if outcome, ok := registry.GetOutcome("alibaba", key); !ok || outcome != PollOutcomeRateLimit {
		t.Errorf("outcome = %q, %v; want rate limit", outcome, ok)
	}

	failure = api.ErrAlibabaUnauthorized
	ag.poll(context.Background())
	if outcome, _ := registry.GetOutcome("alibaba", key); outcome != PollOutcomeAuth {
		t.Errorf("outcome = %q, want auth", outcome)
	}

	failure = nil
	ag.poll(context.Background())
	if _, ok := registry.GetOutcome("alibaba", key); ok {
		t.Error("a successful poll must clear the status")
	}
}

func TestAlibabaPollErrorClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want PollOutcome
	}{
		{api.ErrAlibabaNetwork, PollOutcomeNetwork},
		{api.ErrAlibabaUnauthorized, PollOutcomeAuth},
		{api.ErrAlibabaMissingCookies, PollOutcomeAuth},
		{api.ErrAlibabaRateLimited, PollOutcomeRateLimit},
		{api.ErrAlibabaInvalidResponse, PollOutcomeParse},
		{api.ErrAlibabaServerError, PollOutcomeUpstream},
	}
	for _, tc := range tests {
		if got := ClassifyPollError(fmt.Errorf("x: %w", tc.err)); got != tc.want {
			t.Errorf("ClassifyPollError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestAlibabaAgent_PollingCheckPauses(t *testing.T) {
	t.Parallel()
	s, tr, accountID := newAlibabaAgentTestStore(t)
	fetcher := &fakeAlibabaFetcher{fn: func() (*api.AlibabaSnapshot, error) { return alibabaAgentSnapshot(), nil }}
	ag := NewAlibabaAgentWithAccount(fetcher, s, tr, time.Hour, nil, nil, accountID)
	ag.SetPollingCheck(func() bool { return false })

	ag.poll(context.Background())
	if fetcher.calls.Load() != 0 {
		t.Errorf("paused agent fetched %d times", fetcher.calls.Load())
	}
}

func TestAlibabaAgent_AccountScoping(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	other, err := s.GetOrCreateProviderAccount("alibaba", "other")
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeAlibabaFetcher{fn: func() (*api.AlibabaSnapshot, error) { return alibabaAgentSnapshot(), nil }}
	ag := NewAlibabaAgentWithAccount(fetcher, s, tr, time.Hour, nil, nil, other.ID)

	ag.poll(context.Background())

	if latest, _ := s.QueryLatestAlibaba(other.ID); latest == nil {
		t.Error("snapshot must be stored for the agent's account")
	}
	if latest, _ := s.QueryLatestAlibaba(other.ID + 100); latest != nil {
		t.Error("snapshot leaked to another account")
	}
}

func TestAlibabaAgent_NilClientIsNoop(t *testing.T) {
	t.Parallel()
	s, tr, accountID := newAlibabaAgentTestStore(t)
	ag := NewAlibabaAgentWithAccount(nil, s, tr, time.Hour, nil, nil, accountID)
	ag.poll(context.Background())
}

// fakeAlibabaConsole serves the console page and gateways with a fixed usage.
func fakeAlibabaConsole(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			hits.Add(1)
			_, _ = w.Write([]byte(`<script>SEC_TOKEN: "tok"</script>`))
		case "/zelda":
			inner := `{"per5HourPercentage":0.2,"per5HourResetTime":4102444800000}`
			if r.URL.Query().Get("api") == "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription" {
				inner = `{"remainingDays":9,"specCode":"lite","status":"VALID","instanceCode":"i-1"}`
			}
			_, _ = fmt.Fprintf(w, `{"data":{"DataV2":{"data":{"success":true,"data":%s}}}}`, inner)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createAlibabaTestAccount(t *testing.T, s *store.Store, name, metadata string) int64 {
	t.Helper()
	acc, err := s.GetOrCreateProviderAccount("alibaba", name)
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if err := s.UpdateProviderAccountMetadata(acc.ID, metadata); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}
	return acc.ID
}

func TestAlibabaAgentManager_Defaults(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	mgr := NewAlibabaAgentManager(s, tr, 30*time.Second, nil)
	if mgr.region != "ap-southeast-1" {
		t.Errorf("region = %q", mgr.region)
	}
	if mgr.instances == nil {
		t.Error("instances map must be initialised")
	}
	mgr.SetRegion("us-east-1")
	mgr.SetPollingCheck(func() bool { return true })
	mgr.SetAccountPollingCheck(func(int64) bool { return true })
	if mgr.region != "us-east-1" || mgr.pollingCheck == nil || mgr.accountPollingCheck == nil {
		t.Error("setters did not apply")
	}
}

func TestParseAlibabaAccountMeta(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		cookies string
		region  string
	}{
		{`{"cookies":"a=1; b=2","region":"ap-southeast-1"}`, "a=1; b=2", "ap-southeast-1"},
		{`{"cookies":"a=1"}`, "a=1", ""},
		{`{}`, "", ""},
		{``, "", ""},
		{`not-json`, "", ""},
	}
	for _, tc := range tests {
		meta := parseAlibabaAccountMeta(tc.raw)
		if meta.Cookies != tc.cookies || meta.Region != tc.region {
			t.Errorf("parseAlibabaAccountMeta(%q) = %+v", tc.raw, meta)
		}
	}
}

func TestAlibabaAgentManager_SkipsAccountsWithoutCookies(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	createAlibabaTestAccount(t, s, "empty", `{"region":"ap-southeast-1"}`)
	createAlibabaTestAccount(t, s, "blank", `{"cookies":"   "}`)

	mgr := NewAlibabaAgentManager(s, tr, time.Hour, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	mgr.ctx, mgr.cancel = ctx, cancel
	defer cancel()

	if err := mgr.loadAndStartAccounts(); err != nil {
		t.Fatalf("loadAndStartAccounts: %v", err)
	}
	if running := mgr.GetRunningAccounts(); len(running) != 0 {
		t.Errorf("running = %v, want none (the default account has no cookies either)", running)
	}
}

func TestAlibabaAgentManager_PollsEveryAccountAndReloads(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	var pageHits atomic.Int32
	srv := fakeAlibabaConsole(t, &pageHits)

	id1 := createAlibabaTestAccount(t, s, "work", `{"cookies":"a=1","region":"ap-southeast-1"}`)
	mgr := NewAlibabaAgentManager(s, tr, time.Hour, slog.Default())
	mgr.SetClientOptions(
		api.WithAlibabaPageURL(srv.URL+"/page"),
		api.WithAlibabaZeldaGateway(srv.URL+"/zelda"),
		api.WithAlibabaPOPGateway(srv.URL+"/pop"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = mgr.Run(ctx); close(done) }()

	waitUntil(t, 10*time.Second, func() bool {
		snap, err := s.QueryLatestAlibaba(id1)
		return err == nil && snap != nil
	}, "a snapshot for the first account")

	snap, _ := s.QueryLatestAlibaba(id1)
	if snap.SpecCode != "lite" || snap.InstanceID != "i-1" || len(snap.Quotas) != 1 || snap.Quotas[0].Utilization != 20 {
		t.Errorf("unexpected snapshot: %+v", snap)
	}

	id2 := createAlibabaTestAccount(t, s, "personal", `{"cookies":"b=2"}`)
	mgr.Reload()
	waitUntil(t, 10*time.Second, func() bool {
		snap, err := s.QueryLatestAlibaba(id2)
		return err == nil && snap != nil
	}, "a snapshot for the added account")

	running := mgr.GetRunningAccounts()
	if len(running) != 2 {
		t.Errorf("running = %d, want 2", len(running))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestAlibabaAgentManager_ReloadWithoutRunIsNoop(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	mgr := NewAlibabaAgentManager(s, tr, time.Hour, nil)
	mgr.Reload()
}

func TestAlibabaAgentManager_PerAccountPollingCheck(t *testing.T) {
	t.Parallel()
	s, tr, _ := newAlibabaAgentTestStore(t)
	var pageHits atomic.Int32
	srv := fakeAlibabaConsole(t, &pageHits)

	enabled := createAlibabaTestAccount(t, s, "enabled", `{"cookies":"a=1"}`)
	disabled := createAlibabaTestAccount(t, s, "disabled", `{"cookies":"b=2"}`)

	mgr := NewAlibabaAgentManager(s, tr, time.Hour, slog.Default())
	mgr.SetClientOptions(
		api.WithAlibabaPageURL(srv.URL+"/page"),
		api.WithAlibabaZeldaGateway(srv.URL+"/zelda"),
		api.WithAlibabaPOPGateway(srv.URL+"/pop"),
	)
	mgr.SetAccountPollingCheck(func(id int64) bool { return id == enabled })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Run(ctx) }()

	waitUntil(t, 10*time.Second, func() bool {
		snap, err := s.QueryLatestAlibaba(enabled)
		return err == nil && snap != nil
	}, "a snapshot for the enabled account")

	if snap, _ := s.QueryLatestAlibaba(disabled); snap != nil {
		t.Error("disabled account must not be polled")
	}
	if got := len(mgr.GetRunningAccounts()); got != 2 {
		t.Errorf("both agents run, only polling is gated: got %d", got)
	}
}

func TestAlibabaAgent_RateLimitedErrorIsRecognised(t *testing.T) {
	t.Parallel()
	if !api.IsAlibabaRateLimited(fmt.Errorf("usage: %w", api.ErrAlibabaRateLimited)) {
		t.Error("wrapped rate limit must be recognised")
	}
	if api.IsAlibabaRateLimited(errors.New("other")) {
		t.Error("unrelated error must not be recognised")
	}
}
