package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func waitForAgentStop(t *testing.T, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil agent error, got %v", err)
		}
	case <-time.After(timeout):
		t.Fatalf("agent did not stop within %v", timeout)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", description)
}

func TestConstructors_DefaultLogger_WhenNil(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	baseLogger := slog.Default()

	syntheticClient := api.NewClient("test-key", baseLogger)
	syntheticTracker := tracker.New(str, baseLogger)
	if ag := New(syntheticClient, str, syntheticTracker, time.Second, nil, nil); ag.logger == nil {
		t.Fatal("expected Agent logger to default when nil")
	}

	anthropicClient := api.NewAnthropicClient("test-token", baseLogger)
	anthropicTracker := tracker.NewAnthropicTracker(str, baseLogger)
	if ag := NewAnthropicAgent(anthropicClient, str, anthropicTracker, time.Second, nil, nil); ag.logger == nil {
		t.Fatal("expected AnthropicAgent logger to default when nil")
	}

	copilotClient := api.NewCopilotClient("test-token", baseLogger)
	copilotTracker := tracker.NewCopilotTracker(str, baseLogger)
	if ag := NewCopilotAgent(copilotClient, str, copilotTracker, time.Second, nil, nil); ag.logger == nil {
		t.Fatal("expected CopilotAgent logger to default when nil")
	}

	codexClient := api.NewCodexClient("test-token", baseLogger)
	codexTracker := tracker.NewCodexTracker(str, baseLogger)
	if ag := NewCodexAgent(codexClient, str, codexTracker, time.Second, nil, nil); ag.logger == nil {
		t.Fatal("expected CodexAgent logger to default when nil")
	}

	zaiClient := api.NewZaiClient("test-key", baseLogger)
	zaiTracker := tracker.NewZaiTracker(str, baseLogger)
	if ag := NewZaiAgent(zaiClient, str, zaiTracker, time.Second, nil, nil); ag.logger == nil {
		t.Fatal("expected ZaiAgent logger to default when nil")
	}
}

func TestAgent_SetPollingCheck_DisablesPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()
	<-ctx.Done()
	waitForAgentStop(t, errCh, 2*time.Second)

	// No snapshots should be stored when polling is disabled
	latest, _ := str.QueryLatest()
	if latest != nil {
		t.Logf("Agent logs: %s", buf.String())
		t.Error("expected no snapshot when polling disabled via SetPollingCheck")
	}
}

func TestAgent_SetNotifier_NotifierCalledDuringPoll(t *testing.T) {
	requestReceived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestReceived <- struct{}{}:
		default:
		}
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	// Use test logger that logs to t.Log for visibility in CI failures
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	// Set a real notification engine
	notifier := notify.New(str, logger)
	agent.SetNotifier(notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for at least one request to be received by the server (confirms agent is running).
	var requestTimeout bool
	select {
	case <-requestReceived:
		t.Log("Server received HTTP request from agent")
	case <-time.After(3 * time.Second):
		requestTimeout = true
	}

	// Now wait for snapshot to be stored (should be quick after request).
	// Don't use waitUntil since it doesn't print logs on failure.
	snapshotDeadline := time.Now().Add(2 * time.Second)
	var snapshotFound bool
	for time.Now().Before(snapshotDeadline) {
		latest, err := str.QueryLatest()
		if err != nil {
			t.Logf("QueryLatest error: %v", err)
		}
		if latest != nil {
			snapshotFound = true
			t.Log("Snapshot found in store")
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	waitForAgentStop(t, errCh, 3*time.Second)
	logs := logBuf.String()

	if requestTimeout {
		t.Logf("Agent logs:\n%s", logs)
		t.Fatal("timeout waiting for agent to make HTTP request")
	}

	if !snapshotFound {
		t.Logf("Agent logs:\n%s", logs)
		t.Fatal("timeout waiting for general snapshot to be stored")
	}

	// Verify poll completed with notifier (no panics)
	latest, _ := str.QueryLatest()
	if latest == nil {
		t.Logf("Agent logs:\n%s", logs)
		t.Error("expected snapshot after poll with notifier set")
	}
}

func TestAnthropicAgent_SetPollingCheck_DisablesPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()
	<-ctx.Done()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := str.QueryLatestAnthropic()
	if latest != nil {
		t.Error("expected no Anthropic snapshot when polling disabled")
	}
}

func TestAnthropicAgent_SetNotifier_NotifierCalledDuringPoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(85.0, 30.0)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	notifier := notify.New(str, logger)
	agent.SetNotifier(notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	waitUntil(t, 2*time.Second, func() bool {
		latest, _ := str.QueryLatestAnthropic()
		return latest != nil
	}, "anthropic snapshot to be stored")
	cancel()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Fatal("expected Anthropic snapshot after poll with notifier set")
	}
}

