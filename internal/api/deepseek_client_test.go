package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeepSeekClient(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		respBody   string
		wantErr    error
		check      func(t *testing.T, resp *DeepSeekBalanceResponse)
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			respBody:   `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"125.00","granted_balance":"25.00","topped_up_balance":"100.00"}]}`,
			wantErr:    nil,
			check: func(t *testing.T, resp *DeepSeekBalanceResponse) {
				if resp == nil {
					t.Fatal("expected non-nil response")
				}
				if !resp.IsAvailable {
					t.Errorf("expected IsAvailable true")
				}
				if len(resp.BalanceInfos) != 1 {
					t.Fatalf("expected 1 balance info, got %d", len(resp.BalanceInfos))
				}
				info := resp.BalanceInfos[0]
				if info.Currency != "CNY" {
					t.Errorf("expected currency CNY, got %s", info.Currency)
				}
				if info.TotalBalance != "125.00" {
					t.Errorf("expected total balance 125.00, got %s", info.TotalBalance)
				}
			},
		},
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			respBody:   `{"error":"invalid token"}`,
			wantErr:    ErrDeepSeekUnauthorized,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			respBody:   `{"error":"too many requests"}`,
			wantErr:    ErrDeepSeekRateLimited,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			respBody:   `{"error":"internal server error"}`,
			wantErr:    ErrDeepSeekServerError,
		},
		{
			name:       "invalid json",
			statusCode: http.StatusOK,
			respBody:   `{"is_available": {invalid json`,
			wantErr:    ErrDeepSeekInvalidResponse,
		},
		{
			name:       "empty response",
			statusCode: http.StatusOK,
			respBody:   ``,
			wantErr:    ErrDeepSeekInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
					t.Errorf("expected Bearer test-key, got %s", auth)
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.respBody))
			}))
			defer server.Close()

			client := NewDeepSeekClient("test-key", nil, WithDeepSeekBaseURL(server.URL))
			resp, err := client.FetchBalance(context.Background())

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errorsIs(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.check != nil {
					tt.check(t, resp)
				}
			}
		})
	}
}

func TestDeepSeekTypes(t *testing.T) {
	jsonBody := `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"10.00","granted_balance":"0.00","topped_up_balance":"10.00"},{"currency":"CNY","total_balance":"125.00","granted_balance":"25.00","topped_up_balance":"100.00"}]}`
	resp, err := ParseDeepSeekResponse([]byte(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	
	snap := resp.ToSnapshot(time.Now())
	
	if snap.Currency != "CNY" {
		t.Errorf("expected priority currency CNY, got %s", snap.Currency)
	}
	if snap.TotalBalance != 125.0 {
		t.Errorf("expected total 125.0, got %f", snap.TotalBalance)
	}
}
