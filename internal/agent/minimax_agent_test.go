package agent

import (
	"context"
	"fmt"
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

func setupMiniMaxAgentTest(t *testing.T) (*MiniMaxAgent, *store.Store, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"unauthorized"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"base_resp": {"status_code": 0, "status_msg": "success"},
			"model_remains": [
				{
					"model_name": "MiniMax-M2",
					"start_time": 1771218000000,
					"end_time": 1771236000000,
					"remains_time": 205310,
					"current_interval_total_count": 15000,
					"current_interval_usage_count": 14077
				}
			]
		}`)
	}))
	t.Cleanup(server.Close)

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	logger := slog.Default()
	client := api.NewMiniMaxClient("sk_test_token", logger, api.WithMiniMaxBaseURL(server.URL))
	tr := tracker.NewMiniMaxTracker(s, logger)
	sm := NewSessionManager(s, "minimax", 600*time.Second, logger)

	ag := NewMiniMaxAgent(client, s, tr, 100*time.Millisecond, logger, sm)
	return ag, s, server
}

func TestMiniMaxAgent_SinglePoll(t *testing.T) {
	ag, s, _ := setupMiniMaxAgentTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(250 * time.Millisecond)
	cancel()

	latest, err := s.QueryLatestMiniMax()
	if err != nil {
		t.Fatalf("QueryLatestMiniMax: %v", err)
	}
	if latest == nil {
		t.Fatal("expected snapshot after polling")
	}
	if len(latest.Models) == 0 {
		t.Fatal("expected models in latest snapshot")
	}
}

func TestMiniMaxAgent_PollingCheck(t *testing.T) {
	ag, s, _ := setupMiniMaxAgentTest(t)
	ag.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go ag.Run(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()

	latest, err := s.QueryLatestMiniMax()
	if err != nil {
		t.Fatalf("QueryLatestMiniMax: %v", err)
	}
	if latest != nil {
		t.Fatal("expected no snapshots when polling is disabled")
	}
}

func TestMiniMaxAgent_ContextCancellation(t *testing.T) {
	ag, _, _ := setupMiniMaxAgentTest(t)

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
		t.Fatal("agent did not stop on context cancellation")
	}
}

func TestMiniMaxAgent_SetNotifier(t *testing.T) {
	ag, s, _ := setupMiniMaxAgentTest(t)

	notifier := notify.New(s, slog.Default())
	ag.SetNotifier(notifier)

	if ag.notifier != notifier {
		t.Fatal("expected notifier to be stored on MiniMaxAgent")
	}
}
