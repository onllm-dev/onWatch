package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// codexResetCreditsServer serves usage with a configurable available_count and
// counts detail requests separately, so cadence tests can assert exactly when
// the slow detail fetch runs.
type codexResetCreditsServer struct {
	server        *httptest.Server
	usageCalls    atomic.Int32
	detailCalls   atomic.Int32
	usageCount    atomic.Int64 // available_count on the usage response; -1 omits the field
	detailStatus  atomic.Int32
	detailPayload atomic.Value // string
	mu            sync.Mutex
	detailPaths   []string
}

func newCodexResetCreditsServer(t *testing.T) *codexResetCreditsServer {
	t.Helper()
	cs := &codexResetCreditsServer{}
	cs.usageCount.Store(-1)
	cs.detailStatus.Store(http.StatusOK)
	cs.detailPayload.Store(`{"credits":[],"available_count":0,"total_earned_count":0}`)
	cs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read-only guard: the agent may only GET the two reset-credit paths.
		// Any other method or path fails the test, so URL building cannot route
		// around it.
		if r.Method != http.MethodGet {
			t.Errorf("codex agent used a non-GET method: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			cs.usageCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			count := cs.usageCount.Load()
			extra := ""
			if count >= 0 {
				extra = fmt.Sprintf(`,"rate_limit_reset_credits":{"available_count":%d}`, count)
			}
			fmt.Fprintf(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}%s}`, extra)
		case "/backend-api/wham/rate-limit-reset-credits":
			cs.detailCalls.Add(1)
			cs.mu.Lock()
			cs.detailPaths = append(cs.detailPaths, r.URL.Path)
			cs.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(int(cs.detailStatus.Load()))
			fmt.Fprint(w, cs.detailPayload.Load().(string))
		default:
			t.Errorf("codex agent requested an unexpected path: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cs.server.Close)
	return cs
}

func (cs *codexResetCreditsServer) agent(t *testing.T, interval time.Duration) (*CodexAgent, *store.Store) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return cs.agentWithStore(t, st, interval), st
}

// agentWithStore builds an agent over an existing store, so a test can model a
// daemon restart against the same persisted state.
func (cs *codexResetCreditsServer) agentWithStore(t *testing.T, st *store.Store, interval time.Duration) *CodexAgent {
	t.Helper()
	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger,
		api.WithCodexBaseURL(cs.server.URL+"/backend-api/wham/usage"))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, interval, logger, nil)
	ag.SetCodexAccountID("acct-test")
	return ag
}

func TestCodexAgent_ResetCredits_FetchOnFirstPollThenSlowCadence(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(2)
	cs.detailPayload.Store(`{
	  "credits":[{"id":"credit-1","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-10-01T00:00:00Z","expires_at":null}],
	  "available_count":1,"total_earned_count":1
	}`)
	ag, st := cs.agent(t, time.Minute)

	// A manual clock keeps the cadence deterministic.
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ag.now = func() time.Time { return now }

	ctx := context.Background()
	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls after first poll = %d, want 1", got)
	}

	// Subsequent usage polls inside the slow window must not fetch details.
	for i := 0; i < 3; i++ {
		now = now.Add(5 * time.Minute)
		ag.poll(ctx)
	}
	if got := cs.usageCalls.Load(); got != 4 {
		t.Fatalf("usage calls = %d, want 4", got)
	}
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls inside the slow window = %d, want 1", got)
	}

	// Past the slow cadence, one more fetch runs.
	now = now.Add(codexResetCreditsFetchInterval)
	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 2 {
		t.Fatalf("detail calls after the slow window = %d, want 2", got)
	}

	rows, err := st.QueryCodexResetCredits(DefaultCodexAccountIDForTest())
	if err != nil {
		t.Fatalf("QueryCodexResetCredits: %v", err)
	}
	if len(rows) != 1 || rows[0].CreditID != "credit-1" {
		t.Fatalf("stored rows = %#v", rows)
	}
}

func TestCodexAgent_ResetCredits_CountChangeTriggersImmediateFetch(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(1)
	ag, _ := cs.agent(t, time.Minute)

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ag.now = func() time.Time { return now }
	ctx := context.Background()

	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls = %d, want 1", got)
	}

	// No change and inside the interval: no fetch.
	now = now.Add(time.Minute)
	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls after unchanged count = %d, want 1", got)
	}

	// The usage count changes: fetch immediately even though the interval has
	// not elapsed.
	cs.usageCount.Store(2)
	now = now.Add(time.Minute)
	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 2 {
		t.Fatalf("detail calls after count change = %d, want 2", got)
	}
}

func TestCodexAgent_ResetCredits_UnknownCountDoesNotTriggerFetch(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	// The usage response omits rate_limit_reset_credits entirely.
	ag, _ := cs.agent(t, time.Minute)

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ag.now = func() time.Time { return now }
	ctx := context.Background()

	ag.poll(ctx)
	// First poll always fetches once so the account has a baseline.
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls = %d, want 1", got)
	}
	now = now.Add(time.Minute)
	ag.poll(ctx)
	if got := cs.detailCalls.Load(); got != 1 {
		t.Fatalf("detail calls with unknown count = %d, want 1", got)
	}
}

func TestCodexAgent_ResetCredits_FailureDoesNotAffectUsageOrPause(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(3)
	cs.detailStatus.Store(http.StatusUnauthorized)
	ag, st := cs.agent(t, time.Minute)

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ag.now = func() time.Time { return now }

	ag.poll(context.Background())

	if ag.authFailCount != 0 || ag.AuthPaused() {
		t.Fatalf("authFailCount=%d paused=%v, want no effect from a detail 401", ag.authFailCount, ag.AuthPaused())
	}
	latest, err := st.QueryLatestCodex(DefaultCodexAccountIDForTest())
	if err != nil || latest == nil {
		t.Fatalf("latest snapshot = %#v, err = %v", latest, err)
	}

	status, err := st.QueryCodexResetCreditCheckStatus(DefaultCodexAccountIDForTest())
	if err != nil {
		t.Fatalf("QueryCodexResetCreditCheckStatus: %v", err)
	}
	if status == nil || status.Status != "unavailable" {
		t.Fatalf("check status = %#v, want unavailable", status)
	}
}

func TestCodexAgent_ResetCredits_NotifiesOnceInsideLeadWindow(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(1)

	// Use the wall clock: the detail parser sanity-checks expiries against it.
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(3 * time.Hour).Format(time.RFC3339)
	cs.detailPayload.Store(fmt.Sprintf(`{
	  "credits":[
	    {"id":"credit-soon","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-10-01T00:00:00Z","expires_at":%q},
	    {"id":"credit-never","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-10-01T00:00:00Z","expires_at":null}
	  ],
	  "available_count":2,"total_earned_count":2
	}`, expiry))

	ag, st := cs.agent(t, time.Minute)
	ag.now = func() time.Time { return now }
	ag.SetProfileLabel("work-profile")

	webhookCalls := make(chan notify.WebhookPayload, 4)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload notify.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			webhookCalls <- payload
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookSrv.Close()

	engine := notify.New(st, slog.Default())
	if err := st.SetSetting("webhook", fmt.Sprintf(`{"url":%q,"timeout_seconds":2,"events":{"reset_credit_expiry":true}}`, webhookSrv.URL)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// Webhook delivery is off by default; enable the channel the way the
	// settings UI does.
	if err := st.SetSetting("notifications", `{"channels":{"email":false,"push":false,"webhook":true}}`); err != nil {
		t.Fatalf("SetSetting notifications: %v", err)
	}
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook: %v", err)
	}
	ag.SetNotifier(engine)

	ctx := context.Background()
	ag.poll(ctx)

	select {
	case payload := <-webhookCalls:
		if payload.Event != notify.EventResetCreditExpiry {
			t.Fatalf("event = %q, want %q", payload.Event, notify.EventResetCreditExpiry)
		}
		if payload.AccountID != "1" {
			t.Fatalf("account id = %q, want the database account ID", payload.AccountID)
		}
		if payload.Message == "" || payload.Title == "" {
			t.Fatalf("payload missing text: %#v", payload)
		}
		if !strings.Contains(payload.Message, "work-profile") {
			t.Fatalf("message %q does not name the profile label", payload.Message)
		}
		if !strings.Contains(payload.Message, "/usage") {
			t.Fatalf("message %q does not mention the Codex CLI /usage entry point", payload.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected one reset-credit expiry notification")
	}

	// A never-expiring credit is never notified.
	now = now.Add(30 * time.Minute)
	ag.poll(ctx)
	select {
	case payload := <-webhookCalls:
		t.Fatalf("unexpected second notification: %#v", payload)
	case <-time.After(200 * time.Millisecond):
	}

	rows, err := st.QueryCodexResetCredits(DefaultCodexAccountIDForTest())
	if err != nil {
		t.Fatalf("QueryCodexResetCredits: %v", err)
	}
	for _, row := range rows {
		switch row.CreditID {
		case "credit-soon":
			if row.NotifiedAt == nil {
				t.Fatalf("credit-soon NotifiedAt = nil, want a marker")
			}
		case "credit-never":
			if row.NotifiedAt != nil {
				t.Fatalf("credit-never NotifiedAt = %v, want nil", row.NotifiedAt)
			}
		}
	}

	// A fresh agent (restart) must not re-send: the marker is persisted.
	ag2 := cs.agentWithStore(t, st, time.Minute)
	ag2.now = func() time.Time { return now }
	ag2.SetNotifier(engine)
	now = now.Add(codexResetCreditsFetchInterval)
	ag2.poll(ctx)
	select {
	case payload := <-webhookCalls:
		t.Fatalf("unexpected notification after restart: %#v", payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// DefaultCodexAccountIDForTest mirrors store.DefaultCodexAccountID.
func DefaultCodexAccountIDForTest() int64 {
	return store.DefaultCodexAccountID
}

// TestNoConsumeCodePath asserts onWatch only reads reset credits: no onWatch
// source file may reference the redemption endpoint or client call.
func TestNoConsumeCodePath(t *testing.T) {
	assertNoConsumeReferences(t)
}

// The real backend has returned an available credit whose expires_at equals
// granted_at to the microsecond. It must not be stored as expiring, shown with
// a past expiry, or notified.
func TestCodexAgent_ResetCredits_EqualGrantAndExpiryNotNotified(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(1)

	granted := time.Now().UTC().Truncate(time.Second)
	stamp := granted.Format(time.RFC3339Nano)
	cs.detailPayload.Store(fmt.Sprintf(`{
	  "credits":[{"id":"credit-equal","reset_type":"codex_rate_limits","is_supported_by_plan":true,"status":"available","granted_at":%q,"expires_at":%q}],
	  "available_count":1,"total_earned_count":0
	}`, stamp, stamp))

	ag, st := cs.agent(t, time.Minute)
	ag.now = func() time.Time { return granted }
	ag.SetProfileLabel("work-profile")

	webhookCalls := make(chan notify.WebhookPayload, 4)
	webhookSrv := newResetCreditWebhook(t, webhookCalls)
	engine := newResetCreditNotifier(t, st, webhookSrv.URL)
	ag.SetNotifier(engine)

	ag.poll(context.Background())

	select {
	case payload := <-webhookCalls:
		t.Fatalf("unexpected notification for an equal-grant/expiry credit: %#v", payload)
	case <-time.After(300 * time.Millisecond):
	}

	rows, err := st.QueryCodexResetCredits(DefaultCodexAccountIDForTest())
	if err != nil {
		t.Fatalf("QueryCodexResetCredits: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ExpiresAt != nil || !rows[0].ExpiryUnknown {
		t.Fatalf("row = %#v, want an unknown expiry, not a past one", rows[0])
	}
	if rows[0].NotifiedAt != nil {
		t.Fatalf("NotifiedAt = %v, want nil", rows[0].NotifiedAt)
	}
}

// Credits marked is_supported_by_plan=false must not trigger an expiry warning
// and must not drive next_expires_at.
func TestCodexAgent_ResetCredits_UnsupportedCreditNotNotified(t *testing.T) {
	cs := newCodexResetCreditsServer(t)
	cs.usageCount.Store(1)

	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(2 * time.Hour).Format(time.RFC3339)
	cs.detailPayload.Store(fmt.Sprintf(`{
	  "credits":[
	    {"id":"credit-unsupported","reset_type":"codex_rate_limits","is_supported_by_plan":false,"status":"available","granted_at":"2026-10-01T00:00:00Z","expires_at":%q}
	  ],
	  "available_count":1,"applicable_available_count":0
	}`, expiry))

	ag, st := cs.agent(t, time.Minute)
	ag.now = func() time.Time { return now }
	ag.SetProfileLabel("work-profile")

	webhookCalls := make(chan notify.WebhookPayload, 4)
	webhookSrv := newResetCreditWebhook(t, webhookCalls)
	ag.SetNotifier(newResetCreditNotifier(t, st, webhookSrv.URL))

	ag.poll(context.Background())

	select {
	case payload := <-webhookCalls:
		t.Fatalf("unexpected notification for an unsupported credit: %#v", payload)
	case <-time.After(300 * time.Millisecond):
	}

	check, err := st.QueryCodexResetCreditCheckStatus(DefaultCodexAccountIDForTest())
	if err != nil {
		t.Fatalf("QueryCodexResetCreditCheckStatus: %v", err)
	}
	if check == nil || check.ApplicableCount == nil || *check.ApplicableCount != 0 {
		t.Fatalf("check = %#v, want applicable count 0", check)
	}
}

func newResetCreditWebhook(t *testing.T, calls chan<- notify.WebhookPayload) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload notify.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			select {
			case calls <- payload:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newResetCreditNotifier(t *testing.T, st *store.Store, webhookURL string) *notify.NotificationEngine {
	t.Helper()
	engine := notify.New(st, slog.Default())
	if err := st.SetSetting("webhook", fmt.Sprintf(`{"url":%q,"timeout_seconds":2,"events":{"reset_credit_expiry":true}}`, webhookURL)); err != nil {
		t.Fatalf("SetSetting webhook: %v", err)
	}
	if err := st.SetSetting("notifications", `{"channels":{"email":false,"push":false,"webhook":true}}`); err != nil {
		t.Fatalf("SetSetting notifications: %v", err)
	}
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook: %v", err)
	}
	return engine
}
