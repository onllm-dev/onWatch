package agent

import (
	"context"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/steipete/sweetcookie"
	"io"
	"log/slog"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

type fakeMistralFetcher struct {
	calls    int
	authFail bool
	failure  error
}

func (f *fakeMistralFetcher) FetchSnapshot(_ context.Context, s api.MistralSession) (*api.MistralSnapshot, error) {
	f.calls++
	if f.failure != nil {
		return nil, f.failure
	}
	if f.authFail {
		return nil, api.ErrMistralAuth
	}
	now := time.Now().UTC()
	return &api.MistralSnapshot{Identity: s.Source.Identity(), CapturedAt: now, Status: "ok", Quotas: []api.MistralQuota{{Name: "api_included", Used: 2, Limit: 10, Utilization: 20, CapturedAt: now}}}, nil
}
func TestMistralPauseAndRecovery(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true}, nil)
	defer a.sm.Close()
	source := api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	a.source = &source
	cookieValue := "expired-session"
	imports := 0
	a.read = func(_ context.Context, o sweetcookie.Options) (sweetcookie.Result, error) {
		imports++
		if o.Profiles[sweetcookie.BrowserChrome] != "synthetic" {
			t.Fatal("source switched")
		}
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: cookieValue, Domain: ".mistral.ai", Path: "/"}}}, nil
	}
	client := &fakeMistralFetcher{authFail: true}
	a.client = client
	a.poll(context.Background())
	if !a.paused || client.calls != 2 || imports != 2 {
		t.Fatalf("paused=%v calls=%d imports=%d", a.paused, client.calls, imports)
	}
	a.poll(context.Background())
	if client.calls != 2 {
		t.Fatal("network polled while paused")
	}
	a.next = time.Time{}
	a.imported = time.Now().Add(-11 * time.Minute)
	a.poll(context.Background())
	if client.calls != 2 {
		t.Fatal("unchanged cookie retried")
	}
	cookieValue = "refreshed-session"
	client.authFail = false
	a.next = time.Time{}
	a.imported = time.Now().Add(-11 * time.Minute)
	a.poll(context.Background())
	if a.paused || client.calls != 3 {
		t.Fatal("did not recover")
	}
	snap, e := db.LatestMistral(context.Background())
	if e != nil || snap == nil || snap.Identity != source.Identity() {
		t.Fatalf("snapshot=%+v error=%v", snap, e)
	}
}
func TestMistralManualMissingCookieDoesNotImport(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralAuthMode: "manual"}, nil)
	defer a.sm.Close()
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		t.Fatal("manual mode read browser")
		return sweetcookie.Result{}, nil
	}
	a.poll(context.Background())
	status, _ := db.GetSetting("mistral_status")
	if status != "reconnect" {
		t.Fatal(status)
	}
}

func TestMistralDiscoveryRateLimit(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true, MistralBrowser: "chrome", MistralBrowserProfile: t.TempDir()}, nil)
	defer a.sm.Close()
	a.client = &fakeMistralFetcher{failure: &api.MistralHTTPError{Status: 429, RetryAfter: time.Hour}}
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: "synthetic", Domain: ".mistral.ai", Path: "/"}}}, nil
	}
	a.poll(context.Background())
	status, _ := db.GetSetting("mistral_status")
	if status != "stale" || time.Until(a.next) < 59*time.Minute {
		t.Fatalf("rate limit misreported as login failure: %s delay=%v", status, time.Until(a.next))
	}
}

