package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// TestCodexAgent_SendStarterPing_HitsEndpoint verifies the agent sends a real
// request to the Codex Responses endpoint with the bearer token, account id, and
// a body that asks the model to reply "Quota Resumed".
func TestCodexAgent_SendStarterPing_HitsEndpoint(t *testing.T) {
	type capture struct {
		auth, account, body string
	}
	hit := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hit <- capture{auth: r.Header.Get("Authorization"), account: r.Header.Get("ChatGPT-Account-ID"), body: string(b)}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.created\n\n"))
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)

	ag.SendStarterPing(context.Background(), "acct_123", "five_hour")

	select {
	case c := <-hit:
		if c.auth != "Bearer tok_abc" {
			t.Errorf("Authorization = %q, want Bearer tok_abc", c.auth)
		}
		if c.account != "acct_123" {
			t.Errorf("ChatGPT-Account-ID = %q, want acct_123", c.account)
		}
		if !strings.Contains(c.body, "Quota Resumed") {
			t.Errorf("request body does not ask for \"Quota Resumed\": %s", c.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("starter ping did not reach the endpoint")
	}
}

// TestCodexAgent_SendStarterPing_RateCap verifies at most codexStarterMaxFires
// pings fire per window within the rate window, and that windows are independent.
func TestCodexAgent_SendStarterPing_RateCap(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)

	// Fire the same window more than the cap in quick succession.
	for i := 0; i < codexStarterMaxFires+3; i++ {
		ag.SendStarterPing(context.Background(), "acct", "five_hour")
	}
	// A different window is independent and should still fire.
	ag.SendStarterPing(context.Background(), "acct", "seven_day")

	time.Sleep(200 * time.Millisecond)
	want := int32(codexStarterMaxFires + 1)
	if got := atomic.LoadInt32(&count); got != want {
		t.Errorf("endpoint hit %d times, want %d (cap per window + one other window)", got, want)
	}
}

func TestIsUnstartedCodexWindow(t *testing.T) {
	now := time.Now().UTC()
	mk := func(d time.Duration) *time.Time { ts := now.Add(d); return &ts }

	cases := []struct {
		name     string
		quota    string
		resetsAt *time.Time
		want     bool
	}{
		{"5h pinned at full = unstarted", "five_hour", mk(5 * time.Hour), true},
		{"5h just under full = unstarted", "five_hour", mk(5*time.Hour - 90*time.Second), true},
		{"5h decreased = started", "five_hour", mk(5*time.Hour - 10*time.Minute), false},
		{"weekly at full = unstarted", "seven_day", mk(7 * 24 * time.Hour), true},
		{"weekly decreased = started", "seven_day", mk(5 * 24 * time.Hour), false},
		{"code_review never managed", "code_review", mk(5 * time.Hour), false},
		{"nil reset", "five_hour", nil, false},
		{"already past", "five_hour", mk(-time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnstartedCodexWindow(tc.quota, tc.resetsAt, now); got != tc.want {
				t.Errorf("isUnstartedCodexWindow(%s) = %v, want %v", tc.quota, got, tc.want)
			}
		})
	}
}

// TestCodexAgent_Poll_FiresStarterOnUnstartedWindow is the end-to-end runtime
// check: a real poll() against a usage endpoint reporting an unstarted 5h window
// (reset_at = now + full window) must drive a starter ping to the Responses
// endpoint with the configured account id. This exercises the same path the
// daemon runs (poll -> maybeAutoStartWindows -> SendStarterPing).
func TestCodexAgent_Poll_FiresStarterOnUnstartedWindow(t *testing.T) {
	var starterHits atomic.Int32
	gotAccount := make(chan string, 4)
	starter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starterHits.Add(1)
		select {
		case gotAccount <- r.Header.Get("ChatGPT-Account-ID"):
		default:
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer starter.Close()

	// Usage endpoint reports an UNSTARTED 5h window: reset_at rolls to now + 5h.
	usage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reset := time.Now().Unix() + 18000
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":1,"reset_at":%d,"limit_window_seconds":18000}}}`, reset)
	}))
	defer usage.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger,
		api.WithCodexBaseURL(usage.URL), api.WithCodexStarterURL(starter.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, 50*time.Millisecond, logger, nil)
	ag.SetCodexAccountID("acct_e2e")
	ag.SetAutoStartCheck(func(string) bool { return true })

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go ag.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	if got := starterHits.Load(); got < 1 {
		t.Fatalf("starter endpoint hit %d times, want >=1 (unstarted window must fire)", got)
	}
	if got := starterHits.Load(); int(got) > codexStarterMaxFires {
		t.Errorf("starter fired %d times, exceeds rate cap %d", got, codexStarterMaxFires)
	}
	select {
	case acct := <-gotAccount:
		if acct != "acct_e2e" {
			t.Errorf("ChatGPT-Account-ID = %q, want acct_e2e", acct)
		}
	default:
		t.Error("no account id captured")
	}
}

