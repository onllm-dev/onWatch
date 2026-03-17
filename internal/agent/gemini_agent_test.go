package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func newTestGeminiStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGeminiAgent_Poll(t *testing.T) {
	quotaResp := api.GeminiQuotaResponse{
		Quotas: []api.GeminiQuotaBucket{
			{RemainingFraction: 0.993, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-flash"},
			{RemainingFraction: 1.0, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-pro"},
		},
	}

	tierResp := api.GeminiTierResponse{
		Tier:                    "standard",
		CloudAICompanionProject: "gen-lang-client-12345",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:retrieveUserQuota":
			json.NewEncoder(w).Encode(quotaResp)
		case "/v1internal:loadCodeAssist":
			json.NewEncoder(w).Encode(tierResp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	st := newTestGeminiStore(t)
	tr := tracker.NewGeminiTracker(st, nil)
	client := api.NewGeminiClient("test-token", nil, api.WithGeminiBaseURL(srv.URL))
	sm := NewSessionManager(st, "gemini", 10*time.Minute, nil)

	agent := NewGeminiAgent(client, st, tr, 1*time.Second, nil, sm)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := agent.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Verify data was stored
	latest, err := st.QueryLatestGemini()
	if err != nil {
		t.Fatalf("QueryLatestGemini() error = %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest snapshot")
	}
	if len(latest.Quotas) != 2 {
		t.Errorf("expected 2 quotas, got %d", len(latest.Quotas))
	}
}

func TestGeminiAgent_AuthFailurePause(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/v1internal:loadCodeAssist" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	st := newTestGeminiStore(t)
	tr := tracker.NewGeminiTracker(st, nil)
	client := api.NewGeminiClient("bad-token", nil, api.WithGeminiBaseURL(srv.URL))

	agent := NewGeminiAgent(client, st, tr, 500*time.Millisecond, nil, nil)
	// No creds refresh = can't retry on auth failure, just logs and returns
	agent.SetPollingCheck(func() bool { return true })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = agent.Run(ctx)

	// Should have attempted multiple polls
	if callCount == 0 {
		t.Error("expected at least 1 API call")
	}
}

func TestGeminiAgent_SetNotifier(t *testing.T) {
	agent := NewGeminiAgent(nil, nil, nil, time.Second, nil, nil)
	// Should not panic
	agent.SetNotifier(nil)
	agent.SetPollingCheck(func() bool { return false })
}
