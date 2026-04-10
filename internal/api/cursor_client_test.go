package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewCursorClient(t *testing.T) {
	logger := slog.Default()
	client := NewCursorClient("test_token", logger)
	if client == nil {
		t.Fatal("NewCursorClient returned nil")
	}
	if client.baseURL != CursorBaseURL {
		t.Errorf("baseURL = %q, want %q", client.baseURL, CursorBaseURL)
	}
}

func TestNewCursorClient_WithOptions(t *testing.T) {
	logger := slog.Default()
	client := NewCursorClient("test_token", logger,
		WithCursorBaseURL("http://localhost:1234"),
		WithCursorTimeout(5*time.Second),
	)
	if client.baseURL != "http://localhost:1234" {
		t.Errorf("baseURL = %q, want custom", client.baseURL)
	}
}

func TestCursorClient_SetToken(t *testing.T) {
	logger := slog.Default()
	client := NewCursorClient("initial_token", logger)

	client.SetToken("new_token")
	if client.getToken() != "new_token" {
		t.Errorf("getToken() = %q, want %q", client.getToken(), "new_token")
	}
}

func TestCursorClient_FetchQuotas_IndividualSuccess(t *testing.T) {
	usageHandled := false
	planInfoHandled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aiserver.v1.DashboardService/GetCurrentPeriodUsage" {
			usageHandled = true
			w.Header().Set("Content-Type", "application/json")
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
			return
		}
		if r.URL.Path == "/aiserver.v1.DashboardService/GetPlanInfo" {
			planInfoHandled = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"planInfo": {
					"planName": "Pro",
					"includedAmountCents": 2000,
					"price": "$20/mo"
				}
			}`))
			return
		}
		if r.URL.Path == "/aiserver.v1.DashboardService/GetCreditGrantsBalance" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"hasCreditGrants": true,
				"totalCents": "5000",
				"usedCents": "2000"
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	logger := slog.Default()
	client := NewCursorClient("test_token", logger, WithCursorBaseURL(server.URL))
	snapshot, err := client.FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas: %v", err)
	}

	if !usageHandled {
		t.Error("usage endpoint not called")
	}
	if !planInfoHandled {
		t.Error("plan info endpoint not called")
	}
	if snapshot.AccountType != CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", snapshot.AccountType, CursorAccountIndividual)
	}
	if snapshot.PlanName != "Pro" {
		t.Errorf("PlanName = %q, want %q", snapshot.PlanName, "Pro")
	}
	if len(snapshot.Quotas) == 0 {
		t.Error("Expected at least one quota")
	}
}

func TestCursorClient_FetchQuotas_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	logger := slog.Default()
	client := NewCursorClient("bad_token", logger, WithCursorBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Error("Expected error for 401")
	}
	if !IsCursorAuthError(err) {
		t.Errorf("Expected auth error, got %v", err)
	}
}

func TestCursorClient_FetchQuotas_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.Default()
	client := NewCursorClient("test_token", logger, WithCursorBaseURL(server.URL))
	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Error("Expected error for 500")
	}
}

func TestCursorClient_ConnectRPCHeaders(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"billingCycleStart": "1768399334000",
			"billingCycleEnd": "1771077734000",
			"planUsage": {
				"totalSpend": 5000,
				"remaining": 35000,
				"limit": 40000,
				"totalPercentUsed": 12.5
			},
			"enabled": true
		}`))
	}))
	defer server.Close()

	logger := slog.Default()
	client := NewCursorClient("test_bearer_token", logger, WithCursorBaseURL(server.URL))
	_, _ = client.FetchQuotas(context.Background())

	if receivedHeaders.Get("Authorization") != "Bearer test_bearer_token" {
		t.Errorf("Authorization = %q, want %q", receivedHeaders.Get("Authorization"), "Bearer test_bearer_token")
	}
	if receivedHeaders.Get("Connect-Protocol-Version") != "1" {
		t.Errorf("Connect-Protocol-Version = %q, want %q", receivedHeaders.Get("Connect-Protocol-Version"), "1")
	}
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedHeaders.Get("Content-Type"))
	}
}

func TestCursorClient_RedirectToken(t *testing.T) {
	logger := slog.Default()
	client := NewCursorClient("initial", logger)

	if client.getToken() != "initial" {
		t.Errorf("Initial token = %q, want %q", client.getToken(), "initial")
	}

	client.SetToken("updated")
	if client.getToken() != "updated" {
		t.Errorf("Updated token = %q, want %q", client.getToken(), "updated")
	}
}

