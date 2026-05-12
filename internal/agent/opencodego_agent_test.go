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
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestOpenCodeGoAgent_PausesAfterRepeatedAuthFailures(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.New(slog.DiscardHandler)
	client := api.NewOpenCodeGoClient("auth=bad", logger, api.WithOpenCodeGoBaseURL(server.URL), api.WithOpenCodeGoWorkspaceID("wrk_test"))

	tr := tracker.NewOpenCodeGoTracker(st, logger)
	ag := NewOpenCodeGoAgent(client, st, tr, time.Hour, logger, nil)

	ctx := context.Background()
	for i := 0; i < maxOpenCodeGoAuthFailures; i++ {
		ag.poll(ctx)
	}

	if !ag.authPaused {
		t.Fatal("expected authPaused=true")
	}
	if ag.authFailCount < maxOpenCodeGoAuthFailures {
		t.Fatalf("authFailCount=%d", ag.authFailCount)
	}
}

func TestOpenCodeGoAgent_PollSuccessInsertsSnapshot(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/wrk_test/go" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `window.__INITIAL_STATE__ = {rollingUsage:$R[30]={status:"ok",resetInSec:300,usagePercent:35}};`)
	}))
	defer server.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.New(slog.DiscardHandler)
	client := api.NewOpenCodeGoClient("auth=good", logger, api.WithOpenCodeGoBaseURL(server.URL), api.WithOpenCodeGoWorkspaceID("wrk_test"))
	tr := tracker.NewOpenCodeGoTracker(st, logger)
	ag := NewOpenCodeGoAgent(client, st, tr, time.Hour, logger, nil)

	ag.poll(context.Background())

	latest, err := st.QueryLatestOpenCodeGo()
	if err != nil {
		t.Fatalf("QueryLatestOpenCodeGo: %v", err)
	}
	if latest == nil || len(latest.Windows) != 1 {
		t.Fatal("expected one inserted OpenCode Go snapshot")
	}
}