// TestCodexAgent_MaybeAutoStart fires only for windows that are both enabled and
// observed unstarted.
func TestCodexAgent_MaybeAutoStart(t *testing.T) {
	var count int32
	gotWindows := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetCodexAccountID("acct")

	now := time.Now().UTC()
	fiveUnstarted := now.Add(5 * time.Hour)     // unstarted
	sevenStarted := now.Add(2 * 24 * time.Hour) // started (well below full)
	quotas := []api.CodexQuota{
		{Name: "five_hour", ResetsAt: &fiveUnstarted},
		{Name: "seven_day", ResetsAt: &sevenStarted},
	}

	// Disabled: nothing fires.
	ag.SetAutoStartCheck(func(string) bool { return false })
	ag.maybeAutoStartWindows(context.Background(), quotas, now)
	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Fatalf("disabled auto-start fired %d times, want 0", got)
	}

	// Enabled: only the unstarted five_hour fires.
	ag.SetAutoStartCheck(func(q string) bool { gotWindows <- q; return true })
	ag.maybeAutoStartWindows(context.Background(), quotas, now)
	time.Sleep(250 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("enabled auto-start fired %d times, want 1 (only unstarted five_hour)", got)
	}
}

// starterWebhookEngine wires a notification engine whose webhook channel points
// at a capture server, so starter events can be observed end to end.
func starterWebhookEngine(t *testing.T) (*notify.NotificationEngine, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var events []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notify.WebhookPayload
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		events = append(events, p.Event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	db, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	webhookJSON, _ := json.Marshal(map[string]interface{}{
		"url":             hook.URL,
		"timeout_seconds": 2,
		"events":          map[string]bool{"starter_success": true, "starter_failure": true},
	})
	db.SetSetting("webhook", string(webhookJSON))
	notifJSON, _ := json.Marshal(map[string]interface{}{
		"warning_threshold":  80,
		"critical_threshold": 95,
		"channels":           map[string]bool{"webhook": true},
	})
	db.SetSetting("notifications", string(notifJSON))

	engine := notify.New(db, slog.Default())
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	return engine, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
}

// A successful starter ping notifies the webhook channel.
func TestCodexAgent_SendStarterPing_NotifiesOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.created\n\n"))
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	ag.SendStarterPing(context.Background(), "acct_123", "five_hour")

	events := delivered()
	if len(events) != 1 || events[0] != notify.EventStarterSuccess {
		t.Errorf("delivered events = %v, want [%s]", events, notify.EventStarterSuccess)
	}
}

// A failed starter ping notifies the webhook channel with the failure event.
func TestCodexAgent_SendStarterPing_NotifiesOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	ag.SendStarterPing(context.Background(), "acct_123", "five_hour")

	events := delivered()
	if len(events) != 1 || events[0] != notify.EventStarterFailure {
		t.Errorf("delivered events = %v, want [%s]", events, notify.EventStarterFailure)
	}
}

// A ping skipped by the rate cap is not a delivery outcome and must stay silent.
func TestCodexAgent_SendStarterPing_RateCappedSendsNoEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.created\n\n"))
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	for i := 0; i < codexStarterMaxFires+2; i++ {
		ag.SendStarterPing(context.Background(), "acct", "five_hour")
	}

	if got := len(delivered()); got != codexStarterMaxFires {
		t.Errorf("delivered %d events, want %d (rate-capped pings send nothing)", got, codexStarterMaxFires)
	}
}