func TestRedactCursorToken(t *testing.T) {
	tests := []struct {
		token    string
		expected string
	}{
		{"", "(empty)"},
		{"abc", "***...***"},
		{"eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9", "eyJh***...***CJ9"},
	}

	for _, tt := range tests {
		got := redactCursorToken(tt.token)
		if got != tt.expected {
			t.Errorf("redactCursorToken(%q) = %q, want %q", tt.token, got, tt.expected)
		}
	}
}

func TestRefreshCursorToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "new_access_token",
			"id_token": "new_id_token",
			"refresh_token": "new_refresh_token",
			"shouldLogout": false
		}`))
	}))
	defer server.Close()

	cursorOAuthURL = server.URL
	defer func() { cursorOAuthURL = "https://api2.cursor.sh/oauth/token" }()

	resp, err := RefreshCursorToken(context.Background(), "old_refresh_token")
	if err != nil {
		t.Fatalf("RefreshCursorToken: %v", err)
	}
	if resp.AccessToken != "new_access_token" {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "new_access_token")
	}
	if resp.RefreshToken != "new_refresh_token" {
		t.Errorf("RefreshToken = %q, want %q", resp.RefreshToken, "new_refresh_token")
	}
	if resp.ShouldLogout {
		t.Error("ShouldLogout should be false")
	}
}

func TestRefreshCursorToken_SessionExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "",
			"id_token": "",
			"shouldLogout": true
		}`))
	}))
	defer server.Close()

	cursorOAuthURL = server.URL
	defer func() { cursorOAuthURL = "https://api2.cursor.sh/oauth/token" }()

	_, err := RefreshCursorToken(context.Background(), "expired_refresh_token")
	if err == nil {
		t.Error("Expected error for shouldLogout=true")
	}
	if !IsCursorSessionExpired(err) {
		t.Errorf("Expected ErrCursorSessionExpired, got %v", err)
	}
}

func TestRefreshCursorToken_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer server.Close()

	cursorOAuthURL = server.URL
	defer func() { cursorOAuthURL = "https://api2.cursor.sh/oauth/token" }()

	_, err := RefreshCursorToken(context.Background(), "bad_token")
	if err == nil {
		t.Error("Expected error for HTTP 400")
	}
}

func TestShouldFetchCursorRequestBasedUsage(t *testing.T) {
	tests := []struct {
		name           string
		usage          *CursorUsageResponse
		normalizedPlan string
		want           bool
	}{
		{
			name: "missing plan info still attempts request-based usage",
			usage: &CursorUsageResponse{
				Enabled:   true,
				PlanUsage: &CursorPlanUsage{Limit: 0},
			},
			normalizedPlan: "",
			want:           true,
		},
		{
			name: "team account with zero plan limit attempts request-based usage",
			usage: &CursorUsageResponse{
				Enabled:   true,
				PlanUsage: &CursorPlanUsage{Limit: 0},
			},
			normalizedPlan: "team",
			want:           true,
		},
		{
			name: "standard usage skips request-based endpoint",
			usage: &CursorUsageResponse{
				Enabled:   true,
				PlanUsage: &CursorPlanUsage{Limit: 100},
			},
			normalizedPlan: "",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFetchCursorRequestBasedUsage(tt.usage, tt.normalizedPlan); got != tt.want {
				t.Fatalf("shouldFetchCursorRequestBasedUsage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldUseCursorRequestBasedUsage(t *testing.T) {
	usage := &CursorUsageResponse{
		Enabled:   true,
		PlanUsage: &CursorPlanUsage{Limit: 0},
	}
	requestUsage := &CursorRequestUsageResponse{
		Models: map[string]CursorModelUsage{
			"gpt-4.1": {NumRequests: 10, MaxRequestUsage: 100},
		},
	}

	if !shouldUseCursorRequestBasedUsage(usage, requestUsage) {
		t.Fatal("expected request-based usage to be used when the plan limit is unavailable")
	}
	if shouldUseCursorRequestBasedUsage(&CursorUsageResponse{Enabled: true, PlanUsage: &CursorPlanUsage{Limit: 100}}, requestUsage) {
		t.Fatal("did not expect request-based usage when a standard plan limit is available")
	}
}
