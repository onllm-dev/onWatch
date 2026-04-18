package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func newTestCursorDeps(t *testing.T) (*store.Store, *tracker.CursorTracker, *SessionManager) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	tr := tracker.NewCursorTracker(s, slog.Default())
	sm := NewSessionManager(s, "cursor", 5*time.Minute, slog.Default())
	return s, tr, sm
}

func TestNewCursorAgent(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	if agent == nil {
		t.Fatal("NewCursorAgent returned nil")
	}
}

func TestCursorAgent_SetTokenRefresh(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetTokenRefresh(func() string {
		return "refreshed_token"
	})
}

func TestCursorAgent_SetNotifier(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetNotifier(nil) // Should not panic
}

func TestCursorAgent_SetPollingCheck(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetPollingCheck(func() bool { return true })
}

func TestCursorAgent_PollWithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			w.Write([]byte(`{
				"billingCycleStart": "1768399334000",
				"billingCycleEnd": "1771077734000",
				"planUsage": {
					"totalSpend": 5000,
					"remaining": 35000,
					"limit": 40000,
					"totalPercentUsed": 12.5,
					"autoPercentUsed": 3.0,
					"apiPercentUsed": 9.5
				},
				"enabled": true
			}`))
		case "/aiserver.v1.DashboardService/GetPlanInfo":
			w.Write([]byte(`{
				"planInfo": {
					"planName": "Pro",
					"includedAmountCents": 2000,
					"price": "$20/mo"
				}
			}`))
		case "/aiserver.v1.DashboardService/GetCreditGrantsBalance":
			w.Write([]byte(`{"hasCreditGrants": false, "totalCents": "0", "usedCents": "0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default(), api.WithCursorBaseURL(server.URL))
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Poll once manually
	agent.poll(ctx)

	// Verify snapshot was stored
	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected snapshot to be stored")
	}
	if latest.AccountType != api.CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", latest.AccountType, api.CursorAccountIndividual)
	}
	if latest.PlanName != "Pro" {
		t.Errorf("PlanName = %q, want %q", latest.PlanName, "Pro")
	}
}

func TestCursorAgent_PollDisabled(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	// Set polling check to return false
	agent.SetPollingCheck(func() bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Poll should be skipped
	agent.poll(ctx)

	// No snapshot should be stored
	latest, err := s.QueryLatestCursor()
	if err != nil {
		t.Fatalf("QueryLatestCursor: %v", err)
	}
	if latest != nil {
		t.Error("Expected nil snapshot when polling is disabled")
	}
}

func TestCursorAgent_SetCredentialsRefresh(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("test_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	agent.SetCredentialsRefresh(func() *api.CursorCredentials {
		return &api.CursorCredentials{
			AccessToken:  "test_access",
			RefreshToken: "test_refresh",
			ExpiresAt:    time.Now().Add(30 * time.Minute),
			ExpiresIn:    30 * time.Minute,
			Source:       "sqlite",
		}
	})
}

func TestCursorAgent_ApplyRefreshedCredentials_SavesTokens(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("expired_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)

	var savedAccess, savedRefresh string
	agent.SetTokenSave(func(accessToken, refreshToken string) error {
		savedAccess = accessToken
		savedRefresh = refreshToken
		return nil
	})

	ok := agent.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "fresh_access_token",
		RefreshToken: "fresh_refresh_token",
	})
	if !ok {
		t.Fatal("applyRefreshedCredentials returned false")
	}
	if savedAccess != "fresh_access_token" {
		t.Fatalf("saved access token = %q, want fresh_access_token", savedAccess)
	}
	if savedRefresh != "fresh_refresh_token" {
		t.Fatalf("saved refresh token = %q, want fresh_refresh_token", savedRefresh)
	}
	if got := client.GetToken(); got != "fresh_access_token" {
		t.Fatalf("client token = %q, want fresh_access_token", got)
	}
	if agent.lastToken != "fresh_access_token" {
		t.Fatalf("lastToken = %q, want fresh_access_token", agent.lastToken)
	}
}

func TestCursorAgent_ApplyRefreshedCredentials_FailsWhenSaveFails(t *testing.T) {
	s, tr, sm := newTestCursorDeps(t)
	client := api.NewCursorClient("expired_token", slog.Default())
	agent := NewCursorAgent(client, s, tr, 30*time.Second, slog.Default(), sm)
	agent.lastFailedToken = "expired_token"

	agent.SetTokenSave(func(accessToken, refreshToken string) error {
		return errors.New("save failed")
	})

	ok := agent.applyRefreshedCredentials(&api.CursorOAuthResponse{
		AccessToken:  "fresh_access_token",
		RefreshToken: "fresh_refresh_token",
	})
	if ok {
		t.Fatal("applyRefreshedCredentials returned true despite save failure")
	}
	if got := client.GetToken(); got != "expired_token" {
		t.Fatalf("client token = %q, want expired_token", got)
	}
	if agent.lastToken != "" {
		t.Fatalf("lastToken = %q, want empty", agent.lastToken)
	}
	if agent.lastFailedToken != "expired_token" {
		t.Fatalf("lastFailedToken = %q, want expired_token", agent.lastFailedToken)
	}
}
