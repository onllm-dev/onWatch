package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/internal/api"
	"github.com/onllm-dev/onwatch/internal/store"
	"github.com/onllm-dev/onwatch/internal/tracker"
)

func copilotTestResponse() api.CopilotUserResponse {
	return api.CopilotUserResponse{
		Login:             "testuser",
		CopilotPlan:       "individual_pro",
		QuotaResetDateUTC: "2026-03-01T00:00:00.000Z",
		QuotaSnapshots: map[string]*api.CopilotQuotaSnapshot{
			"premium_interactions": {
				Entitlement:      1500,
				Remaining:        1000,
				PercentRemaining: 66.667,
				Unlimited:        false,
			},
			"chat": {
				Entitlement:      0,
				Remaining:        0,
				PercentRemaining: 100.0,
				Unlimited:        true,
			},
		},
	}
}

func setupCopilotTest(t *testing.T) (*CopilotAgent, *store.Store, *httptest.Server) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if r.Header.Get("Authorization") != "Bearer ghp_test_token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message": "Bad credentials"}`)
			return
		}
		resp := copilotTestResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	logger := slog.Default()
	client := api.NewCopilotClient("ghp_test_token", logger, api.WithCopilotBaseURL(server.URL))
	tr := tracker.NewCopilotTracker(str, logger)
	sm := NewSessionManager(str, "copilot", 600*time.Second, logger)

	ag := NewCopilotAgent(client, str, tr, 100*time.Millisecond, logger, sm)

	return ag, str, server
}

func TestCopilotAgent_SinglePoll(t *testing.T) {
	ag, str, _ := setupCopilotTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)

	// Wait for at least one poll
	time.Sleep(250 * time.Millisecond)
	cancel()

	// Verify a snapshot was stored
	latest, err := str.QueryLatestCopilot()
	if err != nil {
		t.Fatalf("QueryLatestCopilot: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot after poll")
	}
	if latest.CopilotPlan != "individual_pro" {
		t.Errorf("CopilotPlan = %q, want individual_pro", latest.CopilotPlan)
	}
	if len(latest.Quotas) < 2 {
		t.Errorf("Expected at least 2 quotas, got %d", len(latest.Quotas))
	}
}

func TestCopilotAgent_PollingCheck(t *testing.T) {
	ag, str, _ := setupCopilotTest(t)

	// Disable polling
	ag.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()

	// No snapshot should be stored
	latest, err := str.QueryLatestCopilot()
	if err != nil {
		t.Fatalf("QueryLatestCopilot: %v", err)
	}
	if latest != nil {
		t.Error("Expected no snapshot when polling disabled")
	}
}

func TestCopilotAgent_ContextCancellation(t *testing.T) {
	ag, _, _ := setupCopilotTest(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- ag.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Expected nil error on cancel, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Agent did not stop within timeout")
	}
}
