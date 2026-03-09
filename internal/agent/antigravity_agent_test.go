package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// antigravityTestResponse returns a valid Antigravity quota response JSON.
func antigravityTestResponse() string {
	resetTime := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	resp := map[string]interface{}{
		"userStatus": map[string]interface{}{
			"email": "test@example.com",
			"planStatus": map[string]interface{}{
				"availablePromptCredits": 500,
				"planInfo": map[string]interface{}{
					"planName":             "Pro",
					"monthlyPromptCredits": 1000,
				},
			},
			"cascadeModelConfigData": map[string]interface{}{
				"clientModelConfigs": []map[string]interface{}{
					{
						"label":        "Claude Sonnet 4.5",
						"modelOrAlias": map[string]string{"model": "claude-4-5-sonnet"},
						"quotaInfo": map[string]interface{}{
							"remainingFraction": 0.75,
							"resetTime":         resetTime,
						},
					},
					{
						"label":        "GPT 5",
						"modelOrAlias": map[string]string{"model": "gpt-5"},
						"quotaInfo": map[string]interface{}{
							"remainingFraction": 0.50,
							"resetTime":         resetTime,
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

func setupAntigravityTest(t *testing.T) (*AntigravityAgent, *store.Store, *httptest.Server) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(antigravityTestResponse()))
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	conn := &api.AntigravityConnection{
		BaseURL:   server.URL,
		CSRFToken: "test-csrf",
		Protocol:  "http",
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAntigravityClient(logger, api.WithAntigravityConnection(conn))
	tr := tracker.NewAntigravityTracker(st, logger)
	sm := NewSessionManager(st, "antigravity", 600*time.Second, logger)
	ag := NewAntigravityAgent(client, st, tr, 100*time.Millisecond, logger, sm)

	return ag, st, server
}

func TestAntigravityAgent_SinglePoll(t *testing.T) {
	ag, st, _ := setupAntigravityTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(700 * time.Millisecond)
	cancel()

	latest, err := st.QueryLatestAntigravity()
	if err != nil {
		t.Fatalf("QueryLatestAntigravity: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after poll")
	}
	if latest.Email != "test@example.com" {
		t.Errorf("Email = %q, want test@example.com", latest.Email)
	}
	if len(latest.Models) < 2 {
		t.Errorf("expected at least 2 models, got %d", len(latest.Models))
	}
}

func TestAntigravityAgent_PollingCheck_DisablesPolling(t *testing.T) {
	ag, st, _ := setupAntigravityTest(t)
	ag.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(500 * time.Millisecond)
	cancel()

	latest, err := st.QueryLatestAntigravity()
	if err != nil {
		t.Fatalf("QueryLatestAntigravity: %v", err)
	}
	if latest != nil {
		t.Error("expected no snapshot when polling disabled")
	}
}

func TestAntigravityAgent_ContextCancellation(t *testing.T) {
	ag, _, _ := setupAntigravityTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ag.Run(ctx)
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop within timeout")
	}
}

func TestAntigravityAgent_IsConnected(t *testing.T) {
	ag, _, _ := setupAntigravityTest(t)
	if !ag.IsConnected() {
		t.Error("expected IsConnected=true with pre-configured connection")
	}
}

func TestAntigravityAgent_SetNotifier(t *testing.T) {
	ag, st, _ := setupAntigravityTest(t)

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	notifier := notify.New(st, logger)
	ag.SetNotifier(notifier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ag.Run(ctx)
	}()

	foundSnapshot := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		latest, _ := st.QueryLatestAntigravity()
		if latest != nil {
			foundSnapshot = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error on shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop after cancellation")
	}

	if !foundSnapshot {
		t.Fatal("expected snapshot after poll with notifier set")
	}
}

func TestAntigravityAgent_WithManualConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(antigravityTestResponse()))
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	// Create a client without pre-configured connection
	client := api.NewAntigravityClient(logger)
	tr := tracker.NewAntigravityTracker(st, logger)

	// Use manual config option
	ag := NewAntigravityAgent(client, st, tr, 100*time.Millisecond, logger, nil,
		WithAntigravityManualConfig(server.URL, "manual-csrf"))

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(700 * time.Millisecond)
	cancel()

	latest, _ := st.QueryLatestAntigravity()
	if latest == nil {
		t.Fatal("expected snapshot with manual config")
	}
}

func TestAntigravityAgent_NilLogger(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	client := api.NewAntigravityClient(nil)
	tr := tracker.NewAntigravityTracker(st, nil)

	// nil logger should default to slog.Default()
	ag := NewAntigravityAgent(client, st, tr, 100*time.Millisecond, nil, nil)
	if ag.logger == nil {
		t.Fatal("logger should not be nil after construction")
	}
}

func TestAntigravityAgent_APIError_LogsAndContinues(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Return unauthenticated (does not reset connection in FetchQuotas)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "User not authenticated",
			"code":    "UNAUTHENTICATED",
		})
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	conn := &api.AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAntigravityClient(logger, api.WithAntigravityConnection(conn))
	tr := tracker.NewAntigravityTracker(st, logger)

	ag := NewAntigravityAgent(client, st, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ag.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop")
	}

	// Agent should have tried polling multiple times despite auth errors
	if count := callCount.Load(); count < 2 {
		t.Errorf("expected at least 2 calls (continuing after errors), got %d", count)
	}

	// Logs should contain error messages
	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Antigravity")) {
		t.Error("expected log output mentioning Antigravity error")
	}
}

func TestGetAntigravityConfigFromEnv(t *testing.T) {
	t.Setenv("ANTIGRAVITY_BASE_URL", "https://host.docker.internal:42100")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "env-csrf-token")

	baseURL, csrfToken := GetAntigravityConfigFromEnv()
	if baseURL != "https://host.docker.internal:42100" {
		t.Errorf("baseURL = %q, want https://host.docker.internal:42100", baseURL)
	}
	if csrfToken != "env-csrf-token" {
		t.Errorf("csrfToken = %q, want env-csrf-token", csrfToken)
	}
}

func TestGetAntigravityConfigFromEnv_Empty(t *testing.T) {
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "")

	baseURL, csrfToken := GetAntigravityConfigFromEnv()
	if baseURL != "" {
		t.Errorf("baseURL = %q, want empty", baseURL)
	}
	if csrfToken != "" {
		t.Errorf("csrfToken = %q, want empty", csrfToken)
	}
}

func TestHasAntigravityEnvConfig_True(t *testing.T) {
	t.Setenv("ANTIGRAVITY_BASE_URL", "https://host.docker.internal:42100")
	if !HasAntigravityEnvConfig() {
		t.Error("expected HasAntigravityEnvConfig=true")
	}
}

func TestHasAntigravityEnvConfig_False(t *testing.T) {
	t.Setenv("ANTIGRAVITY_BASE_URL", "")
	if HasAntigravityEnvConfig() {
		t.Error("expected HasAntigravityEnvConfig=false")
	}
}

func TestAntigravityAgent_EnvVarConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(antigravityTestResponse()))
	}))
	defer server.Close()

	t.Setenv("ANTIGRAVITY_BASE_URL", server.URL)
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "env-csrf")

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAntigravityClient(logger)
	tr := tracker.NewAntigravityTracker(st, logger)

	// Env vars should be picked up by NewAntigravityAgent
	ag := NewAntigravityAgent(client, st, tr, 100*time.Millisecond, logger, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(700 * time.Millisecond)
	cancel()

	latest, _ := st.QueryLatestAntigravity()
	if latest == nil {
		t.Fatal("expected snapshot with env var config")
	}
}