func TestMistralPollingHeap(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewMistralAgent(db, &config.Config{MistralAuthCookie: "ory_session_test=synthetic"}, logger)
	defer func() { a.sm.Close() }()
	a.client = &fakeMistralFetcher{}
	a.poll(context.Background())
	debug.FreeOSMemory()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 300; i++ {
		a.imported = time.Time{}
		a.poll(context.Background())
		if _, e = db.LatestMistral(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	debug.FreeOSMemory()
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("300 synthetic polls/imports: retained Go heap=%d bytes, growth=%d bytes (not daemon RSS)", after.HeapAlloc, growth)
	if growth > 4<<20 {
		t.Fatalf("unexpected retained heap growth: %d", growth)
	}
}

// An unconfigured Mistral provider must never read browser cookie stores.
// Automatic import prompts for macOS Keychain / Linux keyring access, so it may
// only run once the user has actually enabled the provider.
func TestMistralUnconfiguredNeverReadsBrowsers(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.sm.Close()
	source := api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	a.source = &source
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		t.Error("unconfigured Mistral read a browser cookie store")
		return sweetcookie.Result{}, nil
	}
	client := &fakeMistralFetcher{}
	a.client = client
	a.poll(context.Background())
	if client.calls != 0 {
		t.Fatalf("unconfigured Mistral contacted Mistral: calls=%d", client.calls)
	}
}

// The daemon starts every provider agent at once, so an early settings write
// can lose a WAL race and return SQLITE_BUSY. That must not be fatal: a failed
// Run is never retried, which would disable Mistral until the next restart.
// The identity is re-asserted by SaveMistral on the first successful poll.
func TestMistralStartupSettingFailureKeepsAgentAlive(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	source := api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	a.source = &source
	a.client = &fakeMistralFetcher{}
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{}, nil
	}
	// A closed store makes every write fail the way a contended one does.
	db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("agent exited on a startup settings-write failure: %v", err)
	}
}

// A working session must never be re-read from the browser on a schedule.
// Every read shells out to the platform credential store (macOS `security`,
// Linux keyring), which prompts the user for their login password. A daemon
// that re-imports every cycle prompts indefinitely, and a denied or ignored
// prompt returns no cookies, which silently breaks polling.
func TestMistralValidSessionIsNotReimported(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.sm.Close()
	source := api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	a.source = &source
	imports := 0
	future := time.Now().Add(720 * time.Hour)
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		imports++
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: "v", Domain: ".mistral.ai", Path: "/", Expires: &future}}}, nil
	}
	a.client = &fakeMistralFetcher{}
	a.poll(context.Background())
	if imports != 1 {
		t.Fatalf("first poll imports=%d, want 1", imports)
	}
	for i := 0; i < 5; i++ {
		a.imported = time.Time{} // however much time has passed since the last read
		a.next = time.Time{}
		a.poll(context.Background())
	}
	if imports != 1 {
		t.Fatalf("a valid session was re-read from the browser %d times; each read can prompt for the user's password", imports)
	}
}

// Retention is configurable and 0 disables pruning entirely, matching
// ONWATCH_API_INTEGRATIONS_RETENTION. Pruning with a zero cutoff would delete
// the whole history, so the disabled case must short-circuit before the call.
func TestMistralRetentionDisabledDoesNotPrune(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -400)
	source := api.MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}
	// History is partitioned by identity, so the seeded rows have to share the
	// identity the polled snapshot will pin.
	for _, at := range []time.Time{old, now.Add(-time.Minute)} {
		snap := &api.MistralSnapshot{Identity: source.Identity(), CapturedAt: at, Status: "ok", Quotas: []api.MistralQuota{
			{Name: "api_included", Used: 1, Limit: 10, CapturedAt: at},
		}}
		if e = db.SaveMistral(context.Background(), snap); e != nil {
			t.Fatal(e)
		}
	}
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true, MistralRetention: 0}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.sm.Close()
	a.source = &source
	future := time.Now().Add(720 * time.Hour)
	a.read = func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: "v", Domain: ".mistral.ai", Path: "/", Expires: &future}}}, nil
	}
	a.client = &fakeMistralFetcher{}
	a.lastPrune = time.Time{} // due for a prune
	a.poll(context.Background())

	rows, e := db.MistralHistory(context.Background(), old.Add(-time.Hour), time.Now().Add(time.Hour), 200)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) < 3 {
		t.Fatalf("retention 0 pruned history: %d snapshots left, want the 2 saved plus the polled one", len(rows))
	}
}
