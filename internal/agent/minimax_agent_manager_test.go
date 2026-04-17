package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func setupMiniMaxManagerTest(t *testing.T) (*store.Store, *tracker.MiniMaxTracker, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	tr := tracker.NewMiniMaxTracker(s, nil)
	return s, tr, server
}

func createMiniMaxTestAccount(t *testing.T, s *store.Store, name, apiKey, region string) int64 {
	t.Helper()
	acc, err := s.GetOrCreateProviderAccount("minimax", name)
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	meta := map[string]string{"api_key": apiKey, "region": region}
	metaJSON, _ := json.Marshal(meta)
	if err := s.UpdateProviderAccountMetadata(acc.ID, string(metaJSON)); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}
	return acc.ID
}

func TestNewMiniMaxAgentManager_Defaults(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	mgr := NewMiniMaxAgentManager(s, tr, 30*time.Second, nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.region != "global" {
		t.Errorf("expected default region 'global', got %q", mgr.region)
	}
	if mgr.instances == nil {
		t.Error("expected non-nil instances map")
	}
}

func TestMiniMaxAgentManager_SettersWork(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)
	mgr := NewMiniMaxAgentManager(s, tr, 30*time.Second, nil)

	mgr.SetRegion("cn")
	if mgr.region != "cn" {
		t.Errorf("expected region 'cn', got %q", mgr.region)
	}

	mgr.SetPollingCheck(func() bool { return true })
	if mgr.pollingCheck == nil {
		t.Error("expected non-nil pollingCheck")
	}

	mgr.SetAccountPollingCheck(func(id int64) bool { return id > 0 })
	if mgr.accountPollingCheck == nil {
		t.Error("expected non-nil accountPollingCheck")
	}
}

func TestMiniMaxAgentManager_SkipsEmptyAPIKey(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	// Create account without API key
	acc, err := s.GetOrCreateProviderAccount("minimax", "empty")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if err := s.UpdateProviderAccountMetadata(acc.ID, `{"region":"global"}`); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}

	mgr := NewMiniMaxAgentManager(s, tr, 30*time.Second, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	mgr.ctx = ctx
	mgr.cancel = cancel
	defer cancel()

	if err := mgr.loadAndStartAccounts(); err != nil {
		t.Fatalf("loadAndStartAccounts: %v", err)
	}

	running := mgr.GetRunningAccounts()
	if len(running) != 0 {
		t.Errorf("expected 0 running accounts (no API key), got %d", len(running))
	}
}

func TestMiniMaxAgentManager_LoadAndStartAccounts(t *testing.T) {
	t.Parallel()
	s, tr, server := setupMiniMaxManagerTest(t)

	createMiniMaxTestAccount(t, s, "work", "sk_work", "global")
	createMiniMaxTestAccount(t, s, "personal", "sk_personal", "cn")

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())
	mgr.SetRegion("global")

	ctx, cancel := context.WithCancel(context.Background())
	mgr.ctx = ctx
	mgr.cancel = cancel
	defer cancel()

	// Override base URL by setting region to test server
	_ = server // agents will fail to reach test server but that's OK - we're testing loading

	if err := mgr.loadAndStartAccounts(); err != nil {
		t.Fatalf("loadAndStartAccounts: %v", err)
	}

	running := mgr.GetRunningAccounts()
	if len(running) < 2 {
		t.Errorf("expected at least 2 running accounts, got %d", len(running))
	}

	// Cleanup
	mgr.stopAllAgents()
}

func TestMiniMaxAgentManager_RunAndStop(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	createMiniMaxTestAccount(t, s, "test", "sk_test", "global")

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- mgr.Run(ctx)
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	running := mgr.GetRunningAccounts()
	if len(running) == 0 {
		t.Error("expected at least 1 running account after Run()")
	}

	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestMiniMaxAgentManager_Reload(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	createMiniMaxTestAccount(t, s, "initial", "sk_initial", "global")

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	// Add a second account and reload
	createMiniMaxTestAccount(t, s, "added", "sk_added", "cn")
	mgr.Reload()
	time.Sleep(100 * time.Millisecond)

	running := mgr.GetRunningAccounts()
	if len(running) < 2 {
		t.Errorf("expected 2 running accounts after reload, got %d", len(running))
	}
}

func TestMiniMaxAgentManager_ReloadSkipsWhenContextDone(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())
	// ctx is nil - Reload should be a no-op
	mgr.Reload()
	// No panic = pass
}

func TestMiniMaxAgentManager_PerAccountPollingCheck(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	id1 := createMiniMaxTestAccount(t, s, "enabled", "sk_enabled", "global")
	_ = createMiniMaxTestAccount(t, s, "disabled", "sk_disabled", "global")

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())
	mgr.SetPollingCheck(func() bool { return true })
	mgr.SetAccountPollingCheck(func(accountID int64) bool {
		return accountID == id1
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	// Both agents should be running (polling check only affects poll(), not start)
	running := mgr.GetRunningAccounts()
	if len(running) < 2 {
		t.Errorf("expected 2 running accounts, got %d", len(running))
	}
}

func TestMiniMaxAgentManager_StopAllWaitsForGoroutines(t *testing.T) {
	t.Parallel()
	s, tr, _ := setupMiniMaxManagerTest(t)

	createMiniMaxTestAccount(t, s, "test", "sk_test", "global")

	mgr := NewMiniMaxAgentManager(s, tr, 5*time.Second, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	// stopAllAgents should wait for goroutines and not panic
	mgr.stopAllAgents()

	running := mgr.GetRunningAccounts()
	if len(running) != 0 {
		t.Errorf("expected 0 running accounts after stopAll, got %d", len(running))
	}
}

func TestMinimaxBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		region string
		want   string
	}{
		{"global", "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"},
		{"cn", "https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains"},
		{"", "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"},
		{"unknown", "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"},
	}
	for _, tt := range tests {
		got := minimaxBaseURL(tt.region)
		if got != tt.want {
			t.Errorf("minimaxBaseURL(%q) = %q, want %q", tt.region, got, tt.want)
		}
	}
}

func TestParseMinimaxAccountMeta(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw    string
		key    string
		region string
	}{
		{`{"api_key":"sk_test","region":"cn"}`, "sk_test", "cn"},
		{`{"api_key":"sk_test"}`, "sk_test", ""},
		{`{}`, "", ""},
		{``, "", ""},
		{`invalid-json`, "", ""},
	}
	for _, tt := range tests {
		meta := parseMinimaxAccountMeta(tt.raw)
		if meta.APIKey != tt.key {
			t.Errorf("parseMinimaxAccountMeta(%q).APIKey = %q, want %q", tt.raw, meta.APIKey, tt.key)
		}
		if meta.Region != tt.region {
			t.Errorf("parseMinimaxAccountMeta(%q).Region = %q, want %q", tt.raw, meta.Region, tt.region)
		}
	}
}