func TestAnthropicAgent_SetCredentialsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// Set a credentials refresh function that returns non-expiring credentials
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh",
			ExpiresIn:    2 * time.Hour, // Not expiring soon
		}
	})

	// Run a single immediate poll
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	waitUntil(t, 2*time.Second, func() bool {
		latest, _ := str.QueryLatestAnthropic()
		return latest != nil
	}, "anthropic snapshot with credentials refresh to be stored")
	cancel()
	waitForAgentStop(t, errCh, 2*time.Second)

	// Verify poll completed
	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Fatal("expected Anthropic snapshot with credentials refresh set")
	}
}

func TestCopilotAgent_SetNotifier_NotifierCalledDuringPoll(t *testing.T) {
	ag, st, _ := setupCopilotTest(t)

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	notifier := notify.New(st, logger)
	ag.SetNotifier(notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ag.Run(ctx)
	}()
	waitUntil(t, 2*time.Second, func() bool {
		latest, _ := st.QueryLatestCopilot()
		return latest != nil
	}, "copilot snapshot to be stored")
	cancel()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := st.QueryLatestCopilot()
	if latest == nil {
		t.Fatal("expected Copilot snapshot after poll with notifier set")
	}
}

func TestCodexAgent_SetNotifier_NotifierCalledDuringPoll(t *testing.T) {
	ag, st, _ := setupCodexTest(t)

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	notifier := notify.New(st, logger)
	ag.SetNotifier(notifier)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ag.Run(ctx)
	}()
	waitUntil(t, 2*time.Second, func() bool {
		latest, _ := st.QueryLatestCodex(store.DefaultCodexAccountID)
		return latest != nil
	}, "codex snapshot to be stored")
	cancel()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := st.QueryLatestCodex(store.DefaultCodexAccountID)
	if latest == nil {
		t.Fatal("expected Codex snapshot after poll with notifier set")
	}
}

func TestZaiAgent_SetPollingCheck_DisablesPolling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(zaiResponse(200000000, 50000000, 1000, 19)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewZaiClient("test-key", logger, api.WithZaiBaseURL(server.URL+"/monitor/usage/quota/limit"))
	tr := tracker.NewZaiTracker(str, logger)

	agent := NewZaiAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()
	<-ctx.Done()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := str.QueryLatestZai()
	if latest != nil {
		t.Error("expected no Z.ai snapshot when polling disabled")
	}
}

func TestCodexAgent_SetCredentialsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":45.2,"reset_at":1766000000,"limit_window_seconds":18000}}}`))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewCodexClient("test-token", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(str, logger)

	agent := NewCodexAgent(client, str, tr, 5*time.Second, logger, nil)

	// Set a credentials refresh function that returns non-expiring credentials
	agent.SetCredentialsRefresh(func() *api.CodexCredentials {
		return &api.CodexCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh",
			ExpiresIn:    2 * time.Hour, // Not expiring soon
			ExpiresAt:    time.Now().Add(2 * time.Hour),
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()
	<-ctx.Done()
	waitForAgentStop(t, errCh, 2*time.Second)

	latest, _ := str.QueryLatestCodex(1)
	if latest == nil {
		t.Fatal("expected Codex snapshot after poll")
	}
}
